package mustgather

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	configv1 "github.com/openshift/api/config/v1"
	mustgatherv1 "github.com/openshift/must-gather-operator/api/v1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestCleanupUploadNetworkPolicy(t *testing.T) {
	for _, scenario := range []string{"owned", "foreign", "absent", "foreign job", "delete failure", "get failure"} {
		t.Run(scenario, func(t *testing.T) {
			mg := networkPolicyRequest("request", "host")
			policy, err := desiredUploadNetworkPolicy(mg, nil)
			if err != nil {
				t.Fatal(err)
			}
			policy.UID = "policy-uid"
			other, err := desiredUploadNetworkPolicy(networkPolicyRequest("other", "host:2222"), nil)
			if err != nil {
				t.Fatal(err)
			}
			objects := []client.Object{baselineNetworkPolicy("ns"), other}
			if scenario == "foreign" {
				policy.OwnerReferences[0].UID = "other"
			}
			if scenario != "absent" {
				objects = append(objects, policy)
			}
			if scenario == "foreign job" {
				objects = append(objects, &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: mg.Namespace, UID: "foreign"}})
			}
			deleted := false
			r := newNetworkPolicyTestReconciler(t, objects, interceptClient{
				onGet: func(_ context.Context, _ client.ObjectKey, obj client.Object) error {
					if _, ok := obj.(*networkingv1.NetworkPolicy); ok && scenario == "get failure" {
						return errors.New("unavailable")
					}
					return nil
				},
				onDelete: func(_ context.Context, obj client.Object, opts ...client.DeleteOption) error {
					if _, ok := obj.(*networkingv1.NetworkPolicy); !ok {
						t.Fatal("unexpected resource deletion")
					}
					deleted = true
					if scenario == "delete failure" {
						return errors.New("unavailable")
					}
					options := &client.DeleteOptions{}
					for _, opt := range opts {
						opt.ApplyToDelete(options)
					}
					if options.Preconditions == nil || ptr.Deref(options.Preconditions.UID, "") != policy.UID {
						t.Fatal("missing UID delete precondition")
					}
					return nil
				},
			})
			err = r.cleanupMustGatherResources(context.Background(), logr.Discard(), mg)
			wantErr := scenario == "delete failure" || scenario == "get failure"
			if (err != nil) != wantErr {
				t.Fatalf("error=%v", err)
			}
			if scenario == "foreign" && deleted {
				t.Fatal("deleted foreign policy")
			}
			if scenario == "owned" || scenario == "foreign job" {
				if !deleted {
					t.Fatal("left policy when Job missing/foreign")
				}
				if err := r.cleanupMustGatherResources(context.Background(), logr.Discard(), mg); err != nil {
					t.Fatal("cleanup is not idempotent:", err)
				}
			}
			for _, key := range []client.ObjectKey{{Namespace: "ns", Name: baselineNetworkPolicyName}, client.ObjectKeyFromObject(other)} {
				if err := r.GetClient().Get(context.Background(), key, &networkingv1.NetworkPolicy{}); err != nil && scenario != "get failure" {
					t.Fatal("removed unrelated policy:", err)
				}
			}
		})
	}
}

func TestNetworkPolicyLifecycle(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	for _, scenario := range []string{"completed", "failed", "deleting", "retained complete", "retained delete", "completed status", "cleanup retry"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			mg := networkPolicyRequest("request", "host")
			mg.Spec.RetainResourcesOnCompletion = ptr.To(strings.HasPrefix(scenario, "retained"))
			if scenario == "deleting" || scenario == "retained delete" {
				now := metav1.NewTime(time.Now())
				mg.DeletionTimestamp = &now
			}
			if scenario == "completed status" {
				mg.Status = &mustgatherv1.MustGatherStatus{Completed: ptr.To(true)}
			}
			job := getJobTemplate("gather", "upload", *mg, "", "directory")
			job.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(mg, mustgatherv1.GroupVersion.WithKind("MustGather"))}
			job.Status.Succeeded = 1
			if scenario == "failed" {
				job.Status.Succeeded = 0
				job.Status.Failed = 4
			}
			policy, err := desiredUploadNetworkPolicy(mg, nil)
			if err != nil {
				t.Fatal(err)
			}
			policy.UID = "policy"
			objects := []client.Object{mg, policy, baselineNetworkPolicy("ns")}
			if scenario != "completed status" {
				objects = append(objects, job)
			}
			failOnce := scenario == "cleanup retry"
			r := newNetworkPolicyTestReconciler(t, objects, interceptClient{
				onCreate: func(_ context.Context, obj client.Object, _ ...client.CreateOption) error {
					t.Fatalf("finished request creates %T", obj)
					return nil
				},
				onDelete: func(_ context.Context, obj client.Object, _ ...client.DeleteOption) error {
					if _, ok := obj.(*networkingv1.NetworkPolicy); ok && failOnce {
						failOnce = false
						return errors.New("temporary API error")
					}
					return nil
				},
			})
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mg)})
			if scenario == "cleanup retry" {
				if err == nil {
					t.Fatal("missing cleanup retry error")
				}
				_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mg)})
			}
			if err != nil {
				t.Fatal(err)
			}
			err = r.GetClient().Get(ctx, client.ObjectKeyFromObject(policy), &networkingv1.NetworkPolicy{})
			if strings.HasPrefix(scenario, "retained") {
				if err != nil {
					t.Fatal("explicitly deleted retained policy")
				}
			} else if !apierrors.IsNotFound(err) {
				t.Fatalf("temporary policy remains: %v", err)
			}
			if err := r.GetClient().Get(ctx, client.ObjectKey{Namespace: "ns", Name: baselineNetworkPolicyName}, &networkingv1.NetworkPolicy{}); err != nil {
				t.Fatal("baseline removed")
			}
		})
	}
}

func TestActiveJobPolicyRepair(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://new-proxy:8081")
	t.Setenv("HTTPS_PROXY", "")
	for _, scenario := range []string{"absent", "drift", "legacy", "foreign", "frozen proxy", "no extra access"} {
		t.Run(scenario, func(t *testing.T) {
			mg := networkPolicyRequest("request", "host:2222")
			if scenario == "no extra access" {
				mg.Spec.UploadTarget = nil
				mg.Spec.Obfuscate = &mustgatherv1.ObfuscateConfig{Enabled: ptr.To(true)}
			}
			job := getJobTemplate("gather", "upload", *mg, "", "directory")
			job.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(mg, mustgatherv1.GroupVersion.WithKind("MustGather"))}
			job.Status.Active = 1
			for i := range job.Spec.Template.Spec.Containers {
				if job.Spec.Template.Spec.Containers[i].Name == uploadContainerName {
					job.Spec.Template.Spec.Containers[i].Env = nil
					if scenario == "frozen proxy" {
						job.Spec.Template.Spec.Containers[i].Env = []corev1.EnvVar{{Name: uploadEnvHTTPProxy, Value: "http://old-proxy:8080"}}
					}
				}
			}
			if scenario == "legacy" {
				job.Spec.Template.Labels = nil
			}
			if scenario == "foreign" {
				job.OwnerReferences[0].UID = "foreign"
			}
			objects := []client.Object{mg, job}
			policy, err := desiredUploadNetworkPolicy(networkPolicyRequest("request", "host:2222"), nil)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "drift" || scenario == "no extra access" {
				policy.Spec.Egress = nil
				objects = append(objects, policy)
			}
			r := newNetworkPolicyTestReconciler(t, objects, interceptClient{})
			if _, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mg)}); err != nil {
				t.Fatal(err)
			}
			actual := &networkingv1.NetworkPolicy{}
			err = r.GetClient().Get(context.Background(), client.ObjectKeyFromObject(policy), actual)
			if scenario == "legacy" || scenario == "foreign" || scenario == "no extra access" {
				if !apierrors.IsNotFound(err) {
					t.Fatal("unexpected policy")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []int32
			for _, port := range actual.Spec.Egress[0].Ports {
				got = append(got, port.Port.IntVal)
			}
			want := []int32{2222}
			if scenario == "frozen proxy" {
				want = append(want, 8080)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ports=%v, want %v", got, want)
			}
		})
	}
}

func networkPolicyRequest(name, host string) *mustgatherv1.MustGather {
	return &mustgatherv1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: "uid-" + types.UID(name), Finalizers: []string{mustGatherFinalizer}}, Spec: mustgatherv1.MustGatherSpec{
		ServiceAccountName: "gather",
		UploadTarget:       &mustgatherv1.UploadTargetSpec{Type: mustgatherv1.UploadTypeSFTP, SFTP: &mustgatherv1.SFTPSpec{Host: ptr.To(host), CaseID: "123", CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "creds"}}},
	}}
}

func TestSFTPNetworkPort(t *testing.T) {
	for _, tt := range []struct {
		host string
		port int32
	}{
		{"", 22}, {"sftp.example.com", 22}, {"sftp.example.com:2222", 2222}, {"192.0.2.1", 22}, {"[2001:db8::1]", 22}, {"2001:db8::1", 22}, {"[2001:db8::1]:2022", 2022},
		{" ", 0}, {"host:0", 0}, {"host:-1", 0}, {"host:65536", 0}, {"host:abc", 0}, {"host:", 0}, {":22", 0}, {"http://host", 0}, {"[broken]", 0}, {"[broken]:22", 0}, {"host:+22", 0},
		{"[", 0}, {"[]", 0}, {"[unclosed", 0}, {"[:22", 0},
	} {
		t.Run(tt.host, func(t *testing.T) {
			port, err := sftpNetworkPort(tt.host)
			if port != tt.port || (err != nil) != (tt.port == 0) {
				t.Fatalf("got %d, %v; want %d", port, err, tt.port)
			}
		})
	}
}

func TestDesiredUploadNetworkPolicy(t *testing.T) {
	for _, tt := range []struct {
		name, host string
		upload     bool
		env        []corev1.EnvVar
		want       []int32
		bad        bool
	}{
		{name: "none"}, {name: "sftp default", upload: true, want: []int32{22}},
		{name: "sftp custom", upload: true, host: "host:2022", want: []int32{2022}},
		{name: "empty proxies", env: []corev1.EnvVar{{Name: "HTTP_PROXY"}, {Name: "HTTPS_PROXY"}}},
		{name: "proxy without upload", env: []corev1.EnvVar{{Name: "HTTP_PROXY", Value: "http://proxy"}, {Name: "HTTPS_PROXY", Value: "https://proxy"}}, want: []int32{3128, 3129}},
		{name: "dedup and bypass", upload: true, env: []corev1.EnvVar{{Name: "HTTP_PROXY", Value: "http://proxy:22"}, {Name: "HTTPS_PROXY", Value: "https://proxy:22"}, {Name: "NO_PROXY", Value: "*"}}, want: []int32{22}},
		{name: "encoded credentials IPv6", env: []corev1.EnvVar{{Name: "HTTPS_PROXY", Value: "https://user:p%40s%3As%25@[2001:db8::1]:8443"}}, want: []int32{8443}},
		{name: "scheme-less", env: []corev1.EnvVar{{Name: "HTTP_PROXY", Value: "proxy:8080"}}, want: []int32{8080}},
		{name: "bad SFTP", upload: true, host: "host:65536", bad: true},
		{name: "whitespace proxy", env: []corev1.EnvVar{{Name: "HTTP_PROXY", Value: " "}}, bad: true},
		{name: "invalid proxy port", env: []corev1.EnvVar{{Name: "HTTP_PROXY", Value: "http://user:secret@host:bad"}}, bad: true},
		{name: "empty proxy port defaults", env: []corev1.EnvVar{{Name: "HTTP_PROXY", Value: "http://host:"}}, want: []int32{3128}},
		{name: "overflow proxy", env: []corev1.EnvVar{{Name: "HTTP_PROXY", Value: "http://host:65536"}}, bad: true},
		{name: "non-http scheme", env: []corev1.EnvVar{{Name: "HTTP_PROXY", Value: "socks5://host:1080"}}, want: []int32{1080}},
		{name: "ambiguous IPv6 proxy", env: []corev1.EnvVar{{Name: "HTTP_PROXY", Value: "http://2001:db8::1"}}, bad: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mg := networkPolicyRequest("request", tt.host)
			if !tt.upload {
				mg.Spec.UploadTarget = nil
			}
			policy, err := desiredUploadNetworkPolicy(mg, tt.env)
			if (err != nil) != tt.bad {
				t.Fatalf("unexpected error: %v", err)
			}
			if err != nil {
				if strings.Contains(err.Error(), "secret") {
					t.Fatal("credential leaked")
				}
				return
			}
			if tt.want == nil {
				if policy != nil {
					t.Fatal("unexpected allowance")
				}
				return
			}
			var got []int32
			for _, port := range policy.Spec.Egress[0].Ports {
				got = append(got, port.Port.IntVal)
				if *port.Protocol != corev1.ProtocolTCP {
					t.Fatal("non-TCP allowance")
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ports=%v, want %v", got, tt.want)
			}
			if !metav1.IsControlledBy(policy, mg) || len(policy.Spec.Egress[0].To) != 0 || policy.Spec.PodSelector.MatchLabels[jobNetworkPolicyLabel] != "request" {
				t.Fatal("wrong owner, destinations or selector")
			}
		})
	}
}

func TestUploadPolicyOwnershipAndDrift(t *testing.T) {
	for _, scenario := range []string{"create", "noop", "drift", "foreign", "unmarked", "create failure", "update failure"} {
		t.Run(scenario, func(t *testing.T) {
			mg := networkPolicyRequest("request", "host")
			desired, err := desiredUploadNetworkPolicy(mg, nil)
			if err != nil {
				t.Fatal(err)
			}
			existing := desired.DeepCopy()
			var objects []client.Object
			if scenario == "drift" || scenario == "update failure" {
				existing.Spec.Egress = nil
			}
			if scenario == "foreign" {
				existing.OwnerReferences[0].UID = "other"
			}
			if scenario == "unmarked" {
				existing.Labels = nil
			}
			if scenario != "create" && scenario != "create failure" {
				objects = append(objects, existing)
			}
			updates := 0
			r := newNetworkPolicyTestReconciler(t, objects, interceptClient{
				onCreate: func(_ context.Context, _ client.Object, _ ...client.CreateOption) error {
					if scenario == "create failure" {
						return errors.New("unavailable")
					}
					return nil
				},
				onUpdate: func(_ context.Context, _ client.Object, _ ...client.UpdateOption) error {
					updates++
					if scenario == "update failure" {
						return errors.New("conflict")
					}
					return nil
				},
			})
			err = r.ensureUploadNetworkPolicy(context.Background(), mg, desired)
			wantErr := scenario == "foreign" || scenario == "unmarked" || scenario == "create failure" || scenario == "update failure"
			if (err != nil) != wantErr {
				t.Fatalf("error=%v", err)
			}
			if scenario == "noop" && updates != 0 {
				t.Fatal("unnecessary update")
			}
			if scenario == "foreign" && updates != 0 {
				t.Fatal("modified foreign policy")
			}
			if !wantErr {
				actual := &networkingv1.NetworkPolicy{}
				if err := r.GetClient().Get(context.Background(), client.ObjectKeyFromObject(desired), actual); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(actual.Spec, desired.Spec) {
					t.Fatal("desired policy not established")
				}
			}
		})
	}
}

func TestUploadPoliciesSeparateConcurrentRequests(t *testing.T) {
	first := networkPolicyRequest(strings.Repeat("a", 80), "host:2222")
	second := networkPolicyRequest("second", "host:2223")
	a, err := desiredUploadNetworkPolicy(first, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := desiredUploadNetworkPolicy(second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name == b.Name || reflect.DeepEqual(a.Spec.PodSelector, b.Spec.PodSelector) || len(validation.IsDNS1123Subdomain(a.Name)) != 0 {
		t.Fatal("invalid or overlapping policy identity")
	}
	if strings.Contains(a.Name, "_") {
		t.Fatal("label identity leaked into invalid policy name")
	}
	if len(validation.IsDNS1123Subdomain(b.Name)) != 0 {
		t.Fatal("literal policy name is not a DNS-1123 subdomain")
	}
}

func TestReconcileRequiresUploadPolicy(t *testing.T) {
	t.Setenv("OPERATOR_IMAGE", "upload:test")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	original := sftpDialFunc
	t.Cleanup(func() { sftpDialFunc = original })
	sftpDialFunc = func(context.Context, string, string, string) error { return nil }
	for _, fail := range []bool{false, true} {
		mg := networkPolicyRequest("request", "host:2222")
		baselineCreated, uploadCreated, jobCreated := false, false, false
		r := newNetworkPolicyTestReconciler(t, []client.Object{mg, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "gather", Namespace: "ns"}}, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "ns"}, Data: map[string][]byte{"username": []byte("user"), "password": []byte("password")}}}, interceptClient{
			onCreate: func(_ context.Context, obj client.Object, _ ...client.CreateOption) error {
				switch p := obj.(type) {
				case *networkingv1.NetworkPolicy:
					if p.Name == baselineNetworkPolicyName {
						baselineCreated = true
					} else {
						if fail {
							return errors.New("policy unavailable")
						}
						uploadCreated = true
					}
				case *batchv1.Job:
					if !baselineCreated || !uploadCreated {
						t.Fatal("Job created before both required policies")
					}
					jobCreated = true
				}
				return nil
			},
		})
		_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mg)})
		if (err != nil) != fail || jobCreated == fail {
			t.Fatalf("fail=%v, job=%v, err=%v", fail, jobCreated, err)
		}
	}
}

func TestPolicyInputErrorStatus(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	for _, scenario := range []string{"immutable", "status failure", "proxy config"} {
		t.Run(scenario, func(t *testing.T) {
			mg := networkPolicyRequest("invalid", "host:65536")
			interceptor := interceptClient{onCreate: func(_ context.Context, obj client.Object, _ ...client.CreateOption) error {
				t.Fatalf("invalid input created %T", obj)
				return nil
			}}
			if scenario == "status failure" {
				interceptor.status = failingStatusWriter{}
			}
			if scenario == "proxy config" {
				mg.Spec.UploadTarget = nil
				t.Setenv("HTTP_PROXY", "http://user:secret@proxy:bad")
			}
			r := newNetworkPolicyTestReconciler(t, []client.Object{mg}, interceptor)
			result, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mg)})
			if scenario != "immutable" {
				if err == nil {
					t.Fatal("expected retryable error")
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatal("leaked proxy credentials")
				}
				return
			}
			if err != nil || result != (reconcile.Result{}) {
				t.Fatalf("terminal return=%v,%v", result, err)
			}
			updated := &mustgatherv1.MustGather{}
			if err := r.GetClient().Get(context.Background(), client.ObjectKeyFromObject(mg), updated); err != nil {
				t.Fatal(err)
			}
			if updated.Status == nil || !ptr.Deref(updated.Status.Completed, false) || ptr.Deref(updated.Status.Status, "") != "Failed" {
				t.Fatal("not terminal Failed/Completed")
			}
			condition := apimeta.FindStatusCondition(updated.Status.Conditions, "ReconcileError")
			if condition == nil || condition.Reason != "ValidationFailed" || !strings.Contains(condition.Message, "invalid SFTP destination") {
				t.Fatal("missing validation condition")
			}
			select {
			case event := <-r.GetRecorder().(*record.FakeRecorder).Events:
				if !strings.Contains(event, "Warning ProcessingError") {
					t.Fatal(event)
				}
			default:
				t.Fatal("missing warning event")
			}
		})
	}
}

func newNetworkPolicyTestReconciler(t *testing.T, objects []client.Object, interceptor interceptClient) *MustGatherReconciler {
	t.Helper()
	s := runtime.NewScheme()
	for _, register := range []func(*runtime.Scheme) error{scheme.AddToScheme, configv1.AddToScheme, mustgatherv1.AddToScheme} {
		if err := register(s); err != nil {
			t.Fatal(err)
		}
	}
	interceptor.Client = fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).WithStatusSubresource(&mustgatherv1.MustGather{}).Build()
	return &MustGatherReconciler{
		ReconcilerBase:         util.NewReconcilerBase(interceptor, s, &rest.Config{}, record.NewFakeRecorder(100), nil),
		DefaultMustGatherImage: "gather:test", OperatorNamespace: "operator", OperatorServiceAccountName: "operator",
	}
}

func TestNetworkPolicyIdentity(t *testing.T) {
	seen := map[string]string{}
	for _, name := range []string{"short", "with.dots", strings.Repeat("a", 53), strings.Repeat("a", 63), strings.Repeat("a", 64), strings.Repeat("a", 252) + "b", strings.Repeat("a", 252) + "c"} {
		got := networkPolicyIdentity(name)
		if len(validation.IsValidLabelValue(got)) != 0 || got != networkPolicyIdentity(name) {
			t.Fatalf("invalid or unstable identity %q", got)
		}
		if len(name) <= 63 && got != name {
			t.Fatalf("ordinary name changed: %s", name)
		}
		if previous, ok := seen[got]; ok && previous != name {
			t.Fatal("different names share policy identity")
		}
		seen[got] = name
		if len(name) > 63 && len(validation.IsDNS1123Subdomain(got)) == 0 {
			t.Fatal("hashed identity can be impersonated by a literal CR name")
		}
	}
}

func TestBaselineNetworkPolicyRules(t *testing.T) {
	p := baselineNetworkPolicy("ns")
	if len(p.OwnerReferences) != 0 || len(p.Spec.Ingress) != 0 || !reflect.DeepEqual(p.Spec.PolicyTypes, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}) {
		t.Fatal("baseline must persist and isolate both directions without ingress")
	}
	if !reflect.DeepEqual(p.Spec.PodSelector.MatchLabels, map[string]string{"must-gather.openshift.io/network-policy": "default"}) || len(p.Spec.Egress) != 2 {
		t.Fatal("unexpected selection or egress grants")
	}
	dns := p.Spec.Egress[0]
	if len(dns.To) != 1 || dns.To[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "openshift-dns" || dns.To[0].PodSelector.MatchLabels["dns.operator.openshift.io/daemonset-dns"] != "default" {
		t.Fatal("DNS must use the conjunction of the namespace and Pod selectors")
	}
	got := map[corev1.Protocol][]int32{}
	for _, port := range dns.Ports {
		got[*port.Protocol] = append(got[*port.Protocol], port.Port.IntVal)
	}
	if !reflect.DeepEqual(got, map[corev1.Protocol][]int32{corev1.ProtocolTCP: {53, 5353}, corev1.ProtocolUDP: {53, 5353}}) {
		t.Fatalf("unexpected DNS ports: %v", got)
	}
	api := p.Spec.Egress[1]
	if len(api.To) != 0 || len(api.Ports) != 1 || *api.Ports[0].Protocol != corev1.ProtocolTCP || api.Ports[0].Port.IntVal != 6443 {
		t.Fatal("unexpected API grant")
	}
}

func TestEnsureBaselineNetworkPolicy(t *testing.T) {
	for _, scenario := range []string{"new", "reuse", "foreign", "drift", "owner", "create error", "get error", "race"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			var objects []client.Object
			p := baselineNetworkPolicy("ns")
			if scenario == "foreign" {
				p.Labels = nil
			}
			if scenario == "drift" {
				p.Spec.Egress = nil
			}
			if scenario == "owner" {
				p.OwnerReferences = []metav1.OwnerReference{{UID: "other"}}
			}
			if scenario != "new" && scenario != "create error" {
				objects = []client.Object{p}
			}
			reads := 0
			interceptor := interceptClient{
				onGet: func(_ context.Context, _ client.ObjectKey, _ client.Object) error {
					reads++
					if scenario == "get error" {
						return errors.New("unavailable")
					}
					if scenario == "race" && reads == 1 {
						return apierrors.NewNotFound(schema.GroupResource{Group: "networking.k8s.io", Resource: "networkpolicies"}, p.Name)
					}
					return nil
				},
				onCreate: func(_ context.Context, _ client.Object, _ ...client.CreateOption) error {
					if scenario == "create error" {
						return errors.New("forbidden")
					}
					return nil
				},
				onUpdate: func(_ context.Context, _ client.Object, _ ...client.UpdateOption) error {
					t.Fatal("baseline must never be updated")
					return nil
				},
			}
			r := newNetworkPolicyTestReconciler(t, objects, interceptor)
			err := r.ensureBaselineNetworkPolicy(ctx, "ns")
			wantErr := scenario == "foreign" || scenario == "drift" || scenario == "owner" || scenario == "create error" || scenario == "get error"
			if (err != nil) != wantErr {
				t.Fatalf("error = %v, want error %v", err, wantErr)
			}
			if !wantErr {
				if err := r.ensureBaselineNetworkPolicy(ctx, "ns"); err != nil {
					t.Fatal(err)
				}
				list := &networkingv1.NetworkPolicyList{}
				if err := r.GetClient().List(ctx, list); err != nil {
					t.Fatal(err)
				}
				if len(list.Items) != 1 {
					t.Fatal("baseline duplicated")
				}
			}
		})
	}
}

func TestReconcileRequiresBaseline(t *testing.T) {
	t.Setenv("OPERATOR_IMAGE", "upload:test")
	for _, fail := range []bool{false, true} {
		mg := &mustgatherv1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "gather", Namespace: "ns", UID: "request", Finalizers: []string{mustGatherFinalizer}}, Spec: mustgatherv1.MustGatherSpec{ServiceAccountName: "gather"}}
		policyCreated, jobCreated := false, false
		r := newNetworkPolicyTestReconciler(t, []client.Object{mg, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "gather", Namespace: "ns"}}}, interceptClient{
			onCreate: func(_ context.Context, obj client.Object, _ ...client.CreateOption) error {
				switch obj.(type) {
				case *networkingv1.NetworkPolicy:
					if fail {
						return errors.New("policy create failed")
					}
					policyCreated = true
				case *batchv1.Job:
					if !policyCreated {
						t.Fatal("Job created before baseline")
					}
					jobCreated = true
				}
				return nil
			},
		})
		_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mg)})
		if (err != nil) != fail || jobCreated == fail {
			t.Fatalf("fail=%v, jobCreated=%v, error=%v", fail, jobCreated, err)
		}
	}
}

func TestCompletedRequestDoesNotCreateJob(t *testing.T) {
	for _, retain := range []bool{false, true} {
		mg := &mustgatherv1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "done", Namespace: "ns", UID: "request"}, Spec: mustgatherv1.MustGatherSpec{RetainResourcesOnCompletion: ptr.To(retain)}, Status: &mustgatherv1.MustGatherStatus{Completed: ptr.To(true)}}
		r := newNetworkPolicyTestReconciler(t, []client.Object{mg}, interceptClient{onCreate: func(_ context.Context, obj client.Object, _ ...client.CreateOption) error {
			t.Fatalf("completed request created %T", obj)
			return nil
		}})
		result, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(mg)})
		if err != nil || result != (reconcile.Result{}) {
			t.Fatalf("result=%v, err=%v", result, err)
		}
	}
}
