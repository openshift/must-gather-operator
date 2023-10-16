// DO NOT REMOVE TAGS BELOW. IF ANY NEW TEST FILES ARE CREATED UNDER /osde2e, PLEASE ADD THESE TAGS TO THEM IN ORDER TO BE EXCLUDED FROM UNIT TESTS.
//go:build osde2e
// +build osde2e

package osde2etests

import (
	"context"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	"github.com/openshift/osde2e-common/pkg/clients/openshift"
	. "github.com/openshift/osde2e-common/pkg/gomega/assertions"
	. "github.com/openshift/osde2e-common/pkg/gomega/matchers"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var _ = ginkgo.Describe("must-gather-operator", ginkgo.Ordered, func() {
	var (
		oc                *openshift.Client
		operator          = "must-gather-operator"
		namespace         = "openshift-" + operator
		service           = operator
		rolePrefix        = operator
		clusterRolePrefix = operator
	)

	ginkgo.BeforeEach(func(ctx context.Context) {
		log.SetLogger(ginkgo.GinkgoLogr)

		var err error
		oc, err = openshift.New(ginkgo.GinkgoLogr)
		Expect(err).ShouldNot(HaveOccurred(), "unable to setup openshift client")
		Expect(mustgatherv1alpha1.AddToScheme(oc.GetScheme())).ShouldNot(HaveOccurred(), "failed to register mustgatherv1alpha1 scheme")
	})

	ginkgo.It("is installed", func(ctx context.Context) {
		ginkgo.By("checking the namespace exists")
		err := oc.Get(ctx, namespace, "", &corev1.Namespace{})
		Expect(err).ShouldNot(HaveOccurred(), "namespace %s not found", namespace)

		ginkgo.By("checking the role exists")
		var roles rbacv1.RoleList
		err = oc.WithNamespace(namespace).List(ctx, &roles)
		Expect(err).ShouldNot(HaveOccurred(), "failed to list roles")
		Expect(&roles).Should(ContainItemWithPrefix(rolePrefix), "unable to find roles with prefix %s", rolePrefix)

		ginkgo.By("checking the rolebinding exists")
		var rolebindings rbacv1.RoleBindingList
		err = oc.List(ctx, &rolebindings)
		Expect(err).ShouldNot(HaveOccurred(), "failed to list rolebindings")
		Expect(&rolebindings).Should(ContainItemWithPrefix(rolePrefix), "unable to find rolebindings with prefix %s", rolePrefix)

		ginkgo.By("checking the clusterrole exists")
		var clusterRoles rbacv1.ClusterRoleList
		err = oc.List(ctx, &clusterRoles)
		Expect(err).ShouldNot(HaveOccurred(), "failed to list clusterroles")
		Expect(&clusterRoles).Should(ContainItemWithPrefix(clusterRolePrefix), "unable to find cluster role with prefix %s", clusterRolePrefix)

		ginkgo.By("checking the clusterrolebinding exists")
		var clusterRoleBindings rbacv1.ClusterRoleBindingList
		err = oc.List(ctx, &clusterRoleBindings)
		Expect(err).ShouldNot(HaveOccurred(), "unable to list clusterrolebindings")
		Expect(&clusterRoleBindings).Should(ContainItemWithPrefix(clusterRolePrefix), "unable to find clusterrolebinding with prefix %s", clusterRolePrefix)
		ginkgo.By("checking the service exists")
		err = oc.Get(ctx, service, namespace, &corev1.Service{})
		Expect(err).ShouldNot(HaveOccurred(), "service %s/%s not found", namespace, service)

		ginkgo.By("checking the deployment exists and is available")
		EventuallyDeployment(ctx, oc, operator, namespace).Should(BeAvailable())
	})

	ginkgo.DescribeTable("MustGather can be created", func(ctx context.Context, user, group string) {
		client, err := oc.Impersonate(user, group)
		Expect(err).ShouldNot(HaveOccurred(), "unable to impersonate %s user in %s group", user, group)

		mustgather := &mustgatherv1alpha1.MustGather{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "osde2e-",
				Namespace:    namespace,
			},
			Spec: mustgatherv1alpha1.MustGatherSpec{
				CaseID: "0000000",
				CaseManagementAccountSecretRef: corev1.LocalObjectReference{
					Name: "case-management-creds",
				},
				ServiceAccountRef: corev1.LocalObjectReference{
					Name: "must-gather-admin",
				},
			},
		}

		err = client.Create(ctx, mustgather)
		Expect(err).ShouldNot(HaveOccurred(), "failed to create mustgather as %s", group)

		// TODO: do something with them?

		err = client.Delete(ctx, mustgather)
		Expect(err).ShouldNot(HaveOccurred(), "failed to delete mustgather as %s", group)
	},
		ginkgo.Entry("by backplane-cluster-admin", "backplane-cluster-admin", ""),
		ginkgo.Entry("by openshift-backplane-cee", "test@redhat.com", "system:serviceaccounts:openshift-backplane-cee"),
	)

	ginkgo.It("can be upgraded", func(ctx context.Context) {
		ginkgo.By("forcing operator upgrade")
		err := oc.UpgradeOperator(ctx, operator, namespace)
		Expect(err).NotTo(HaveOccurred(), "operator upgrade failed")
	})
})
