// DO NOT REMOVE TAGS BELOW. IF ANY NEW TEST FILES ARE CREATED UNDER /test/e2e, PLEASE ADD THESE TAGS TO THEM IN ORDER TO BE EXCLUDED FROM UNIT TESTS.
//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"embed"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// Test suite constants
const (
	nonAdminUser       = "must-gather-nonadmin-user"
	nonAdminCRRoleName = "must-gather-nonadmin-clusterrole"
	serviceAccount     = "must-gather-serviceaccount"
	nonAdminLabel      = "support-log-gather"

	// Operator constants
	operatorNamespace  = "must-gather-operator"
	operatorDeployment = "must-gather-operator"

	// Job/Pod constants
	gatherContainerName = "gather"
	outputVolumeName    = "must-gather-output"
	jobNameLabelKey     = "job-name"
)

//go:embed testdata/*
var testassets embed.FS

// Test suite variables
var (
	testCtx         context.Context
	testScheme      *k8sruntime.Scheme
	adminRestConfig *rest.Config
	adminClient     client.Client
	nonAdminClient  client.Client
	setupComplete   bool
)

func init() {
	testScheme = k8sruntime.NewScheme()
	utilruntime.Must(mustgatherv1alpha1.AddToScheme(testScheme))
	utilruntime.Must(appsv1.AddToScheme(testScheme))
	utilruntime.Must(corev1.AddToScheme(testScheme))
	utilruntime.Must(rbacv1.AddToScheme(testScheme))
	utilruntime.Must(batchv1.AddToScheme(testScheme))
}

var _ = ginkgo.Describe("MustGather resource", ginkgo.Ordered, func() {

	// BeforeAll - Admin Setup Phase
	ginkgo.BeforeAll(func() {
		testCtx = context.Background()

		ginkgo.By("STEP 1: Admin sets up clients and ensures operator is installed")
		var err error
		adminRestConfig, err = config.GetConfig()
		Expect(err).NotTo(HaveOccurred(), "Failed to get admin kube config")

		adminClient, err = client.New(adminRestConfig, client.Options{Scheme: testScheme})
		Expect(err).NotTo(HaveOccurred(), "Failed to create admin typed client")

		ginkgo.By("Verifying must-gather-operator is deployed and available")
		verifyOperatorDeployment()

		ginkgo.By("STEP 2: Creates test namespace")
		namespace, err := loader.CreateTestNS("must-gather-operator-e2e", false)
		Expect(err).NotTo(HaveOccurred())
		ns = namespace

		ginkgo.By("STEP 3: Creating ClusterRole for MustGather CRs")
		loader.CreateFromFile(testassets.ReadFile, filepath.Join("testdata", "nonadmin-clusterrole.yaml"), ns.Name)

		ginkgo.By("STEP 4: Creating ClusterRoleBinding for non-admin user")
		loader.CreateFromFile(testassets.ReadFile, filepath.Join("testdata", "nonadmin-clusterrole-binding.yaml"), ns.Name)

		ginkgo.By("STEP 5: Creating ServiceAccount and associated RBAC")
		loader.CreateFromFile(testassets.ReadFile, filepath.Join("testdata", "serviceaccount.yaml"), ns.Name)
		loader.CreateFromFile(testassets.ReadFile, filepath.Join("testdata", "serviceaccount-clusterrole.yaml"), ns.Name)
		loader.CreateFromFile(testassets.ReadFile, filepath.Join("testdata", "serviceaccount-clusterrole-binding.yaml"), ns.Name)

		ginkgo.By("Initializing non-admin client for tests")
		nonAdminClient = createNonAdminClient()

		setupComplete = true
		ginkgo.GinkgoWriter.Println("Admin setup complete - RBAC and ServiceAccount configured")
	})

	// AfterAll - Cleanup
	ginkgo.AfterAll(func() {
		if !setupComplete {
			ginkgo.GinkgoWriter.Println("Setup was not complete, skipping cleanup")
			return
		}

		ginkgo.By("CLEANUP: Removing all test resources")
		// Deleting namespace and all resources in it (including ServiceAccount)
		loader.DeleteTestingNS(ns.Name, func() bool { return ginkgo.CurrentSpecReport().Failed() })
		// Deleting ClusterRole, ClusterRoleBinding, and associated RBAC
		loader.DeleteFromFile(testassets.ReadFile, filepath.Join("testdata", "nonadmin-clusterrole.yaml"), ns.Name)
		loader.DeleteFromFile(testassets.ReadFile, filepath.Join("testdata", "nonadmin-clusterrole-binding.yaml"), ns.Name)
		loader.DeleteFromFile(testassets.ReadFile, filepath.Join("testdata", "serviceaccount-clusterrole.yaml"), ns.Name)
		loader.DeleteFromFile(testassets.ReadFile, filepath.Join("testdata", "serviceaccount-clusterrole-binding.yaml"), ns.Name)
	})

	// Test Cases

	ginkgo.Context("Non-Admin User Operations", func() {
		var mustGatherName string
		var mustGatherCR *mustgatherv1alpha1.MustGather

		ginkgo.BeforeEach(func() {
			mustGatherName = fmt.Sprintf("non-admin-must-gather-e2e-%d", time.Now().UnixNano())
		})

		ginkgo.AfterEach(func() {
			if mustGatherCR != nil {
				ginkgo.By("Cleaning up MustGather CR")
				_ = adminClient.Delete(testCtx, mustGatherCR)

				// Wait for cleanup
				Eventually(func() bool {
					err := adminClient.Get(testCtx, client.ObjectKey{
						Name:      mustGatherName,
						Namespace: ns.Name,
					}, &mustgatherv1alpha1.MustGather{})
					return apierrors.IsNotFound(err)
				}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())

				mustGatherCR = nil
			}
		})

		ginkgo.It("can create, get, and list MustGather CRs", func() {
			ginkgo.By("Creating MustGather CR using impersonation")
			mustGatherCR = &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mustGatherName,
					Namespace: ns.Name,
					Annotations: map[string]string{
						"test.description": "Non-admin user submitted MustGather",
						"test.user":        nonAdminUser,
					},
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: serviceAccount,
				},
			}

			err := nonAdminClient.Create(testCtx, mustGatherCR)
			Expect(err).NotTo(HaveOccurred(), "Non-admin user should be able to create MustGather CR")

			ginkgo.By("Verifying MustGather CR was created successfully")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			err = nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: ns.Name,
			}, fetchedMG)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchedMG.Spec.ServiceAccountName).To(Equal(serviceAccount))

			ginkgo.GinkgoWriter.Printf("Non-admin user '%s' successfully created MustGather CR: %s\n",
				nonAdminUser, mustGatherName)

			ginkgo.By("Non-admin user listing MustGather CRs in their namespace")
			mgList := &mustgatherv1alpha1.MustGatherList{}
			err = nonAdminClient.List(testCtx, mgList, client.InNamespace(ns.Name))
			Expect(err).NotTo(HaveOccurred(), "Non-admin should be able to list MustGather CRs")
			Expect(mgList.Items).NotTo(BeEmpty(),
				"List should contain at least the MustGather CR we just created")

			ginkgo.By("Verifying the created CR is in the list")
			found := false
			for _, mg := range mgList.Items {
				if mg.Name == mustGatherName {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "Created MustGather CR should be in the list")

			ginkgo.GinkgoWriter.Printf("Non-admin user can see %d MustGather CRs in namespace %s\n",
				len(mgList.Items), ns.Name)
		})

		ginkgo.It("CANNOT perform admin operations", func() {
			ginkgo.By("Attempting to delete a namespace (should fail)")
			testNS := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-delete-namespace",
				},
			}
			err := nonAdminClient.Delete(testCtx, testNS)
			Expect(err).To(HaveOccurred(), "Non-admin should NOT be able to delete namespaces")
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "Should get Forbidden error")

			ginkgo.By("Attempting to create a ClusterRole (should fail)")
			testCR := &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-unauthorized-clusterrole",
				},
			}
			err = nonAdminClient.Create(testCtx, testCR)
			Expect(err).To(HaveOccurred(), "Non-admin should NOT be able to create ClusterRoles")
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "Should get Forbidden error")

			ginkgo.GinkgoWriter.Println("Non-admin user correctly blocked from admin operations")
		})

		ginkgo.It("should create Job using ServiceAccount", func() {
			ginkgo.By("creating MustGather CR")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, false)

			ginkgo.By("Waiting for operator to create Job")
			job := &batchv1.Job{}
			Eventually(func() error {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
				if err != nil {
					// Debug: Check if Job exists with admin client
					jobDebug := &batchv1.Job{}
					errAdmin := adminClient.Get(testCtx, client.ObjectKey{
						Name:      mustGatherName,
						Namespace: ns.Name,
					}, jobDebug)
					if errAdmin == nil {
						ginkgo.GinkgoWriter.Printf("Job exists but non-admin can't see it (RBAC issue)\n")
					} else {
						ginkgo.GinkgoWriter.Printf("Job not found by operator yet: %v\n", errAdmin)
					}
				}
				return err
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"Non-admin user should be able to see Job created by operator")

			ginkgo.By("Verifying Job uses ServiceAccount")
			Expect(job.Spec.Template.Spec.ServiceAccountName).To(Equal(serviceAccount),
				"Job must use the ServiceAccount specified in MustGather CR")

			ginkgo.By("Verifying Job has required specifications")
			Expect(len(job.Spec.Template.Spec.Containers)).To(BeNumerically(">=", 1),
				"Job should have at least one container")

			hasGatherContainer := false
			for _, container := range job.Spec.Template.Spec.Containers {
				if container.Name == gatherContainerName {
					hasGatherContainer = true
					break
				}
			}
			Expect(hasGatherContainer).To(BeTrue(), "Job should have gather container")

			ginkgo.By("Verifying Job has output volume for artifacts")
			hasOutputVolume := false
			for _, volume := range job.Spec.Template.Spec.Volumes {
				if volume.Name == outputVolumeName {
					hasOutputVolume = true
					break
				}
			}
			Expect(hasOutputVolume).To(BeTrue(), "Job should have must-gather-output volume")
		})

		ginkgo.It("should be able to monitor MustGather progress", func() {
			ginkgo.By("creating MustGather CR")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, false)

			ginkgo.By("checking MustGather CR status")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			err := nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: ns.Name,
			}, fetchedMG)
			Expect(err).NotTo(HaveOccurred(), "Non-admin user should be able to get MustGather CR")
			ginkgo.GinkgoWriter.Printf("Non-admin can read MustGather status: %s\n", fetchedMG.Status.Status)

			ginkgo.By("listing Jobs in namespace")
			jobList := &batchv1.JobList{}
			err = nonAdminClient.List(testCtx, jobList, client.InNamespace(ns.Name))
			Expect(err).NotTo(HaveOccurred(), "Non-admin user should be able to list Jobs")
			ginkgo.GinkgoWriter.Printf("Non-admin can see %d Jobs\n", len(jobList.Items))

			ginkgo.By("listing Pods in namespace")
			podList := &corev1.PodList{}
			err = nonAdminClient.List(testCtx, podList, client.InNamespace(ns.Name))
			Expect(err).NotTo(HaveOccurred(), "Non-admin user should be able to list Pods")
			ginkgo.GinkgoWriter.Printf("Non-admin can see %d Pods\n", len(podList.Items))

			ginkgo.By("checking for gather Pods")
			Eventually(func() bool {
				pods := &corev1.PodList{}
				if err := nonAdminClient.List(testCtx, pods,
					client.InNamespace(ns.Name),
					client.MatchingLabels{jobNameLabelKey: mustGatherName}); err != nil {
					return false
				}
				if len(pods.Items) > 0 {
					ginkgo.GinkgoWriter.Printf("Non-admin can see gather pod: %s\n", pods.Items[0].Name)
				}
				return len(pods.Items) > 0
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())

			ginkgo.GinkgoWriter.Println("Non-admin user successfully monitored MustGather progress")
		})

		ginkgo.It("can delete their MustGather CR", func() {
			mustGatherName := fmt.Sprintf("test-cleanup-%d", time.Now().UnixNano())

			ginkgo.By("creating MustGather CR")
			mg := createMustGatherCR(mustGatherName, ns.Name, serviceAccount, false)

			ginkgo.By("deleting their MustGather CR")
			err := nonAdminClient.Delete(testCtx, mg)
			Expect(err).NotTo(HaveOccurred(), "Non-admin should be able to delete their own CR")

			ginkgo.By("Verifying CR is deleted")
			Eventually(func() bool {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, &mustgatherv1alpha1.MustGather{})
				return apierrors.IsNotFound(err)
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())

			ginkgo.GinkgoWriter.Printf("Non-admin user successfully deleted MustGather CR: %s\n", mustGatherName)
		})

		ginkgo.It("should clean up Job and Pod when MustGather CR is deleted", func() {
			mustGatherName := fmt.Sprintf("test-cascading-delete-%d", time.Now().UnixNano())

			ginkgo.By("Creating MustGather CR")
			mg := createMustGatherCR(mustGatherName, ns.Name, serviceAccount, false)

			ginkgo.By("Waiting for Job to be created")
			Eventually(func() error {
				return adminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, &batchv1.Job{})
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			ginkgo.By("Waiting for Pod to be created")
			Eventually(func() int {
				podList := &corev1.PodList{}
				if err := adminClient.List(testCtx, podList,
					client.InNamespace(ns.Name),
					client.MatchingLabels{jobNameLabelKey: mustGatherName}); err != nil {
					return 0
				}
				return len(podList.Items)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(BeNumerically(">=", 1),
				"Pod should be created by Job")

			ginkgo.By("Deleting MustGather CR")
			err := nonAdminClient.Delete(testCtx, mg)
			Expect(err).NotTo(HaveOccurred())

			ginkgo.By("Verifying Job is eventually cleaned up")
			Eventually(func() bool {
				err := adminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, &batchv1.Job{})
				return apierrors.IsNotFound(err)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(BeTrue(),
				"Job should be cleaned up when MustGather CR is deleted")

			ginkgo.By("Verifying Pods are eventually cleaned up")
			Eventually(func() int {
				podList := &corev1.PodList{}
				if err := adminClient.List(testCtx, podList,
					client.InNamespace(ns.Name),
					client.MatchingLabels{jobNameLabelKey: mustGatherName}); err != nil {
					return 0
				}
				return len(podList.Items)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Equal(0),
				"Pods should be cleaned up when MustGather CR is deleted")
		})
	})

	ginkgo.Context("Admin User Operations", func() {
		var mustGatherName string
		var mustGatherCR *mustgatherv1alpha1.MustGather

		ginkgo.BeforeEach(func() {
			mustGatherName = fmt.Sprintf("non-admin-mg-with-retain-resources-%d", time.Now().UnixNano())
		})

		ginkgo.AfterEach(func() {
			if mustGatherCR != nil {
				ginkgo.By("Cleaning up MustGather CR")
				_ = adminClient.Delete(testCtx, mustGatherCR)

				// Wait for cleanup
				Eventually(func() bool {
					err := adminClient.Get(testCtx, client.ObjectKey{
						Name:      mustGatherName,
						Namespace: ns.Name,
					}, &mustgatherv1alpha1.MustGather{})
					return apierrors.IsNotFound(err)
				}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())

				mustGatherCR = nil
			}
		})
		ginkgo.It("Pod should gather data and complete successfully", func() {
			ginkgo.By("creating MustGather CR with RetainResourcesOnCompletion")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true)

			ginkgo.By("Waiting for Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			ginkgo.By("Waiting for Pod to be created")
			var gatherPod *corev1.Pod
			Eventually(func() bool {
				podList := &corev1.PodList{}
				if err := nonAdminClient.List(testCtx, podList,
					client.InNamespace(ns.Name),
					client.MatchingLabels{jobNameLabelKey: mustGatherName}); err != nil {
					return false
				}
				if len(podList.Items) > 0 {
					gatherPod = &podList.Items[0]
					return true
				}
				return false
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(BeTrue(),
				"Pod should be created by Job")

			ginkgo.By("Verifying Pod is scheduled")
			Eventually(func() string {
				pod := &corev1.Pod{}
				_ = nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      gatherPod.Name,
					Namespace: ns.Name,
				}, pod)
				return pod.Spec.NodeName
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).ShouldNot(BeEmpty(),
				"Pod should be scheduled to a node")

			ginkgo.By("Monitoring Pod execution phase")
			Eventually(func() corev1.PodPhase {
				pod := &corev1.Pod{}
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      gatherPod.Name,
					Namespace: ns.Name,
				}, pod)
				if err != nil {
					return corev1.PodUnknown
				}
				return pod.Status.Phase
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(
				Or(Equal(corev1.PodRunning), Equal(corev1.PodSucceeded)),
				"Pod should reach Running or Succeeded state")

			ginkgo.By("Verifying gather container is collecting data")
			Eventually(func() bool {
				pod := &corev1.Pod{}
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      gatherPod.Name,
					Namespace: ns.Name,
				}, pod)
				if err != nil {
					return false
				}

				for _, cs := range pod.Status.ContainerStatuses {
					if cs.Name == gatherContainerName {
						ginkgo.GinkgoWriter.Printf("Gather container - Ready: %v, RestartCount: %d\n",
							cs.Ready, cs.RestartCount)

						// Container should be running and not crash-looping
						if cs.RestartCount > 2 {
							return false
						}
						// Return true if container has started (even if not ready yet, data collection may be in progress)
						if cs.State.Running != nil || cs.State.Terminated != nil {
							return true
						}
					}
				}
				return false
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(BeTrue(),
				"Gather container should be running and collecting data without excessive restarts")

			ginkgo.By("Waiting for Job to complete successfully")
			Eventually(func() int32 {
				_ = nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
				return job.Status.Succeeded
			}).WithTimeout(5*time.Minute).WithPolling(5*time.Second).Should(
				BeNumerically(">=", 1), "Job should complete successfully")

			Expect(job.Status.Failed).To(Equal(int32(0)), "Job should not have any failed pods")

			ginkgo.By("Verifying MustGather CR status is updated")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			Eventually(func() string {
				_ = nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, fetchedMG)
				return fetchedMG.Status.Status
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).ShouldNot(BeEmpty(),
				"MustGather status should be updated by operator")

			ginkgo.GinkgoWriter.Printf("MustGather Status: %s - Completed: %v - Reason: %s\n",
				fetchedMG.Status.Status, fetchedMG.Status.Completed, fetchedMG.Status.Reason)

			ginkgo.By("Verifying resources are retained after completion (RetainResourcesOnCompletion=true)")
			// Wait a bit to ensure the operator has had time to process the completion
			time.Sleep(10 * time.Second)

			ginkgo.By("Verifying Job still exists after completion")
			retainedJob := &batchv1.Job{}
			err := nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: ns.Name,
			}, retainedJob)
			Expect(err).NotTo(HaveOccurred(), "Job should still exist when RetainResourcesOnCompletion is true")
			ginkgo.GinkgoWriter.Printf("Job %s is retained after completion\n", retainedJob.Name)

			ginkgo.By("Verifying Pod still exists after completion")
			retainedPodList := &corev1.PodList{}
			err = nonAdminClient.List(testCtx, retainedPodList,
				client.InNamespace(ns.Name),
				client.MatchingLabels{jobNameLabelKey: mustGatherName})
			Expect(err).NotTo(HaveOccurred())
			Expect(retainedPodList.Items).NotTo(BeEmpty(),
				"Pod should still exist when RetainResourcesOnCompletion is true")
			ginkgo.GinkgoWriter.Printf("Pod %s is retained after completion\n", retainedPodList.Items[0].Name)

			ginkgo.GinkgoWriter.Println("RetainResourcesOnCompletion=true verified: Job and Pod are retained after completion")
		})
	})

	ginkgo.Context("Security and Isolation Tests", func() {
		var mg *mustgatherv1alpha1.MustGather

		ginkgo.AfterEach(func() {
			if mg != nil {
				ginkgo.By("Cleaning up MustGather CR")
				_ = nonAdminClient.Delete(testCtx, mg)
				mg = nil
			}
		})

		ginkgo.It("should fail when non-admin user tries to use privileged ServiceAccount", func() {
			mustGatherName := fmt.Sprintf("test-privileged-sa-%d", time.Now().UnixNano())

			ginkgo.By("Attempting to create MustGather with non-existent privileged SA")
			mg = createMustGatherCR(mustGatherName, ns.Name, "cluster-admin-sa", false) // SA doesn't exist

			ginkgo.By("Verifying Job is created but Pod fails due to missing SA")
			job := &batchv1.Job{}
			Eventually(func() error {
				return adminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			// Job should exist but pods won't be created due to missing SA
			Consistently(func() int32 {
				_ = adminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
				return job.Status.Active
			}).WithTimeout(30*time.Second).WithPolling(5*time.Second).Should(Equal(int32(0)),
				"Job should not have active pods due to missing ServiceAccount")
		})

		ginkgo.It("should enforce ServiceAccount permissions during data collection", func() {
			mustGatherName := fmt.Sprintf("test-sa-permissions-%d", time.Now().UnixNano())

			ginkgo.By("Creating MustGather with SA")
			mg = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, false)

			ginkgo.By("Waiting for Pod to start")
			var gatherPod *corev1.Pod
			Eventually(func() bool {
				podList := &corev1.PodList{}
				if err := adminClient.List(testCtx, podList,
					client.InNamespace(ns.Name),
					client.MatchingLabels{jobNameLabelKey: mustGatherName}); err != nil {
					return false
				}
				if len(podList.Items) > 0 {
					gatherPod = &podList.Items[0]
					return true
				}
				return false
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())

			ginkgo.By("Verifying Pod has no privilege escalation")
			Expect(gatherPod.Spec.ServiceAccountName).To(Equal(serviceAccount))

			// Check that pod doesn't request privileged mode
			for _, container := range gatherPod.Spec.Containers {
				if container.SecurityContext != nil && container.SecurityContext.Privileged != nil {
					Expect(*container.SecurityContext.Privileged).To(BeFalse(),
						"Container should not run in privileged mode")
				}
			}

			ginkgo.By("Waiting for Pod to start running")
			Eventually(func() corev1.PodPhase {
				pod := &corev1.Pod{}
				if err := adminClient.Get(testCtx, client.ObjectKey{
					Name:      gatherPod.Name,
					Namespace: ns.Name,
				}, pod); err != nil {
					return corev1.PodUnknown
				}
				return pod.Status.Phase
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(
				Or(Equal(corev1.PodRunning), Equal(corev1.PodSucceeded)),
				"Pod should be running or succeeded before checking events")

			ginkgo.By("Verifying Pod runs with restricted permissions (no privileged escalation events)")
			events := &corev1.EventList{}
			err := adminClient.List(testCtx, events, client.InNamespace(ns.Name))
			Expect(err).NotTo(HaveOccurred(), "Should be able to list events")

			// Check that there are no privilege escalation or security context violation events
			for _, event := range events.Items {
				if event.InvolvedObject.Name == mustGatherName || event.InvolvedObject.Name == gatherPod.Name {
					// Only check Warning events for security issues
					if event.Type == corev1.EventTypeWarning {
						lowerMsg := strings.ToLower(event.Message)
						lowerReason := strings.ToLower(event.Reason)
						// Check for specific security violation patterns
						Expect(lowerReason).NotTo(Equal("failedcreate"),
							"Pod creation should not fail: %s", event.Message)
						Expect(lowerMsg).NotTo(ContainSubstring("forbidden"),
							"Pod should not have forbidden errors: %s", event.Message)
						Expect(lowerMsg).NotTo(ContainSubstring("denied"),
							"Pod should not have permission denied errors: %s", event.Message)
					}
				}
			}

			ginkgo.GinkgoWriter.Println("Pod is running with properly restricted permissions")
		})

		ginkgo.It("should prevent non-admin user from modifying RBAC", func() {
			ginkgo.By("Attempting to update ClusterRole (should fail)")
			cr := &rbacv1.ClusterRole{}
			err := adminClient.Get(testCtx, client.ObjectKey{Name: nonAdminCRRoleName}, cr)
			Expect(err).NotTo(HaveOccurred(), "ClusterRole should exist")

			err = nonAdminClient.Update(testCtx, cr)
			Expect(err).To(HaveOccurred(), "Non-admin should NOT be able to modify ClusterRoles")
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "Should get Forbidden error")

			ginkgo.By("Attempting to update ServiceAccount (should fail)")
			sa := &corev1.ServiceAccount{}
			err = adminClient.Get(testCtx, client.ObjectKey{Name: serviceAccount, Namespace: ns.Name}, sa)
			Expect(err).NotTo(HaveOccurred(), "ServiceAccount should exist")

			err = nonAdminClient.Update(testCtx, sa)
			Expect(err).To(HaveOccurred(), "Non-admin should NOT be able to modify ServiceAccounts")
			Expect(apierrors.IsForbidden(err)).To(BeTrue(), "Should get Forbidden error")
		})
	})

	ginkgo.Context("UploadTarget SFTP Configuration Tests", func() {
		var mustGatherName string
		var mustGatherCR *mustgatherv1alpha1.MustGather
		var sftpSecret *corev1.Secret

		ginkgo.AfterEach(func() {
			if mustGatherCR != nil {
				ginkgo.By("Cleaning up MustGather CR")
				_ = adminClient.Delete(testCtx, mustGatherCR)

				// Wait for cleanup
				Eventually(func() bool {
					err := adminClient.Get(testCtx, client.ObjectKey{
						Name:      mustGatherName,
						Namespace: ns.Name,
					}, &mustgatherv1alpha1.MustGather{})
					return apierrors.IsNotFound(err)
				}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())

				mustGatherCR = nil
			}

			if sftpSecret != nil {
				ginkgo.By("Cleaning up SFTP secret")
				_ = adminClient.Delete(testCtx, sftpSecret)
				sftpSecret = nil
			}
		})

		ginkgo.It("should fail upload with invalid SFTP credentials", func() {
			mustGatherName = fmt.Sprintf("mg-upload-target-e2e-test-%d", time.Now().UnixNano())

			ginkgo.By("Creating secret with invalid SFTP credentials")
			sftpSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("sftp-secret-%d", time.Now().UnixNano()),
					Namespace: ns.Name,
				},
				Data: map[string][]byte{
					"username": []byte("invalid-user"),
					"password": []byte("invalid-password"),
				},
			}
			err := adminClient.Create(testCtx, sftpSecret)
			Expect(err).NotTo(HaveOccurred(), "Failed to create SFTP secret")

			ginkgo.By("Creating MustGather with invalid SFTP credentials")
			mustGatherCR = &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mustGatherName,
					Namespace: ns.Name,
					Labels: map[string]string{
						"e2e-test": nonAdminLabel,
					},
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: serviceAccount,
					UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
						Type: mustgatherv1alpha1.UploadTypeSFTP,
						SFTP: &mustgatherv1alpha1.SFTPSpec{
							Host: "invalid-sftp-host.example.com",
							CaseManagementAccountSecretRef: corev1.LocalObjectReference{
								Name: sftpSecret.Name,
							},
						},
					},
				},
			}
			err = adminClient.Create(testCtx, mustGatherCR)
			Expect(err).NotTo(HaveOccurred(), "Failed to create MustGather CR")

			ginkgo.By("Verifying MustGather status is set to Failed due to SFTP validation failure")
			Eventually(func() string {
				err := adminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, mustGatherCR)
				if err != nil {
					return ""
				}
				return mustGatherCR.Status.Status
			}).WithTimeout(30*time.Second).WithPolling(2*time.Second).Should(Equal("Failed"),
				"MustGather status should be set to Failed when SFTP validation fails")

			ginkgo.By("Verifying status indicates SFTP validation failure")
			Expect(mustGatherCR.Status.Completed).To(BeTrue(),
				"MustGather should be marked as completed")
			Expect(mustGatherCR.Status.Reason).To(ContainSubstring("SFTP validation failed"),
				"Status reason should indicate SFTP validation failure")

			ginkgo.By("Verifying that NO Job was created due to pre-flight validation failure")
			job := &batchv1.Job{}
			err = adminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: ns.Name,
			}, job)
			Expect(err).To(HaveOccurred(),
				"Job should NOT be created when SFTP validation fails")
			Expect(apierrors.IsNotFound(err)).To(BeTrue(),
				"Job should not exist when pre-flight SFTP validation fails")

			ginkgo.GinkgoWriter.Printf(
				"SFTP validation correctly prevented Job creation for MustGather: %s\n"+
					"Status: %s, Reason: %s\n",
				mustGatherName, mustGatherCR.Status.Status, mustGatherCR.Status.Reason)
		})
	})
})

// Helper Functions

func createNonAdminClient() client.Client {
	ginkgo.By("Creating impersonated non-admin client")
	nonAdminConfig := rest.CopyConfig(adminRestConfig)
	nonAdminConfig.Impersonate = rest.ImpersonationConfig{
		UserName: nonAdminUser,
	}

	c, err := client.New(nonAdminConfig, client.Options{Scheme: testScheme})
	Expect(err).NotTo(HaveOccurred(), "Failed to create non-admin impersonation client")

	ginkgo.GinkgoWriter.Printf("Created impersonated client for user: %s\n", nonAdminUser)
	return c
}

func verifyOperatorDeployment() {
	ginkgo.By("Checking must-gather-operator namespace exists")
	ns := &corev1.Namespace{}
	err := adminClient.Get(testCtx, client.ObjectKey{Name: operatorNamespace}, ns)
	Expect(err).NotTo(HaveOccurred(), "must-gather-operator namespace should exist")

	ginkgo.By("Checking must-gather-operator deployment exists and is available")
	deployment := &appsv1.Deployment{}
	err = adminClient.Get(testCtx, client.ObjectKey{
		Name:      operatorDeployment,
		Namespace: operatorNamespace,
	}, deployment)
	Expect(err).NotTo(HaveOccurred(), "must-gather-operator deployment should exist")

	// Verify deployment has at least one ready replica
	Expect(deployment.Status.ReadyReplicas > 0).To(BeTrue(),
		"must-gather-operator deployment should have at least one ready replica")

	ginkgo.GinkgoWriter.Printf("must-gather-operator is deployed and available (ready replicas: %d)\n",
		deployment.Status.ReadyReplicas)
}

func createMustGatherCR(name, namespace, serviceAccountName string, retainResources bool) *mustgatherv1alpha1.MustGather {
	mg := &mustgatherv1alpha1.MustGather{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"e2e-test": nonAdminLabel,
			},
		},
		Spec: mustgatherv1alpha1.MustGatherSpec{
			ServiceAccountName:          serviceAccountName,
			RetainResourcesOnCompletion: &retainResources,
		},
	}

	err := nonAdminClient.Create(testCtx, mg)
	Expect(err).NotTo(HaveOccurred(), "Failed to create MustGather CR")

	return mg
}
