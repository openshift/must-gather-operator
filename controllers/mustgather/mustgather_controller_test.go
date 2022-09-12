package mustgather

import (
	"context"
	"os"
	"testing"

	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"

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

	s := scheme.Scheme
	s.AddKnownTypes(mustgatherv1alpha1.GroupVersion, mgObj)

	cl := fake.NewFakeClientWithScheme(s, objs...)

	r := MustGatherReconciler{
		Scheme: s,
		Client: cl,
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
				Name: "must-gather-admin",
			},
			MustGatherImages: []string{"quay.io/openshift/origin-must-gather:latest"},
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
