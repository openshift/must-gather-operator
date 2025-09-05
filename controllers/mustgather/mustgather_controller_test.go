package mustgather

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"

	//nolint:staticcheck -- code is tied to a specific controller-runtime version. See OSD-11458

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestMustGatherController(t *testing.T) {
	mgObj := createMustGatherObject()
	secObj := createMustGatherSecretObject()

	objs := []runtime.Object{
		mgObj,
		secObj,
	}

	var cfg *rest.Config

	s := scheme.Scheme
	s.AddKnownTypes(mustgatherv1alpha1.GroupVersion, mgObj)

	cl := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build()

	eventRec := &record.FakeRecorder{}

	r := MustGatherReconciler{
		ReconcilerBase: util.NewReconcilerBase(cl, s, cfg, eventRec, nil),
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      mgObj.Name,
			Namespace: mgObj.Namespace,
		},
	}

	res, err := r.Reconcile(context.TODO(), req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
	}

	if res != (reconcile.Result{}) {
		t.Error("reconcile did not return an empty Result")
	}
}

func createMustGatherObject() *mustgatherv1alpha1.MustGather {
	return &mustgatherv1alpha1.MustGather{
		TypeMeta: metav1.TypeMeta{
			Kind: "MustGather",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-must-gather",
			Namespace: "must-gather-operator",
		},
		Spec: mustgatherv1alpha1.MustGatherSpec{
			CaseID: "01234567",
			CaseManagementAccountSecretRef: corev1.LocalObjectReference{
				Name: "case-management-creds",
			},
			ServiceAccountRef: corev1.LocalObjectReference{
				Name: "",
			},
		},
	}
}

func createMustGatherSecretObject() *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "case-management-creds",
			Namespace: "must-gather-operator",
		},
		Data: map[string][]byte{
			"username": []byte("somefakeuser"),
			"password": []byte("somefakepassword"),
		},
	}
}

func generateFakeClient(objs ...client.Object) (client.Client, error) {
	s := runtime.NewScheme()
	if err := configv1.AddToScheme(s); err != nil {
		return nil, err
	}
	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build(), nil
}

func TestMustGatherReconciler_getClusterVersionForJobTemplate(t *testing.T) {
	tests := []struct {
		name               string
		clusterVersionObj  *configv1.ClusterVersion
		clusterVersionName string
		want               string
		wantErr            bool
	}{
		{
			name: "clusterVersion doesn't exist",
			clusterVersionObj: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
			},
			clusterVersionName: "badName",
			wantErr:            true,
		},
		{
			name: "clusterVersion does not have any status history",
			clusterVersionObj: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Status:     configv1.ClusterVersionStatus{},
			},
			clusterVersionName: "cluster",
			wantErr:            true,
		},
		{
			name: "clusterVersion does not have a Completed status history",
			clusterVersionObj: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Status:     configv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{State: "Foo"}}},
			},
			clusterVersionName: "cluster",
			wantErr:            true,
		},
		{
			name: "clusterVersion Completed status history does not have a version",
			clusterVersionObj: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Status:     configv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{State: "Completed"}}},
			},
			clusterVersionName: "cluster",
			wantErr:            true,
		},
		{
			name: "clusterVersion is a standard version in x.y.z format",
			clusterVersionObj: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Status:     configv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}}},
			},
			clusterVersionName: "cluster",
			want:               "1.2",
		},
		{
			name: "clusterVersion contains a build identifier",
			clusterVersionObj: &configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Status:     configv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3-rc.0"}}},
			},
			clusterVersionName: "cluster",
			want:               "1.2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{tt.clusterVersionObj}
			fakeClient, err := generateFakeClient(objects...)
			if err != nil {
				t.Errorf("failed to generate fake client")
			}
			r := &MustGatherReconciler{
				ReconcilerBase: util.NewReconcilerBase(fakeClient, fakeClient.Scheme(), &rest.Config{}, &record.FakeRecorder{}, nil),
			}
			got, err := r.getClusterVersionForJobTemplate(tt.clusterVersionName)
			if (err != nil) != tt.wantErr {
				t.Errorf("getClusterVersionForJobTemplate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getClusterVersionForJobTemplate() got = %v, want %v", got, tt.want)
			}
		})
	}
}

// failingStatusClient wraps a client.Client and forces Status().Update to return an error
type failingStatusClient struct{ client.Client }

func (c failingStatusClient) Status() client.StatusWriter {
	return failingStatusWriter{c.Client.Status()}
}

type failingStatusWriter struct{ client.StatusWriter }

func (w failingStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return errors.New("forced status update error")
}

func TestReconcile_JobSucceeded_StatusUpdateFails(t *testing.T) {
	t.Setenv("OPERATOR_IMAGE", "quay.io/openshift/must-gather-operator:latest")

	// Objects: MustGather (finalizer present), Job with Succeeded>0, ClusterVersion
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "test-must-gather", Namespace: "must-gather-operator", Finalizers: []string{mustGatherFinalizer}}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseID: "01234567", CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "case-management-creds"}, ServiceAccountRef: corev1.LocalObjectReference{Name: "default"}}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: mg.Namespace}, Status: batchv1.JobStatus{Succeeded: 1}}
	cv := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}, Status: configv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}}}}

	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1: %v", err)
	}
	if err := batchv1.AddToScheme(s); err != nil {
		t.Fatalf("add batchv1: %v", err)
	}
	if err := mustgatherv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add mustgatherv1alpha1: %v", err)
	}
	if err := configv1.AddToScheme(s); err != nil {
		t.Fatalf("add configv1: %v", err)
	}

	inner := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, job, cv).WithStatusSubresource(mg).Build()
	wrapped := failingStatusClient{Client: inner}
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(wrapped, s, &rest.Config{}, &record.FakeRecorder{}, nil)}

	res, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	_ = res
	if err == nil {
		t.Fatalf("expected error from status update failure, got nil")
	}
}

func TestReconcile_JobFailed_StatusUpdateFails(t *testing.T) {
	t.Setenv("OPERATOR_IMAGE", "quay.io/openshift/must-gather-operator:latest")

	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "test-must-gather", Namespace: "must-gather-operator", Finalizers: []string{mustGatherFinalizer}}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseID: "01234567", CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "case-management-creds"}, ServiceAccountRef: corev1.LocalObjectReference{Name: "default"}}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: mg.Namespace}, Status: batchv1.JobStatus{Failed: 1}}
	cv := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}, Status: configv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}}}}

	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1: %v", err)
	}
	if err := batchv1.AddToScheme(s); err != nil {
		t.Fatalf("add batchv1: %v", err)
	}
	if err := mustgatherv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add mustgatherv1alpha1: %v", err)
	}
	if err := configv1.AddToScheme(s); err != nil {
		t.Fatalf("add configv1: %v", err)
	}

	inner := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, job, cv).WithStatusSubresource(mg).Build()
	wrapped := failingStatusClient{Client: inner}
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(wrapped, s, &rest.Config{}, &record.FakeRecorder{}, nil)}

	res, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	_ = res
	if err == nil {
		t.Fatalf("expected error from status update failure, got nil")
	}
}

// interceptClient allows injecting failures for specific CRUD operations
type interceptClient struct {
	client.Client
	onGet    func(ctx context.Context, key client.ObjectKey, obj client.Object) error
	onList   func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
	onDelete func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error
	onUpdate func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error
	status   client.StatusWriter
}

func (c interceptClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if c.onGet != nil {
		if err := c.onGet(ctx, key, obj); err != nil {
			return err
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}
func (c interceptClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if c.onList != nil {
		if err := c.onList(ctx, list, opts...); err != nil {
			return err
		}
	}
	return c.Client.List(ctx, list, opts...)
}
func (c interceptClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if c.onDelete != nil {
		if err := c.onDelete(ctx, obj, opts...); err != nil {
			return err
		}
	}
	return c.Client.Delete(ctx, obj, opts...)
}
func (c interceptClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if c.onUpdate != nil {
		if err := c.onUpdate(ctx, obj, opts...); err != nil {
			return err
		}
	}
	return c.Client.Update(ctx, obj, opts...)
}
func (c interceptClient) Status() client.StatusWriter {
	if c.status != nil {
		return c.status
	}
	return c.Client.Status()
}

// ===================== cleanupMustGatherResources tests =====================

func TestCleanupResources_AllDeletedSuccessfully(t *testing.T) {
	// Arrange
	operatorNs := "must-gather-operator"
	secretName := "case-management-creds"
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: secretName}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: operatorNs}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: operatorNs, UID: "abc-123"}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: operatorNs, Labels: map[string]string{"controller-uid": string(job.UID)}}}

	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, secret, job, pod).Build()
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil)}

	// Act
	err := r.cleanupMustGatherResources(logf.Log, mg, operatorNs)

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	chkSecret := &corev1.Secret{}
	if getErr := cl.Get(context.TODO(), types.NamespacedName{Namespace: operatorNs, Name: secretName}, chkSecret); getErr == nil {
		t.Fatalf("expected secret to be deleted")
	}
	chkJob := &batchv1.Job{}
	if getErr := cl.Get(context.TODO(), types.NamespacedName{Namespace: operatorNs, Name: job.Name}, chkJob); getErr == nil {
		t.Fatalf("expected job to be deleted")
	}
}

func TestCleanupResources_SecretNotFound_Continues(t *testing.T) {
	operatorNs := "must-gather-operator"
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "missing"}}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: operatorNs}}

	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, job).Build()
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	if err := r.cleanupMustGatherResources(logf.Log, mg, operatorNs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCleanupResources_SecretGetError_ContinuesAndCleansJob(t *testing.T) {
	operatorNs := "must-gather-operator"
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"}}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: operatorNs, UID: "u1"}}

	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, job).Build()
	wrap := interceptClient{Client: base, onGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
		if _, ok := obj.(*corev1.Secret); ok && key.Name == "s" {
			return errors.New("boom secret get")
		}
		return nil
	}}
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(wrap, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	if err := r.cleanupMustGatherResources(logf.Log, mg, operatorNs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// job should be deleted since no pods exist
	chk := &batchv1.Job{}
	if e := base.Get(context.TODO(), types.NamespacedName{Namespace: operatorNs, Name: job.Name}, chk); e == nil {
		t.Fatalf("expected job to be deleted")
	}
}

func TestCleanupResources_SecretDeleteError_ReturnsError(t *testing.T) {
	operatorNs := "must-gather-operator"
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: operatorNs}}

	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, secret).Build()
	wrap := interceptClient{Client: base, onDelete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
		if _, ok := obj.(*corev1.Secret); ok {
			return errors.New("boom secret delete")
		}
		return nil
	}}
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(wrap, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	if err := r.cleanupMustGatherResources(logf.Log, mg, operatorNs); err == nil {
		t.Fatalf("expected error")
	}
}

func TestCleanupResources_JobNotFound_Continues(t *testing.T) {
	operatorNs := "must-gather-operator"
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"}}}

	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mg).Build()
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	if err := r.cleanupMustGatherResources(logf.Log, mg, operatorNs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCleanupResources_JobGetError_Continues(t *testing.T) {
	operatorNs := "must-gather-operator"
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"}}}

	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(mg).Build()
	wrap := interceptClient{Client: base, onGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
		if _, ok := obj.(*batchv1.Job); ok && key.Name == mg.Name {
			return errors.New("boom job get")
		}
		return nil
	}}
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(wrap, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	if err := r.cleanupMustGatherResources(logf.Log, mg, operatorNs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCleanupResources_PodListError_LeavesJob(t *testing.T) {
	operatorNs := "must-gather-operator"
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"}}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: operatorNs, UID: "u"}}

	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, job).Build()
	wrap := interceptClient{Client: base, onList: func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
		if _, ok := list.(*corev1.PodList); ok {
			return errors.New("boom list pods")
		}
		return nil
	}}
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(wrap, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	if err := r.cleanupMustGatherResources(logf.Log, mg, operatorNs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// job should still exist
	chk := &batchv1.Job{}
	if e := base.Get(context.TODO(), types.NamespacedName{Namespace: operatorNs, Name: job.Name}, chk); e != nil {
		t.Fatalf("expected job to remain, get err: %v", e)
	}
}

func TestCleanupResources_PodDeleteError_ReturnsError(t *testing.T) {
	operatorNs := "must-gather-operator"
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"}}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: operatorNs, UID: "u"}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: operatorNs, Labels: map[string]string{"controller-uid": string(job.UID)}}}

	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, job, pod).Build()
	wrap := interceptClient{Client: base, onDelete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
		if _, ok := obj.(*corev1.Pod); ok {
			return errors.New("boom pod delete")
		}
		return nil
	}}
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(wrap, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	if err := r.cleanupMustGatherResources(logf.Log, mg, operatorNs); err == nil {
		t.Fatalf("expected error")
	}
}

func TestCleanupResources_JobDeleteError_ReturnsError(t *testing.T) {
	operatorNs := "must-gather-operator"
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"}}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: operatorNs, UID: "u"}}

	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, job).Build()
	wrap := interceptClient{Client: base, onDelete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
		if _, ok := obj.(*batchv1.Job); ok {
			return errors.New("boom job delete")
		}
		return nil
	}}
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(wrap, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	if err := r.cleanupMustGatherResources(logf.Log, mg, operatorNs); err == nil {
		t.Fatalf("expected error")
	}
}

// ===================== Additional Reconcile scenario tests =====================

func TestReconcile_GetMustGather_NotFound(t *testing.T) {
	s := runtime.NewScheme()
	_ = mustgatherv1alpha1.AddToScheme(s)
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(fake.NewClientBuilder().WithScheme(s).Build(), s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	res, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "x", Namespace: "y"}})
	if err != nil || res != (reconcile.Result{}) {
		t.Fatalf("expected no error and empty result; got %v %v", res, err)
	}
}

func TestReconcile_GetMustGather_Error(t *testing.T) {
	s := runtime.NewScheme()
	_ = mustgatherv1alpha1.AddToScheme(s)
	base := fake.NewClientBuilder().WithScheme(s).Build()
	wrap := interceptClient{Client: base, onGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
		if _, ok := obj.(*mustgatherv1alpha1.MustGather); ok {
			return errors.New("boom get mg")
		}
		return base.Get(ctx, key, obj)
	}}
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(wrap, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "x", Namespace: "y"}})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcile_IsInitialized_UpdateSucceeds(t *testing.T) {
	s := runtime.NewScheme()
	_ = mustgatherv1alpha1.AddToScheme(s)
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns"}}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mg).Build()
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReconcile_IsInitialized_UpdateFails(t *testing.T) {
	s := runtime.NewScheme()
	_ = mustgatherv1alpha1.AddToScheme(s)
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns"}}
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(mg).Build()
	wrap := interceptClient{Client: base, onUpdate: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
		if _, ok := obj.(*mustgatherv1alpha1.MustGather); ok {
			return errors.New("boom update mg")
		}
		return base.Update(ctx, obj, opts...)
	}}
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(wrap, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcile_DeletionTimestamp_Finalizer_KeepOrCleanup(t *testing.T) {
	// Will exercise deletion with cleanup and finalizer removal
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	operatorNs := "must-gather-operator"
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: operatorNs}}
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs, Finalizers: []string{mustGatherFinalizer}, DeletionTimestamp: &metav1.Time{Time: time.Now()}}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"}, ServiceAccountRef: corev1.LocalObjectReference{Name: "default"}}}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, secret).Build()
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// finalizer should be removed
	out := &mustgatherv1alpha1.MustGather{}
	_ = cl.Get(context.TODO(), types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}, out)
	if contains(out.GetFinalizers(), mustGatherFinalizer) {
		t.Fatalf("expected finalizer removed")
	}
}

func TestReconcile_DeletionTimestamp_CleanupError_ReturnsError(t *testing.T) {
	// Force local run mode so operator namespace defaults to must-gather-operator in CI
	t.Setenv("OSDK_FORCE_RUN_MODE", "local")
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	operatorNs := "must-gather-operator"
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs, Finalizers: []string{mustGatherFinalizer}, DeletionTimestamp: &metav1.Time{Time: time.Now()}}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"}, ServiceAccountRef: corev1.LocalObjectReference{Name: "default"}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: operatorNs}}
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, secret).Build()
	wrap := interceptClient{Client: base, onDelete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
		if _, ok := obj.(*corev1.Secret); ok {
			return errors.New("boom secret delete")
		}
		return nil
	}}
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(wrap, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcile_AddFinalizer_UpdateFails(t *testing.T) {
	s := runtime.NewScheme()
	_ = mustgatherv1alpha1.AddToScheme(s)
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns"}}
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(mg).Build()
	wrap := interceptClient{Client: base, onUpdate: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
		if _, ok := obj.(*mustgatherv1alpha1.MustGather); ok {
			return errors.New("boom add finalizer")
		}
		return base.Update(ctx, obj, opts...)
	}}
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(wrap, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcile_GetJobFromInstance_EnvMissing_ReturnsError(t *testing.T) {
	osUnset := os.Getenv("OPERATOR_IMAGE")
	_ = os.Unsetenv("OPERATOR_IMAGE")
	defer os.Setenv("OPERATOR_IMAGE", osUnset)
	s := runtime.NewScheme()
	_ = mustgatherv1alpha1.AddToScheme(s)
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns"}, Spec: mustgatherv1alpha1.MustGatherSpec{ServiceAccountRef: corev1.LocalObjectReference{Name: "default"}}}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mg).Build()
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcile_GetJobFromInstance_ClusterVersionMissing_ReturnsError(t *testing.T) {
	os.Setenv("OPERATOR_IMAGE", "img")
	s := runtime.NewScheme()
	_ = mustgatherv1alpha1.AddToScheme(s)
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns"}, Spec: mustgatherv1alpha1.MustGatherSpec{ServiceAccountRef: corev1.LocalObjectReference{Name: "default"}}}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mg).Build()
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcile_JobNotFound_UserSecretError(t *testing.T) {
	os.Setenv("OPERATOR_IMAGE", "img")
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	_ = configv1.AddToScheme(s)
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"}, ServiceAccountRef: corev1.LocalObjectReference{Name: "default"}}}
	// Provide ClusterVersion for job template
	cv := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}, Status: configv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}}}}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, cv).Build()
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcile_JobNotFound_NewSecretGetOtherError(t *testing.T) {
	os.Setenv("OPERATOR_IMAGE", "img")
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	_ = configv1.AddToScheme(s)
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"}, ServiceAccountRef: corev1.LocalObjectReference{Name: "default"}}}
	userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: mg.Namespace}}
	cv := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}, Status: configv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}}}}
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, userSecret, cv).Build()
	wrap := interceptClient{Client: base, onGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
		if _, ok := obj.(*corev1.Secret); ok && key.Namespace == defaultMustGatherNamespace && key.Name == "sec" {
			return errors.New("boom newSecret get")
		}
		return nil
	}}
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(wrap, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcile_JobActive_CallsUpdateStatus(t *testing.T) {
	os.Setenv("OPERATOR_IMAGE", "img")
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	_ = configv1.AddToScheme(s)
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"}, ServiceAccountRef: corev1.LocalObjectReference{Name: "default"}}}
	userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: mg.Namespace}}
	// Have ClusterVersion for template
	cv := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}, Status: configv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}}}}
	// Pre-create a Job with Active>0 so Get(job1) succeeds
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: mg.Namespace}}
	job.Status.Active = 1
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, userSecret, cv, job).WithStatusSubresource(mg).Build()
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReconcile_Succeeded_RetainTrue_NoCleanup(t *testing.T) {
	os.Setenv("OPERATOR_IMAGE", "img")
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	_ = configv1.AddToScheme(s)
	operatorNs := "must-gather-operator"
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs, Finalizers: []string{mustGatherFinalizer}}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"}, ServiceAccountRef: corev1.LocalObjectReference{Name: "default"}, InternalUser: true}}
	mg.Spec.RetainResourcesOnCompletion = true
	userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: operatorNs}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: operatorNs}}
	job.Status.Succeeded = 1
	cv := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}, Status: configv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}}}}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, userSecret, cv, job).WithStatusSubresource(mg).Build()
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// verify job still exists
	chk := &batchv1.Job{}
	if e := cl.Get(context.TODO(), types.NamespacedName{Namespace: operatorNs, Name: mg.Name}, chk); e != nil {
		t.Fatalf("expected job to remain, err: %v", e)
	}
}

func TestReconcile_Succeeded_CleanupError_ReturnsError(t *testing.T) {
	// Force local run mode so operator namespace defaults to must-gather-operator in CI
	t.Setenv("OSDK_FORCE_RUN_MODE", "local")
	os.Setenv("OPERATOR_IMAGE", "img")
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	_ = configv1.AddToScheme(s)
	operatorNs := "must-gather-operator"
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs, Finalizers: []string{mustGatherFinalizer}}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"}, ServiceAccountRef: corev1.LocalObjectReference{Name: "default"}}}
	userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: operatorNs}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: operatorNs}}
	job.Status.Succeeded = 1
	cv := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}, Status: configv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}}}}
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, userSecret, cv, job).WithStatusSubresource(mg).Build()
	wrap := interceptClient{Client: base, onDelete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
		if _, ok := obj.(*batchv1.Job); ok {
			return errors.New("boom job delete")
		}
		return nil
	}}
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(wrap, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcile_Failed_CleanupError_ReturnsError(t *testing.T) {
	// Force local run mode so operator namespace defaults to must-gather-operator in CI
	t.Setenv("OSDK_FORCE_RUN_MODE", "local")
	os.Setenv("OPERATOR_IMAGE", "img")
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	_ = configv1.AddToScheme(s)
	operatorNs := "must-gather-operator"
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs, Finalizers: []string{mustGatherFinalizer}}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"}, ServiceAccountRef: corev1.LocalObjectReference{Name: "default"}}}
	userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: operatorNs}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: operatorNs}}
	job.Status.Failed = 1
	cv := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}, Status: configv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}}}}
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, userSecret, cv, job).WithStatusSubresource(mg).Build()
	wrap := interceptClient{Client: base, onDelete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
		if _, ok := obj.(*corev1.Secret); ok {
			return errors.New("boom secret delete")
		}
		return nil
	}}
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(wrap, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestReconcile_NoActiveNoTerminal_CallsUpdateStatus(t *testing.T) {
	os.Setenv("OPERATOR_IMAGE", "img")
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	_ = configv1.AddToScheme(s)
	mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}}, Spec: mustgatherv1alpha1.MustGatherSpec{CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"}, ServiceAccountRef: corev1.LocalObjectReference{Name: "default"}}}
	userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: mg.Namespace}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: mg.Namespace}}
	// completion time set -> updateStatus should set Completed=true
	ct := metav1.NewTime(time.Now())
	job.Status.CompletionTime = &ct
	cv := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}, Status: configv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}}}}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, userSecret, cv, job).WithStatusSubresource(mg).Build()
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
	_, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := &mustgatherv1alpha1.MustGather{}
	_ = cl.Get(context.TODO(), types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}, out)
	if !out.Status.Completed {
		t.Fatalf("expected Completed=true from updateStatus")
	}
}

func TestReconcile_Failed_RetainTrue_NoCleanup(t *testing.T) {
	os.Setenv("OPERATOR_IMAGE", "img")
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	_ = configv1.AddToScheme(s)

	mg := &mustgatherv1alpha1.MustGather{
		ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
		Spec: mustgatherv1alpha1.MustGatherSpec{
			CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"},
			ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
		},
	}
	mg.Spec.RetainResourcesOnCompletion = true

	// Pre-create a Job with Failed>0 so Get(job1) succeeds and failed branch is taken
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: mg.Namespace}}
	job.Status.Failed = 1

	// Provide ClusterVersion for getJobFromInstance
	cv := &configv1.ClusterVersion{ObjectMeta: metav1.ObjectMeta{Name: "version"}, Status: configv1.ClusterVersionStatus{History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}}}}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mg, cv, job).WithStatusSubresource(mg).Build()
	r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil)}

	res, err := r.Reconcile(context.TODO(), reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}})
	if err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}
	if res != (reconcile.Result{}) {
		t.Fatalf("expected empty result, got: %+v", res)
	}

	out := &mustgatherv1alpha1.MustGather{}
	if getErr := cl.Get(context.TODO(), types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}, out); getErr != nil {
		t.Fatalf("failed to get mustgather: %v", getErr)
	}
	if !out.Status.Completed || out.Status.Status != "Failed" || out.Status.Reason != "MustGather Job pods failed" {
		t.Fatalf("unexpected status after failed without cleanup: %+v", out.Status)
	}
}
