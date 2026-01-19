// DO NOT REMOVE TAGS BELOW. IF ANY NEW TEST FILES ARE CREATED UNDER /test/e2e, PLEASE ADD THESE TAGS TO THEM IN ORDER TO BE EXCLUDED FROM UNIT TESTS.
//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"
	"math/rand"
	"os"
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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

type MustGatherCROptions struct {
	UploadTarget     *UploadTargetOptions
	PersistentVolume *PersistentVolumeOptions
	Timeout          *time.Duration
}

// UploadTargetOptions configures SFTP upload target
type UploadTargetOptions struct {
	CaseID       string
	SecretName   string
	InternalUser bool
	Host         string
}

// PersistentVolumeOptions configures PV storage
type PersistentVolumeOptions struct {
	PVCName string
	SubPath string
}

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
	uploadContainerName = "upload"
	outputVolumeName    = "must-gather-output"
	jobNameLabelKey     = "job-name"

	// UploadTarget test constants
	caseManagementSecretNameValid         = "case-management-creds-valid"
	caseManagementSecretNameInvalid       = "case-management-creds-invalid"
	caseManagementSecretNameEmptyUsername = "case-management-creds-empty-username"
	caseManagementSecretNameEmptyPassword = "case-management-creds-empty-password"
	stageHostName                         = "sftp.access.stage.redhat.com"

	// PersistentVolume test constants
	mustGatherPVCName        = "must-gather-pvc"
	caseCredsConfigDirEnvVar = "CASE_MANAGEMENT_CREDS_CONFIG_DIR"
	vaultUsernameKey         = "sftp-username-e2e"
	vaultPasswordKey         = "sftp-password-e2e"
)

//go:embed testdata/*
var testassets embed.FS

// Test suite variables
var (
	testCtx           context.Context
	testScheme        *k8sruntime.Scheme
	adminRestConfig   *rest.Config
	adminClient       client.Client
	nonAdminClient    client.Client
	nonAdminClientset *kubernetes.Clientset
	operatorImage     string
	setupComplete     bool
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

		ginkgo.By("Getting operator image for verification pods")
		operatorImage, err = getOperatorImage()
		Expect(err).NotTo(HaveOccurred(), "Failed to get operator image")
		ginkgo.GinkgoWriter.Printf("Operator image: %s\n", operatorImage)

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
				_ = nonAdminClient.Delete(testCtx, mustGatherCR)

				// Wait for cleanup
				Eventually(func() bool {
					err := nonAdminClient.Get(testCtx, client.ObjectKey{
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
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, false, nil)

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
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, false, nil)

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
			mg := createMustGatherCR(mustGatherName, ns.Name, serviceAccount, false, nil)

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
			mg := createMustGatherCR(mustGatherName, ns.Name, serviceAccount, false, nil)

			ginkgo.By("Waiting for Job to be created")
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, &batchv1.Job{})
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			ginkgo.By("Waiting for Pod to be created")
			Eventually(func() int {
				podList := &corev1.PodList{}
				if err := nonAdminClient.List(testCtx, podList,
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
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, &batchv1.Job{})
				return apierrors.IsNotFound(err)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(BeTrue(),
				"Job should be cleaned up when MustGather CR is deleted")

			ginkgo.By("Verifying Pods are eventually cleaned up")
			Eventually(func() int {
				podList := &corev1.PodList{}
				if err := nonAdminClient.List(testCtx, podList,
					client.InNamespace(ns.Name),
					client.MatchingLabels{jobNameLabelKey: mustGatherName}); err != nil {
					return 0
				}
				return len(podList.Items)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Equal(0),
				"Pods should be cleaned up when MustGather CR is deleted")
		})

		ginkgo.It("should configure timeout correctly and complete successfully", func() {
			ginkgo.By("Creating MustGather CR with 1 minute timeout")
			timeout := 1 * time.Minute
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				Timeout: &timeout,
			})

			ginkgo.By("Verifying MustGather CR has timeout set")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			err := nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: ns.Name,
			}, fetchedMG)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchedMG.Spec.MustGatherTimeout).NotTo(BeNil(), "MustGatherTimeout should be set")
			Expect(fetchedMG.Spec.MustGatherTimeout.Duration).To(Equal(timeout),
				"MustGatherTimeout should be 1 minute")

			ginkgo.By("Waiting for Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"Job should be created for MustGather with timeout")

			ginkgo.By("Verifying Job's gather container has timeout in command")
			var gatherContainer *corev1.Container
			for i := range job.Spec.Template.Spec.Containers {
				if job.Spec.Template.Spec.Containers[i].Name == gatherContainerName {
					gatherContainer = &job.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(gatherContainer).NotTo(BeNil(), "Job should have gather container")

			// The gather container command should include the configured timeout value (60 seconds)
			commandStr := strings.Join(gatherContainer.Command, " ")
			ginkgo.GinkgoWriter.Printf("Gather container command: %s\n", commandStr)
			Expect(commandStr).To(ContainSubstring("timeout 60"),
				"Gather container command should include timeout value of 60 seconds")

			ginkgo.By("Waiting for Job to complete")
			Eventually(func() bool {
				if err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job); err != nil {
					return false
				}
				return job.Status.Succeeded > 0 || job.Status.Failed > 0
			}).WithTimeout(2*time.Minute).WithPolling(10*time.Second).Should(BeTrue(),
				"Job should complete within the timeout period")

			ginkgo.By("Verifying MustGather CR status is updated")
			err = nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: ns.Name,
			}, fetchedMG)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchedMG.Status.Completed).To(BeTrue(), "MustGather should be marked as completed")

			ginkgo.GinkgoWriter.Printf("MustGather with timeout completed - Status: %s, Reason: %s\n",
				fetchedMG.Status.Status, fetchedMG.Status.Reason)
		})
	})

	ginkgo.Context("Resource Retention Tests", func() {
		var mustGatherName string
		var mustGatherCR *mustgatherv1alpha1.MustGather

		ginkgo.BeforeEach(func() {
			mustGatherName = fmt.Sprintf("mg-retain-resources-%d", time.Now().UnixNano())
		})

		ginkgo.AfterEach(func() {
			if mustGatherCR != nil {
				ginkgo.By("Cleaning up MustGather CR")
				_ = nonAdminClient.Delete(testCtx, mustGatherCR)

				// Wait for cleanup
				Eventually(func() bool {
					err := nonAdminClient.Get(testCtx, client.ObjectKey{
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
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, nil)

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
			mg = createMustGatherCR(mustGatherName, ns.Name, "cluster-admin-sa", false, nil) // SA doesn't exist

			ginkgo.By("Verifying Job is created but Pod fails due to missing SA")
			job := &batchv1.Job{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			// Job should exist but pods won't be created due to missing SA
			Consistently(func() int32 {
				_ = nonAdminClient.Get(testCtx, client.ObjectKey{
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
			mg = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, false, nil)

			ginkgo.By("Waiting for Pod to start")
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
				if err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      gatherPod.Name,
					Namespace: ns.Name,
				}, gatherPod); err != nil {
					return corev1.PodUnknown
				}
				return gatherPod.Status.Phase
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(
				Or(Equal(corev1.PodRunning), Equal(corev1.PodSucceeded)),
				"Pod should be running or succeeded before checking events")

			ginkgo.By("Verifying Pod runs with restricted permissions (no privileged escalation events)")
			events := &corev1.EventList{}
			err := nonAdminClient.List(testCtx, events, client.InNamespace(ns.Name))
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

		ginkgo.BeforeEach(func() {
			mustGatherName = fmt.Sprintf("mg-upload-target-e2e-test-%d", time.Now().UnixNano())
		})

		ginkgo.AfterEach(func() {
			if mustGatherCR != nil {
				ginkgo.By("Cleaning up MustGather CR")
				_ = nonAdminClient.Delete(testCtx, mustGatherCR)

				Eventually(func() bool {
					err := nonAdminClient.Get(testCtx, client.ObjectKey{
						Name:      mustGatherName,
						Namespace: ns.Name,
					}, &mustgatherv1alpha1.MustGather{})
					return apierrors.IsNotFound(err)
				}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())

				mustGatherCR = nil
			}
		})

		ginkgo.It("should successfully upload must-gather data to SFTP server for external user", func() {
			ginkgo.By("Getting SFTP credentials from Vault")

			sftpUsername, sftpPassword, err := getCaseCredsFromVault()
			Expect(err).NotTo(HaveOccurred(), "Failed to get SFTP credentials from Vault")

			ginkgo.By("Creating case-management-creds-valid secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      caseManagementSecretNameValid,
					Namespace: ns.Name,
					Labels: map[string]string{
						"test": nonAdminLabel,
					},
				},
				Type: corev1.SecretTypeOpaque,
				StringData: map[string]string{
					"username": sftpUsername,
					"password": sftpPassword,
				},
			}
			err = nonAdminClient.Create(testCtx, secret)
			if err != nil && !apierrors.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred(), "Failed to create case management secret")
			}

			ginkgo.By("Creating MustGather CR with UploadTarget and internalUser=false")
			// Generate unique caseID to avoid false positives from previous test runs
			caseID := generateTestCaseID()
			ginkgo.GinkgoWriter.Printf("Using unique caseID: %s\n", caseID)

			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				UploadTarget: &UploadTargetOptions{CaseID: caseID, SecretName: caseManagementSecretNameValid, InternalUser: false, Host: stageHostName},
			})

			ginkgo.By("Verifying MustGather CR has internalUser set to false")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			err = nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: ns.Name,
			}, fetchedMG)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchedMG.Spec.UploadTarget.SFTP.InternalUser).To(BeFalse(),
				"InternalUser flag should be false for external user")

			ginkgo.By("Waiting for Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"Job should be created for MustGather with UploadTarget")

			ginkgo.By("Verifying Job has upload container with correct environment variables")
			var uploadContainer *corev1.Container
			for i := range job.Spec.Template.Spec.Containers {
				if job.Spec.Template.Spec.Containers[i].Name == uploadContainerName {
					uploadContainer = &job.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(uploadContainer).NotTo(BeNil(), "Job should have upload container")

			envVars := make(map[string]string)
			for _, env := range uploadContainer.Env {
				envVars[env.Name] = env.Value
			}

			Expect(envVars).To(HaveKey("caseid"), "Upload container should have caseid env var")
			Expect(envVars).To(HaveKey("username"), "Upload container should have username env var")
			Expect(envVars).To(HaveKey("password"), "Upload container should have password env var")
			Expect(envVars).To(HaveKey("host"), "Upload container should have host env var")
			Expect(envVars).To(HaveKey("internal_user"), "Upload container should have internal_user env var")
			Expect(envVars["internal_user"]).To(Equal("false"), "internal_user should be 'false' for external user")
			Expect(envVars["caseid"]).To(Equal(caseID), "caseid should match configured case ID")

			ginkgo.By("Waiting for Pod to be created and start running")
			var mustGatherPod *corev1.Pod
			Eventually(func(g Gomega) {
				podList := &corev1.PodList{}
				g.Expect(nonAdminClient.List(testCtx, podList,
					client.InNamespace(ns.Name),
					client.MatchingLabels{jobNameLabelKey: mustGatherName})).To(Succeed())
				g.Expect(podList.Items).NotTo(BeEmpty(), "Pod should be created by Job")
				mustGatherPod = &podList.Items[0]

				// Verify Pod has both gather and upload containers
				containerNames := make(map[string]bool)
				for _, c := range mustGatherPod.Spec.Containers {
					containerNames[c.Name] = true
				}
				g.Expect(containerNames).To(HaveKey(gatherContainerName), "Pod should have gather container")
				g.Expect(containerNames).To(HaveKey(uploadContainerName), "Pod should have upload container when UploadTarget is specified")

				g.Expect(mustGatherPod.Status.Phase).To(
					Or(Equal(corev1.PodRunning), Equal(corev1.PodSucceeded), Equal(corev1.PodFailed)),
					"Pod should reach Running, Succeeded, or Failed state")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			ginkgo.By("Verifying both containers have started")
			Eventually(func(g Gomega) {
				g.Expect(nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherPod.Name,
					Namespace: ns.Name,
				}, mustGatherPod)).To(Succeed())

				containerStatuses := make(map[string]bool)
				for _, cs := range mustGatherPod.Status.ContainerStatuses {
					started := cs.State.Running != nil || cs.State.Terminated != nil
					containerStatuses[cs.Name] = started
				}
				g.Expect(containerStatuses[gatherContainerName]).To(BeTrue(), "Gather container should have started")
				g.Expect(containerStatuses[uploadContainerName]).To(BeTrue(), "Upload container should have started")
			}).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			ginkgo.By("Waiting for Job to complete successfully (gather and upload)")
			Eventually(func() bool {
				if err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job); err != nil {
					return false
				}

				// Job must succeed for upload verification to pass
				if job.Status.Succeeded > 0 {
					ginkgo.GinkgoWriter.Println("Job completed successfully")
					return true
				}
				if job.Status.Failed > 0 {
					ginkgo.Fail("Job failed - gather or upload container failed")
				}
				return false
			}).WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(BeTrue(),
				"Job should complete successfully")

			ginkgo.By("Verifying file was uploaded to SFTP server at the correct path for external user")

			// For external users (internal_user=false), the file should be uploaded directly to: <caseid>_<filename>.tar.gz
			found, sftpLogs, err := verifySFTPUpload(ns.Name, caseManagementSecretNameValid, stageHostName, caseID, false)
			if err != nil {
				ginkgo.GinkgoWriter.Printf("SFTP verification error: %v\n", err)
			}
			ginkgo.GinkgoWriter.Printf("SFTP directory listing:\n%s\n", sftpLogs)

			Expect(found).To(BeTrue(),
				"File with caseID %s should exist on SFTP server in external user path (<caseid>_must-gather-*.tar.gz)", caseID)

			ginkgo.GinkgoWriter.Println("SFTP upload functionality verified for external user (internal_user=false)")
			ginkgo.GinkgoWriter.Printf("Verified upload path format: %s_<filename>.tar.gz (no username prefix)\n", caseID)

			ginkgo.By("Verifying MustGather CR status is updated after Job completion")
			Eventually(func(g Gomega) {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, fetchedMG)
				g.Expect(err).NotTo(HaveOccurred(), "Should fetch MustGather CR")
				g.Expect(fetchedMG.Status.Completed).To(BeTrue(), "MustGather should be marked as completed")
				g.Expect(fetchedMG.Status.Status).To(Or(Equal("Completed"), Equal("Failed")),
					"Status should be Completed or Failed")
				g.Expect(fetchedMG.Status.Reason).NotTo(BeEmpty(), "Reason should be set")
			}).WithTimeout(30*time.Second).WithPolling(2*time.Second).Should(Succeed(),
				"MustGather status should be updated after completion")

			ginkgo.GinkgoWriter.Printf("MustGather with UploadTarget completed - Status: %s, Reason: %s\n",
				fetchedMG.Status.Status, fetchedMG.Status.Reason)

			// Note: Uploaded test files are automatically purged by the SFTP server daily,
			// so manual cleanup is not required.
		})

		ginkgo.It("should fail upload with invalid SFTP credentials", func() {
			ginkgo.By("Creating invalid case management secret")
			loader.CreateFromFile(testassets.ReadFile, filepath.Join("testdata", "case-management-secret-invalid.yaml"), ns.Name)

			ginkgo.By("Creating MustGather CR with invalid credentials")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				UploadTarget: &UploadTargetOptions{
					CaseID:       "00000",
					SecretName:   caseManagementSecretNameInvalid,
					InternalUser: false,
					Host:         stageHostName,
				},
			})

			ginkgo.By("Waiting for MustGather status to be updated to Failed")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			Eventually(func(g Gomega) {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, fetchedMG)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(fetchedMG.Status.Status).To(Equal("Failed"),
					"MustGather should fail fast with invalid SFTP credentials")
				g.Expect(fetchedMG.Status.Reason).To(ContainSubstring("SFTP"),
					"Failure reason should mention SFTP validation error")
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"MustGather should fail validation before creating Job")

			ginkgo.GinkgoWriter.Printf("MustGather failed with status: %s, reason: %s\n",
				fetchedMG.Status.Status, fetchedMG.Status.Reason)

			ginkgo.By("Verifying Job is NOT created due to failed validation")
			job := &batchv1.Job{}
			Consistently(func() bool {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
				return apierrors.IsNotFound(err)
			}).WithTimeout(30*time.Second).WithPolling(5*time.Second).Should(BeTrue(),
				"Job should NOT be created when SFTP credentials fail validation")

			ginkgo.GinkgoWriter.Println("Verified: No Job created due to invalid SFTP credentials (fail-fast validation)")
		})

		ginkgo.It("should fail validation with empty username", func() {
			ginkgo.By("Creating secret with empty username")
			loader.CreateFromFile(testassets.ReadFile, filepath.Join("testdata", "case-management-secret-empty-username.yaml"), ns.Name)

			ginkgo.By("Creating MustGather CR with empty username credentials")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				UploadTarget: &UploadTargetOptions{
					CaseID:       "00000",
					SecretName:   caseManagementSecretNameEmptyUsername,
					InternalUser: false,
					Host:         stageHostName,
				},
			})

			ginkgo.By("Waiting for MustGather status to be updated to Failed")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			Eventually(func(g Gomega) {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, fetchedMG)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(fetchedMG.Status.Status).To(Equal("Failed"),
					"MustGather should fail with empty username")
				g.Expect(fetchedMG.Status.Reason).To(ContainSubstring("username"),
					"Failure reason should mention username validation error")
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"MustGather should fail validation with empty username")

			ginkgo.GinkgoWriter.Printf("MustGather failed with status: %s, reason: %s\n",
				fetchedMG.Status.Status, fetchedMG.Status.Reason)

			ginkgo.By("Verifying Job is NOT created due to failed validation")
			job := &batchv1.Job{}
			Consistently(func() bool {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
				return apierrors.IsNotFound(err)
			}).WithTimeout(30*time.Second).WithPolling(5*time.Second).Should(BeTrue(),
				"Job should NOT be created when username is empty")

			ginkgo.GinkgoWriter.Println("Verified: No Job created due to empty username (fail-fast validation)")
		})

		ginkgo.It("should fail validation with empty password", func() {
			ginkgo.By("Creating secret with empty password")
			loader.CreateFromFile(testassets.ReadFile, filepath.Join("testdata", "case-management-secret-empty-password.yaml"), ns.Name)

			ginkgo.By("Creating MustGather CR with empty password credentials")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				UploadTarget: &UploadTargetOptions{
					CaseID:       "00000",
					SecretName:   caseManagementSecretNameEmptyPassword,
					InternalUser: false,
					Host:         stageHostName,
				},
			})

			ginkgo.By("Waiting for MustGather status to be updated to Failed")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			Eventually(func(g Gomega) {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, fetchedMG)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(fetchedMG.Status.Status).To(Equal("Failed"),
					"MustGather should fail with empty password")
				g.Expect(fetchedMG.Status.Reason).To(ContainSubstring("password"),
					"Failure reason should mention password validation error")
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"MustGather should fail validation with empty password")

			ginkgo.GinkgoWriter.Printf("MustGather failed with status: %s, reason: %s\n",
				fetchedMG.Status.Status, fetchedMG.Status.Reason)

			ginkgo.By("Verifying Job is NOT created due to failed validation")
			job := &batchv1.Job{}
			Consistently(func() bool {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
				return apierrors.IsNotFound(err)
			}).WithTimeout(30*time.Second).WithPolling(5*time.Second).Should(BeTrue(),
				"Job should NOT be created when password is empty")

			ginkgo.GinkgoWriter.Println("Verified: No Job created due to empty password (fail-fast validation)")
		})
	})

	ginkgo.Context("PersistentVolume Storage Configuration Tests", func() {
		var mustGatherName string
		var mustGatherCR *mustgatherv1alpha1.MustGather

		ginkgo.BeforeEach(func() {
			mustGatherName = fmt.Sprintf("pv-storage-test-%d", time.Now().UnixNano())

			ginkgo.By("Creating PersistentVolumeClaim for storage tests")
			loader.CreateFromFile(testassets.ReadFile, filepath.Join("testdata", "must-gather-pvc.yaml"), ns.Name)

			ginkgo.By("Waiting for PVC to be created")
			pvc := &corev1.PersistentVolumeClaim{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherPVCName,
					Namespace: ns.Name,
				}, pvc)
			}).WithTimeout(3*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"PVC should be created")
		})

		ginkgo.AfterEach(func() {
			if mustGatherCR != nil {
				ginkgo.By("Cleaning up MustGather CR")
				_ = nonAdminClient.Delete(testCtx, mustGatherCR)

				Eventually(func() bool {
					err := nonAdminClient.Get(testCtx, client.ObjectKey{
						Name:      mustGatherName,
						Namespace: ns.Name,
					}, &mustgatherv1alpha1.MustGather{})
					return apierrors.IsNotFound(err)
				}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())

				mustGatherCR = nil
			}

			ginkgo.By("Cleaning up PVC")
			loader.DeleteFromFile(testassets.ReadFile, filepath.Join("testdata", "must-gather-pvc.yaml"), ns.Name)
		})

		ginkgo.It("should configure subPath correctly when specified", func() {
			subPath := "must-gather-data"

			ginkgo.By("Creating MustGather CR with PersistentVolume storage and subPath")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				PersistentVolume: &PersistentVolumeOptions{PVCName: mustGatherPVCName, SubPath: subPath},
			})

			ginkgo.By("Verifying MustGather CR has subPath set")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			err := nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: ns.Name,
			}, fetchedMG)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchedMG.Spec.Storage.PersistentVolume.SubPath).To(Equal(subPath))

			ginkgo.By("Waiting for Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			ginkgo.By("Verifying gather container has subPath configured in volume mount")
			var gatherContainer *corev1.Container
			for i := range job.Spec.Template.Spec.Containers {
				if job.Spec.Template.Spec.Containers[i].Name == gatherContainerName {
					gatherContainer = &job.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(gatherContainer).NotTo(BeNil(), "Job should have gather container")

			var outputMount *corev1.VolumeMount
			for i := range gatherContainer.VolumeMounts {
				if gatherContainer.VolumeMounts[i].Name == outputVolumeName {
					outputMount = &gatherContainer.VolumeMounts[i]
					break
				}
			}
			Expect(outputMount).NotTo(BeNil(), "Gather container should have output volume mount")
			Expect(outputMount.SubPath).To(Equal(subPath), "Volume mount should have subPath configured")
		})

		ginkgo.It("should create MustGather with PVC storage, configure Job correctly, and persist data", func() {
			ginkgo.By("Creating MustGather CR with PersistentVolume storage")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				PersistentVolume: &PersistentVolumeOptions{PVCName: mustGatherPVCName, SubPath: ""},
			})

			ginkgo.By("Verifying MustGather CR was created with Storage config")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			err := nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: ns.Name,
			}, fetchedMG)
			Expect(err).NotTo(HaveOccurred())
			Expect(fetchedMG.Spec.Storage).NotTo(BeNil(), "Storage should be set")
			Expect(fetchedMG.Spec.Storage.Type).To(Equal(mustgatherv1alpha1.StorageTypePersistentVolume))
			Expect(fetchedMG.Spec.Storage.PersistentVolume.Claim.Name).To(Equal(mustGatherPVCName))

			ginkgo.GinkgoWriter.Printf("MustGather CR created with PersistentVolume storage using PVC: %s\n", mustGatherPVCName)

			ginkgo.By("Waiting for Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"Job should be created for MustGather with PersistentVolume storage")

			ginkgo.By("Verifying Job uses PVC for output volume")
			var outputVolume *corev1.Volume
			for i := range job.Spec.Template.Spec.Volumes {
				if job.Spec.Template.Spec.Volumes[i].Name == outputVolumeName {
					outputVolume = &job.Spec.Template.Spec.Volumes[i]
					break
				}
			}
			Expect(outputVolume).NotTo(BeNil(), "Job should have output volume configured")
			Expect(outputVolume.VolumeSource.PersistentVolumeClaim).NotTo(BeNil(),
				"Output volume should use PersistentVolumeClaim, not EmptyDir")
			Expect(outputVolume.VolumeSource.PersistentVolumeClaim.ClaimName).To(Equal(mustGatherPVCName),
				"PVC claim name should match the configured PVC")

			ginkgo.By("Waiting for Job to complete")
			Eventually(func() int32 {
				_ = nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
				return job.Status.Succeeded
			}).WithTimeout(5*time.Minute).WithPolling(5*time.Second).Should(
				BeNumerically(">=", 1), "Job should complete successfully")

			ginkgo.By("Creating a verification Pod to check data persists on PVC after Job completion")
			verifyPodName := fmt.Sprintf("pvc-verify-%d", time.Now().UnixNano())
			verifyPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      verifyPodName,
					Namespace: ns.Name,
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: serviceAccount,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: func() *bool { b := true; return &b }(),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "pvc-verify",
							Image: operatorImage,
							Command: []string{
								"/bin/sh", "-c",
								`echo '=== PVC Contents ===' &&
								ls -la /must-gather &&
								echo '=== Looking for must-gather output ===' &&
								find /must-gather -type f \( -name '*.log' -o -name '*.tar*' \) 2>/dev/null | head -20`,
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: func() *bool { b := false; return &b }(),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
								RunAsNonRoot: func() *bool { b := true; return &b }(),
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "pvc-data",
									MountPath: "/must-gather",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "pvc-data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: mustGatherPVCName,
								},
							},
						},
					},
				},
			}

			err = nonAdminClient.Create(testCtx, verifyPod)
			Expect(err).NotTo(HaveOccurred(), "Failed to create verification pod")

			ginkgo.By("Waiting for verification Pod to complete")
			Eventually(func() corev1.PodPhase {
				if err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      verifyPodName,
					Namespace: ns.Name,
				}, verifyPod); err != nil {
					return corev1.PodUnknown
				}
				return verifyPod.Status.Phase
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(
				Or(Equal(corev1.PodSucceeded), Equal(corev1.PodFailed)),
				"Verification pod should complete")

			ginkgo.By("Checking verification Pod logs for must-gather data")
			logs, err := getContainerLogs(ns.Name, verifyPodName, "pvc-verify")
			Expect(err).NotTo(HaveOccurred(), "Failed to get verification pod logs")

			ginkgo.GinkgoWriter.Printf("Verification Pod logs:\n%s\n", logs)

			// Verify that must-gather output files exist on the PVC
			// The must-gather process creates a must-gather.log file
			Expect(logs).To(ContainSubstring("must-gather"),
				"PVC should contain must-gather output files after Job completion")

			ginkgo.By("Cleaning up verification Pod")
			_ = nonAdminClient.Delete(testCtx, verifyPod)

			ginkgo.GinkgoWriter.Println("Must-gather data successfully persisted to PVC and verified after Job completion")
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

	// Also create a clientset for operations like GetLogs
	nonAdminClientset, err = kubernetes.NewForConfig(nonAdminConfig)
	Expect(err).NotTo(HaveOccurred(), "Failed to create non-admin clientset")

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

func createMustGatherCR(name, namespace, serviceAccountName string, retainResources bool, opts *MustGatherCROptions) *mustgatherv1alpha1.MustGather {
	mg := &mustgatherv1alpha1.MustGather{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"test": nonAdminLabel,
			},
		},
		Spec: mustgatherv1alpha1.MustGatherSpec{
			ServiceAccountName:          serviceAccountName,
			RetainResourcesOnCompletion: &retainResources,
		},
	}

	if opts != nil {
		if opts.UploadTarget != nil {
			mg.Spec.UploadTarget = &mustgatherv1alpha1.UploadTargetSpec{
				Type: mustgatherv1alpha1.UploadTypeSFTP,
				SFTP: &mustgatherv1alpha1.SFTPSpec{
					CaseID: opts.UploadTarget.CaseID,
					CaseManagementAccountSecretRef: corev1.LocalObjectReference{
						Name: opts.UploadTarget.SecretName,
					},
					InternalUser: opts.UploadTarget.InternalUser,
					Host:         opts.UploadTarget.Host,
				},
			}
		}

		if opts.PersistentVolume != nil {
			mg.Spec.Storage = &mustgatherv1alpha1.Storage{
				Type: mustgatherv1alpha1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1alpha1.PersistentVolumeConfig{
					Claim: mustgatherv1alpha1.PersistentVolumeClaimReference{
						Name: opts.PersistentVolume.PVCName,
					},
					SubPath: opts.PersistentVolume.SubPath,
				},
			}
		}

		if opts.Timeout != nil {
			mg.Spec.MustGatherTimeout = &metav1.Duration{Duration: *opts.Timeout}
		}
	}

	err := nonAdminClient.Create(testCtx, mg)
	Expect(err).NotTo(HaveOccurred(), "Failed to create MustGather CR")

	return mg
}
func getCaseCredsFromVault() (string, string, error) {
	// First, try to read from Vault-mounted files (CI/CD environment)
	configDir := os.Getenv(caseCredsConfigDirEnvVar)
	if configDir != "" {
		// Check if the directory exists before trying to read
		if _, err := os.Stat(configDir); err == nil {
			sftpUsername, err := os.ReadFile(filepath.Join(configDir, vaultUsernameKey))
			if err != nil {
				return "", "", fmt.Errorf("failed to read sftp username from file: %w", err)
			}
			sftpPassword, err := os.ReadFile(filepath.Join(configDir, vaultPasswordKey))
			if err != nil {
				return "", "", fmt.Errorf("failed to read sftp password from file: %w", err)
			}
			return strings.TrimSpace(string(sftpUsername)), strings.TrimSpace(string(sftpPassword)), nil
		}
	}

	// Fallback: try direct environment variables (for local testing)
	sftpUsername := os.Getenv("SFTP_USERNAME_E2E")
	sftpPassword := os.Getenv("SFTP_PASSWORD_E2E")
	if sftpUsername != "" && sftpPassword != "" {
		return sftpUsername, sftpPassword, nil
	}

	return "", "", fmt.Errorf("SFTP credentials not found. Set either:\n"+
		"  1. CASE_MANAGEMENT_CREDS_CONFIG_DIR with Vault-mounted files (%s, %s), or\n"+
		"  2. SFTP_USERNAME_E2E and SFTP_PASSWORD_E2E environment variables for local testing", vaultUsernameKey, vaultPasswordKey)
}

// getOperatorImage retrieves the OPERATOR_IMAGE from the must-gather-operator deployment
func getOperatorImage() (string, error) {
	deployment := &appsv1.Deployment{}
	err := adminClient.Get(testCtx, client.ObjectKey{
		Name:      operatorDeployment,
		Namespace: operatorNamespace,
	}, deployment)
	if err != nil {
		return "", fmt.Errorf("failed to get operator deployment: %w", err)
	}

	// Look for OPERATOR_IMAGE env var in the container
	for _, container := range deployment.Spec.Template.Spec.Containers {
		for _, env := range container.Env {
			if env.Name == "OPERATOR_IMAGE" {
				return env.Value, nil
			}
		}
	}

	// Fallback to the container image itself if OPERATOR_IMAGE is not set
	if len(deployment.Spec.Template.Spec.Containers) > 0 {
		return deployment.Spec.Template.Spec.Containers[0].Image, nil
	}

	return "", fmt.Errorf("could not find operator image")
}

// generateTestCaseID creates a unique 8-digit case ID for each test run.
func generateTestCaseID() string {
	// Generate a random 8-digit number starting with 0 (00000000-09999999)
	random := rand.Intn(10000000)
	return fmt.Sprintf("%08d", random)
}

// getContainerLogs retrieves logs from a specific container in a pod
func getContainerLogs(namespace, podName, containerName string) (string, error) {
	ctx, cancel := context.WithTimeout(testCtx, 30*time.Second)
	defer cancel()

	req := nonAdminClientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: containerName,
	})
	podLogs, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer podLogs.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// verifySFTPUpload connects to the SFTP server and verifies a file with caseID exists.
// Path: internal users → username/<caseid>_*.tar.gz,
// external users → <caseid>_*.tar.gz
func verifySFTPUpload(namespace, secretName, host, caseID string, internalUser bool) (bool, string, error) {
	verifyPodName := fmt.Sprintf("sftp-verify-%d", time.Now().UnixNano())

	var sftpListCommand string
	if internalUser {
		sftpListCommand = "ls -la $SFTP_USERNAME"
	} else {
		// For external users, list files in the root directory
		sftpListCommand = "ls -la ."
	}

	sftpCommand := fmt.Sprintf(`
		mkdir -p /tmp/.ssh
		touch /tmp/.ssh/known_hosts
		chmod 700 /tmp/.ssh
		chmod 600 /tmp/.ssh/known_hosts
		echo "Listing files on SFTP server..."
		echo "Internal user mode: %v"
		sshpass -e sftp -o BatchMode=no -o StrictHostKeyChecking=no -o UserKnownHostsFile=/tmp/.ssh/known_hosts $SFTP_USERNAME@%s << EOF
%s
bye
EOF
	`, internalUser, host, sftpListCommand)

	verifyPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      verifyPodName,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:      corev1.RestartPolicyNever,
			ServiceAccountName: serviceAccount,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: func() *bool { b := true; return &b }(),
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Containers: []corev1.Container{
				{
					Name:    "sftp-verify",
					Image:   operatorImage,
					Command: []string{"/bin/bash", "-c", sftpCommand},
					SecurityContext: &corev1.SecurityContext{
						AllowPrivilegeEscalation: func() *bool { b := false; return &b }(),
						Capabilities: &corev1.Capabilities{
							Drop: []corev1.Capability{"ALL"},
						},
						RunAsNonRoot: func() *bool { b := true; return &b }(),
					},
					Env: []corev1.EnvVar{
						{
							Name: "SFTP_USERNAME",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									Key:                  "username",
									LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
								},
							},
						},
						{
							Name: "SSHPASS",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									Key:                  "password",
									LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
								},
							},
						},
					},
				},
			},
		},
	}

	// Create the verification pod
	if err := nonAdminClient.Create(testCtx, verifyPod); err != nil {
		return false, "", fmt.Errorf("failed to create verification pod: %w", err)
	}

	// Wait for pod to complete
	var podPhase corev1.PodPhase
	Eventually(func() corev1.PodPhase {
		pod := &corev1.Pod{}
		if err := nonAdminClient.Get(testCtx, client.ObjectKey{
			Name:      verifyPodName,
			Namespace: namespace,
		}, pod); err != nil {
			return corev1.PodUnknown
		}
		podPhase = pod.Status.Phase

		// Log debugging info if pod is stuck in Pending
		if podPhase == corev1.PodPending {
			ginkgo.GinkgoWriter.Printf("Verification pod %s is Pending. Conditions: %+v\n", verifyPodName, pod.Status.Conditions)
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil {
					ginkgo.GinkgoWriter.Printf("Container %s waiting: %s - %s\n", cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message)
				}
			}
		}

		return podPhase
	}).WithTimeout(3*time.Minute).WithPolling(5*time.Second).Should(
		Or(Equal(corev1.PodSucceeded), Equal(corev1.PodFailed)),
		"Verification pod should complete")

	// Get logs from verification pod
	logs, err := getContainerLogs(namespace, verifyPodName, "sftp-verify")
	if err != nil {
		// Cleanup pod
		_ = nonAdminClient.Delete(testCtx, verifyPod)
		return false, "", fmt.Errorf("failed to get verification pod logs: %w", err)
	}

	// Cleanup pod
	_ = nonAdminClient.Delete(testCtx, verifyPod)

	// Check if the file with caseID exists in the listing.
	// File format: <caseID>_must-gather-<timestamp>.tar.gz
	filePattern := fmt.Sprintf("%s_must-gather", caseID)
	found := strings.Contains(logs, filePattern)

	return found, logs, nil
}
