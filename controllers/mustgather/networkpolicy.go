package mustgather

import (
	"context"
	"crypto/sha256"
	goerror "errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	mustgatherv1 "github.com/openshift/must-gather-operator/api/v1"
	"github.com/operator-framework/operator-lib/proxy"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var errSFTPPolicyInput = goerror.New("invalid SFTP destination host or port")

// networkPolicyIdentity returns the CR name if it fits in a label value,
// or a stable hash otherwise. Hashed values contain an underscore so they
// can never collide with valid literal CR names.
func networkPolicyIdentity(name string) string {
	if name != "" && len(validation.IsValidLabelValue(name)) == 0 {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return fmt.Sprintf("mg_%x", sum[:30])
}

func networkPolicyPort(protocol corev1.Protocol, port int32) networkingv1.NetworkPolicyPort {
	return networkingv1.NetworkPolicyPort{Protocol: ptr.To(protocol), Port: ptr.To(intstr.FromInt32(port))}
}

func uploadNetworkPolicyName(name string) string {
	id := networkPolicyIdentity(name)
	if id == name {
		return "must-gather-" + name + "-sftp"
	}
	// Resource names must be DNS-1123: map the label underscore to a hyphen.
	// The "long" prefix keeps hashed names out of the literal naming domain.
	return "must-gather-long-" + strings.ReplaceAll(id, "_", "-") + "-sftp"
}

func numericNetworkPort(value string) (int32, error) {
	port, err := strconv.ParseUint(value, 10, 16)
	if err != nil || port == 0 {
		return 0, errSFTPPolicyInput
	}
	return int32(port), nil
}

func sftpNetworkPort(host string) (int32, error) {
	if host == "" {
		return 22, nil
	}
	if strings.ContainsAny(host, "/@?# \t\r\n") {
		return 0, errSFTPPolicyInput
	}
	// Reject bracketed non-IPs before normalize turns "[broken]" into "broken:22".
	if strings.HasPrefix(host, "[") {
		var h string
		if containsPort(host) {
			var err error
			h, _, err = net.SplitHostPort(host)
			if err != nil {
				return 0, errSFTPPolicyInput
			}
		} else {
			// Require a closed "[...]" form; lone "[" or "[unclosed" would panic on slice.
			if len(host) < 3 || !strings.HasSuffix(host, "]") {
				return 0, errSFTPPolicyInput
			}
			h = host[1 : len(host)-1]
		}
		if net.ParseIP(h) == nil {
			return 0, errSFTPPolicyInput
		}
	}
	addr := normalizeHostAddress(host)
	if addr == "" {
		return 0, errSFTPPolicyInput
	}
	h, p, err := net.SplitHostPort(addr)
	if err != nil || h == "" {
		return 0, errSFTPPolicyInput
	}
	return numericNetworkPort(p)
}

func proxyNetworkPort(value string) (int32, error) {
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" {
		return 0, fmt.Errorf("proxy must have a valid host and port")
	}
	if port := u.Port(); port != "" {
		return numericNetworkPort(port)
	}
	if u.Scheme == "https" {
		return 3129, nil
	}
	return 3128, nil
}

// desiredUploadNetworkPolicy grants configured ports, including proxies when no
// upload is requested or NO_PROXY bypasses them. It never resolves destinations.
func desiredUploadNetworkPolicy(instance *mustgatherv1.MustGather, env []corev1.EnvVar) (*networkingv1.NetworkPolicy, error) {
	ports := []int32{}
	if hasSFTPUpload(*instance) {
		port, err := sftpNetworkPort(derefString(instance.Spec.UploadTarget.SFTP.Host))
		if err != nil {
			return nil, err
		}
		ports = append(ports, port)
	}
	for _, v := range env {
		if (v.Name != "HTTP_PROXY" && v.Name != "HTTPS_PROXY") || v.Value == "" {
			continue
		}
		port, err := proxyNetworkPort(v.Value)
		if err != nil {
			// Never include the URL or parser error: both can contain credentials.
			return nil, fmt.Errorf("invalid %s: expected an HTTP or HTTPS proxy with a valid port", v.Name)
		}
		ports = append(ports, port)
	}
	if len(ports) == 0 {
		return nil, nil
	}
	slices.Sort(ports)
	ports = slices.Compact(ports)
	rules := make([]networkingv1.NetworkPolicyPort, 0, len(ports))
	for _, port := range ports {
		rules = append(rules, networkPolicyPort(corev1.ProtocolTCP, port))
	}
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: uploadNetworkPolicyName(instance.Name), Namespace: instance.Namespace,
			Labels:          map[string]string{networkPolicyManagedBy: networkPolicyManager},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(instance, mustgatherv1.GroupVersion.WithKind("MustGather"))},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{jobNetworkPolicyLabel: networkPolicyIdentity(instance.Name)}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      []networkingv1.NetworkPolicyEgressRule{{Ports: rules}},
		},
	}, nil
}

func (r *MustGatherReconciler) ensureUploadNetworkPolicy(ctx context.Context, instance *mustgatherv1.MustGather, desired *networkingv1.NetworkPolicy) error {
	if desired == nil {
		return nil
	}
	existing := &networkingv1.NetworkPolicy{}
	if err := r.GetClient().Get(ctx, client.ObjectKeyFromObject(desired), existing); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("get upload network policy: %w", err)
		}
		if err := r.GetClient().Create(ctx, desired); err != nil {
			return fmt.Errorf("create upload network policy: %w", err)
		}
		return nil
	}
	if !metav1.IsControlledBy(existing, instance) || existing.Labels[networkPolicyManagedBy] != networkPolicyManager {
		return fmt.Errorf("upload network policy %s/%s is not managed by this request", existing.Namespace, existing.Name)
	}
	if equality.Semantic.DeepEqual(existing.Spec, desired.Spec) {
		return nil
	}
	existing.Spec = desired.Spec
	if err := r.GetClient().Update(ctx, existing); err != nil {
		return fmt.Errorf("update upload network policy: %w", err)
	}
	return nil
}

func uploadPolicyFromEnvironment(instance *mustgatherv1.MustGather) (*networkingv1.NetworkPolicy, error) {
	return desiredUploadNetworkPolicy(instance, proxy.ReadProxyVarsFromEnv())
}

func (r *MustGatherReconciler) cleanupUploadNetworkPolicy(ctx context.Context, instance *mustgatherv1.MustGather) error {
	policy := &networkingv1.NetworkPolicy{}
	key := client.ObjectKey{Namespace: instance.Namespace, Name: uploadNetworkPolicyName(instance.Name)}
	if err := r.GetClient().Get(ctx, key, policy); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get upload network policy for cleanup: %w", err)
	}
	if !metav1.IsControlledBy(policy, instance) {
		return nil
	}
	// Prevent a same-name replacement between Get and Delete from being removed.
	if err := r.GetClient().Delete(ctx, policy, client.Preconditions{UID: &policy.UID}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete upload network policy: %w", err)
	}
	return nil
}

func (r *MustGatherReconciler) reconcileActiveJobPolicies(ctx context.Context, instance *mustgatherv1.MustGather, job *batchv1.Job) error {
	if !metav1.IsControlledBy(job, instance) || job.Spec.Template.Labels[networkPolicyLabel] != "default" ||
		job.Spec.Template.Labels[jobNetworkPolicyLabel] != networkPolicyIdentity(instance.Name) {
		// Old or foreign Jobs must not be relabeled or retroactively isolated.
		return nil
	}
	env := proxy.ReadProxyVarsFromEnv()
	for _, container := range job.Spec.Template.Spec.Containers {
		if container.Name != uploadContainerName {
			continue
		}
		// A running upload uses the environment frozen into its Pod template,
		// even if the operator's proxy configuration has since changed.
		env = nil
		for _, v := range container.Env {
			switch v.Name {
			case uploadEnvHTTPProxy:
				env = append(env, corev1.EnvVar{Name: "HTTP_PROXY", Value: v.Value})
			case uploadEnvHTTPSProxy:
				env = append(env, corev1.EnvVar{Name: "HTTPS_PROXY", Value: v.Value})
			}
		}
		break
	}
	desired, err := desiredUploadNetworkPolicy(instance, env)
	if err != nil {
		return err
	}
	if err := r.ensureBaselineNetworkPolicy(ctx, instance.Namespace); err != nil {
		return err
	}
	if desired == nil {
		return r.cleanupUploadNetworkPolicy(ctx, instance)
	}
	return r.ensureUploadNetworkPolicy(ctx, instance, desired)
}

func baselineNetworkPolicy(namespace string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: baselineNetworkPolicyName, Namespace: namespace,
			Labels: map[string]string{networkPolicyManagedBy: networkPolicyManager},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{networkPolicyLabel: "default"}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					To: []networkingv1.NetworkPolicyPeer{{
						NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "openshift-dns"}},
						PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{"dns.operator.openshift.io/daemonset-dns": "default"}},
					}},
					Ports: []networkingv1.NetworkPolicyPort{
						networkPolicyPort(corev1.ProtocolTCP, 53), networkPolicyPort(corev1.ProtocolUDP, 53),
						networkPolicyPort(corev1.ProtocolTCP, 5353), networkPolicyPort(corev1.ProtocolUDP, 5353),
					},
				},
				{Ports: []networkingv1.NetworkPolicyPort{networkPolicyPort(corev1.ProtocolTCP, 6443)}},
			},
		},
	}
}

func (r *MustGatherReconciler) ensureBaselineNetworkPolicy(ctx context.Context, namespace string) error {
	desired := baselineNetworkPolicy(namespace)
	existing := &networkingv1.NetworkPolicy{}
	if err := r.GetClient().Get(ctx, client.ObjectKeyFromObject(desired), existing); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("get baseline network policy: %w", err)
		}
		if err := r.GetClient().Create(ctx, desired); err != nil {
			if !errors.IsAlreadyExists(err) {
				return fmt.Errorf("create baseline network policy: %w", err)
			}
			// A concurrent request may have created the namespace baseline.
			if err := r.GetClient().Get(ctx, client.ObjectKeyFromObject(desired), existing); err != nil {
				return fmt.Errorf("read concurrently created baseline network policy: %w", err)
			}
		} else {
			return nil
		}
	}
	// The persistent baseline is never adopted or updated. A conflicting object
	// requires administrator attention before a new Job can safely start.
	if existing.Labels[networkPolicyManagedBy] != networkPolicyManager || len(existing.OwnerReferences) != 0 ||
		!equality.Semantic.DeepEqual(existing.Spec, desired.Spec) {
		return fmt.Errorf("baseline network policy %s/%s conflicts with the required managed policy", namespace, baselineNetworkPolicyName)
	}
	return nil
}
