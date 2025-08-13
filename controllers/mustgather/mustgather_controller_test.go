package mustgather

import (
	"context"
	"errors"
	"testing"

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

func TestReconcile_JobStatusVariants_StatusUpdateSucceeds(t *testing.T) {

	t.Setenv("OPERATOR_IMAGE", "quay.io/openshift/must-gather-operator:latest")

	baseMustGather := func(namespace string) *mustgatherv1alpha1.MustGather {
		return &mustgatherv1alpha1.MustGather{
			TypeMeta: metav1.TypeMeta{Kind: "MustGather"},
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-must-gather",
				Namespace:  namespace,
				Finalizers: []string{mustGatherFinalizer},
			},
			Spec: mustgatherv1alpha1.MustGatherSpec{
				CaseID:                         "01234567",
				CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "case-management-creds"},
				ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
			},
		}
	}

	jobFor := func(name, namespace string, active, succeeded, failed int32) *batchv1.Job {
		return &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Status:     batchv1.JobStatus{Active: active, Succeeded: succeeded, Failed: failed},
		}
	}

	clusterVersionObj := func() *configv1.ClusterVersion {
		return &configv1.ClusterVersion{
			ObjectMeta: metav1.ObjectMeta{Name: "version"},
			Status: configv1.ClusterVersionStatus{
				History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
			},
		}
	}

	setup := func(objs ...client.Object) (*MustGatherReconciler, *mustgatherv1alpha1.MustGather) {
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

		var mg *mustgatherv1alpha1.MustGather
		for _, o := range objs {
			if v, ok := o.(*mustgatherv1alpha1.MustGather); ok {
				mg = v
				break
			}
		}
		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithStatusSubresource(mg).Build()
		r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil)}
		return r, mg
	}

	run := func(t *testing.T, active, succeeded, failed int32, assert func(t *testing.T, got reconcile.Result, err error, r *MustGatherReconciler, mg *mustgatherv1alpha1.MustGather)) {
		ns := "must-gather-operator"
		mg := baseMustGather(ns)
		job := jobFor(mg.Name, mg.Namespace, active, succeeded, failed)
		cv := clusterVersionObj()
		r, _ := setup(mg, job, cv)

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}}
		res, err := r.Reconcile(context.TODO(), req)
		assert(t, res, err, r, mg)
	}

	// Active > 0
	t.Run("job active", func(t *testing.T) {
		run(t, 1, 0, 0, func(t *testing.T, got reconcile.Result, err error, r *MustGatherReconciler, mg *mustgatherv1alpha1.MustGather) {
			if err != nil {
				t.Fatalf("unexpected reconcile error: %v", err)
			}
			if got != (reconcile.Result{}) {
				t.Fatalf("expected empty result, got: %+v", got)
			}
			out := &mustgatherv1alpha1.MustGather{}
			if getErr := r.GetClient().Get(context.TODO(), types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}, out); getErr != nil {
				t.Fatalf("failed to get mustgather: %v", getErr)
			}
			if out.Status.Completed {
				t.Fatalf("expected Completed=false for active job, got true")
			}
		})
	})

	// Succeeded > 0
	t.Run("job succeeded", func(t *testing.T) {
		run(t, 0, 1, 0, func(t *testing.T, got reconcile.Result, err error, r *MustGatherReconciler, mg *mustgatherv1alpha1.MustGather) {
			if err != nil {
				t.Fatalf("unexpected reconcile error: %v", err)
			}
			if got != (reconcile.Result{}) {
				t.Fatalf("expected empty result, got: %+v", got)
			}
			out := &mustgatherv1alpha1.MustGather{}
			if getErr := r.GetClient().Get(context.TODO(), types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}, out); getErr != nil {
				t.Fatalf("failed to get mustgather: %v", getErr)
			}
			if !out.Status.Completed || out.Status.Status != "Completed" || out.Status.Reason != "MustGather Job pods succeeded" {
				t.Fatalf("unexpected status after success: %+v", out.Status)
			}
		})
	})

	// Failed > 0
	t.Run("job failed", func(t *testing.T) {
		run(t, 0, 0, 1, func(t *testing.T, got reconcile.Result, err error, r *MustGatherReconciler, mg *mustgatherv1alpha1.MustGather) {
			if err != nil {
				t.Fatalf("unexpected reconcile error: %v", err)
			}
			if got != (reconcile.Result{}) {
				t.Fatalf("expected empty result, got: %+v", got)
			}
			out := &mustgatherv1alpha1.MustGather{}
			if getErr := r.GetClient().Get(context.TODO(), types.NamespacedName{Name: mg.Name, Namespace: mg.Namespace}, out); getErr != nil {
				t.Fatalf("failed to get mustgather: %v", getErr)
			}
			if !out.Status.Completed || out.Status.Status != "Failed" || out.Status.Reason != "MustGather Job pods failed" {
				t.Fatalf("unexpected status after failure: %+v", out.Status)
			}
		})
	})
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
	mg := &mustgatherv1alpha1.MustGather{
		ObjectMeta: metav1.ObjectMeta{Name: "test-must-gather", Namespace: "must-gather-operator", Finalizers: []string{mustGatherFinalizer}},
		Spec: mustgatherv1alpha1.MustGatherSpec{
			CaseID:                         "01234567",
			CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "case-management-creds"},
			ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
		},
	}
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

	mg := &mustgatherv1alpha1.MustGather{
		ObjectMeta: metav1.ObjectMeta{Name: "test-must-gather", Namespace: "must-gather-operator", Finalizers: []string{mustGatherFinalizer}},
		Spec: mustgatherv1alpha1.MustGatherSpec{
			CaseID:                         "01234567",
			CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "case-management-creds"},
			ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
		},
	}
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
