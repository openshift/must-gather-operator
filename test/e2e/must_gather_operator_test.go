// DO NOT REMOVE TAGS BELOW. IF ANY NEW TEST FILES ARE CREATED UNDER /test/e2e, PLEASE ADD THESE TAGS TO THEM IN ORDER TO BE EXCLUDED FROM UNIT TESTS.
//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	imagev1 "github.com/openshift/api/image/v1"
	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
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
	ImageStreamRef   *mustgatherv1alpha1.ImageStreamTagRef
	GatherSpec       *mustgatherv1alpha1.GatherSpec
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
	prodHostName                          = "sftp.access.redhat.com"

	// PersistentVolume test constants
	mustGatherPVCName        = "must-gather-pvc"
	caseCredsConfigDirEnvVar = "CASE_MANAGEMENT_CREDS_CONFIG_DIR"
	// vaultOfflineTokenKey is the RH SSO offline refresh token mounted from Vault for CI.
	vaultOfflineTokenKey          = "offline-token-e2e"
	refreshSFTPTokenScript        = "refresh-sftp-token.sh"
	refreshSFTPTokenScriptTimeout = 60 * time.Second
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
	utilruntime.Must(imagev1.AddToScheme(testScheme))
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
		Expect(err).NotTo(HaveOccurred(), "Failed to create test namespace")
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
			Expect(err).NotTo(HaveOccurred(), "Failed to fetch MustGather CR")
			Expect(fetchedMG.Spec.ServiceAccountName).To(Equal(serviceAccount),
				"ServiceAccountName should match the configured service account")

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
			Expect(err).NotTo(HaveOccurred(), "Non-admin should be able to delete MustGather CR")

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
			Expect(err).NotTo(HaveOccurred(), "Failed to get MustGather CR for timeout verification")
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
			Expect(err).NotTo(HaveOccurred(), "Failed to get MustGather CR after completion")
			Expect(fetchedMG.Status.Completed).To(BeTrue(), "MustGather should be marked as completed")

			ginkgo.GinkgoWriter.Printf("MustGather with timeout completed - Status: %s, Reason: %s\n",
				fetchedMG.Status.Status, fetchedMG.Status.Reason)
		})

		ginkgo.It("should store 10s timeout correctly and destroy pod after completion", func() {
			mgName10s := fmt.Sprintf("non-admin-timeout-10s-%d", time.Now().UnixNano())
			timeout10s := 10 * time.Second
			mg10s := createMustGatherCR(mgName10s, ns.Name, serviceAccount, false, &MustGatherCROptions{
				Timeout: &timeout10s,
			})
			defer func() {
				_ = nonAdminClient.Delete(testCtx, mg10s)
			}()

			fetchedMG := &mustgatherv1alpha1.MustGather{}
			err := nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mgName10s,
				Namespace: ns.Name,
			}, fetchedMG)
			Expect(err).NotTo(HaveOccurred(), "Failed to get MustGather CR for 10s timeout")
			Expect(fetchedMG.Spec.MustGatherTimeout).NotTo(BeNil(), "MustGatherTimeout should be set for 10s CR")
			Expect(fetchedMG.Spec.MustGatherTimeout.Duration).To(Equal(10*time.Second),
				"MustGatherTimeout should be 10s")

			ginkgo.By("Waiting for MustGather CR status to become Completed after 10s timeout")
			Eventually(func() bool {
				mg := &mustgatherv1alpha1.MustGather{}
				if err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mgName10s,
					Namespace: ns.Name,
				}, mg); err != nil {
					return false
				}
				return mg.Status.Completed
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(BeTrue(),
				"MustGather should complete after timeout expires")

			ginkgo.By("Verifying pod is destroyed after 10s timeout (retainResources=false)")
			Eventually(func() int {
				podList := &corev1.PodList{}
				if err := nonAdminClient.List(testCtx, podList,
					client.InNamespace(ns.Name),
					client.MatchingLabels{jobNameLabelKey: mgName10s}); err != nil {
					return -1
				}
				return len(podList.Items)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Equal(0),
				"Pod should be destroyed after timeout when retainResources is false")
		})

		ginkgo.It("should store 10m timeout correctly", func() {
			mgName10m := fmt.Sprintf("non-admin-timeout-10m-%d", time.Now().UnixNano())
			timeout10m := 10 * time.Minute
			mg10m := createMustGatherCR(mgName10m, ns.Name, serviceAccount, false, &MustGatherCROptions{
				Timeout: &timeout10m,
			})
			defer func() {
				_ = nonAdminClient.Delete(testCtx, mg10m)
			}()

			fetchedMG10m := &mustgatherv1alpha1.MustGather{}
			err := nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mgName10m,
				Namespace: ns.Name,
			}, fetchedMG10m)
			Expect(err).NotTo(HaveOccurred(), "Failed to get MustGather CR for 10m timeout")
			Expect(fetchedMG10m.Spec.MustGatherTimeout).NotTo(BeNil(), "MustGatherTimeout should be set for 10m CR")
			Expect(fetchedMG10m.Spec.MustGatherTimeout.Duration).To(Equal(10*time.Minute),
				"MustGatherTimeout should be 10m0s")
		})

		ginkgo.It("should store 10h timeout correctly", func() {
			mgName10h := fmt.Sprintf("non-admin-timeout-10h-%d", time.Now().UnixNano())
			timeout10h := 10 * time.Hour
			mg10h := createMustGatherCR(mgName10h, ns.Name, serviceAccount, false, &MustGatherCROptions{
				Timeout: &timeout10h,
			})
			defer func() {
				_ = nonAdminClient.Delete(testCtx, mg10h)
			}()

			fetchedMG10h := &mustgatherv1alpha1.MustGather{}
			err := nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mgName10h,
				Namespace: ns.Name,
			}, fetchedMG10h)
			Expect(err).NotTo(HaveOccurred(), "Failed to get MustGather CR for 10h timeout")
			Expect(fetchedMG10h.Spec.MustGatherTimeout).NotTo(BeNil(), "MustGatherTimeout should be set for 10h CR")
			Expect(fetchedMG10h.Spec.MustGatherTimeout.Duration).To(Equal(10*time.Hour),
				"MustGatherTimeout should be 10h0m0s")
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
			Expect(err).NotTo(HaveOccurred(), "Failed to list retained pods")
			Expect(retainedPodList.Items).NotTo(BeEmpty(),
				"Pod should still exist when RetainResourcesOnCompletion is true")
			ginkgo.GinkgoWriter.Printf("Pod %s is retained after completion\n", retainedPodList.Items[0].Name)

			ginkgo.GinkgoWriter.Println("RetainResourcesOnCompletion=true verified: Job and Pod are retained after completion")
		})

		ginkgo.It("should clean up Job and Pod when retainResourcesOnCompletion defaults to false", func() {
			ginkgo.By("Creating MustGather CR without setting retainResourcesOnCompletion (defaults to false)")
			mg := &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mustGatherName,
					Namespace: ns.Name,
					Labels: map[string]string{
						"test": nonAdminLabel,
					},
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: serviceAccount,
				},
			}
			err := nonAdminClient.Create(testCtx, mg)
			Expect(err).NotTo(HaveOccurred(), "Failed to create MustGather CR")
			mustGatherCR = mg

			ginkgo.By("Verifying retainResourcesOnCompletion is nil (defaults to false)")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			err = nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: ns.Name,
			}, fetchedMG)
			Expect(err).NotTo(HaveOccurred(), "Failed to get MustGather CR for retain resources check")
			if fetchedMG.Spec.RetainResourcesOnCompletion != nil {
				Expect(*fetchedMG.Spec.RetainResourcesOnCompletion).To(BeFalse(),
					"retainResourcesOnCompletion should default to false")
			}

			ginkgo.By("Waiting for Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			ginkgo.By("Waiting for MustGather to complete")
			fetchedMG = &mustgatherv1alpha1.MustGather{}
			Eventually(func() bool {
				if err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, fetchedMG); err != nil {
					return false
				}
				return fetchedMG.Status.Completed
			}).WithTimeout(5*time.Minute).WithPolling(5*time.Second).Should(BeTrue(),
				"MustGather should complete")

			ginkgo.GinkgoWriter.Printf("MustGather Status: %s - Completed: %v - Reason: %s\n",
				fetchedMG.Status.Status, fetchedMG.Status.Completed, fetchedMG.Status.Reason)

			ginkgo.By("Verifying Job is cleaned up after completion (retainResources defaults to false)")
			Eventually(func() bool {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, &batchv1.Job{})
				return apierrors.IsNotFound(err)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(BeTrue(),
				"Job should be cleaned up when retainResourcesOnCompletion is false (default)")

			ginkgo.By("Verifying Pods are cleaned up after completion")
			Eventually(func() int {
				podList := &corev1.PodList{}
				if err := nonAdminClient.List(testCtx, podList,
					client.InNamespace(ns.Name),
					client.MatchingLabels{jobNameLabelKey: mustGatherName}); err != nil {
					return -1
				}
				return len(podList.Items)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Equal(0),
				"Pods should be cleaned up when retainResourcesOnCompletion is false (default)")

			ginkgo.GinkgoWriter.Println("RetainResourcesOnCompletion default (false) verified: Job and Pod are cleaned up after completion")
			mustGatherCR = nil
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

		ginkgo.It("should report error when ServiceAccount does not exist", func() {
			mustGatherName := fmt.Sprintf("test-missing-sa-%d", time.Now().UnixNano())

			ginkgo.By("Creating MustGather with non-existent ServiceAccount")
			mg = createMustGatherCR(mustGatherName, ns.Name, "non-existent-sa-e2e", false, nil)

			ginkgo.By("Verifying MustGather status has error condition")
			Eventually(func() bool {
				fetchedMG := &mustgatherv1alpha1.MustGather{}
				err := adminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, fetchedMG)
				if err != nil {
					return false
				}
				// Check for error condition with service account message
				for _, cond := range fetchedMG.Status.Conditions {
					if cond.Type == "ReconcileError" && cond.Status == metav1.ConditionTrue {
						if strings.Contains(strings.ToLower(cond.Message), "service account") &&
							strings.Contains(strings.ToLower(cond.Message), "not found") {
							ginkgo.GinkgoWriter.Printf("Found expected error condition: %s\n", cond.Message)
							return true
						}
					}
				}
				return false
			}).WithTimeout(1*time.Minute).WithPolling(5*time.Second).Should(BeTrue(),
				"MustGather status should contain error condition about missing ServiceAccount")

			ginkgo.By("Verifying Job was not created")
			job := &batchv1.Job{}
			err := adminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: ns.Name,
			}, job)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(),
				"Job should not be created when ServiceAccount is missing")

			ginkgo.By("Verifying warning event was generated")
			events := &corev1.EventList{}
			err = adminClient.List(testCtx, events, client.InNamespace(ns.Name))
			Expect(err).NotTo(HaveOccurred(), "Failed to list events in test namespace")

			foundWarningEvent := false
			for _, event := range events.Items {
				if event.InvolvedObject.Name == mustGatherName &&
					event.Type == corev1.EventTypeWarning &&
					strings.Contains(strings.ToLower(event.Message), "service account") {
					foundWarningEvent = true
					ginkgo.GinkgoWriter.Printf("Found warning event: %s\n", event.Message)
					break
				}
			}
			Expect(foundWarningEvent).To(BeTrue(), "Warning event should be generated for missing ServiceAccount")

			ginkgo.GinkgoWriter.Println("ServiceAccount validation correctly prevented Job creation and reported error")
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
			Expect(gatherPod.Spec.ServiceAccountName).To(Equal(serviceAccount),
				"Pod should use the configured service account")

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

		ginkgo.It("should successfully upload must-gather data to SFTP server for external user [Skipped:Disconnected]", func() {
			ginkgo.By("Getting SFTP credentials from Vault")

			sftpUsername, sftpPassword, err := getCaseCreds()
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
				UploadTarget: &UploadTargetOptions{CaseID: caseID, SecretName: caseManagementSecretNameValid, InternalUser: false, Host: prodHostName},
			})

			ginkgo.By("Verifying MustGather CR has internalUser set to false")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			err = nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: ns.Name,
			}, fetchedMG)
			Expect(err).NotTo(HaveOccurred(), "Failed to get MustGather CR for external user upload test")
			Expect(fetchedMG.Spec.UploadTarget.SFTP.InternalUser).To(BeFalse(),
				"InternalUser flag should be false for external user")

			ginkgo.By("Waiting for Job to be created")
			job := &batchv1.Job{}
			Eventually(func(g Gomega) {
				// First check if MustGather has failed validation
				mg := &mustgatherv1alpha1.MustGather{}
				g.Expect(nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, mg)).To(Succeed())

				// If status is Failed, fail immediately with the reason
				if mg.Status.Status == "Failed" {
					ginkgo.Fail(fmt.Sprintf("MustGather validation failed before Job creation: %s", mg.Status.Reason))
				}

				// Otherwise, check if Job was created
				g.Expect(nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)).To(Succeed(), "Job should be created for MustGather with UploadTarget")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

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
					var details []string

					// Include job conditions (often contains the failure reason/message)
					for _, c := range job.Status.Conditions {
						details = append(details, fmt.Sprintf(
							"jobCondition[%s]=%s reason=%q message=%q",
							c.Type, c.Status, c.Reason, c.Message,
						))
					}

					// Best-effort include container termination details
					if mustGatherPod != nil && mustGatherPod.Name != "" {
						tmpPod := &corev1.Pod{}
						if err := nonAdminClient.Get(testCtx, client.ObjectKey{
							Name:      mustGatherPod.Name,
							Namespace: ns.Name,
						}, tmpPod); err == nil {
							for _, cs := range tmpPod.Status.ContainerStatuses {
								if cs.State.Terminated != nil {
									details = append(details, fmt.Sprintf(
										"container[%s] terminated exitCode=%d reason=%q message=%q",
										cs.Name, cs.State.Terminated.ExitCode, cs.State.Terminated.Reason, cs.State.Terminated.Message,
									))
								} else if cs.State.Waiting != nil {
									details = append(details, fmt.Sprintf(
										"container[%s] waiting reason=%q message=%q",
										cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message,
									))
								}
							}
						} else {
							details = append(details, fmt.Sprintf("failed to get pod %s: %v", mustGatherPod.Name, err))
						}
					}

					detailStr := strings.Join(details, "; ")
					if detailStr == "" {
						detailStr = "<no failure details available>"
					}
					ginkgo.Fail(fmt.Sprintf("Job failed - gather or upload container failed. Details: %s", detailStr))
				}
				return false
			}).WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(BeTrue(),
				"Job should complete successfully")

			ginkgo.By("Verifying file was uploaded to SFTP server at the correct path for external user")

			// For external users (internal_user=false), the file should be uploaded directly to: <caseid>_<filename>.tar.gz
			found, sftpLogs, err := verifySFTPUpload(ns.Name, caseManagementSecretNameValid, prodHostName, caseID, false)
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
		})

		ginkgo.It("should fail upload with invalid SFTP credentials [Skipped:Disconnected]", func() {
			ginkgo.By("Creating invalid case management secret")
			loader.CreateFromFile(testassets.ReadFile, filepath.Join("testdata", "case-management-secret-invalid.yaml"), ns.Name)

			ginkgo.By("Creating MustGather CR with invalid credentials")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				UploadTarget: &UploadTargetOptions{
					CaseID:       "00000",
					SecretName:   caseManagementSecretNameInvalid,
					InternalUser: false,
					Host:         prodHostName,
				},
			})

			ginkgo.By("Waiting for MustGather status to be updated to Failed")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			Eventually(func(g Gomega) {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, fetchedMG)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get MustGather CR for SFTP validation check")
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

		ginkgo.It("should fail validation with empty username [Skipped:Disconnected]", func() {
			ginkgo.By("Creating secret with empty username")
			loader.CreateFromFile(testassets.ReadFile, filepath.Join("testdata", "case-management-secret-empty-username.yaml"), ns.Name)

			ginkgo.By("Creating MustGather CR with empty username credentials")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				UploadTarget: &UploadTargetOptions{
					CaseID:       "00000",
					SecretName:   caseManagementSecretNameEmptyUsername,
					InternalUser: false,
					Host:         prodHostName,
				},
			})

			ginkgo.By("Waiting for MustGather status to be updated to Failed")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			Eventually(func(g Gomega) {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, fetchedMG)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get MustGather CR for username validation check")
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

		ginkgo.It("should fail validation with empty password [Skipped:Disconnected]", func() {
			ginkgo.By("Creating secret with empty password")
			loader.CreateFromFile(testassets.ReadFile, filepath.Join("testdata", "case-management-secret-empty-password.yaml"), ns.Name)

			ginkgo.By("Creating MustGather CR with empty password credentials")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				UploadTarget: &UploadTargetOptions{
					CaseID:       "00000",
					SecretName:   caseManagementSecretNameEmptyPassword,
					InternalUser: false,
					Host:         prodHostName,
				},
			})

			ginkgo.By("Waiting for MustGather status to be updated to Failed")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			Eventually(func(g Gomega) {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, fetchedMG)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get MustGather CR for password validation check")
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

	ginkgo.Context("Proxy Upload Tests", func() {
		var mustGatherName string
		var mustGatherCR *mustgatherv1alpha1.MustGather
		var httpProxy, httpsProxy, noProxy string

		ginkgo.BeforeEach(func() {
			var hasProxy bool
			httpProxy, httpsProxy, noProxy, hasProxy = getOperatorProxyEnvVars()
			if !hasProxy {
				ginkgo.Skip("cluster does not have proxy configured — operator deployment has no HTTP_PROXY/HTTPS_PROXY env vars")
			}
			ginkgo.GinkgoWriter.Printf("Proxy detected — HTTP_PROXY=%q, HTTPS_PROXY=%q, NO_PROXY=%q\n", redactProxyURL(httpProxy), redactProxyURL(httpsProxy), noProxy)
			mustGatherName = fmt.Sprintf("mg-proxy-upload-e2e-%d", time.Now().UnixNano())
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

		ginkgo.It("should propagate proxy environment variables to upload container", func() {
			ginkgo.By("Getting SFTP credentials from Vault")
			sftpUsername, sftpPassword, err := getCaseCreds()
			Expect(err).NotTo(HaveOccurred(), "Failed to get SFTP credentials from Vault")

			ginkgo.By("Creating case management secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      caseManagementSecretNameValid,
					Namespace: ns.Name,
					Labels:    map[string]string{"test": nonAdminLabel},
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

			ginkgo.By("Creating MustGather CR with UploadTarget")
			caseID := generateTestCaseID()
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				UploadTarget: &UploadTargetOptions{CaseID: caseID, SecretName: caseManagementSecretNameValid, InternalUser: false, Host: prodHostName},
			})

			ginkgo.By("Waiting for Job to be created")
			job := &batchv1.Job{}
			Eventually(func(g Gomega) {
				mg := &mustgatherv1alpha1.MustGather{}
				g.Expect(nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, mg)).To(Succeed())
				if mg.Status.Status == "Failed" {
					ginkgo.Fail(fmt.Sprintf("MustGather validation failed before Job creation: %s", mg.Status.Reason))
				}
				g.Expect(nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)).To(Succeed(), "Job should be created for MustGather with UploadTarget")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			ginkgo.By("Verifying upload container has proxy environment variables")
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

			if httpProxy != "" {
				Expect(envVars).To(HaveKeyWithValue("http_proxy", httpProxy),
					"Upload container should have http_proxy matching operator's HTTP_PROXY")
			} else {
				Expect(envVars).NotTo(HaveKey("http_proxy"),
					"Upload container should not have http_proxy when operator has no HTTP_PROXY")
			}

			if httpsProxy != "" {
				Expect(envVars).To(HaveKeyWithValue("https_proxy", httpsProxy),
					"Upload container should have https_proxy matching operator's HTTPS_PROXY")
			} else {
				Expect(envVars).NotTo(HaveKey("https_proxy"),
					"Upload container should not have https_proxy when operator has no HTTPS_PROXY")
			}

			if noProxy != "" {
				Expect(envVars).To(HaveKeyWithValue("no_proxy", noProxy),
					"Upload container should have no_proxy matching operator's NO_PROXY")
			} else {
				Expect(envVars).NotTo(HaveKey("no_proxy"),
					"Upload container should not have no_proxy when operator has no NO_PROXY")
			}

			ginkgo.GinkgoWriter.Println("Verified: proxy environment variables correctly propagated to upload container")
		})

		ginkgo.It("should successfully upload must-gather data through proxy for external user", func() {
			ginkgo.By("Getting SFTP credentials from Vault")
			sftpUsername, sftpPassword, err := getCaseCreds()
			Expect(err).NotTo(HaveOccurred(), "Failed to get SFTP credentials from Vault")

			ginkgo.By("Creating case management secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      caseManagementSecretNameValid,
					Namespace: ns.Name,
					Labels:    map[string]string{"test": nonAdminLabel},
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
			caseID := generateTestCaseID()
			ginkgo.GinkgoWriter.Printf("Using unique caseID: %s\n", caseID)

			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				UploadTarget: &UploadTargetOptions{CaseID: caseID, SecretName: caseManagementSecretNameValid, InternalUser: false, Host: prodHostName},
			})

			ginkgo.By("Waiting for Job to be created")
			job := &batchv1.Job{}
			Eventually(func(g Gomega) {
				mg := &mustgatherv1alpha1.MustGather{}
				g.Expect(nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, mg)).To(Succeed())
				if mg.Status.Status == "Failed" {
					ginkgo.Fail(fmt.Sprintf("MustGather validation failed before Job creation: %s", mg.Status.Reason))
				}
				g.Expect(nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)).To(Succeed(), "Job should be created for MustGather with UploadTarget")
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			ginkgo.By("Verifying upload container has proxy env vars (sanity check)")
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
			Expect(envVars).To(SatisfyAny(HaveKey("http_proxy"), HaveKey("https_proxy")),
				"Upload container should have at least one proxy env var")

			ginkgo.By("Waiting for Pod to be created and start running")
			var mustGatherPod *corev1.Pod
			Eventually(func(g Gomega) {
				podList := &corev1.PodList{}
				g.Expect(nonAdminClient.List(testCtx, podList,
					client.InNamespace(ns.Name),
					client.MatchingLabels{jobNameLabelKey: mustGatherName})).To(Succeed())
				g.Expect(podList.Items).NotTo(BeEmpty(), "Pod should be created by Job")
				mustGatherPod = &podList.Items[0]

				containerNames := make(map[string]bool)
				for _, c := range mustGatherPod.Spec.Containers {
					containerNames[c.Name] = true
				}
				g.Expect(containerNames).To(HaveKey(gatherContainerName), "Pod should have gather container")
				g.Expect(containerNames).To(HaveKey(uploadContainerName), "Pod should have upload container")

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

			ginkgo.By("Waiting for Job to complete successfully (gather and upload through proxy)")
			Eventually(func() bool {
				if err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job); err != nil {
					return false
				}

				if job.Status.Succeeded > 0 {
					ginkgo.GinkgoWriter.Println("Job completed successfully through proxy")
					return true
				}
				if job.Status.Failed > 0 {
					var details []string
					for _, c := range job.Status.Conditions {
						details = append(details, fmt.Sprintf(
							"jobCondition[%s]=%s reason=%q message=%q",
							c.Type, c.Status, c.Reason, c.Message,
						))
					}
					if mustGatherPod != nil && mustGatherPod.Name != "" {
						tmpPod := &corev1.Pod{}
						if err := nonAdminClient.Get(testCtx, client.ObjectKey{
							Name:      mustGatherPod.Name,
							Namespace: ns.Name,
						}, tmpPod); err == nil {
							for _, cs := range tmpPod.Status.ContainerStatuses {
								if cs.State.Terminated != nil {
									details = append(details, fmt.Sprintf(
										"container[%s] terminated exitCode=%d reason=%q message=%q",
										cs.Name, cs.State.Terminated.ExitCode, cs.State.Terminated.Reason, cs.State.Terminated.Message,
									))
								} else if cs.State.Waiting != nil {
									details = append(details, fmt.Sprintf(
										"container[%s] waiting reason=%q message=%q",
										cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message,
									))
								}
							}
						} else {
							details = append(details, fmt.Sprintf("failed to get pod %s: %v", mustGatherPod.Name, err))
						}
					}
					detailStr := strings.Join(details, "; ")
					if detailStr == "" {
						detailStr = "<no failure details available>"
					}
					ginkgo.Fail(fmt.Sprintf("Job failed — proxy upload may have failed. Details: %s", detailStr))
				}
				return false
			}).WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(BeTrue(),
				"Job should complete successfully through proxy")

			ginkgo.By("Verifying file was uploaded to SFTP server for external user through proxy")
			found, sftpLogs, err := verifySFTPUpload(ns.Name, caseManagementSecretNameValid, prodHostName, caseID, false)
			if err != nil {
				ginkgo.GinkgoWriter.Printf("SFTP verification error: %v\n", err)
			}
			ginkgo.GinkgoWriter.Printf("SFTP directory listing:\n%s\n", sftpLogs)

			Expect(found).To(BeTrue(),
				"File with caseID %s should exist on SFTP server (external user path, uploaded through proxy)", caseID)

			ginkgo.By("Verifying MustGather CR status is updated after proxy upload completion")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
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
				"MustGather status should be updated after proxy upload completion")

			ginkgo.GinkgoWriter.Printf("Proxy upload completed — Status: %s, Reason: %s\n",
				fetchedMG.Status.Status, fetchedMG.Status.Reason)
		})

	})

	ginkgo.Context("Custom Image [apigroup:image.openshift.io] [Skipped:Disconnected]", func() {
		var mustGatherName string
		var mustGatherCR *mustgatherv1alpha1.MustGather
		var imageStreamName string
		var customImage string

		ginkgo.BeforeEach(func() {
			mustGatherName = fmt.Sprintf("mg-custom-image-e2e-test-%d", time.Now().UnixNano())
			imageStreamName = fmt.Sprintf("custom-image-stream-%d", time.Now().UnixNano())
			customImage = os.Getenv("CUSTOM_MUST_GATHER_IMAGE")
			if customImage == "" {
				customImage = operatorImage
			}
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
			if imageStreamName != "" {
				ginkgo.By("Cleaning up ImageStream")
				deleteImageStream(imageStreamName)
				imageStreamName = ""
			}
		})

		ginkgo.It("should successfully run must-gather with a valid ImageStreamRef", func() {
			ginkgo.By("Creating a valid ImageStream")
			createImageStream(imageStreamName, customImage, "latest")

			ginkgo.By("Creating MustGather CR with ImageStreamRef")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				ImageStreamRef: &mustgatherv1alpha1.ImageStreamTagRef{
					Name: imageStreamName,
					Tag:  "latest",
				},
			})

			ginkgo.By("Waiting for Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			ginkgo.By("Verifying Job uses the custom image from ImageStream")
			Expect(job.Spec.Template.Spec.Containers[0].Image).To(ContainSubstring(customImage),
				"Job container image should reference the custom ImageStream image")
		})

		ginkgo.It("should reject creation when gatherSpec.audit is true with imageStreamRef", func() {
			ginkgo.By("Creating MustGather CR with custom image and audit enabled (invalid per CRD validation)")
			retain := false
			mg := &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mustGatherName,
					Namespace: ns.Name,
					Labels: map[string]string{
						"test": nonAdminLabel,
					},
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName:          serviceAccount,
					RetainResourcesOnCompletion: &retain,
					ImageStreamRef: &mustgatherv1alpha1.ImageStreamTagRef{
						Name: imageStreamName,
						Tag:  "latest",
					},
					GatherSpec: &mustgatherv1alpha1.GatherSpec{
						Audit: true,
					},
				},
			}
			err := nonAdminClient.Create(testCtx, mg)
			Expect(err).To(HaveOccurred(), "apiserver should reject MustGather with audit and imageStreamRef")
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected Invalid (422) from CRD validation, got: %v", err)
			Expect(strings.ToLower(err.Error())).To(ContainSubstring("audit"),
				"error should describe audit validation failure")
		})

		ginkgo.It("should fail if the referenced ImageStream does not exist", func() {
			ginkgo.By("Creating MustGather CR with a non-existent ImageStreamRef")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				ImageStreamRef: &mustgatherv1alpha1.ImageStreamTagRef{
					Name: "non-existent-imagestream",
					Tag:  "latest",
				},
			})

			ginkgo.By("Verifying MustGather CR status is updated to Failed")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			Eventually(func() string {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, fetchedMG)
				if err != nil {
					return ""
				}
				return fetchedMG.Status.Status
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Equal("Failed"))
		})

		ginkgo.It("should fail if the referenced ImageStreamTag does not exist", func() {
			ginkgo.By("Creating a valid ImageStream with a specific tag")
			createImageStream(imageStreamName, customImage, "v1.0")

			ginkgo.By("Creating MustGather CR with a non-existent ImageStreamTag")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				ImageStreamRef: &mustgatherv1alpha1.ImageStreamTagRef{
					Name: imageStreamName,
					Tag:  "non-existent-tag",
				},
			})

			ginkgo.By("Verifying MustGather CR status is updated to Failed")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			Eventually(func() string {
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, fetchedMG)
				if err != nil {
					return ""
				}
				return fetchedMG.Status.Status
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Equal("Failed"))
		})

		ginkgo.It("should successfully run with command and args override", func() {
			ginkgo.By("Creating a valid ImageStream")
			createImageStream(imageStreamName, customImage, "latest")

			ginkgo.By("Creating MustGather CR with command and args override")
			command := []string{"/bin/bash"}
			args := []string{"-c", "echo 'Custom command executed'"}
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				ImageStreamRef: &mustgatherv1alpha1.ImageStreamTagRef{
					Name: imageStreamName,
					Tag:  "latest",
				},
				GatherSpec: &mustgatherv1alpha1.GatherSpec{
					Command: command,
					Args:    args,
				},
			})

			ginkgo.By("Waiting for Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

			ginkgo.By("Verifying Job has the command and args override")
			Expect(job.Spec.Template.Spec.Containers[0].Command).To(Equal(command),
				"Job container command should match the configured override")
			Expect(job.Spec.Template.Spec.Containers[0].Args).To(Equal(args),
				"Job container args should match the configured override")
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

			ginkgo.By("Waiting for PVC to be fully deleted")
			Eventually(func() bool {
				pvc := &corev1.PersistentVolumeClaim{}
				err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherPVCName,
					Namespace: ns.Name,
				}, pvc)
				return apierrors.IsNotFound(err)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(BeTrue(),
				"PVC should be fully deleted before next test runs")
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
			Expect(err).NotTo(HaveOccurred(), "Failed to get MustGather CR for subPath verification")
			Expect(fetchedMG.Spec.Storage.PersistentVolume.SubPath).To(Equal(subPath),
				"PersistentVolume subPath should match the configured value")

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
			Expect(outputMount.SubPathExpr).To(Equal(subPath+"/$(POD_NAME)"), "Volume mount should have subPathExpr configured")
			Expect(outputMount.SubPath).To(BeEmpty(), "Volume mount subPath should be empty when using subPathExpr")
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
			Expect(err).NotTo(HaveOccurred(), "Failed to get MustGather CR for PV storage verification")
			Expect(fetchedMG.Spec.Storage).NotTo(BeNil(), "Storage should be set")
			Expect(fetchedMG.Spec.Storage.Type).To(Equal(mustgatherv1alpha1.StorageTypePersistentVolume),
				"Storage type should be PersistentVolume")
			Expect(fetchedMG.Spec.Storage.PersistentVolume.Claim.Name).To(Equal(mustGatherPVCName),
				"PVC name should match the configured value")

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
								`ls -la /must-gather && find /must-gather -type f \( -name '*.log' -o -name '*.tar*' \) 2>/dev/null | head -20 | grep .`,
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

			Expect(verifyPod.Status.Phase).To(Equal(corev1.PodSucceeded),
				"Verification pod should succeed, indicating output files were found on PVC")

			ginkgo.By("Checking verification Pod logs for must-gather data")
			logs, err := getContainerLogs(ns.Name, verifyPodName, "pvc-verify")
			Expect(err).NotTo(HaveOccurred(), "Failed to get verification pod logs")

			ginkgo.GinkgoWriter.Printf("Verification Pod logs:\n%s\n", logs)

			Expect(logs).To(ContainSubstring(".log"),
				"PVC should contain must-gather .log files after Job completion")

			ginkgo.By("Cleaning up verification Pod")
			_ = nonAdminClient.Delete(testCtx, verifyPod)

			ginkgo.GinkgoWriter.Println("Must-gather data successfully persisted to PVC and verified after Job completion")
		})
	})

	ginkgo.Context("Proxy Configuration Tests", func() {
		var mustGatherName string
		var mustGatherCR *mustgatherv1alpha1.MustGather

		ginkgo.BeforeEach(func() {
			mustGatherName = fmt.Sprintf("mg-proxy-verify-%d", time.Now().UnixNano())
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

		ginkgo.It("should verify operator pod has proxy environment variables matching cluster proxy config [Skipped:Disconnected]", func() {
			ginkgo.By("Checking if cluster is proxy-enabled")

			// Read proxy config directly from cluster proxy object via operator pod env vars
			deployment := &appsv1.Deployment{}
			err := adminClient.Get(testCtx, client.ObjectKey{
				Name:      operatorDeployment,
				Namespace: operatorNamespace,
			}, deployment)
			Expect(err).NotTo(HaveOccurred(), "Failed to get operator deployment")

			// Extract proxy env vars from the operator container
			operatorHTTPProxy := ""
			operatorHTTPSProxy := ""
			operatorNoProxy := ""
			for _, container := range deployment.Spec.Template.Spec.Containers {
				for _, env := range container.Env {
					switch env.Name {
					case "HTTP_PROXY":
						operatorHTTPProxy = env.Value
					case "HTTPS_PROXY":
						operatorHTTPSProxy = env.Value
					case "NO_PROXY":
						operatorNoProxy = env.Value
					}
				}
			}

			// If none of the proxy vars are set, the cluster is not proxy-enabled
			if operatorHTTPProxy == "" && operatorHTTPSProxy == "" {
				ginkgo.Skip("Skip for non-proxy cluster - operator has no proxy environment variables configured")
			}

			ginkgo.GinkgoWriter.Printf("Operator deployment proxy configuration:\n")
			ginkgo.GinkgoWriter.Printf("  HTTP_PROXY: %s\n", redactProxyURL(operatorHTTPProxy))
			ginkgo.GinkgoWriter.Printf("  HTTPS_PROXY: %s\n", redactProxyURL(operatorHTTPSProxy))
			ginkgo.GinkgoWriter.Printf("  NO_PROXY: %s\n", operatorNoProxy)

			ginkgo.By("Verifying operator pod has the proxy environment variables from the deployment spec")
			podList := &corev1.PodList{}
			err = adminClient.List(testCtx, podList,
				client.InNamespace(operatorNamespace),
				client.MatchingLabels{"name": operatorDeployment})
			Expect(err).NotTo(HaveOccurred(), "Failed to list operator pods")
			Expect(podList.Items).NotTo(BeEmpty(), "Operator pod should exist")

			operatorPod := &podList.Items[0]
			ginkgo.GinkgoWriter.Printf("Found operator pod: %s\n", operatorPod.Name)

			podHTTPProxy := ""
			podHTTPSProxy := ""
			podNoProxy := ""
			for _, container := range operatorPod.Spec.Containers {
				for _, env := range container.Env {
					switch env.Name {
					case "HTTP_PROXY":
						podHTTPProxy = env.Value
					case "HTTPS_PROXY":
						podHTTPSProxy = env.Value
					case "NO_PROXY":
						podNoProxy = env.Value
					}
				}
			}

			ginkgo.GinkgoWriter.Printf("Operator pod proxy configuration:\n")
			ginkgo.GinkgoWriter.Printf("  HTTP_PROXY: %s\n", redactProxyURL(podHTTPProxy))
			ginkgo.GinkgoWriter.Printf("  HTTPS_PROXY: %s\n", redactProxyURL(podHTTPSProxy))
			ginkgo.GinkgoWriter.Printf("  NO_PROXY: %s\n", podNoProxy)

			Expect(podHTTPProxy).To(Equal(operatorHTTPProxy),
				"Operator pod HTTP_PROXY should match deployment spec")
			Expect(podHTTPSProxy).To(Equal(operatorHTTPSProxy),
				"Operator pod HTTPS_PROXY should match deployment spec")
			Expect(podNoProxy).To(Equal(operatorNoProxy),
				"Operator pod NO_PROXY should match deployment spec")

			ginkgo.By("Verifying proxy vars would propagate to must-gather Jobs")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				UploadTarget: &UploadTargetOptions{
					CaseID:       "00000",
					SecretName:   caseManagementSecretNameInvalid,
					InternalUser: false,
					Host:         prodHostName,
				},
			})

			job := &batchv1.Job{}
			Eventually(func() error {
				return adminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"Job should be created")

			// Check upload container has proxy env vars
			for _, container := range job.Spec.Template.Spec.Containers {
				if container.Name == uploadContainerName {
					envVars := make(map[string]string)
					for _, env := range container.Env {
						envVars[env.Name] = env.Value
					}
					if operatorHTTPProxy != "" {
						Expect(envVars).To(HaveKeyWithValue("HTTP_PROXY", operatorHTTPProxy),
							"Upload container should inherit HTTP_PROXY from operator")
					}
					if operatorHTTPSProxy != "" {
						Expect(envVars).To(HaveKeyWithValue("HTTPS_PROXY", operatorHTTPSProxy),
							"Upload container should inherit HTTPS_PROXY from operator")
					}
					if operatorNoProxy != "" {
						Expect(envVars).To(HaveKeyWithValue("NO_PROXY", operatorNoProxy),
							"Upload container should inherit NO_PROXY from operator")
					}
					break
				}
			}

			ginkgo.GinkgoWriter.Println("Operator pod has correct proxy environment variables matching cluster config")
		})
	})

	ginkgo.Context("No-Upload Mode Tests", func() {
		var mustGatherName string
		var mustGatherCR *mustgatherv1alpha1.MustGather

		ginkgo.BeforeEach(func() {
			mustGatherName = fmt.Sprintf("mg-no-upload-%d", time.Now().UnixNano())
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

		ginkgo.It("should not create upload container when uploadTarget is not specified", func() {
			ginkgo.By("Creating MustGather CR without uploadTarget configuration")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, nil)

			ginkgo.By("Verifying MustGather CR has no uploadTarget set")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			err := nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: ns.Name,
			}, fetchedMG)
			Expect(err).NotTo(HaveOccurred(), "Failed to get MustGather CR for no-upload verification")
			Expect(fetchedMG.Spec.UploadTarget).To(BeNil(),
				"UploadTarget should not be set")

			ginkgo.By("Waiting for Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"Job should be created for MustGather without uploadTarget")

			ginkgo.By("Verifying Job does NOT have an upload container")
			containerNames := make([]string, 0, len(job.Spec.Template.Spec.Containers))
			hasUploadContainer := false
			for _, container := range job.Spec.Template.Spec.Containers {
				containerNames = append(containerNames, container.Name)
				if container.Name == uploadContainerName {
					hasUploadContainer = true
				}
			}
			ginkgo.GinkgoWriter.Printf("Job containers: %v\n", containerNames)
			Expect(hasUploadContainer).To(BeFalse(),
				"Job should NOT have upload container when uploadTarget is not specified")

			ginkgo.By("Verifying Job has only the gather container")
			hasGatherContainer := false
			for _, container := range job.Spec.Template.Spec.Containers {
				if container.Name == gatherContainerName {
					hasGatherContainer = true
				}
			}
			Expect(hasGatherContainer).To(BeTrue(),
				"Job should have gather container")

			ginkgo.By("Waiting for Job to complete successfully")
			Eventually(func() bool {
				if err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job); err != nil {
					return false
				}
				return job.Status.Succeeded > 0 || job.Status.Failed > 0
			}).WithTimeout(5*time.Minute).WithPolling(10*time.Second).Should(BeTrue(),
				"Job should complete")

			ginkgo.By("Verifying MustGather CR status is updated")
			err = nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: ns.Name,
			}, fetchedMG)
			Expect(err).NotTo(HaveOccurred(), "Failed to get MustGather CR status after no-upload completion")
			Expect(fetchedMG.Status.Completed).To(BeTrue(),
				"MustGather should be marked as completed")

			ginkgo.GinkgoWriter.Printf("MustGather without upload completed - Status: %s\n",
				fetchedMG.Status.Status)
			ginkgo.GinkgoWriter.Println("Verified: No upload container when uploadTarget is not specified")
		})
	})

	ginkgo.Context("Audit Log Collection Tests", func() {
		var mustGatherName string
		var mustGatherCR *mustgatherv1alpha1.MustGather

		ginkgo.BeforeEach(func() {
			mustGatherName = fmt.Sprintf("mg-audit-test-%d", time.Now().UnixNano())
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

		ginkgo.It("should use audit gather command when audit is enabled", func() {
			ginkgo.By("Creating MustGather CR with audit enabled")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				GatherSpec: &mustgatherv1alpha1.GatherSpec{
					Audit: true,
				},
			})

			ginkgo.By("Waiting for Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"Job should be created for MustGather with audit enabled")

			ginkgo.By("Verifying gather container uses audit gather command")
			var gatherContainer *corev1.Container
			for i := range job.Spec.Template.Spec.Containers {
				if job.Spec.Template.Spec.Containers[i].Name == gatherContainerName {
					gatherContainer = &job.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(gatherContainer).NotTo(BeNil(), "Job should have gather container")

			commandStr := strings.Join(gatherContainer.Command, " ")
			ginkgo.GinkgoWriter.Printf("Gather container command: %s\n", commandStr)
			Expect(commandStr).To(ContainSubstring("gather_audit_logs"),
				"Gather container command should use gather_audit_logs binary when audit is enabled")
		})

		ginkgo.It("should default audit to false and use standard gather command", func() {
			ginkgo.By("Creating MustGather CR without specifying audit (default)")
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, nil)

			ginkgo.By("Verifying MustGather CR has no GatherSpec (audit defaults to false)")
			fetchedMG := &mustgatherv1alpha1.MustGather{}
			err := nonAdminClient.Get(testCtx, client.ObjectKey{
				Name:      mustGatherName,
				Namespace: ns.Name,
			}, fetchedMG)
			Expect(err).NotTo(HaveOccurred(), "Failed to get MustGather CR for audit log defaults check")
			if fetchedMG.Spec.GatherSpec != nil {
				Expect(fetchedMG.Spec.GatherSpec.Audit).To(BeFalse(),
					"Audit field should default to false")
			}

			ginkgo.By("Waiting for Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"Job should be created")

			ginkgo.By("Verifying gather container uses standard (non-audit) gather command")
			var gatherContainer *corev1.Container
			for i := range job.Spec.Template.Spec.Containers {
				if job.Spec.Template.Spec.Containers[i].Name == gatherContainerName {
					gatherContainer = &job.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(gatherContainer).NotTo(BeNil(), "Job should have gather container")

			commandStr := strings.Join(gatherContainer.Command, " ")
			ginkgo.GinkgoWriter.Printf("Gather container command: %s\n", commandStr)
			Expect(commandStr).NotTo(ContainSubstring("gather_audit_logs"),
				"Gather container command should NOT use gather_audit_logs when audit is not enabled")
		})

		ginkgo.Context("PVC audit log collection", ginkgo.Ordered, func() {
			var pvcName string
			var auditPVC *corev1.PersistentVolumeClaim
			var auditMustGatherName string
			var auditMustGatherCR *mustgatherv1alpha1.MustGather

			ginkgo.BeforeAll(func() {
				auditMustGatherName = fmt.Sprintf("mg-audit-pvc-%d", time.Now().UnixNano())
				pvcName = fmt.Sprintf("audit-pvc-%d", time.Now().UnixNano())
			})

			ginkgo.AfterAll(func() {
				if auditMustGatherCR != nil {
					_ = nonAdminClient.Delete(testCtx, auditMustGatherCR)
					Eventually(func() bool {
						err := nonAdminClient.Get(testCtx, client.ObjectKey{
							Name:      auditMustGatherName,
							Namespace: ns.Name,
						}, &mustgatherv1alpha1.MustGather{})
						return apierrors.IsNotFound(err)
					}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())
				}
				if auditPVC != nil {
					_ = adminClient.Delete(testCtx, auditPVC)
				}
			})

			ginkgo.It("should bind PVC for audit storage", func() {
				auditPVC = &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pvcName,
						Namespace: ns.Name,
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse("5Gi"),
							},
						},
					},
				}
				err := adminClient.Create(testCtx, auditPVC)
				if err != nil {
					ginkgo.Skip(fmt.Sprintf("Skip: Cannot create PVC (no StorageClass available): %v", err))
				}

				Eventually(func() corev1.PersistentVolumeClaimPhase {
					pvc := &corev1.PersistentVolumeClaim{}
					if err := adminClient.Get(testCtx, client.ObjectKey{
						Name:      pvcName,
						Namespace: ns.Name,
					}, pvc); err != nil {
						return ""
					}
					return pvc.Status.Phase
				}).WithTimeout(3*time.Minute).WithPolling(5*time.Second).Should(
					Equal(corev1.ClaimBound),
					"PVC should become Bound")
			})

			ginkgo.It("should create and complete audit gather Job with PVC", func() {
				subPath := fmt.Sprintf("audit-test-%d", time.Now().UnixNano())
				auditMustGatherCR = createMustGatherCR(auditMustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
					GatherSpec: &mustgatherv1alpha1.GatherSpec{
						Audit: true,
					},
					PersistentVolume: &PersistentVolumeOptions{
						PVCName: pvcName,
						SubPath: subPath,
					},
				})

				job := &batchv1.Job{}
				Eventually(func() error {
					return nonAdminClient.Get(testCtx, client.ObjectKey{
						Name:      auditMustGatherName,
						Namespace: ns.Name,
					}, job)
				}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
					"Job should be created")

				Eventually(func() bool {
					if err := nonAdminClient.Get(testCtx, client.ObjectKey{
						Name:      auditMustGatherName,
						Namespace: ns.Name,
					}, job); err != nil {
						return false
					}
					return job.Status.Succeeded > 0 || job.Status.Failed > 0
				}).WithTimeout(10*time.Minute).WithPolling(10*time.Second).Should(BeTrue(),
					"Job should complete")
			})

			ginkgo.It("should contain audit_logs directory on PVC", func() {
				readerPodName := fmt.Sprintf("audit-reader-%d", time.Now().UnixNano())
				readerPod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      readerPodName,
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
								Name:  "audit-reader",
								Image: operatorImage,
								Command: []string{
									"/bin/sh", "-c",
									`ls -la /must-gather && find /must-gather -type d -name 'audit_logs' 2>/dev/null | grep . && find /must-gather -type d 2>/dev/null | head -30`,
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
										ClaimName: pvcName,
									},
								},
							},
						},
					},
				}

				err := nonAdminClient.Create(testCtx, readerPod)
				Expect(err).NotTo(HaveOccurred(), "Failed to create reader pod")
				defer func() {
					_ = nonAdminClient.Delete(testCtx, readerPod)
				}()

				Eventually(func() corev1.PodPhase {
					if err := nonAdminClient.Get(testCtx, client.ObjectKey{
						Name:      readerPodName,
						Namespace: ns.Name,
					}, readerPod); err != nil {
						return corev1.PodUnknown
					}
					return readerPod.Status.Phase
				}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(
					Or(Equal(corev1.PodSucceeded), Equal(corev1.PodFailed)),
					"Reader pod should complete")

				Expect(readerPod.Status.Phase).To(Equal(corev1.PodSucceeded),
					"Reader pod should succeed, indicating audit_logs directory was found on PVC")

				logs, err := getContainerLogs(ns.Name, readerPodName, "audit-reader")
				Expect(err).NotTo(HaveOccurred(), "Failed to get reader pod logs")

				ginkgo.GinkgoWriter.Printf("Reader Pod logs:\n%s\n", logs)

				Expect(logs).To(ContainSubstring("audit_logs"),
					"PVC should contain audit_logs directory when audit is enabled")
			})
		})
	})

	ginkgo.Context("Must-Gather Log Content Verification Tests", func() {
		var mustGatherName string
		var mustGatherCR *mustgatherv1alpha1.MustGather

		ginkgo.BeforeEach(func() {
			mustGatherName = fmt.Sprintf("mg-log-content-%d", time.Now().UnixNano())
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

		ginkgo.It("should include must-gather.log in the gathered output on PVC", func() {
			ginkgo.By("Checking if cluster has StorageClass available")
			pvcName := fmt.Sprintf("log-verify-pvc-%d", time.Now().UnixNano())
			logPVC := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: ns.Name,
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("5Gi"),
						},
					},
				},
			}
			err := adminClient.Create(testCtx, logPVC)
			if err != nil {
				ginkgo.Skip(fmt.Sprintf("Skip: Cannot create PVC (no StorageClass available): %v", err))
			}
			defer func() {
				_ = adminClient.Delete(testCtx, logPVC)
			}()

			ginkgo.By("Creating MustGather CR with PVC storage")
			subPath := fmt.Sprintf("log-test-%d", time.Now().UnixNano())
			mustGatherCR = createMustGatherCR(mustGatherName, ns.Name, serviceAccount, true, &MustGatherCROptions{
				PersistentVolume: &PersistentVolumeOptions{
					PVCName: pvcName,
					SubPath: subPath,
				},
			})

			ginkgo.By("Waiting for PVC to become Bound")
			pvcBound := false
			Eventually(func() corev1.PersistentVolumeClaimPhase {
				pvc := &corev1.PersistentVolumeClaim{}
				if err := adminClient.Get(testCtx, client.ObjectKey{
					Name:      pvcName,
					Namespace: ns.Name,
				}, pvc); err != nil {
					return ""
				}
				if pvc.Status.Phase == corev1.ClaimBound {
					pvcBound = true
				}
				return pvc.Status.Phase
			}).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(
				Or(Equal(corev1.ClaimBound), Equal(corev1.ClaimPending)),
			)
			if !pvcBound {
				ginkgo.Skip("Skip: PVC did not become Bound within timeout (no dynamic provisioner available)")
			}

			ginkgo.By("Waiting for Job to complete")
			job := &batchv1.Job{}
			Eventually(func() error {
				return nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job)
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
				"Job should be created")

			Eventually(func() bool {
				if err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      mustGatherName,
					Namespace: ns.Name,
				}, job); err != nil {
					return false
				}
				return job.Status.Succeeded > 0 || job.Status.Failed > 0
			}).WithTimeout(10*time.Minute).WithPolling(10*time.Second).Should(BeTrue(),
				"Job should complete")

			ginkgo.By("Creating reader pod to verify must-gather.log content on PVC")
			readerPodName := fmt.Sprintf("log-reader-%d", time.Now().UnixNano())
			readerPod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      readerPodName,
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
							Name:  "log-reader",
							Image: operatorImage,
							Command: []string{
								"/bin/sh", "-c",
								`find /must-gather -name 'must-gather.log' -type f 2>/dev/null | grep . && find /must-gather -name 'must-gather.log' -type f -exec head -50 {} \; 2>/dev/null && find /must-gather -name '*.tar*' -type f 2>/dev/null | head -10`,
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
									ClaimName: pvcName,
								},
							},
						},
					},
				},
			}

			err = nonAdminClient.Create(testCtx, readerPod)
			Expect(err).NotTo(HaveOccurred(), "Failed to create reader pod")
			defer func() {
				_ = nonAdminClient.Delete(testCtx, readerPod)
			}()

			ginkgo.By("Waiting for reader pod to complete")
			Eventually(func() corev1.PodPhase {
				if err := nonAdminClient.Get(testCtx, client.ObjectKey{
					Name:      readerPodName,
					Namespace: ns.Name,
				}, readerPod); err != nil {
					return corev1.PodUnknown
				}
				return readerPod.Status.Phase
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(
				Or(Equal(corev1.PodSucceeded), Equal(corev1.PodFailed)),
				"Reader pod should complete")

			Expect(readerPod.Status.Phase).To(Equal(corev1.PodSucceeded),
				"Reader pod should succeed, indicating must-gather.log was found on PVC")

			ginkgo.By("Checking reader pod logs for must-gather.log")
			logs, err := getContainerLogs(ns.Name, readerPodName, "log-reader")
			Expect(err).NotTo(HaveOccurred(), "Failed to get reader pod logs")

			ginkgo.GinkgoWriter.Printf("Reader Pod logs:\n%s\n", logs)

			Expect(logs).To(ContainSubstring("must-gather.log"),
				"PVC should contain must-gather.log file from the gather process")

			ginkgo.GinkgoWriter.Println("Verified: must-gather.log is included in the gathered output")
		})
	})
})

// Helper Functions

// createNonAdminClient creates a Kubernetes client that impersonates a non-admin user for RBAC testing.
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

// verifyOperatorDeployment asserts that the must-gather-operator deployment exists and has ready replicas.
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

// createMustGatherCR builds and creates a MustGather custom resource with the given parameters.
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

		if opts.ImageStreamRef != nil {
			mg.Spec.ImageStreamRef = opts.ImageStreamRef
		}

		if opts.GatherSpec != nil {
			mg.Spec.GatherSpec = opts.GatherSpec
		}
	}

	err := nonAdminClient.Create(testCtx, mg)
	Expect(err).NotTo(HaveOccurred(), "Failed to create MustGather CR")

	return mg
}

// getCaseCreds retrieves SFTP credentials from Vault or environment variables for case upload tests.
func getCaseCreds() (string, string, error) {
	// Check for offline token in Vault first
	offlineToken, err := readOfflineTokenFromVault()
	if err != nil {
		return "", "", err
	}
	if offlineToken != "" {
		return refreshSFTPToken(offlineToken)
	}

	// Fall back to local credentials
	sftpUsername := os.Getenv("SFTP_USERNAME_E2E")
	sftpPassword := os.Getenv("SFTP_PASSWORD_E2E")
	if sftpUsername != "" && sftpPassword != "" {
		return sftpUsername, sftpPassword, nil
	}

	return "", "", fmt.Errorf("SFTP credentials not found. Set either:\n"+
		"  1. SFTP_USERNAME_E2E and SFTP_PASSWORD_E2E (typical for local runs), or\n"+
		"  2. mount %q under CASE_MANAGEMENT_CREDS_CONFIG_DIR for refresh-sftp-token.sh", vaultOfflineTokenKey)
}

type sftpCreds struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// refreshSFTPToken runs test/e2e/refresh-sftp-token.sh script
// to refresh the SFTP token when the offline token is provided
func refreshSFTPToken(offlineToken string) (string, string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", "", fmt.Errorf("could not resolve path to %s", refreshSFTPTokenScript)
	}
	script := filepath.Join(filepath.Dir(thisFile), refreshSFTPTokenScript)
	if _, err := os.Stat(script); err != nil {
		return "", "", fmt.Errorf("SFTP token generation script %q: %w", script, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), refreshSFTPTokenScriptTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", script)
	cmd.Env = append(os.Environ(), "RH_OFFLINE_TOKEN="+strings.TrimSpace(offlineToken))

	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", "", fmt.Errorf("refresh-sftp-token script exceeded deadline %s: %w", refreshSFTPTokenScriptTimeout, err)
		}
		return "", "", fmt.Errorf("refresh-sftp-token script failed: %w", err)
	}

	var creds sftpCreds
	if err := json.Unmarshal(bytes.TrimSpace(out), &creds); err != nil {
		return "", "", fmt.Errorf("error in parsing generate script output: %w", err)
	}
	if creds.Username == "" || creds.Password == "" {
		return "", "", fmt.Errorf("generate script returned empty username or password")
	}
	return creds.Username, creds.Password, nil
}

// readOfflineTokenFromVault returns the RH SSO offline refresh token for refresh-sftp-token.sh
func readOfflineTokenFromVault() (string, error) {
	configDir := os.Getenv(caseCredsConfigDirEnvVar)
	if configDir == "" {
		return "", nil
	}
	if _, err := os.Stat(configDir); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("stat CASE_MANAGEMENT_CREDS_CONFIG_DIR %q: %w", configDir, err)
	}

	path := filepath.Join(configDir, vaultOfflineTokenKey)
	offline_token, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("expected offline refresh token at %q (Vault key %s)", path, vaultOfflineTokenKey)
		}
		return "", fmt.Errorf("read offline token %q: %w", path, err)
	}
	if trimmed := strings.TrimSpace(string(offline_token)); trimmed != "" {
		return trimmed, nil
	}
	return "", fmt.Errorf("offline token file %q is empty", path)
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

	// Internal users upload to $SFTP_USERNAME/<caseid>_<file>, so list that directory.
	// External users upload directly to the root directory.
	sftpListCommand := "ls -la"
	if internalUser {
		sftpListCommand = "ls -la $SFTP_USERNAME"
	}

	sftpHost := host
	if net.ParseIP(host) != nil && strings.Contains(host, ":") {
		sftpHost = "[" + host + "]"
	}

	sftpCommand := fmt.Sprintf(`
		mkdir -p /tmp/.ssh
		touch /tmp/.ssh/known_hosts
		chmod 700 /tmp/.ssh
		chmod 600 /tmp/.ssh/known_hosts
		echo "Listing files on SFTP server..."
		echo "Internal user mode: %v"

		# Build proxy config if proxy env vars are set
		SSH_CONFIG="/tmp/.ssh/config"
		echo "Host *" > ${SSH_CONFIG}
		echo "  BatchMode no" >> ${SSH_CONFIG}
		echo "  StrictHostKeyChecking no" >> ${SSH_CONFIG}
		echo "  UserKnownHostsFile /tmp/.ssh/known_hosts" >> ${SSH_CONFIG}

		if [ -n "${http_proxy}" ] || [ -n "${https_proxy}" ]; then
		  PROXY_URL="${https_proxy:-${http_proxy}}"
		  PROXY_NO_PROTOCOL=$(echo "${PROXY_URL}" | sed -E 's|^https?://||')
		  if echo "${PROXY_NO_PROTOCOL}" | grep -q '@'; then
		    PROXY_AUTH=$(echo "${PROXY_NO_PROTOCOL}" | sed -E 's|^([^@]+)@.*|\1|')
		    PROXY_USER=$(echo "${PROXY_AUTH}" | cut -d: -f1)
		    PROXY_PASSWORD=$(echo "${PROXY_AUTH}" | cut -d: -f2)
		    PROXY_HOST_PORT=$(echo "${PROXY_NO_PROTOCOL}" | sed -E 's|^[^@]+@([^/]+).*|\1|')
		  else
		    PROXY_HOST_PORT=$(echo "${PROXY_NO_PROTOCOL}" | sed -E 's|^([^/]+).*|\1|')
		    PROXY_USER=""
		    PROXY_PASSWORD=""
		  fi
		  if [ -n "${PROXY_HOST_PORT}" ]; then
		    if echo "${PROXY_URL}" | grep -q '^https://'; then
		      export PROXY_HOST_PORT PROXY_USER PROXY_PASSWORD
		      echo "  ProxyCommand https-proxy-connect-util %%h %%p" >> ${SSH_CONFIG}
		    else
		      if [ -n "${PROXY_USER}" ] && [ -n "${PROXY_PASSWORD}" ]; then
		        echo "  ProxyCommand nc --proxy ${PROXY_HOST_PORT} --proxy-auth ${PROXY_USER}:${PROXY_PASSWORD} --proxy-type http %%h %%p" >> ${SSH_CONFIG}
		      else
		        echo "  ProxyCommand nc --proxy ${PROXY_HOST_PORT} --proxy-type http %%h %%p" >> ${SSH_CONFIG}
		      fi
		    fi
		    echo "Using proxy: ${PROXY_HOST_PORT}"
		  fi
		fi
		chmod 600 ${SSH_CONFIG}
		sed 's/--proxy-auth [^ ]*/--proxy-auth REDACTED/' ${SSH_CONFIG}

		sshpass -e sftp -F ${SSH_CONFIG} $SFTP_USERNAME@%s << EOF
%s
bye
EOF
	`, internalUser, sftpHost, sftpListCommand)

	// Read proxy env vars from the operator deployment to pass to the verification pod
	httpProxy, httpsProxy, noProxy, _ := getOperatorProxyEnvVars()

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
					Env: buildVerifyPodEnvVars(secretName, httpProxy, httpsProxy, noProxy),
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

func buildVerifyPodEnvVars(secretName, httpProxy, httpsProxy, noProxy string) []corev1.EnvVar {
	envVars := []corev1.EnvVar{
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
	}
	if httpProxy != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "http_proxy", Value: httpProxy})
	}
	if httpsProxy != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "https_proxy", Value: httpsProxy})
	}
	if noProxy != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "no_proxy", Value: noProxy})
	}
	return envVars
}

func getOperatorProxyEnvVars() (httpProxy, httpsProxy, noProxy string, hasProxy bool) {
	deployment := &appsv1.Deployment{}
	err := adminClient.Get(testCtx, client.ObjectKey{
		Name:      operatorDeployment,
		Namespace: operatorNamespace,
	}, deployment)
	Expect(err).NotTo(HaveOccurred(), "Failed to get operator deployment for proxy detection")

	for _, container := range deployment.Spec.Template.Spec.Containers {
		for _, env := range container.Env {
			if env.ValueFrom != nil {
				continue
			}
			switch env.Name {
			case "HTTP_PROXY":
				httpProxy = env.Value
			case "HTTPS_PROXY":
				httpsProxy = env.Value
			case "NO_PROXY":
				noProxy = env.Value
			}
		}
	}
	hasProxy = httpProxy != "" || httpsProxy != ""
	return
}

func redactProxyURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid-url>"
	}
	if u.User != nil {
		u.User = url.UserPassword("<redacted>", "<redacted>")
	}
	return u.String()
}

func createImageStream(name, imageName, tagName string) {
	imageStream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: operatorNamespace,
		},
		Spec: imagev1.ImageStreamSpec{
			LookupPolicy: imagev1.ImageLookupPolicy{
				Local: false,
			},
			Tags: []imagev1.TagReference{
				{
					Name: tagName,
					From: &corev1.ObjectReference{
						Kind: "DockerImage",
						Name: imageName,
					},
					ImportPolicy: imagev1.TagImportPolicy{
						Scheduled: true,
					},
				},
			},
		},
	}
	err := adminClient.Create(testCtx, imageStream)
	Expect(err).NotTo(HaveOccurred(), "Failed to create ImageStream")
}

// deleteImageStream removes the named ImageStream from the operator namespace.
func deleteImageStream(name string) {
	imageStream := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: operatorNamespace,
		},
	}
	err := adminClient.Delete(testCtx, imageStream)
	if err != nil && !apierrors.IsNotFound(err) {
		Expect(err).NotTo(HaveOccurred(), "Failed to delete ImageStream")
	}
}
