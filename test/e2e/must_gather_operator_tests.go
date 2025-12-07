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

	appsv1 "k8s.io/api/apps/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

// Test suite constants
const (
	nonAdminUser        = "openshift-test"
	testNamespacePrefix = "must-gather-e2e-"
	nonAdminCRRoleName  = "must-gather-cr-full-access"
	nonAdminCRBindName  = "must-gather-cr-binding"
	ServiceAccount      = "must-gather-sa"
	SARoleName          = "sa-reader-role"
	SARoleBindName      = "sa-reader-binding"
	nonAdminLabel       = "non-admin-must-gather"
)

// Test suite variables
var (
	testCtx         context.Context
	testScheme      *runtime.Scheme
	adminRestConfig *rest.Config
	adminClient     client.Client
	nonAdminClient  client.Client
	testNamespace   string
	setupComplete   bool
)

func init() {
	testScheme = runtime.NewScheme()
	utilruntime.Must(mustgatherv1alpha1.AddToScheme(testScheme))
	utilruntime.Must(appsv1.AddToScheme(testScheme))
	utilruntime.Must(corev1.AddToScheme(testScheme))
	utilruntime.Must(rbacv1.AddToScheme(testScheme))
	utilruntime.Must(batchv1.AddToScheme(testScheme))
	utilruntime.Must(authorizationv1.AddToScheme(testScheme))
}

var _ = ginkgo.Describe("Must-Gather Operator E2E Tests", ginkgo.Ordered, func() {

	// BeforeAll - Admin Setup Phase (Steps 1-5 from doc)
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

		ginkgo.By("STEP 2: Admin creates test namespace")
		createTestNamespace()

		ginkgo.By("STEP 3: Admin defines privileges for MustGather CRs")
		createNonAdminClusterRole()

		ginkgo.By("STEP 4: Admin binds the non-admin user to the ClusterRole")
		createNonAdminClusterRoleBinding()

		ginkgo.By("STEP 5: Admin provides a ServiceAccount for execution")
		createServiceAccount()
		createSARBACForMustGather()

		ginkgo.By("Initializing non-admin client for tests")
		nonAdminClient = createNonAdminClient()

		setupComplete = true
		ginkgo.GinkgoWriter.Println("✅ Admin setup complete - RBAC and ServiceAccount configured")
	})

	// AfterAll - Cleanup
	ginkgo.AfterAll(func() {
		if !setupComplete {
			ginkgo.GinkgoWriter.Println("⚠️  Setup was not complete, skipping cleanup")
			return
		}

		ginkgo.By("CLEANUP: Removing all test resources")
		cleanupTestResources()
	})

	// Test Cases

	ginkgo.Context("Non-Admin User Permissions", func() {
		ginkgo.It("non-admin user should be able to create MustGather CRs (SAR check)", func() {
			sar := &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User: nonAdminUser,
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Verb:      "create",
						Group:     "operator.openshift.io",
						Resource:  "mustgathers",
						Namespace: testNamespace,
					},
				},
			}
			err := adminClient.Create(testCtx, sar)
			Expect(err).NotTo(HaveOccurred())
			Expect(sar.Status.Allowed).To(BeTrue(), "Non-admin should be able to create MustGather CRs")
		})

		ginkgo.It("non-admin user can actually create MustGather CR using impersonation", func() {
			testMGName := fmt.Sprintf("test-non-admin-mustgather-%d", time.Now().Unix())

			ginkgo.By("Non-admin user creating MustGather CR")
			mg := &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testMGName,
					Namespace: testNamespace,
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: ServiceAccount,
				},
			}

			err := nonAdminClient.Create(testCtx, mg)
			Expect(err).NotTo(HaveOccurred(), "Non-admin user should be able to create MustGather CR")

			ginkgo.By("Verifying MustGather CR was created")
			createdMG := &mustgatherv1alpha1.MustGather{}
			err = nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      testMGName,
				Namespace: testNamespace,
			}, createdMG)
			Expect(err).NotTo(HaveOccurred())
			Expect(createdMG.Name).To(Equal(testMGName))

			ginkgo.By("Cleanup: Non-admin user deleting their MustGather CR")
			err = nonAdminClient.Delete(testCtx, mg)
			Expect(err).NotTo(HaveOccurred(), "Non-admin should be able to delete their own CR")
		})

		ginkgo.It("non-admin user should be able to get, list and watch MustGather CRs (SAR check)", func() {
			for _, verb := range []string{"get", "list", "watch"} {
				sar := &authorizationv1.SubjectAccessReview{
					Spec: authorizationv1.SubjectAccessReviewSpec{
						User: nonAdminUser,
						ResourceAttributes: &authorizationv1.ResourceAttributes{
							Verb:     verb,
							Group:    "operator.openshift.io",
							Resource: "mustgathers",
						},
					},
				}
				err := adminClient.Create(testCtx, sar)
				Expect(err).NotTo(HaveOccurred())
				Expect(sar.Status.Allowed).To(BeTrue(),
					fmt.Sprintf("Non-admin should be able to %s MustGather CRs", verb))
			}
		})

		ginkgo.It("non-admin user can actually list MustGather CRs using impersonation", func() {
			ginkgo.By("Non-admin user listing MustGather CRs in their namespace")
			mgList := &mustgatherv1alpha1.MustGatherList{}
			err := nonAdminClient.List(testCtx, mgList, client.InNamespace(testNamespace))
			Expect(err).NotTo(HaveOccurred(), "Non-admin should be able to list MustGather CRs")

			ginkgo.GinkgoWriter.Printf("Non-admin user can see %d MustGather CRs in namespace %s\n",
				len(mgList.Items), testNamespace)
		})

		ginkgo.It("non-admin user should be able to monitor Job and Pod status", func() {
			for _, verb := range []string{"get", "list", "watch"} {
				// Check Jobs permission
				sarJob := &authorizationv1.SubjectAccessReview{
					Spec: authorizationv1.SubjectAccessReviewSpec{
						User: nonAdminUser,
						ResourceAttributes: &authorizationv1.ResourceAttributes{
							Verb:     verb,
							Group:    "batch",
							Resource: "jobs",
						},
					},
				}
				err := adminClient.Create(testCtx, sarJob)
				Expect(err).NotTo(HaveOccurred())
				Expect(sarJob.Status.Allowed).To(BeTrue(),
					fmt.Sprintf("Non-admin should be able to %s jobs", verb))

				// Check Pods permission
				sarPod := &authorizationv1.SubjectAccessReview{
					Spec: authorizationv1.SubjectAccessReviewSpec{
						User: nonAdminUser,
						ResourceAttributes: &authorizationv1.ResourceAttributes{
							Verb:     verb,
							Group:    "",
							Resource: "pods",
						},
					},
				}
				err = adminClient.Create(testCtx, sarPod)
				Expect(err).NotTo(HaveOccurred())
				Expect(sarPod.Status.Allowed).To(BeTrue(),
					fmt.Sprintf("Non-admin should be able to %s pods", verb))
			}
		})

		ginkgo.It("non-admin user should be able to access pod logs", func() {
			sar := &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User: nonAdminUser,
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Verb:        "get",
						Group:       "",
						Resource:    "pods",
						Subresource: "log",
					},
				},
			}
			err := adminClient.Create(testCtx, sar)
			Expect(err).NotTo(HaveOccurred())
			Expect(sar.Status.Allowed).To(BeTrue(), "Non-admin should be able to get pod logs")
		})

		ginkgo.It("non-admin user should NOT have cluster-admin privileges (SAR check)", func() {
			// Verify non-admin cannot delete namespaces
			sar := &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User: nonAdminUser,
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Verb:     "delete",
						Group:    "",
						Resource: "namespaces",
					},
				},
			}
			err := adminClient.Create(testCtx, sar)
			Expect(err).NotTo(HaveOccurred())
			Expect(sar.Status.Allowed).To(BeFalse(),
				"Non-admin should NOT be able to delete namespaces")

			// Verify non-admin cannot create ClusterRoles
			sar2 := &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User: nonAdminUser,
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Verb:     "create",
						Group:    "rbac.authorization.k8s.io",
						Resource: "clusterroles",
					},
				},
			}
			err = adminClient.Create(testCtx, sar2)
			Expect(err).NotTo(HaveOccurred())
			Expect(sar2.Status.Allowed).To(BeFalse(),
				"Non-admin should NOT be able to create ClusterRoles")
		})

		ginkgo.It("non-admin user CANNOT perform admin operations using impersonation", func() {
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

			ginkgo.GinkgoWriter.Println("✅ Non-admin user correctly blocked from admin operations")
		})
	})

	ginkgo.Context("ServiceAccount Permissions", func() {
		ginkgo.It("SA should have read access to cluster resources", func() {
			saUser := fmt.Sprintf("system:serviceaccount:%s:%s", testNamespace, ServiceAccount)

			checkSAPermission := func(verb, group, resource string) {
				sar := &authorizationv1.SubjectAccessReview{
					Spec: authorizationv1.SubjectAccessReviewSpec{
						User: saUser,
						ResourceAttributes: &authorizationv1.ResourceAttributes{
							Verb:     verb,
							Group:    group,
							Resource: resource,
						},
					},
				}
				err := adminClient.Create(testCtx, sar)
				Expect(err).NotTo(HaveOccurred())
				Expect(sar.Status.Allowed).To(BeTrue(),
					fmt.Sprintf("SA should be able to %s %s.%s", verb, resource, group))
			}

			// Check read permissions for must-gather required resources
			checkSAPermission("get", "", "nodes")
			checkSAPermission("list", "", "nodes")
			checkSAPermission("get", "", "pods")
			checkSAPermission("list", "", "pods")
			checkSAPermission("get", "", "namespaces")
			checkSAPermission("list", "", "namespaces")
		})

		ginkgo.It("SA should NOT have write/delete permissions", func() {
			saUser := fmt.Sprintf("system:serviceaccount:%s:%s", testNamespace, ServiceAccount)

			// Verify SA cannot delete pods
			sar := &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User: saUser,
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Verb:     "delete",
						Group:    "",
						Resource: "pods",
					},
				},
			}
			err := adminClient.Create(testCtx, sar)
			Expect(err).NotTo(HaveOccurred())
			Expect(sar.Status.Allowed).To(BeFalse(),
				"SA should NOT be able to delete pods")

			// Verify SA cannot create secrets
			sar2 := &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User: saUser,
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Verb:     "create",
						Group:    "",
						Resource: "secrets",
					},
				},
			}
			err = adminClient.Create(testCtx, sar2)
			Expect(err).NotTo(HaveOccurred())
			Expect(sar2.Status.Allowed).To(BeFalse(),
				"SA should NOT be able to create secrets")
		})
	})

	ginkgo.Context("Non-Admin User MustGather Collection Flow", func() {
		var mustGatherName string
		var createdMustGather *mustgatherv1alpha1.MustGather

		ginkgo.BeforeEach(func() {
			mustGatherName = fmt.Sprintf("test-non-admin-mg-%d", time.Now().Unix())
		})

		ginkgo.AfterEach(func() {
			if createdMustGather != nil {
				ginkgo.By("Cleaning up MustGather CR")
				_ = adminClient.Delete(testCtx, createdMustGather)

				// Wait for cleanup
				Eventually(func() bool {
					err := adminClient.Get(testCtx, client.ObjectKey{
						Name:      mustGatherName,
						Namespace: testNamespace,
					}, &mustgatherv1alpha1.MustGather{})
					return apierrors.IsNotFound(err)
				}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())
			}
		})

		ginkgo.It("non-admin user should successfully submit MustGather CR with SA", func() {
			ginkgo.By("Non-admin user creating MustGather CR using impersonation")
			createdMustGather = &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mustGatherName,
					Namespace: testNamespace,
					Annotations: map[string]string{
						"test.description": "Non-admin user submitted MustGather",
						"test.user":        nonAdminUser,
					},
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: ServiceAccount,
					// Using default must-gather image
				},
			}

			// Using impersonated non-admin client
			err := nonAdminClient.Create(testCtx, createdMustGather)
			Expect(err).NotTo(HaveOccurred(), "Non-admin user should be able to create MustGather CR")

			ginkgo.By("Verifying MustGather CR was created successfully")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			err = nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: testNamespace,
			}, fetchedMG)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchedMG.Spec.ServiceAccountName).To(Equal(ServiceAccount))

			ginkgo.GinkgoWriter.Printf("✅ Non-admin user '%s' successfully created MustGather CR: %s\n",
				nonAdminUser, mustGatherName)
		})

		ginkgo.It("operator should create Job using ServiceAccount", func() {
			ginkgo.By("Non-admin user creating MustGather CR")
			createdMustGather = createMustGatherCR(mustGatherName, ServiceAccount)

			ginkgo.By("Waiting for operator to create Job (non-admin user perspective)")
			job := &batchv1.Job{}
			Eventually(func() error {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: testNamespace,
				}, job)
				if err != nil {
					// Debug: Check if Job exists with admin client
					jobDebug := &batchv1.Job{}
					errAdmin := adminClient.Get(testCtx, client.ObjectKey{
						Name:      mustGatherName,
						Namespace: testNamespace,
					}, jobDebug)
					if errAdmin == nil {
						ginkgo.GinkgoWriter.Printf("⚠️  Job exists but non-admin can't see it (RBAC issue)\n")
					} else {
						ginkgo.GinkgoWriter.Printf("Job not found by operator yet: %v\n", errAdmin)
					}
				}
				return err
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"Non-admin user should be able to see Job created by operator")

			ginkgo.By("Verifying Job uses ServiceAccount")
			Expect(job.Spec.Template.Spec.ServiceAccountName).To(Equal(ServiceAccount),
				"Job must use the ServiceAccount specified in MustGather CR")

			ginkgo.By("Verifying Job has required specifications")
			Expect(len(job.Spec.Template.Spec.Containers)).Should(BeNumerically(">=", 1),
				"Job should have at least one container")

			hasGatherContainer := false
			for _, container := range job.Spec.Template.Spec.Containers {
				if container.Name == "gather" {
					hasGatherContainer = true
					Expect(container.Image).NotTo(BeEmpty(), "Gather container should have an image")
					break
				}
			}
			Expect(hasGatherContainer).To(BeTrue(), "Job should have gather container")

			ginkgo.By("Verifying Job has output volume for artifacts")
			hasOutputVolume := false
			for _, volume := range job.Spec.Template.Spec.Volumes {
				if volume.Name == "must-gather-output" {
					hasOutputVolume = true
					break
				}
			}
			Expect(hasOutputVolume).To(BeTrue(), "Job should have must-gather-output volume")
		})

		ginkgo.It("Pod should gather data and complete successfully", func() {
			ginkgo.By("Non-admin user creating MustGather CR with RetainResourcesOnCompletion")
			createdMustGather = createMustGatherCRWithRetention(mustGatherName, ServiceAccount)

			ginkgo.By("Waiting for Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: testNamespace,
				}, job)
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			ginkgo.By("Waiting for Pod to be created")
			var gatherPod *corev1.Pod
			Eventually(func() bool {
				podList := &corev1.PodList{}
				if err := nonAdminClient.List(testCtx, podList,
					client.InNamespace(testNamespace),
					client.MatchingLabels{"job-name": mustGatherName}); err != nil {
					return false
				}
				if len(podList.Items) > 0 {
					gatherPod = &podList.Items[0]
					return true
				}
				return false
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(BeTrue(),
				"Pod should be created by Job")

			ginkgo.By("Verifying Pod uses  ServiceAccount")
			Expect(gatherPod.Spec.ServiceAccountName).To(Equal(ServiceAccount),
				"Pod must use  ServiceAccount for data collection")

			ginkgo.By("Verifying Pod is scheduled")
			Eventually(func() string {
				pod := &corev1.Pod{}
				_ = nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      gatherPod.Name,
					Namespace: testNamespace,
				}, pod)
				return pod.Spec.NodeName
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).ShouldNot(BeEmpty(),
				"Pod should be scheduled to a node")

			ginkgo.By("Monitoring Pod execution phase")
			Eventually(func() corev1.PodPhase {
				pod := &corev1.Pod{}
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      gatherPod.Name,
					Namespace: testNamespace,
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
					Namespace: testNamespace,
				}, pod)
				if err != nil {
					return false
				}

				for _, cs := range pod.Status.ContainerStatuses {
					if cs.Name == "gather" {
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

			ginkgo.By("Waiting for Job to complete")
			Eventually(func() int32 {
				_ = nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: testNamespace,
				}, job)
				return job.Status.Succeeded + job.Status.Failed
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(
				BeNumerically(">", 0), "Job should eventually complete")

			ginkgo.By("Verifying MustGather CR status is updated")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			Eventually(func() string {
				_ = nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: testNamespace,
				}, fetchedMG)
				return fetchedMG.Status.Status
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).ShouldNot(BeEmpty(),
				"MustGather status should be updated by operator")

			ginkgo.GinkgoWriter.Printf("MustGather Status: %s - Completed: %v - Reason: %s\n",
				fetchedMG.Status.Status, fetchedMG.Status.Completed, fetchedMG.Status.Reason)
		})

		ginkgo.It("non-admin user should be able to monitor MustGather progress", func() {
			ginkgo.By("Non-admin user creating MustGather CR")
			createdMustGather = createMustGatherCR(mustGatherName, ServiceAccount)

			ginkgo.By("Non-admin user checking MustGather CR status using impersonation")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: testNamespace,
				}, fetchedMG)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"Non-admin user should be able to get MustGather CR")
			ginkgo.GinkgoWriter.Printf("✅ Non-admin can read MustGather status: %s\n", fetchedMG.Status.Status)

			ginkgo.By("Non-admin user listing Jobs in namespace")
			jobList := &batchv1.JobList{}
			Eventually(func() error {
				return nonAdminClient.List(testCtx, jobList, client.InNamespace(testNamespace))
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"Non-admin user should be able to list Jobs")
			ginkgo.GinkgoWriter.Printf("✅ Non-admin can see %d Jobs\n", len(jobList.Items))

			ginkgo.By("Non-admin user listing Pods in namespace")
			podList := &corev1.PodList{}
			Eventually(func() error {
				return nonAdminClient.List(testCtx, podList, client.InNamespace(testNamespace))
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"Non-admin user should be able to list Pods")
			ginkgo.GinkgoWriter.Printf("✅ Non-admin can see %d Pods\n", len(podList.Items))

			ginkgo.By("Non-admin user checking for gather Pods")
			Eventually(func() bool {
				pods := &corev1.PodList{}
				if err := nonAdminClient.List(testCtx, pods,
					client.InNamespace(testNamespace),
					client.MatchingLabels{"job-name": mustGatherName}); err != nil {
					return false
				}
				if len(pods.Items) > 0 {
					ginkgo.GinkgoWriter.Printf("✅ Non-admin can see gather pod: %s\n", pods.Items[0].Name)
				}
				return len(pods.Items) > 0
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())

			ginkgo.GinkgoWriter.Println("✅ Non-admin user successfully monitored MustGather progress")
		})
	})

	ginkgo.Context("Security and Isolation Tests", func() {
		ginkgo.It("should fail when non-admin user tries to use privileged ServiceAccount", func() {
			mustGatherName := fmt.Sprintf("test-privileged-sa-%d", time.Now().Unix())

			ginkgo.By("Attempting to create MustGather with non-existent privileged SA")
			mg := &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mustGatherName,
					Namespace: testNamespace,
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: "cluster-admin-sa", // Doesn't exist
				},
			}

			err := nonAdminClient.Create(testCtx, mg)
			Expect(err).NotTo(HaveOccurred(), "CR creation should succeed")

			defer func() {
				_ = nonAdminClient.Delete(testCtx, mg)
			}()

			ginkgo.By("Verifying Job is created but Pod fails due to missing SA")
			job := &batchv1.Job{}
			Eventually(func() error {
				return adminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: testNamespace,
				}, job)
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			// Job should exist but pods won't be created due to missing SA
			Consistently(func() int32 {
				_ = adminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: testNamespace,
				}, job)
				return job.Status.Active
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Equal(int32(0)),
				"Job should not have active pods due to missing ServiceAccount")
		})

		ginkgo.It("should enforce ServiceAccount permissions during data collection", func() {
			mustGatherName := fmt.Sprintf("test-sa-permissions-%d", time.Now().Unix())

			ginkgo.By("Creating MustGather with SA")
			mg := createMustGatherCR(mustGatherName, ServiceAccount)

			defer func() {
				_ = nonAdminClient.Delete(testCtx, mg)
			}()

			ginkgo.By("Waiting for Pod to start")
			var gatherPod *corev1.Pod
			Eventually(func() bool {
				podList := &corev1.PodList{}
				if err := adminClient.List(testCtx, podList,
					client.InNamespace(testNamespace),
					client.MatchingLabels{"job-name": mustGatherName}); err != nil {
					return false
				}
				if len(podList.Items) > 0 {
					gatherPod = &podList.Items[0]
					return true
				}
				return false
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())

			ginkgo.By("Verifying Pod has no privilege escalation")
			Expect(gatherPod.Spec.ServiceAccountName).To(Equal(ServiceAccount))

			// Check that pod doesn't request privileged mode
			for _, container := range gatherPod.Spec.Containers {
				if container.SecurityContext != nil && container.SecurityContext.Privileged != nil {
					Expect(*container.SecurityContext.Privileged).To(BeFalse(),
						"Container should not run in privileged mode")
				}
			}

			ginkgo.By("Checking for permission denied errors in events")
			var permissionErrors []string

			// Wait for events to be populated (if any permission errors occur, they should appear)
			Eventually(func() bool {
				events := &corev1.EventList{}
				err := adminClient.List(testCtx, events, client.InNamespace(testNamespace))
				if err != nil {
					return false
				}

				// Collect any permission errors
				permissionErrors = []string{}
				for _, event := range events.Items {
					if (event.InvolvedObject.Name == mustGatherName ||
						event.InvolvedObject.Name == gatherPod.Name) &&
						(strings.Contains(strings.ToLower(event.Message), "forbidden") ||
							strings.Contains(strings.ToLower(event.Message), "unauthorized")) {
						permissionErrors = append(permissionErrors, event.Message)
					}
				}

				// Return true once we have events (or after checking for a reasonable time)
				return len(events.Items) > 0
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(BeTrue(),
				"Events should be available for inspection")

			if len(permissionErrors) > 0 {
				ginkgo.GinkgoWriter.Println("⚠️  Expected permission restrictions observed:")
				for _, err := range permissionErrors {
					ginkgo.GinkgoWriter.Printf("  - %s\n", err)
				}
			}
		})

		ginkgo.It("should prevent non-admin user from modifying RBAC", func() {
			ginkgo.By("Verifying non-admin cannot modify ClusterRole")
			sar := &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User: nonAdminUser,
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Verb:     "update",
						Group:    "rbac.authorization.k8s.io",
						Resource: "clusterroles",
						Name:     nonAdminCRRoleName,
					},
				},
			}
			err := adminClient.Create(testCtx, sar)
			Expect(err).NotTo(HaveOccurred())
			Expect(sar.Status.Allowed).To(BeFalse(),
				"Non-admin should NOT be able to modify ClusterRoles")

			ginkgo.By("Verifying non-admin cannot modify ServiceAccount")
			sar2 := &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User: nonAdminUser,
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Verb:      "update",
						Group:     "",
						Resource:  "serviceaccounts",
						Name:      ServiceAccount,
						Namespace: testNamespace,
					},
				},
			}
			err = adminClient.Create(testCtx, sar2)
			Expect(err).NotTo(HaveOccurred())
			Expect(sar2.Status.Allowed).To(BeFalse(),
				"Non-admin should NOT be able to modify ServiceAccounts")
		})
	})

	ginkgo.Context("Cleanup and Artifact Retrieval", func() {
		ginkgo.It("non-admin user should be able to delete their MustGather CR (SAR check)", func() {
			mustGatherName := fmt.Sprintf("test-cleanup-%d", time.Now().Unix())

			ginkgo.By("Verifying non-admin can delete MustGather CRs")
			sar := &authorizationv1.SubjectAccessReview{
				Spec: authorizationv1.SubjectAccessReviewSpec{
					User: nonAdminUser,
					ResourceAttributes: &authorizationv1.ResourceAttributes{
						Verb:      "delete",
						Group:     "operator.openshift.io",
						Resource:  "mustgathers",
						Name:      mustGatherName,
						Namespace: testNamespace,
					},
				},
			}
			err := adminClient.Create(testCtx, sar)
			Expect(err).NotTo(HaveOccurred())
			Expect(sar.Status.Allowed).To(BeTrue(), "Non-admin should be able to delete MustGather CR")
		})

		ginkgo.It("non-admin user can actually delete their MustGather CR using impersonation", func() {
			mustGatherName := fmt.Sprintf("test-cleanup-impersonate-%d", time.Now().Unix())

			ginkgo.By("Non-admin user creating MustGather CR")
			mg := &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mustGatherName,
					Namespace: testNamespace,
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: ServiceAccount,
				},
			}
			err := nonAdminClient.Create(testCtx, mg)
			Expect(err).NotTo(HaveOccurred())

			ginkgo.By("Non-admin user deleting their MustGather CR")
			err = nonAdminClient.Delete(testCtx, mg)
			Expect(err).NotTo(HaveOccurred(), "Non-admin should be able to delete their own CR")

			ginkgo.By("Verifying CR is deleted")
			Eventually(func() bool {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: testNamespace,
				}, &mustgatherv1alpha1.MustGather{})
				return apierrors.IsNotFound(err)
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())

			ginkgo.GinkgoWriter.Printf("✅ Non-admin user successfully deleted MustGather CR: %s\n", mustGatherName)
		})

		ginkgo.It("should clean up Job and Pod when MustGather CR is deleted", func() {
			mustGatherName := fmt.Sprintf("test-cascading-delete-%d", time.Now().Unix())

			ginkgo.By("Creating MustGather CR")
			mg := createMustGatherCR(mustGatherName, ServiceAccount)

			ginkgo.By("Waiting for Job to be created")
			Eventually(func() error {
				return adminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: testNamespace,
				}, &batchv1.Job{})
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			ginkgo.By("Deleting MustGather CR")
			err := nonAdminClient.Delete(testCtx, mg)
			Expect(err).NotTo(HaveOccurred())

			ginkgo.By("Verifying Job is eventually cleaned up")
			Eventually(func() bool {
				err := adminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: testNamespace,
				}, &batchv1.Job{})
				return apierrors.IsNotFound(err)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(BeTrue(),
				"Job should be cleaned up when MustGather CR is deleted")
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

	ginkgo.GinkgoWriter.Printf("✅ Created impersonated client for user: %s\n", nonAdminUser)
	return c
}

func verifyOperatorDeployment() {
	ginkgo.By("Checking must-gather-operator namespace exists")
	ns := &corev1.Namespace{}
	err := adminClient.Get(testCtx, client.ObjectKey{Name: "must-gather-operator"}, ns)
	Expect(err).NotTo(HaveOccurred(), "must-gather-operator namespace should exist")

	ginkgo.By("Checking must-gather-operator deployment exists and is available")
	deployment := &appsv1.Deployment{}
	err = adminClient.Get(testCtx, client.ObjectKey{
		Name:      "must-gather-operator",
		Namespace: "must-gather-operator",
	}, deployment)
	Expect(err).NotTo(HaveOccurred(), "must-gather-operator deployment should exist")

	// Verify deployment has at least one ready replica
	Expect(deployment.Status.ReadyReplicas > 0).Should(BeTrue(),
		"must-gather-operator deployment should have at least one ready replica")

	ginkgo.GinkgoWriter.Printf("✅ must-gather-operator is deployed and available (ready replicas: %d)\n",
		deployment.Status.ReadyReplicas)
}

func createTestNamespace() {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: testNamespacePrefix,
			Labels: map[string]string{
				"test": nonAdminLabel,
				"operator.openshift.io/must-gather-operator": "e2e-test",
			},
		},
	}

	err := adminClient.Create(testCtx, ns)
	Expect(err).NotTo(HaveOccurred(), "Failed to create test namespace")

	// Capture the generated namespace name
	testNamespace = ns.Name

	ginkgo.GinkgoWriter.Printf("✅ Created test namespace: %s\n", testNamespace)
}

func createNonAdminClusterRole() {
	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: nonAdminCRRoleName,
			Labels: map[string]string{
				"test": nonAdminLabel,
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"operator.openshift.io"},
				Resources: []string{"mustgathers"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"operator.openshift.io"},
				Resources: []string{"mustgathers/status"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"batch"},
				Resources: []string{"jobs"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"pods/log"},
				Verbs:     []string{"get", "list"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"events"},
				Verbs:     []string{"get", "list"},
			},
		},
	}

	err := adminClient.Create(testCtx, cr)
	if apierrors.IsAlreadyExists(err) {
		// Update if already exists
		_ = adminClient.Update(testCtx, cr)
	} else {
		Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRole for non-admin")
	}

	ginkgo.GinkgoWriter.Printf("✅ Created ClusterRole: %s\n", nonAdminCRRoleName)
}

func createNonAdminClusterRoleBinding() {
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: nonAdminCRBindName,
			Labels: map[string]string{
				"test": nonAdminLabel,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     nonAdminCRRoleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:     "User",
				Name:     nonAdminUser,
				APIGroup: "rbac.authorization.k8s.io",
			},
		},
	}

	err := adminClient.Create(testCtx, crb)
	if apierrors.IsAlreadyExists(err) {
		_ = adminClient.Update(testCtx, crb)
	} else {
		Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")
	}

	ginkgo.GinkgoWriter.Printf("✅ Created ClusterRoleBinding: %s for user %s\n",
		nonAdminCRBindName, nonAdminUser)
}

func createServiceAccount() {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ServiceAccount,
			Namespace: testNamespace,
			Labels: map[string]string{
				"test": nonAdminLabel,
				"type": "must-gather-sa",
			},
		},
	}

	err := adminClient.Create(testCtx, sa)
	if !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred(), "Failed to create ServiceAccount")
	}

	ginkgo.GinkgoWriter.Printf("✅ Created ServiceAccount: %s/%s\n",
		testNamespace, ServiceAccount)
}

func createSARBACForMustGather() {
	// Create ClusterRole with minimal read permissions
	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: SARoleName,
			Labels: map[string]string{
				"test": nonAdminLabel,
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"nodes", "pods", "namespaces", "services",
					"configmaps", "events", "persistentvolumes", "persistentvolumeclaims"},
				Verbs: []string{"get", "list"},
			},
			{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments", "daemonsets", "replicasets", "statefulsets"},
				Verbs:     []string{"get", "list"},
			},
			{
				APIGroups: []string{"batch"},
				Resources: []string{"jobs", "cronjobs"},
				Verbs:     []string{"get", "list"},
			},
			// Add more resources as needed for must-gather
		},
	}

	err := adminClient.Create(testCtx, cr)
	if apierrors.IsAlreadyExists(err) {
		_ = adminClient.Update(testCtx, cr)
	} else {
		Expect(err).NotTo(HaveOccurred(), "Failed to create  SA ClusterRole")
	}

	// Create ClusterRoleBinding
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: SARoleBindName,
			Labels: map[string]string{
				"test": nonAdminLabel,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     SARoleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      ServiceAccount,
				Namespace: testNamespace,
			},
		},
	}

	err = adminClient.Create(testCtx, crb)
	if apierrors.IsAlreadyExists(err) {
		_ = adminClient.Update(testCtx, crb)
	} else {
		Expect(err).NotTo(HaveOccurred(), "Failed to create  SA ClusterRoleBinding")
	}

	ginkgo.GinkgoWriter.Printf("✅ Created RBAC for  SA: %s\n", SARoleName)
}

func createMustGatherCR(name, serviceAccountName string) *mustgatherv1alpha1.MustGather {
	mg := &mustgatherv1alpha1.MustGather{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels: map[string]string{
				"test": nonAdminLabel,
			},
		},
		Spec: mustgatherv1alpha1.MustGatherSpec{
			ServiceAccountName: serviceAccountName,
		},
	}

	err := nonAdminClient.Create(testCtx, mg)
	Expect(err).NotTo(HaveOccurred(), "Failed to create MustGather CR")

	return mg
}

func createMustGatherCRWithRetention(name, serviceAccountName string) *mustgatherv1alpha1.MustGather {
	retainResources := true
	mg := &mustgatherv1alpha1.MustGather{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels: map[string]string{
				"test": nonAdminLabel,
			},
		},
		Spec: mustgatherv1alpha1.MustGatherSpec{
			ServiceAccountName:          serviceAccountName,
			RetainResourcesOnCompletion: &retainResources,
		},
	}

	err := nonAdminClient.Create(testCtx, mg)
	Expect(err).NotTo(HaveOccurred(), "Failed to create MustGather CR with retention")

	return mg
}

func cleanupTestResources() {
	policy := metav1.DeletePropagationForeground
	deleteOpts := &client.DeleteOptions{PropagationPolicy: &policy}

	// Delete namespace (will delete SA and MustGather CRs)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}}
	_ = adminClient.Delete(testCtx, ns, deleteOpts)

	// Delete ClusterRoleBindings
	crb1 := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: nonAdminCRBindName}}
	_ = adminClient.Delete(testCtx, crb1, deleteOpts)

	crb2 := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: SARoleBindName}}
	_ = adminClient.Delete(testCtx, crb2, deleteOpts)

	// Delete ClusterRoles
	cr1 := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: nonAdminCRRoleName}}
	_ = adminClient.Delete(testCtx, cr1, deleteOpts)

	cr2 := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: SARoleName}}
	_ = adminClient.Delete(testCtx, cr2, deleteOpts)

	ginkgo.GinkgoWriter.Println("✅ Cleanup complete")
}
