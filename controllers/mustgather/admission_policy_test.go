package mustgather

import (
	"context"
	"testing"

	"github.com/redhat-cop/operator-utils/pkg/util"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func newAdmissionPolicyReconciler(t *testing.T, objects []client.Object) *MustGatherReconciler {
	t.Helper()

	s := runtime.NewScheme()
	if err := admissionregistrationv1.AddToScheme(s); err != nil {
		t.Fatalf("add admissionregistrationv1 to scheme: %v", err)
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()
	return &MustGatherReconciler{
		ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil),
	}
}

func TestBlockMustGatherRestrictedNamespacesPolicy(t *testing.T) {
	vap := blockMustGatherRestrictedNamespacesPolicy()

	if vap.Name != "block-mustgather-restricted-namespaces" {
		t.Fatalf("expected policy name %q, got %q", "block-mustgather-restricted-namespaces", vap.Name)
	}

	if vap.Spec.FailurePolicy == nil || *vap.Spec.FailurePolicy != admissionregistrationv1.Fail {
		t.Fatal("expected failurePolicy to be Fail")
	}

	if vap.Spec.MatchConstraints == nil {
		t.Fatal("expected matchConstraints to be set")
	}
	if len(vap.Spec.MatchConstraints.ResourceRules) != 1 {
		t.Fatalf("expected 1 resourceRule, got %d", len(vap.Spec.MatchConstraints.ResourceRules))
	}

	rule := vap.Spec.MatchConstraints.ResourceRules[0]
	if len(rule.Operations) != 1 || rule.Operations[0] != admissionregistrationv1.Create {
		t.Fatalf("expected single CREATE operation, got %v", rule.Operations)
	}
	if len(rule.APIGroups) != 1 || rule.APIGroups[0] != "operator.openshift.io" {
		t.Fatalf("expected apiGroup operator.openshift.io, got %v", rule.APIGroups)
	}
	expectedVersions := []string{"v1alpha1", "v1"}
	if len(rule.APIVersions) != len(expectedVersions) {
		t.Fatalf("expected apiVersions %v, got %v", expectedVersions, rule.APIVersions)
	}
	for i, v := range expectedVersions {
		if rule.APIVersions[i] != v {
			t.Fatalf("expected apiVersions[%d] = %q, got %q", i, v, rule.APIVersions[i])
		}
	}
	if len(rule.Resources) != 1 || rule.Resources[0] != "mustgathers" {
		t.Fatalf("expected resource mustgathers, got %v", rule.Resources)
	}

	if len(vap.Spec.Validations) != 6 {
		t.Fatalf("expected 6 validations, got %d", len(vap.Spec.Validations))
	}

	expectedExpressions := []string{
		"object.metadata.namespace != 'openshift'",
		"!object.metadata.namespace.startsWith('openshift-')",
		"object.metadata.namespace != 'kube'",
		"!object.metadata.namespace.startsWith('kube-')",
		"object.metadata.namespace != 'hypershift'",
		"!object.metadata.namespace.startsWith('hypershift-')",
	}
	for i, expected := range expectedExpressions {
		if vap.Spec.Validations[i].Expression != expected {
			t.Errorf("validation[%d]: expected expression %q, got %q", i, expected, vap.Spec.Validations[i].Expression)
		}
	}
}

func TestBlockMustGatherRestrictedNamespacesPolicyBinding(t *testing.T) {
	binding := blockMustGatherRestrictedNamespacesPolicyBinding()

	if binding.Name != "block-mustgather-restricted-namespaces" {
		t.Fatalf("expected binding name %q, got %q", "block-mustgather-restricted-namespaces", binding.Name)
	}
	if binding.Spec.PolicyName != "block-mustgather-restricted-namespaces" {
		t.Fatalf("expected policyName %q, got %q", "block-mustgather-restricted-namespaces", binding.Spec.PolicyName)
	}
	if len(binding.Spec.ValidationActions) != 1 || binding.Spec.ValidationActions[0] != admissionregistrationv1.Deny {
		t.Fatalf("expected single Deny validationAction, got %v", binding.Spec.ValidationActions)
	}
}

func TestEnsureVAP(t *testing.T) {
	tests := []struct {
		name      string
		objects   []client.Object
		postCheck func(t *testing.T, cl client.Client)
	}{
		{
			name:    "creates when missing",
			objects: nil,
			postCheck: func(t *testing.T, cl client.Client) {
				vap := &admissionregistrationv1.ValidatingAdmissionPolicy{}
				if err := cl.Get(context.TODO(), client.ObjectKey{Name: blockMustGatherRestrictedNamespacesPolicyName}, vap); err != nil {
					t.Fatalf("expected VAP to be created, got: %v", err)
				}
				if len(vap.Spec.Validations) != 6 {
					t.Fatalf("expected 6 validations, got %d", len(vap.Spec.Validations))
				}
			},
		},
		{
			name: "updates when spec differs",
			objects: []client.Object{
				&admissionregistrationv1.ValidatingAdmissionPolicy{
					ObjectMeta: metav1.ObjectMeta{Name: blockMustGatherRestrictedNamespacesPolicyName},
					Spec: admissionregistrationv1.ValidatingAdmissionPolicySpec{
						Validations: []admissionregistrationv1.Validation{
							{Expression: "true", Message: "placeholder"},
						},
					},
				},
			},
			postCheck: func(t *testing.T, cl client.Client) {
				vap := &admissionregistrationv1.ValidatingAdmissionPolicy{}
				if err := cl.Get(context.TODO(), client.ObjectKey{Name: blockMustGatherRestrictedNamespacesPolicyName}, vap); err != nil {
					t.Fatalf("expected VAP to exist: %v", err)
				}
				if len(vap.Spec.Validations) != 6 {
					t.Fatalf("expected spec to be updated to 6 validations, got %d", len(vap.Spec.Validations))
				}
			},
		},
		{
			name: "no-op when unchanged",
			objects: func() []client.Object {
				return []client.Object{blockMustGatherRestrictedNamespacesPolicy()}
			}(),
			postCheck: func(t *testing.T, cl client.Client) {
				vap := &admissionregistrationv1.ValidatingAdmissionPolicy{}
				if err := cl.Get(context.TODO(), client.ObjectKey{Name: blockMustGatherRestrictedNamespacesPolicyName}, vap); err != nil {
					t.Fatalf("expected VAP to exist: %v", err)
				}
				if len(vap.Spec.Validations) != 6 {
					t.Fatalf("expected 6 validations, got %d", len(vap.Spec.Validations))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newAdmissionPolicyReconciler(t, tt.objects)
			err := r.ensureVAP(context.TODO(), r.GetClient(), r.GetClient(), logf.Log)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.postCheck(t, r.GetClient())
		})
	}
}

func TestEnsureVAPBinding(t *testing.T) {
	tests := []struct {
		name      string
		objects   []client.Object
		postCheck func(t *testing.T, cl client.Client)
	}{
		{
			name:    "creates when missing",
			objects: nil,
			postCheck: func(t *testing.T, cl client.Client) {
				binding := &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
				if err := cl.Get(context.TODO(), client.ObjectKey{Name: blockMustGatherRestrictedNamespacesPolicyName}, binding); err != nil {
					t.Fatalf("expected binding to be created, got: %v", err)
				}
				if binding.Spec.PolicyName != blockMustGatherRestrictedNamespacesPolicyName {
					t.Fatalf("expected policyName %q, got %q", blockMustGatherRestrictedNamespacesPolicyName, binding.Spec.PolicyName)
				}
			},
		},
		{
			name: "updates when spec differs",
			objects: []client.Object{
				&admissionregistrationv1.ValidatingAdmissionPolicyBinding{
					ObjectMeta: metav1.ObjectMeta{Name: blockMustGatherRestrictedNamespacesPolicyName},
					Spec: admissionregistrationv1.ValidatingAdmissionPolicyBindingSpec{
						PolicyName:        "wrong-policy",
						ValidationActions: []admissionregistrationv1.ValidationAction{admissionregistrationv1.Warn},
					},
				},
			},
			postCheck: func(t *testing.T, cl client.Client) {
				binding := &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
				if err := cl.Get(context.TODO(), client.ObjectKey{Name: blockMustGatherRestrictedNamespacesPolicyName}, binding); err != nil {
					t.Fatalf("expected binding to exist: %v", err)
				}
				if binding.Spec.PolicyName != blockMustGatherRestrictedNamespacesPolicyName {
					t.Fatalf("expected policyName to be updated, got %q", binding.Spec.PolicyName)
				}
				if len(binding.Spec.ValidationActions) != 1 || binding.Spec.ValidationActions[0] != admissionregistrationv1.Deny {
					t.Fatalf("expected Deny action, got %v", binding.Spec.ValidationActions)
				}
			},
		},
		{
			name: "no-op when unchanged",
			objects: func() []client.Object {
				return []client.Object{blockMustGatherRestrictedNamespacesPolicyBinding()}
			}(),
			postCheck: func(t *testing.T, cl client.Client) {
				binding := &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
				if err := cl.Get(context.TODO(), client.ObjectKey{Name: blockMustGatherRestrictedNamespacesPolicyName}, binding); err != nil {
					t.Fatalf("expected binding to exist: %v", err)
				}
				if binding.Spec.PolicyName != blockMustGatherRestrictedNamespacesPolicyName {
					t.Fatalf("expected policyName %q, got %q", blockMustGatherRestrictedNamespacesPolicyName, binding.Spec.PolicyName)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newAdmissionPolicyReconciler(t, tt.objects)
			err := r.ensureVAPBinding(context.TODO(), r.GetClient(), r.GetClient(), logf.Log)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.postCheck(t, r.GetClient())
		})
	}
}
