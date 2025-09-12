package mustgather

import (
	"context"
	"testing"

	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/rest"

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


