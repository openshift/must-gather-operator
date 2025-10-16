package mustgather

import (
	"context"
	"errors"
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

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func generateFakeClient(objs ...runtime.Object) (client.Client, *runtime.Scheme) {
	s := scheme.Scheme
	s.AddKnownTypes(mustgatherv1alpha1.GroupVersion, &mustgatherv1alpha1.MustGather{})
	cl := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build()
	return cl, s
}

func TestMustGatherController(t *testing.T) {
	mgObj := createMustGatherObject()
	secObj := createMustGatherSecretObject()

	objs := []runtime.Object{
		mgObj,
		secObj,
	}

	var cfg *rest.Config

	cl, s := generateFakeClient(objs...)

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

func TestIsValid(t *testing.T) {
	// A mock Reconciler is needed to call IsValid
	r := &MustGatherReconciler{}

	testCases := []struct {
		name          string
		mustGather    *mustgatherv1alpha1.MustGather
		expectedValid bool
		expectedError error
	}{
		{
			name: "Default image should be valid without hosted cluster info",
			mustGather: &mustgatherv1alpha1.MustGather{
				Spec: mustgatherv1alpha1.MustGatherSpec{
					MustGatherImage: "default",
				},
			},
			expectedValid: true,
			expectedError: nil,
		},
		{
			name: "ACM HCP image should be valid with all required info",
			mustGather: &mustgatherv1alpha1.MustGather{
				Spec: mustgatherv1alpha1.MustGatherSpec{
					MustGatherImage:        "acm_hcp",
					HostedClusterNamespace: "test-ns",
					HostedClusterName:      "test-cluster",
				},
			},
			expectedValid: true,
			expectedError: nil,
		},
		{
			name: "ACM HCP image should be invalid without hosted cluster name",
			mustGather: &mustgatherv1alpha1.MustGather{
				Spec: mustgatherv1alpha1.MustGatherSpec{
					MustGatherImage:        "acm_hcp",
					HostedClusterNamespace: "test-ns",
				},
			},
			expectedValid: false,
			expectedError: errors.New("hostedClusterNamespace and hostedClusterName must be set when using the acm_hcp must gather image"),
		},
		{
			name: "ACM HCP image should be invalid without hosted cluster namespace",
			mustGather: &mustgatherv1alpha1.MustGather{
				Spec: mustgatherv1alpha1.MustGatherSpec{
					MustGatherImage:   "acm_hcp",
					HostedClusterName: "test-cluster",
				},
			},
			expectedValid: false,
			expectedError: errors.New("hostedClusterNamespace and hostedClusterName must be set when using the acm_hcp must gather image"),
		},
		{
			name: "ACM HCP image should be invalid without any hosted cluster info",
			mustGather: &mustgatherv1alpha1.MustGather{
				Spec: mustgatherv1alpha1.MustGatherSpec{
					MustGatherImage: "acm_hcp",
				},
			},
			expectedValid: false,
			expectedError: errors.New("hostedClusterNamespace and hostedClusterName must be set when using the acm_hcp must gather image"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			valid, err := r.IsValid(tc.mustGather)

			if valid != tc.expectedValid {
				t.Errorf("Expected valid to be %v, but got %v", tc.expectedValid, valid)
			}

			if (err != nil && tc.expectedError == nil) || (err == nil && tc.expectedError != nil) || (err != nil && tc.expectedError != nil && err.Error() != tc.expectedError.Error()) {
				t.Errorf("Expected error '%v', but got '%v'", tc.expectedError, err)
			}
		})
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
