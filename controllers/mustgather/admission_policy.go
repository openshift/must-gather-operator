package mustgather

import (
	"context"

	"github.com/go-logr/logr"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const blockMustGatherRestrictedNamespacesPolicyName = "block-mustgather-restricted-namespaces"

func blockMustGatherRestrictedNamespacesPolicy() *admissionregistrationv1.ValidatingAdmissionPolicy {
	failurePolicy := admissionregistrationv1.Fail
	return &admissionregistrationv1.ValidatingAdmissionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: blockMustGatherRestrictedNamespacesPolicyName,
		},
		Spec: admissionregistrationv1.ValidatingAdmissionPolicySpec{
			FailurePolicy: &failurePolicy,
			MatchConstraints: &admissionregistrationv1.MatchResources{
				ResourceRules: []admissionregistrationv1.NamedRuleWithOperations{
					{
						RuleWithOperations: admissionregistrationv1.RuleWithOperations{
							Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
							Rule: admissionregistrationv1.Rule{
								APIGroups:   []string{"operator.openshift.io"},
								APIVersions: []string{"v1alpha1", "v1"},
								Resources:   []string{"mustgathers"},
							},
						},
					},
				},
			},
			Validations: []admissionregistrationv1.Validation{
				{
					Expression: "object.metadata.namespace != 'openshift'",
					Message:    "MustGather resources cannot be created in the openshift namespace.",
				},
				{
					Expression: "!object.metadata.namespace.startsWith('openshift-')",
					Message:    "MustGather resources cannot be created in openshift-* namespaces.",
				},
				{
					Expression: "object.metadata.namespace != 'kube'",
					Message:    "MustGather resources cannot be created in the kube namespace.",
				},
				{
					Expression: "!object.metadata.namespace.startsWith('kube-')",
					Message:    "MustGather resources cannot be created in kube-* namespaces.",
				},
				{
					Expression: "object.metadata.namespace != 'hypershift'",
					Message:    "MustGather resources cannot be created in the hypershift namespace.",
				},
				{
					Expression: "!object.metadata.namespace.startsWith('hypershift-')",
					Message:    "MustGather resources cannot be created in hypershift-* namespaces.",
				},
			},
		},
	}
}

func blockMustGatherRestrictedNamespacesPolicyBinding() *admissionregistrationv1.ValidatingAdmissionPolicyBinding {
	return &admissionregistrationv1.ValidatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: blockMustGatherRestrictedNamespacesPolicyName,
		},
		Spec: admissionregistrationv1.ValidatingAdmissionPolicyBindingSpec{
			PolicyName:        blockMustGatherRestrictedNamespacesPolicyName,
			ValidationActions: []admissionregistrationv1.ValidationAction{admissionregistrationv1.Deny},
		},
	}
}

func (r *MustGatherReconciler) ensureAdmissionPolicy(ctx context.Context, c client.Client, reader client.Reader) error {
	setupLog := log.WithName("admission-policy")

	if err := r.ensureVAP(ctx, c, reader, setupLog); err != nil {
		setupLog.Error(err, "failed to ensure ValidatingAdmissionPolicy (non-fatal)")
	}

	if err := r.ensureVAPBinding(ctx, c, reader, setupLog); err != nil {
		setupLog.Error(err, "failed to ensure ValidatingAdmissionPolicyBinding (non-fatal)")
	}

	return nil
}

func (r *MustGatherReconciler) ensureVAP(ctx context.Context, c client.Client, reader client.Reader, setupLog logr.Logger) error {
	vap := blockMustGatherRestrictedNamespacesPolicy()

	existing := &admissionregistrationv1.ValidatingAdmissionPolicy{}
	err := reader.Get(ctx, client.ObjectKeyFromObject(vap), existing)
	if errors.IsNotFound(err) {
		setupLog.Info("creating ValidatingAdmissionPolicy", "name", vap.Name)
		return c.Create(ctx, vap)
	} else if err != nil {
		return err
	} else if !equality.Semantic.DeepEqual(existing.Spec, vap.Spec) {
		setupLog.Info("updating ValidatingAdmissionPolicy", "name", vap.Name)
		existing.Spec = vap.Spec
		return c.Update(ctx, existing)
	}
	return nil
}

func (r *MustGatherReconciler) ensureVAPBinding(ctx context.Context, c client.Client, reader client.Reader, setupLog logr.Logger) error {
	binding := blockMustGatherRestrictedNamespacesPolicyBinding()

	existingBinding := &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
	err := reader.Get(ctx, client.ObjectKeyFromObject(binding), existingBinding)
	if errors.IsNotFound(err) {
		setupLog.Info("creating ValidatingAdmissionPolicyBinding", "name", binding.Name)
		return c.Create(ctx, binding)
	} else if err != nil {
		return err
	} else if !equality.Semantic.DeepEqual(existingBinding.Spec, binding.Spec) {
		setupLog.Info("updating ValidatingAdmissionPolicyBinding", "name", binding.Name)
		existingBinding.Spec = binding.Spec
		return c.Update(ctx, existingBinding)
	}
	return nil
}
