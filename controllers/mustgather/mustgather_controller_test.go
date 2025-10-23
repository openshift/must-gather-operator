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
	t.Setenv("OPERATOR_IMAGE", "test-image")

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

func TestMustGatherControllerWithUploadTarget(t *testing.T) {
	tests := []struct {
		name                  string
		mustGather            *mustgatherv1alpha1.MustGather
		expectedContainers    int
		expectUploadContainer bool
	}{
		{
			name:                  "With UploadTarget",
			mustGather:            createMustGatherObjectWithUploadTarget(),
			expectedContainers:    2,
			expectUploadContainer: true,
		},
		{
			name:                  "Without UploadTarget",
			mustGather:            createMustGatherObjectWithoutUploadTarget(),
			expectedContainers:    1,
			expectUploadContainer: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPERATOR_IMAGE", "test-image")
			secObj := createMustGatherSecretObject()
			objs := []runtime.Object{tt.mustGather, secObj}
			cl, s := generateFakeClient(objs...)
			eventRec := &record.FakeRecorder{}
			var cfg *rest.Config

			r := MustGatherReconciler{
				ReconcilerBase: util.NewReconcilerBase(cl, s, cfg, eventRec, nil),
			}

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.mustGather.Name,
					Namespace: tt.mustGather.Namespace,
				},
			}

			_, err := r.Reconcile(context.TODO(), req)
			if err != nil {
				t.Fatalf("reconcile: (%v)", err)
			}

			job, err := r.getJobFromInstance(tt.mustGather)
			if err != nil {
				t.Fatalf("getJobFromInstance : (%v)", err)
			}

			if len(job.Spec.Template.Spec.Containers) != tt.expectedContainers {
				t.Errorf("expected %d containers, got %d", tt.expectedContainers, len(job.Spec.Template.Spec.Containers))
			}

			hasUploadContainer := false
			for _, container := range job.Spec.Template.Spec.Containers {
				if container.Name == "upload" {
					hasUploadContainer = true
					break
				}
			}

			if hasUploadContainer != tt.expectUploadContainer {
				t.Errorf("expected upload container to be %v, but it was %v", tt.expectUploadContainer, hasUploadContainer)
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
			ServiceAccountRef: corev1.LocalObjectReference{
				Name: "",
			},
		},
	}
}

func createMustGatherObjectWithUploadTarget() *mustgatherv1alpha1.MustGather {
	mg := createMustGatherObject()
	mg.Spec.UploadTarget = &mustgatherv1alpha1.UploadTargetSpec{
		Type: mustgatherv1alpha1.UploadTypeSFTP,
		SFTP: &mustgatherv1alpha1.SFTPSpec{
			CaseID: "01234567",
			CaseManagementAccountSecretRef: corev1.LocalObjectReference{
				Name: "case-management-creds",
			},
			InternalUser: true,
			Host:         "sftp.example.com",
		},
	}
	return mg
}

func createMustGatherObjectWithoutUploadTarget() *mustgatherv1alpha1.MustGather {
	mg := createMustGatherObject()
	mg.Spec.UploadTarget = nil
	return mg
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
