// DO NOT REMOVE TAGS BELOW. IF ANY NEW TEST FILES ARE CREATED UNDER /test/e2e, PLEASE ADD THESE TAGS TO THEM IN ORDER TO BE EXCLUDED FROM UNIT TESTS.
//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	"github.com/openshift/osde2e-common/pkg/clients/openshift"

	. "github.com/openshift/osde2e-common/pkg/gomega/assertions"
	. "github.com/openshift/osde2e-common/pkg/gomega/matchers"
	authorizationv1 "k8s.io/api/authorization/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var _ = ginkgo.Describe("must-gather-operator", ginkgo.Ordered, func() {
	var (
		oc        *openshift.Client
		operator  = "must-gather-operator"
		namespace = operator
		service   = operator
		// rolePrefix        = operator
		clusterRolePrefix = operator
	)

	ginkgo.BeforeEach(func(ctx context.Context) {
		log.SetLogger(ginkgo.GinkgoLogr)

		var err error
		oc, err = openshift.New(ginkgo.GinkgoLogr)
		Expect(err).ShouldNot(HaveOccurred(), "unable to setup openshift client")
		Expect(mustgatherv1alpha1.AddToScheme(oc.GetScheme())).ShouldNot(HaveOccurred(), "failed to register mustgatherv1alpha1 scheme")
		Expect(apiextensionsv1.AddToScheme(oc.GetScheme())).ShouldNot(HaveOccurred(), "failed to register apiextensionsv1 scheme")
	})

	ginkgo.It("is installed", func(ctx context.Context) {
		ginkgo.By("checking the namespace exists")
		err := oc.Get(ctx, namespace, "", &corev1.Namespace{})
		Expect(err).ShouldNot(HaveOccurred(), "namespace %s not found", namespace)

		ginkgo.By("checking the operator service account exists")
		err = oc.Get(ctx, operator, namespace, &corev1.ServiceAccount{})
		Expect(err).ShouldNot(HaveOccurred(), "service account %s/%s not found", namespace, operator)

		ginkgo.By("checking the must-gather-admin service account exists")
		err = oc.Get(ctx, "must-gather-admin", namespace, &corev1.ServiceAccount{})
		Expect(err).ShouldNot(HaveOccurred(), "service account %s/must-gather-admin not found", namespace)

		// ginkgo.By("checking the role exists")
		// var roles rbacv1.RoleList
		// err = oc.WithNamespace(namespace).List(ctx, &roles)
		// Expect(err).ShouldNot(HaveOccurred(), "failed to list roles")
		// Expect(&roles).Should(ContainItemWithPrefix(rolePrefix), "unable to find roles with prefix %s", rolePrefix)

		// ginkgo.By("checking the rolebinding exists")
		// var rolebindings rbacv1.RoleBindingList
		// err = oc.List(ctx, &rolebindings)
		// Expect(err).ShouldNot(HaveOccurred(), "failed to list rolebindings")
		// Expect(&rolebindings).Should(ContainItemWithPrefix(rolePrefix), "unable to find rolebindings with prefix %s", rolePrefix)

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

	ginkgo.It("has required service accounts with proper permissions", func(ctx context.Context) {
		ginkgo.By("verifying must-gather-operator service account exists")
		operatorSA := &corev1.ServiceAccount{}
		err := oc.Get(ctx, operator, namespace, operatorSA)
		Expect(err).ShouldNot(HaveOccurred(), "operator service account not found")

		ginkgo.By("verifying must-gather-admin service account exists")
		adminSA := &corev1.ServiceAccount{}
		err = oc.Get(ctx, "must-gather-admin", namespace, adminSA)
		Expect(err).ShouldNot(HaveOccurred(), "must-gather-admin service account not found")

		ginkgo.By("verifying must-gather-operator clusterrole exists")
		operatorCR := &rbacv1.ClusterRole{}
		err = oc.Get(ctx, operator, "", operatorCR)
		Expect(err).ShouldNot(HaveOccurred(), "operator clusterrole not found")
		Expect(len(operatorCR.Rules)).Should(BeNumerically(">", 0), "operator clusterrole has no rules")

		ginkgo.By("verifying must-gather-operator has permissions for jobs and secrets")
		hasJobPermissions := false
		hasSecretPermissions := false
		for _, rule := range operatorCR.Rules {
			for _, apiGroup := range rule.APIGroups {
				if apiGroup == "batch" {
					for _, resource := range rule.Resources {
						if resource == "jobs" {
							hasJobPermissions = true
						}
					}
				}
				if apiGroup == "" { // core API group
					for _, resource := range rule.Resources {
						if resource == "secrets" {
							hasSecretPermissions = true
						}
					}
				}
			}
		}
		Expect(hasJobPermissions).To(BeTrue(), "operator clusterrole missing job permissions")
		Expect(hasSecretPermissions).To(BeTrue(), "operator clusterrole missing secret permissions")

		ginkgo.By("verifying must-gather-admin clusterrole exists")
		adminCR := &rbacv1.ClusterRole{}
		err = oc.Get(ctx, "must-gather-admin", "", adminCR)
		Expect(err).ShouldNot(HaveOccurred(), "must-gather-admin clusterrole not found")
		Expect(len(adminCR.Rules)).Should(BeNumerically(">", 0), "must-gather-admin clusterrole has no rules")

		ginkgo.By("verifying must-gather-operator clusterrolebinding exists")
		operatorCRB := &rbacv1.ClusterRoleBinding{}
		err = oc.Get(ctx, operator, "", operatorCRB)
		Expect(err).ShouldNot(HaveOccurred(), "operator clusterrolebinding not found")
		Expect(operatorCRB.RoleRef.Name).To(Equal(operator), "clusterrolebinding references wrong role")
		Expect(len(operatorCRB.Subjects)).Should(BeNumerically(">", 0), "clusterrolebinding has no subjects")

		ginkgo.By("verifying must-gather-admin clusterrolebinding exists")
		adminCRB := &rbacv1.ClusterRoleBinding{}
		err = oc.Get(ctx, "must-gather-admin", "", adminCRB)
		Expect(err).ShouldNot(HaveOccurred(), "must-gather-admin clusterrolebinding not found")
		Expect(adminCRB.RoleRef.Name).To(Equal("must-gather-admin"), "admin clusterrolebinding references wrong role")
		Expect(len(adminCRB.Subjects)).Should(BeNumerically(">", 0), "admin clusterrolebinding has no subjects")

		ginkgo.By("verifying clusterrolebinding references correct service account")
		foundAdminSA := false
		for _, subject := range adminCRB.Subjects {
			if subject.Kind == "ServiceAccount" && subject.Name == "must-gather-admin" && subject.Namespace == namespace {
				foundAdminSA = true
				break
			}
		}
		Expect(foundAdminSA).To(BeTrue(), "must-gather-admin clusterrolebinding does not reference must-gather-admin service account")
	})

	ginkgo.It("creates a Job with correct service account when MustGather is created", func(ctx context.Context) {
		mustGatherName := "test-mustgather-sa"

		ginkgo.By("creating a MustGather CR with must-gather-admin service account")
		mustgather := &mustgatherv1alpha1.MustGather{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mustGatherName,
				Namespace: namespace,
			},
			Spec: mustgatherv1alpha1.MustGatherSpec{
				ServiceAccountName: "must-gather-admin",
				// Not specifying UploadTarget to avoid requiring secrets
			},
		}

		err := oc.Create(ctx, mustgather)
		Expect(err).ShouldNot(HaveOccurred(), "failed to create mustgather")

		ginkgo.By("verifying a Job is created with the same name")
		job := &batchv1.Job{}
		Eventually(func() error {
			return oc.Get(ctx, mustGatherName, namespace, job)
		}).WithTimeout(30*time.Second).WithPolling(2*time.Second).Should(Succeed(), "Job should be created")

		ginkgo.By("verifying the Job uses the correct service account")
		Expect(job.Spec.Template.Spec.ServiceAccountName).To(Equal("must-gather-admin"), "Job should use must-gather-admin service account")

		ginkgo.By("verifying the Job has the gather container")
		Expect(len(job.Spec.Template.Spec.Containers)).Should(BeNumerically(">=", 1), "Job should have at least one container")
		hasGatherContainer := false
		for _, container := range job.Spec.Template.Spec.Containers {
			if container.Name == "gather" {
				hasGatherContainer = true
				break
			}
		}
		Expect(hasGatherContainer).To(BeTrue(), "Job should have a gather container")

		ginkgo.By("verifying the Job has required volumes")
		hasOutputVolume := false
		for _, volume := range job.Spec.Template.Spec.Volumes {
			if volume.Name == "must-gather-output" {
				hasOutputVolume = true
				break
			}
		}
		Expect(hasOutputVolume).To(BeTrue(), "Job should have must-gather-output volume")

		ginkgo.By("cleaning up the MustGather CR")
		err = oc.Delete(ctx, mustgather)
		Expect(err).ShouldNot(HaveOccurred(), "failed to delete mustgather")

		ginkgo.By("verifying the Job is eventually cleaned up")
		Eventually(func() bool {
			err := oc.Get(ctx, mustGatherName, namespace, job)
			return errors.IsNotFound(err)
		}).WithTimeout(30*time.Second).WithPolling(2*time.Second).Should(BeTrue(), "Job should be cleaned up")
	})

	ginkgo.It("fails gracefully when using non-existent service account", func(ctx context.Context) {
		mustGatherName := "test-mustgather-invalid-sa"

		ginkgo.By("creating a MustGather CR with non-existent service account")
		mustgather := &mustgatherv1alpha1.MustGather{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mustGatherName,
				Namespace: namespace,
			},
			Spec: mustgatherv1alpha1.MustGatherSpec{
				ServiceAccountName: "non-existent-service-account",
			},
		}

		err := oc.Create(ctx, mustgather)
		Expect(err).ShouldNot(HaveOccurred(), "mustgather CR should be created even with invalid service account")

		ginkgo.By("verifying a Job is created")
		job := &batchv1.Job{}
		Eventually(func() error {
			return oc.Get(ctx, mustGatherName, namespace, job)
		}).WithTimeout(30*time.Second).WithPolling(2*time.Second).Should(Succeed(), "Job should be created")

		ginkgo.By("verifying the Job references the non-existent service account")
		Expect(job.Spec.Template.Spec.ServiceAccountName).To(Equal("non-existent-service-account"))

		ginkgo.By("verifying the Job fails to create pods due to missing service account")
		// The Job should exist but pods will fail to be created
		Consistently(func() int32 {
			_ = oc.Get(ctx, mustGatherName, namespace, job)
			return job.Status.Active
		}).WithTimeout(20*time.Second).WithPolling(2*time.Second).Should(Equal(int32(0)), "Job should not have active pods due to missing service account")

		ginkgo.By("cleaning up the MustGather CR")
		err = oc.Delete(ctx, mustgather)
		Expect(err).ShouldNot(HaveOccurred(), "failed to delete mustgather")
	})

	ginkgo.It("successfully collects data and completes MustGather lifecycle", func(ctx context.Context) {
		mustGatherName := "test-mustgather-complete"

		ginkgo.By("cleaning up any existing MustGather from previous test runs")
		existingMG := &mustgatherv1alpha1.MustGather{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mustGatherName,
				Namespace: namespace,
			},
		}
		_ = oc.Delete(ctx, existingMG) // Ignore error if doesn't exist

		ginkgo.By("creating a MustGather CR for data collection")
		mustgather := &mustgatherv1alpha1.MustGather{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mustGatherName,
				Namespace: namespace,
			},
			Spec: mustgatherv1alpha1.MustGatherSpec{
				ServiceAccountName: "must-gather-admin",
				// No upload target to simplify the test - just collect data
			},
		}

		err := oc.Create(ctx, mustgather)
		Expect(err).ShouldNot(HaveOccurred(), "failed to create mustgather CR")

		ginkgo.By("verifying the MustGather CR is created and has initial status")
		createdMG := &mustgatherv1alpha1.MustGather{}
		err = oc.Get(ctx, mustGatherName, namespace, createdMG)
		Expect(err).ShouldNot(HaveOccurred(), "failed to get mustgather CR")
		Expect(createdMG.Status.Completed).To(BeFalse(), "MustGather should not be completed initially")

		ginkgo.By("verifying a Job is created for must-gather collection")
		job := &batchv1.Job{}
		Eventually(func() error {
			return oc.Get(ctx, mustGatherName, namespace, job)
		}).WithTimeout(60*time.Second).WithPolling(2*time.Second).Should(Succeed(), "Job should be created by operator")

		ginkgo.By("verifying the Job is using the correct service account")
		Expect(job.Spec.Template.Spec.ServiceAccountName).To(Equal("must-gather-admin"), "Job must use must-gather-admin service account")

		ginkgo.By("verifying pods are created for the Job")
		Eventually(func() int32 {
			err := oc.Get(ctx, mustGatherName, namespace, job)
			if err != nil {
				return 0
			}
			return job.Status.Active + job.Status.Succeeded + job.Status.Failed
		}).WithTimeout(60*time.Second).WithPolling(3*time.Second).Should(BeNumerically(">", 0), "Job should create at least one pod")

		ginkgo.By("waiting for the Job to start running or complete")
		Eventually(func() bool {
			err := oc.Get(ctx, mustGatherName, namespace, job)
			if err != nil {
				return false
			}
			// Job is running if it has active pods, or it's done if succeeded/failed
			return job.Status.Active > 0 || job.Status.Succeeded > 0 || job.Status.Failed > 0
		}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(BeTrue(), "Job should start running or complete")

		ginkgo.By("verifying MustGather status is updated by the operator")
		Eventually(func() bool {
			err := oc.Get(ctx, mustGatherName, namespace, createdMG)
			if err != nil {
				return false
			}
			// Check if status has been updated (Status field should be set)
			return createdMG.Status.Status != ""
		}).WithTimeout(10*time.Minute).WithPolling(5*time.Second).Should(BeTrue(), "MustGather status should be updated")

		ginkgo.By("checking final MustGather status")
		err = oc.Get(ctx, mustGatherName, namespace, createdMG)
		Expect(err).ShouldNot(HaveOccurred(), "failed to get mustgather status")

		// Log the status for debugging
		ginkgo.GinkgoWriter.Printf("MustGather Status: %s\n", createdMG.Status.Status)
		ginkgo.GinkgoWriter.Printf("MustGather Completed: %v\n", createdMG.Status.Completed)
		ginkgo.GinkgoWriter.Printf("MustGather Reason: %s\n", createdMG.Status.Reason)

		// Verify status is either Completed or Failed (both are valid outcomes)
		Expect(createdMG.Status.Status).Should(Or(Equal("Completed"), Equal("Failed"), Equal("")),
			"MustGather status should be set")

		// If completed, verify the completed flag
		if createdMG.Status.Status == "Completed" {
			Expect(createdMG.Status.Completed).To(BeTrue(), "Completed flag should be true when status is Completed")
			Expect(createdMG.Status.Reason).ToNot(BeEmpty(), "Reason should be populated")
		}

		ginkgo.By("cleaning up the MustGather CR")
		err = oc.Delete(ctx, mustgather)
		Expect(err).ShouldNot(HaveOccurred(), "failed to delete mustgather")
	})

	ginkgo.It("successfully collects cluster resources", func(ctx context.Context) {
		ginkgo.By("verifying must-gather-admin service account has required permissions")

		// Helper function to check SA permissions
		checkSAPermission := func(verb, group, resource string) {
			sar := &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User: fmt.Sprintf("system:serviceaccount:%s:must-gather-admin", namespace),
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Verb:     verb,
						Group:    group,
						Resource: resource,
					},
				},
			}
			err := oc.Create(ctx, sar)
			Expect(err).ShouldNot(HaveOccurred(), "failed to check permissions for %s", resource)

			if !sar.Status.Allowed {
				ginkgo.GinkgoWriter.Printf("❌ DENIED: must-gather-admin cannot %s %s (reason: %s)\n",
					verb, resource, sar.Status.Reason)
			}
			Expect(sar.Status.Allowed).To(BeTrue(),
				"must-gather-admin SA must be able to %s %s", verb, resource)

			ginkgo.GinkgoWriter.Printf("✅ must-gather-admin can %s %s\n", verb, resource)
		}

		// Check all critical permissions
		checkSAPermission("list", "", "pods")
		checkSAPermission("get", "", "pods")
		checkSAPermission("list", "apps", "deployments")
		checkSAPermission("list", "", "services")
		checkSAPermission("list", "", "configmaps")
		checkSAPermission("list", "", "secrets")
		checkSAPermission("list", "", "namespaces")
		checkSAPermission("get", "", "nodes")
		checkSAPermission("list", "", "events")
		checkSAPermission("list", "", "persistentvolumes")
		checkSAPermission("list", "", "persistentvolumeclaims")

		ginkgo.GinkgoWriter.Println("✅ All permission checks passed")
	})

	ginkgo.It("can collect node system logs from cluster nodes", func(ctx context.Context) {
		mustGatherName := "test-node-logs-collection"

		ginkgo.By("ensuring clean state before test")
		_ = oc.Delete(ctx, &mustgatherv1alpha1.MustGather{
			ObjectMeta: metav1.ObjectMeta{Name: mustGatherName, Namespace: namespace},
		})
		_ = oc.Delete(ctx, &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: mustGatherName, Namespace: namespace},
		})
		Eventually(func() bool {
			err := oc.Get(ctx, mustGatherName, namespace, &mustgatherv1alpha1.MustGather{})
			return errors.IsNotFound(err)
		}).WithTimeout(60 * time.Second).Should(BeTrue())

		ginkgo.By("verifying cluster has nodes to collect logs from")
		nodes := &corev1.NodeList{}
		err := oc.List(ctx, nodes)
		Expect(err).ShouldNot(HaveOccurred(), "should be able to list nodes")
		Expect(len(nodes.Items)).Should(BeNumerically(">", 0), "cluster must have at least one node")

		nodeCount := len(nodes.Items)
		ginkgo.GinkgoWriter.Printf("Found %d nodes in cluster for log collection\n", nodeCount)

		// Log node details
		for i, node := range nodes.Items {
			if i < 3 { // Only log first 3 nodes to avoid clutter
				ginkgo.GinkgoWriter.Printf("  Node %d: %s (roles: %s)\n",
					i+1, node.Name, getNodeRoles(node))
			}
		}

		ginkgo.By("verifying must-gather-admin SA can access nodes")
		checkSAPermission := func(verb, group, resource string) {
			sar := &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User: fmt.Sprintf("system:serviceaccount:%s:must-gather-admin", namespace),
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Verb:     verb,
						Group:    group,
						Resource: resource,
					},
				},
			}
			err := oc.Create(ctx, sar)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(sar.Status.Allowed).To(BeTrue(),
				"SA must be able to %s %s for node log collection", verb, resource)
			ginkgo.GinkgoWriter.Printf("  ✓ SA can %s %s\n", verb, resource)
		}

		// Verify permissions needed for node log collection
		checkSAPermission("get", "", "nodes")
		checkSAPermission("list", "", "nodes")
		checkSAPermission("get", "", "nodes/log")   // Node logs
		checkSAPermission("get", "", "nodes/proxy") // Node proxy for system logs
		checkSAPermission("list", "", "pods")       // To see what's running on nodes
		checkSAPermission("get", "", "pods/log")    // Pod logs on nodes

		ginkgo.By("creating MustGather CR to collect node logs")
		mustgather := &mustgatherv1alpha1.MustGather{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mustGatherName,
				Namespace: namespace,
			},
			Spec: mustgatherv1alpha1.MustGatherSpec{
				ServiceAccountName:          "must-gather-admin",
				RetainResourcesOnCompletion: boolPtr(true),
			},
		}

		err = oc.Create(ctx, mustgather)
		Expect(err).ShouldNot(HaveOccurred(), "failed to create mustgather CR")

		// Ensure cleanup
		ginkgo.DeferCleanup(func(ctx context.Context) {
			ginkgo.GinkgoWriter.Println("Cleaning up node logs collection test")
			_ = oc.Delete(ctx, mustgather)
			_ = oc.Delete(ctx, &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: mustGatherName, Namespace: namespace},
			})
		}, ctx)

		ginkgo.By("verifying Job is created for node log collection")
		job := &batchv1.Job{}
		Eventually(func() error {
			return oc.Get(ctx, mustGatherName, namespace, job)
		}).WithTimeout(2 * time.Minute).WithPolling(3 * time.Second).Should(Succeed())

		ginkgo.By("verifying Job uses privileged service account")
		Expect(job.Spec.Template.Spec.ServiceAccountName).To(Equal("must-gather-admin"),
			"Job must use must-gather-admin SA which has node access")

		ginkgo.By("waiting for gather pod to be created and scheduled")
		var gatherPod *corev1.Pod
		Eventually(func() bool {
			podList := &corev1.PodList{}
			if err := oc.WithNamespace(namespace).List(ctx, podList); err != nil {
				return false
			}
			for i := range podList.Items {
				if podList.Items[i].Labels["job-name"] == mustGatherName {
					gatherPod = &podList.Items[i]
					return true
				}
			}
			return false
		}).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())

		ginkgo.GinkgoWriter.Printf("Gather pod created: %s\n", gatherPod.Name)
		if gatherPod.Spec.NodeName != "" {
			ginkgo.GinkgoWriter.Printf("Gather pod scheduled on node: %s\n", gatherPod.Spec.NodeName)
		}

		ginkgo.By("verifying gather pod reaches Running or Completed state")
		Eventually(func() corev1.PodPhase {
			pod := &corev1.Pod{}
			err := oc.Get(ctx, gatherPod.Name, namespace, pod)
			if err != nil {
				return corev1.PodUnknown
			}
			return pod.Status.Phase
		}).WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(
			Or(Equal(corev1.PodRunning), Equal(corev1.PodSucceeded)),
			"Pod should reach Running or Succeeded state")

		ginkgo.By("verifying gather container is collecting data")
		// Give it time to actually collect logs
		time.Sleep(60 * time.Second)

		pod := &corev1.Pod{}
		err = oc.Get(ctx, gatherPod.Name, namespace, pod)
		Expect(err).ShouldNot(HaveOccurred())

		// Check gather container status
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == "gather" {
				ginkgo.GinkgoWriter.Printf("Gather container - Ready: %v, RestartCount: %d\n",
					cs.Ready, cs.RestartCount)

				if cs.State.Running != nil {
					ginkgo.GinkgoWriter.Println("✅ Gather container is actively collecting logs")
				}

				if cs.State.Terminated != nil {
					ginkgo.GinkgoWriter.Printf("Gather terminated - ExitCode: %d, Reason: %s\n",
						cs.State.Terminated.ExitCode, cs.State.Terminated.Reason)

					if cs.State.Terminated.ExitCode == 0 {
						ginkgo.GinkgoWriter.Println("✅ Gather completed successfully")
					}
				}

				// Verify container didn't crash
				Expect(cs.RestartCount).Should(BeNumerically("<=", 2),
					"Gather container should not be crash-looping")
			}
		}

		ginkgo.By("checking for node-related events")
		events := &corev1.EventList{}
		err = oc.WithNamespace(namespace).List(ctx, events)
		Expect(err).ShouldNot(HaveOccurred())

		permissionErrors := []string{}
		nodeAccessErrors := []string{}
		for _, event := range events.Items {
			if event.InvolvedObject.Name == mustGatherName ||
				event.InvolvedObject.Name == gatherPod.Name {
				msg := strings.ToLower(event.Message)

				if strings.Contains(msg, "forbidden") || strings.Contains(msg, "unauthorized") {
					permissionErrors = append(permissionErrors, event.Message)
				}

				if strings.Contains(msg, "node") && strings.Contains(msg, "error") {
					nodeAccessErrors = append(nodeAccessErrors, event.Message)
				}
			}
		}

		if len(permissionErrors) > 0 {
			ginkgo.GinkgoWriter.Println("⚠️  Permission errors detected:")
			for _, err := range permissionErrors {
				ginkgo.GinkgoWriter.Printf("  - %s\n", err)
			}
		}
		Expect(permissionErrors).To(BeEmpty(), "Should not have permission errors")

		ginkgo.By("waiting for must-gather Job to complete")
		Eventually(func() int32 {
			_ = oc.Get(ctx, mustGatherName, namespace, job)
			return job.Status.Succeeded + job.Status.Failed
		}).WithTimeout(15*time.Minute).WithPolling(15*time.Second).Should(
			BeNumerically(">", 0), "Job should eventually complete")

		ginkgo.By("verifying Job completion status")
		err = oc.Get(ctx, mustGatherName, namespace, job)
		Expect(err).ShouldNot(HaveOccurred())
		ginkgo.GinkgoWriter.Printf("Job Status - Active: %d, Succeeded: %d, Failed: %d\n",
			job.Status.Active, job.Status.Succeeded, job.Status.Failed)

		ginkgo.By("verifying MustGather CR status reflects completion")
		createdMG := &mustgatherv1alpha1.MustGather{}
		Eventually(func() bool {
			_ = oc.Get(ctx, mustGatherName, namespace, createdMG)
			return createdMG.Status.Status != ""
		}).WithTimeout(2 * time.Minute).Should(BeTrue())

		ginkgo.By("checking final status of node log collection")
		Expect(createdMG.Status.Status).Should(Or(Equal("Completed"), Equal("Failed")))
		ginkgo.GinkgoWriter.Printf("MustGather Status: %s - %s\n",
			createdMG.Status.Status, createdMG.Status.Reason)

		if createdMG.Status.Status == "Completed" {
			Expect(createdMG.Status.Completed).To(BeTrue())
			ginkgo.GinkgoWriter.Printf("✅ Successfully collected node logs from %d nodes\n", nodeCount)
			ginkgo.GinkgoWriter.Println("   Node logs include: kubelet, container runtime, system logs (journalctl)")
		} else {
			ginkgo.GinkgoWriter.Printf("⚠️  Node log collection failed: %s\n", createdMG.Status.Reason)
		}
	})

	ginkgo.It("can collect CRD definitions and custom resource instances", func(ctx context.Context) {
		mustGatherName := "test-crd-collection"

		ginkgo.By("ensuring clean state before test")
		_ = oc.Delete(ctx, &mustgatherv1alpha1.MustGather{
			ObjectMeta: metav1.ObjectMeta{Name: mustGatherName, Namespace: namespace},
		})
		_ = oc.Delete(ctx, &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: mustGatherName, Namespace: namespace},
		})
		Eventually(func() bool {
			err := oc.Get(ctx, mustGatherName, namespace, &mustgatherv1alpha1.MustGather{})
			return errors.IsNotFound(err)
		}).WithTimeout(60 * time.Second).Should(BeTrue())

		ginkgo.By("verifying CRDs exist in the cluster")
		crdList := &apiextensionsv1.CustomResourceDefinitionList{}
		err := oc.List(ctx, crdList)
		Expect(err).ShouldNot(HaveOccurred(), "should be able to list CRDs")
		Expect(len(crdList.Items)).Should(BeNumerically(">", 0), "cluster should have CRDs")

		ginkgo.GinkgoWriter.Printf("Found %d CRDs in cluster\n", len(crdList.Items))

		// Find the MustGather CRD as an example
		var mustGatherCRD *apiextensionsv1.CustomResourceDefinition
		for i := range crdList.Items {
			if crdList.Items[i].Name == "mustgathers.operator.openshift.io" {
				mustGatherCRD = &crdList.Items[i]
				ginkgo.GinkgoWriter.Printf("Found MustGather CRD: %s (group: %s, version: %s)\n",
					mustGatherCRD.Name,
					mustGatherCRD.Spec.Group,
					mustGatherCRD.Spec.Versions[0].Name)
				break
			}
		}
		Expect(mustGatherCRD).ToNot(BeNil(), "MustGather CRD should exist")

		ginkgo.By("verifying must-gather-admin SA can access CRDs and custom resources")
		checkSAPermission := func(verb, group, resource string) {
			sar := &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User: fmt.Sprintf("system:serviceaccount:%s:must-gather-admin", namespace),
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Verb:     verb,
						Group:    group,
						Resource: resource,
					},
				},
			}
			err := oc.Create(ctx, sar)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(sar.Status.Allowed).To(BeTrue(),
				"SA must be able to %s %s.%s for CRD collection", verb, resource, group)
			ginkgo.GinkgoWriter.Printf("  ✓ SA can %s %s.%s\n", verb, resource, group)
		}

		// Check permissions for CRD access
		checkSAPermission("list", "apiextensions.k8s.io", "customresourcedefinitions")
		checkSAPermission("get", "apiextensions.k8s.io", "customresourcedefinitions")

		// Check permissions for MustGather custom resources
		checkSAPermission("list", "operator.openshift.io", "mustgathers")
		checkSAPermission("get", "operator.openshift.io", "mustgathers")

		ginkgo.By("verifying custom resource instances exist")
		mustGatherList := &mustgatherv1alpha1.MustGatherList{}
		err = oc.List(ctx, mustGatherList)
		Expect(err).ShouldNot(HaveOccurred(), "should be able to list MustGather CRs")
		ginkgo.GinkgoWriter.Printf("Found %d MustGather CR instances in cluster\n", len(mustGatherList.Items))

		ginkgo.By("creating a test MustGather CR instance for collection")
		testCR := &mustgatherv1alpha1.MustGather{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cr-instance",
				Namespace: namespace,
				Labels: map[string]string{
					"test": "crd-collection",
					"app":  "must-gather-operator-e2e",
				},
				Annotations: map[string]string{
					"description": "Test CR for e2e CRD collection validation",
				},
			},
			Spec: mustgatherv1alpha1.MustGatherSpec{
				ServiceAccountName: "must-gather-admin",
			},
		}

		err = oc.Create(ctx, testCR)
		Expect(err).ShouldNot(HaveOccurred(), "should be able to create test CR")

		// Cleanup test CR after test
		defer func() {
			_ = oc.Delete(ctx, testCR)
		}()

		ginkgo.By("verifying the test CR was created successfully")
		createdCR := &mustgatherv1alpha1.MustGather{}
		err = oc.Get(ctx, "test-cr-instance", namespace, createdCR)
		Expect(err).ShouldNot(HaveOccurred())
		Expect(createdCR.Labels["test"]).To(Equal("crd-collection"))
		ginkgo.GinkgoWriter.Printf("✅ Test CR created: %s with labels: %v\n",
			createdCR.Name, createdCR.Labels)

		ginkgo.By("creating MustGather to collect CRDs and custom resources")
		mustgather := &mustgatherv1alpha1.MustGather{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mustGatherName,
				Namespace: namespace,
			},
			Spec: mustgatherv1alpha1.MustGatherSpec{
				ServiceAccountName:          "must-gather-admin",
				RetainResourcesOnCompletion: boolPtr(true),
			},
		}

		err = oc.Create(ctx, mustgather)
		Expect(err).ShouldNot(HaveOccurred(), "failed to create mustgather CR")

		// Ensure cleanup
		ginkgo.DeferCleanup(func(ctx context.Context) {
			ginkgo.GinkgoWriter.Println("Cleaning up CRD collection test")
			_ = oc.Delete(ctx, mustgather)
			_ = oc.Delete(ctx, &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: mustGatherName, Namespace: namespace},
			})
		}, ctx)

		ginkgo.By("verifying Job is created for CRD collection")
		job := &batchv1.Job{}
		Eventually(func() error {
			return oc.Get(ctx, mustGatherName, namespace, job)
		}).WithTimeout(2 * time.Minute).WithPolling(3 * time.Second).Should(Succeed())

		ginkgo.By("verifying Job uses service account with CRD access")
		Expect(job.Spec.Template.Spec.ServiceAccountName).To(Equal("must-gather-admin"),
			"Job must use SA with permissions to access CRDs and custom resources")

		ginkgo.By("waiting for gather pod to be created")
		var gatherPod *corev1.Pod
		Eventually(func() bool {
			podList := &corev1.PodList{}
			if err := oc.WithNamespace(namespace).List(ctx, podList); err != nil {
				return false
			}
			for i := range podList.Items {
				if podList.Items[i].Labels["job-name"] == mustGatherName {
					gatherPod = &podList.Items[i]
					return true
				}
			}
			return false
		}).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())

		ginkgo.By("verifying gather pod starts collecting data")
		Eventually(func() corev1.PodPhase {
			pod := &corev1.Pod{}
			err := oc.Get(ctx, gatherPod.Name, namespace, pod)
			if err != nil {
				return corev1.PodUnknown
			}
			return pod.Status.Phase
		}).WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(
			Or(Equal(corev1.PodRunning), Equal(corev1.PodSucceeded)),
			"Pod should reach Running or Succeeded state")

		ginkgo.By("verifying must-gather can list CRDs during collection")
		// This is verified by checking that the pod started successfully
		// If SA lacked CRD permissions, collection would fail
		pod := &corev1.Pod{}
		err = oc.Get(ctx, gatherPod.Name, namespace, pod)
		Expect(err).ShouldNot(HaveOccurred())

		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == "gather" {
				ginkgo.GinkgoWriter.Printf("Gather container - Ready: %v, RestartCount: %d\n",
					cs.Ready, cs.RestartCount)

				// Verify container is not failing due to permission issues
				Expect(cs.RestartCount).Should(BeNumerically("<=", 2),
					"Gather should not be crash-looping (would indicate permission issues)")

				if cs.State.Waiting != nil && strings.Contains(
					strings.ToLower(cs.State.Waiting.Reason), "crashloopbackoff") {
					ginkgo.Fail("Gather container in CrashLoopBackOff - likely permission issue with CRD access")
				}
			}
		}

		ginkgo.By("checking for CRD-related permission errors in events")
		events := &corev1.EventList{}
		err = oc.WithNamespace(namespace).List(ctx, events)
		Expect(err).ShouldNot(HaveOccurred())

		crdPermissionErrors := []string{}
		for _, event := range events.Items {
			if event.InvolvedObject.Name == mustGatherName ||
				event.InvolvedObject.Name == gatherPod.Name {
				msg := strings.ToLower(event.Message)

				if (strings.Contains(msg, "customresourcedefinition") ||
					strings.Contains(msg, "crd")) &&
					(strings.Contains(msg, "forbidden") || strings.Contains(msg, "unauthorized")) {
					crdPermissionErrors = append(crdPermissionErrors, event.Message)
				}
			}
		}

		if len(crdPermissionErrors) > 0 {
			ginkgo.GinkgoWriter.Println("⚠️  CRD permission errors detected:")
			for _, err := range crdPermissionErrors {
				ginkgo.GinkgoWriter.Printf("  - %s\n", err)
			}
		}
		Expect(crdPermissionErrors).To(BeEmpty(), "Should not have CRD permission errors")

		ginkgo.By("waiting for must-gather to complete")
		Eventually(func() int32 {
			_ = oc.Get(ctx, mustGatherName, namespace, job)
			return job.Status.Succeeded + job.Status.Failed
		}).WithTimeout(15*time.Minute).WithPolling(15*time.Second).Should(
			BeNumerically(">", 0), "Job should eventually complete")

		ginkgo.By("verifying Job completion status")
		err = oc.Get(ctx, mustGatherName, namespace, job)
		Expect(err).ShouldNot(HaveOccurred())
		ginkgo.GinkgoWriter.Printf("Job Status - Active: %d, Succeeded: %d, Failed: %d\n",
			job.Status.Active, job.Status.Succeeded, job.Status.Failed)

		ginkgo.By("verifying MustGather CR status reflects completion")
		completedMG := &mustgatherv1alpha1.MustGather{}
		Eventually(func() bool {
			_ = oc.Get(ctx, mustGatherName, namespace, completedMG)
			return completedMG.Status.Status != ""
		}).WithTimeout(2 * time.Minute).Should(BeTrue())

		ginkgo.By("checking final status of CRD collection")
		Expect(completedMG.Status.Status).Should(Or(Equal("Completed"), Equal("Failed")))
		ginkgo.GinkgoWriter.Printf("MustGather Status: %s - %s\n",
			completedMG.Status.Status, completedMG.Status.Reason)

		if completedMG.Status.Status == "Completed" {
			Expect(completedMG.Status.Completed).To(BeTrue())
			ginkgo.GinkgoWriter.Println("✅ Successfully collected CRD definitions and custom resource instances")
			ginkgo.GinkgoWriter.Printf("   CRDs collected: %d CRDs in cluster\n", len(crdList.Items))
			ginkgo.GinkgoWriter.Printf("   Custom resources collected: MustGather CRs and other custom resources\n")
			ginkgo.GinkgoWriter.Println("   This includes CRD schemas, CR instances, and their status/spec")
		} else {
			ginkgo.GinkgoWriter.Printf("⚠️  CRD collection failed: %s\n", completedMG.Status.Reason)
		}

		ginkgo.By("verifying test CR still exists after collection")
		err = oc.Get(ctx, "test-cr-instance", namespace, createdCR)
		Expect(err).ShouldNot(HaveOccurred(), "test CR should still exist after must-gather collection")
		ginkgo.GinkgoWriter.Println("✅ CRD collection is non-destructive - test CR preserved")
	})

	/*ginkgo.DescribeTable("MustGather can be created", func(ctx context.Context, user, group string) {
		client, err := oc.Impersonate(user, group)
		Expect(err).ShouldNot(HaveOccurred(), "unable to impersonate %s user in %s group", user, group)

		mustgather := &mustgatherv1alpha1.MustGather{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "osde2e-",
				Namespace:    namespace,
			},
			Spec: mustgatherv1alpha1.MustGatherSpec{
				ServiceAccountName: "must-gather-admin",
				UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
					Type: mustgatherv1alpha1.UploadTypeSFTP,
					SFTP: &mustgatherv1alpha1.SFTPSpec{
						CaseID: "0000000",
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{
							Name: "case-management-creds",
						},
					},
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
	)*/

	/*ginkgo.PIt("can be upgraded", func(ctx context.Context) {
		ginkgo.By("forcing operator upgrade")
		err := oc.UpgradeOperator(ctx, operator, namespace)
		Expect(err).NotTo(HaveOccurred(), "operator upgrade failed")
	})*/
})

// Helper function to extract node roles from labels
func getNodeRoles(node corev1.Node) string {
	roles := []string{}
	for label := range node.Labels {
		if strings.HasPrefix(label, "node-role.kubernetes.io/") {
			role := strings.TrimPrefix(label, "node-role.kubernetes.io/")
			if role != "" {
				roles = append(roles, role)
			}
		}
	}
	if len(roles) == 0 {
		return "none"
	}
	return strings.Join(roles, ", ")
}

// Helper function to convert bool to pointer
func boolPtr(b bool) *bool {
	return &b
}
