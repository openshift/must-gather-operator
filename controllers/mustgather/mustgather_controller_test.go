package mustgather

import (
	"context"
	configv1 "github.com/openshift/api/config/v1"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"testing"

	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
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
	os.Setenv("JOB_TEMPLATE_FILE_NAME", "../../../build/templates/job.template.yaml")

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
			Namespace: "openshift-must-gather-operator",
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
			Namespace: "openshift-must-gather-operator",
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
