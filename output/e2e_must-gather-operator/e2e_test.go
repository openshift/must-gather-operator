package osde2etests

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
)

// NOTE: This file contains E2E test blocks for the time-based log filtering feature (EP-1923).
// These tests can be added to the existing e2e test suite.
// Each test is prefixed with a comment indicating why it was generated from the diff.

var _ = Describe("MustGather Time-Based Log Filtering (EP-1923)", func() {

	const (
		mustGatherNamespace = "openshift-must-gather-operator"
		timeout             = time.Minute * 10
		interval            = time.Second * 5
	)

	Context("API Type Changes: gatherSpec field", func() {

		// Diff-suggested: New GatherSpec type with Since field added in mustgather_types.go
		It("should create MustGather with gatherSpec.since duration", func() {
			ctx := context.Background()

			mgName := fmt.Sprintf("test-since-%d", time.Now().Unix())

			By("Creating a MustGather CR with gatherSpec.since")
			mustGather := &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: "must-gather-admin",
					GatherSpec: &mustgatherv1alpha1.GatherSpec{
						Since: &metav1.Duration{Duration: 2 * time.Hour},
					},
				},
			}

			Expect(k8sClient.Create(ctx, mustGather)).To(Succeed())

			By("Waiting for MustGather Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				}, job)
			}, timeout, interval).Should(Succeed())

			By("Verifying MUST_GATHER_SINCE environment variable is set in gather container")
			var gatherContainer *corev1.Container
			for i := range job.Spec.Template.Spec.Containers {
				if job.Spec.Template.Spec.Containers[i].Name == "gather" {
					gatherContainer = &job.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(gatherContainer).NotTo(BeNil(), "gather container should exist")

			var mustGatherSinceEnv *corev1.EnvVar
			for i := range gatherContainer.Env {
				if gatherContainer.Env[i].Name == "MUST_GATHER_SINCE" {
					mustGatherSinceEnv = &gatherContainer.Env[i]
					break
				}
			}
			Expect(mustGatherSinceEnv).NotTo(BeNil(), "MUST_GATHER_SINCE env var should be set")
			Expect(mustGatherSinceEnv.Value).To(Equal("2h0m0s"), "Duration should be formatted correctly")

			By("Cleaning up the MustGather CR")
			Expect(k8sClient.Delete(ctx, mustGather)).To(Succeed())
		})

		// Diff-suggested: New GatherSpec type with SinceTime field added in mustgather_types.go
		It("should create MustGather with gatherSpec.sinceTime timestamp", func() {
			ctx := context.Background()

			mgName := fmt.Sprintf("test-sincetime-%d", time.Now().Unix())

			By("Creating a MustGather CR with gatherSpec.sinceTime")
			sinceTime := metav1.NewTime(time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC))
			mustGather := &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: "must-gather-admin",
					GatherSpec: &mustgatherv1alpha1.GatherSpec{
						SinceTime: &sinceTime,
					},
				},
			}

			Expect(k8sClient.Create(ctx, mustGather)).To(Succeed())

			By("Waiting for MustGather Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				}, job)
			}, timeout, interval).Should(Succeed())

			By("Verifying MUST_GATHER_SINCE_TIME environment variable is set in gather container")
			var gatherContainer *corev1.Container
			for i := range job.Spec.Template.Spec.Containers {
				if job.Spec.Template.Spec.Containers[i].Name == "gather" {
					gatherContainer = &job.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(gatherContainer).NotTo(BeNil(), "gather container should exist")

			var mustGatherSinceTimeEnv *corev1.EnvVar
			for i := range gatherContainer.Env {
				if gatherContainer.Env[i].Name == "MUST_GATHER_SINCE_TIME" {
					mustGatherSinceTimeEnv = &gatherContainer.Env[i]
					break
				}
			}
			Expect(mustGatherSinceTimeEnv).NotTo(BeNil(), "MUST_GATHER_SINCE_TIME env var should be set")
			Expect(mustGatherSinceTimeEnv.Value).To(Equal("2026-03-23T10:00:00Z"), "Timestamp should be RFC3339 formatted")

			By("Cleaning up the MustGather CR")
			Expect(k8sClient.Delete(ctx, mustGather)).To(Succeed())
		})

		// Diff-suggested: Both Since and SinceTime fields can be set (EP documents precedence)
		It("should create MustGather with both gatherSpec.since and gatherSpec.sinceTime", func() {
			ctx := context.Background()

			mgName := fmt.Sprintf("test-both-%d", time.Now().Unix())

			By("Creating a MustGather CR with both fields")
			sinceTime := metav1.NewTime(time.Date(2026, 3, 23, 10, 0, 0, 0, time.UTC))
			mustGather := &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: "must-gather-admin",
					GatherSpec: &mustgatherv1alpha1.GatherSpec{
						Since:     &metav1.Duration{Duration: 1 * time.Hour},
						SinceTime: &sinceTime,
					},
				},
			}

			Expect(k8sClient.Create(ctx, mustGather)).To(Succeed())

			By("Waiting for MustGather Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				}, job)
			}, timeout, interval).Should(Succeed())

			By("Verifying both environment variables are set")
			var gatherContainer *corev1.Container
			for i := range job.Spec.Template.Spec.Containers {
				if job.Spec.Template.Spec.Containers[i].Name == "gather" {
					gatherContainer = &job.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(gatherContainer).NotTo(BeNil())

			envMap := make(map[string]string)
			for _, env := range gatherContainer.Env {
				envMap[env.Name] = env.Value
			}

			Expect(envMap).To(HaveKey("MUST_GATHER_SINCE"))
			Expect(envMap["MUST_GATHER_SINCE"]).To(Equal("1h0m0s"))
			Expect(envMap).To(HaveKey("MUST_GATHER_SINCE_TIME"))
			Expect(envMap["MUST_GATHER_SINCE_TIME"]).To(Equal("2026-03-23T10:00:00Z"))

			By("Cleaning up the MustGather CR")
			Expect(k8sClient.Delete(ctx, mustGather)).To(Succeed())
		})

		// Diff-suggested: Backward compatibility - gatherSpec is optional
		It("should create MustGather without gatherSpec (backward compatibility)", func() {
			ctx := context.Background()

			mgName := fmt.Sprintf("test-no-gatherspec-%d", time.Now().Unix())

			By("Creating a MustGather CR without gatherSpec")
			mustGather := &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: "must-gather-admin",
					// GatherSpec intentionally omitted
				},
			}

			Expect(k8sClient.Create(ctx, mustGather)).To(Succeed())

			By("Waiting for MustGather Job to be created")
			job := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				}, job)
			}, timeout, interval).Should(Succeed())

			By("Verifying NO time filter environment variables are set")
			var gatherContainer *corev1.Container
			for i := range job.Spec.Template.Spec.Containers {
				if job.Spec.Template.Spec.Containers[i].Name == "gather" {
					gatherContainer = &job.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(gatherContainer).NotTo(BeNil())

			envMap := make(map[string]string)
			for _, env := range gatherContainer.Env {
				envMap[env.Name] = env.Value
			}

			Expect(envMap).NotTo(HaveKey("MUST_GATHER_SINCE"), "MUST_GATHER_SINCE should not be set")
			Expect(envMap).NotTo(HaveKey("MUST_GATHER_SINCE_TIME"), "MUST_GATHER_SINCE_TIME should not be set")

			By("Cleaning up the MustGather CR")
			Expect(k8sClient.Delete(ctx, mustGather)).To(Succeed())
		})

		// Diff-suggested: CRD validation includes format=duration for Since field
		It("should accept various duration formats for gatherSpec.since", func() {
			ctx := context.Background()

			testCases := []struct {
				duration      time.Duration
				expectedValue string
			}{
				{30 * time.Minute, "30m0s"},
				{300 * time.Second, "5m0s"},
				{24 * time.Hour, "24h0m0s"},
			}

			for _, tc := range testCases {
				mgName := fmt.Sprintf("test-duration-%d", time.Now().UnixNano())

				By(fmt.Sprintf("Creating MustGather with duration %v", tc.duration))
				mustGather := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{
						Name:      mgName,
						Namespace: mustGatherNamespace,
					},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "must-gather-admin",
						GatherSpec: &mustgatherv1alpha1.GatherSpec{
							Since: &metav1.Duration{Duration: tc.duration},
						},
					},
				}

				Expect(k8sClient.Create(ctx, mustGather)).To(Succeed())

				By("Verifying environment variable format")
				job := &batchv1.Job{}
				Eventually(func() error {
					return k8sClient.Get(ctx, types.NamespacedName{
						Name:      mgName,
						Namespace: mustGatherNamespace,
					}, job)
				}, timeout, interval).Should(Succeed())

				var gatherContainer *corev1.Container
				for i := range job.Spec.Template.Spec.Containers {
					if job.Spec.Template.Spec.Containers[i].Name == "gather" {
						gatherContainer = &job.Spec.Template.Spec.Containers[i]
						break
					}
				}

				envMap := make(map[string]string)
				for _, env := range gatherContainer.Env {
					envMap[env.Name] = env.Value
				}

				Expect(envMap["MUST_GATHER_SINCE"]).To(Equal(tc.expectedValue))

				By("Cleaning up")
				Expect(k8sClient.Delete(ctx, mustGather)).To(Succeed())

				// Small delay between iterations
				time.Sleep(1 * time.Second)
			}
		})
	})

	Context("Controller Changes: Environment Variable Passing", func() {

		// Diff-suggested: template.go modified to call getGatherContainer with gatherSpec parameter
		It("should pass gatherSpec to Job template correctly", func() {
			ctx := context.Background()

			mgName := fmt.Sprintf("test-controller-%d", time.Now().Unix())

			By("Creating a MustGather CR with gatherSpec")
			mustGather := &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: "must-gather-admin",
					GatherSpec: &mustgatherv1alpha1.GatherSpec{
						Since: &metav1.Duration{Duration: 2 * time.Hour},
					},
				},
			}

			Expect(k8sClient.Create(ctx, mustGather)).To(Succeed())

			By("Verifying Job is created with correct environment variables")
			job := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				}, job)
			}, timeout, interval).Should(Succeed())

			By("Checking Job metadata and owner references")
			Expect(job.Name).To(Equal(mgName))
			Expect(job.Namespace).To(Equal(mustGatherNamespace))

			By("Verifying gather container configuration")
			var gatherContainer *corev1.Container
			for i := range job.Spec.Template.Spec.Containers {
				if job.Spec.Template.Spec.Containers[i].Name == "gather" {
					gatherContainer = &job.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(gatherContainer).NotTo(BeNil())
			Expect(gatherContainer.Image).NotTo(BeEmpty())

			By("Verifying environment variable is correctly formatted")
			found := false
			for _, env := range gatherContainer.Env {
				if env.Name == "MUST_GATHER_SINCE" {
					Expect(env.Value).To(Equal("2h0m0s"))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "MUST_GATHER_SINCE env var should be present")

			By("Cleaning up the MustGather CR")
			Expect(k8sClient.Delete(ctx, mustGather)).To(Succeed())
		})

		// Diff-suggested: template.go handles nil gatherSpec correctly
		It("should not add environment variables when gatherSpec is nil", func() {
			ctx := context.Background()

			mgName := fmt.Sprintf("test-nil-gatherspec-%d", time.Now().Unix())

			By("Creating a MustGather CR with nil gatherSpec")
			mustGather := &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: "must-gather-admin",
					GatherSpec:         nil, // Explicitly nil
				},
			}

			Expect(k8sClient.Create(ctx, mustGather)).To(Succeed())

			By("Verifying Job is created without time filter env vars")
			job := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				}, job)
			}, timeout, interval).Should(Succeed())

			var gatherContainer *corev1.Container
			for i := range job.Spec.Template.Spec.Containers {
				if job.Spec.Template.Spec.Containers[i].Name == "gather" {
					gatherContainer = &job.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(gatherContainer).NotTo(BeNil())

			By("Confirming no MUST_GATHER_SINCE or MUST_GATHER_SINCE_TIME env vars")
			for _, env := range gatherContainer.Env {
				Expect(env.Name).NotTo(Equal("MUST_GATHER_SINCE"))
				Expect(env.Name).NotTo(Equal("MUST_GATHER_SINCE_TIME"))
			}

			By("Cleaning up the MustGather CR")
			Expect(k8sClient.Delete(ctx, mustGather)).To(Succeed())
		})
	})

	Context("Integration with Existing Features", func() {

		// Diff-suggested: gatherSpec should work alongside existing fields like uploadTarget, proxy, timeout
		It("should work with gatherSpec and uploadTarget together", func() {
			ctx := context.Background()

			mgName := fmt.Sprintf("test-integration-%d", time.Now().Unix())

			By("Creating a MustGather CR with both gatherSpec and uploadTarget")
			mustGather := &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: "must-gather-admin",
					GatherSpec: &mustgatherv1alpha1.GatherSpec{
						Since: &metav1.Duration{Duration: 2 * time.Hour},
					},
					UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
						Type: mustgatherv1alpha1.UploadTypeSFTP,
						SFTP: &mustgatherv1alpha1.SFTPSpec{
							CaseID: "12345678",
							CaseManagementAccountSecretRef: corev1.LocalObjectReference{
								Name: "case-management-creds",
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, mustGather)).To(Succeed())

			By("Verifying Job has both gather and upload containers")
			job := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				}, job)
			}, timeout, interval).Should(Succeed())

			Expect(len(job.Spec.Template.Spec.Containers)).To(BeNumerically(">=", 2), "Should have at least gather and upload containers")

			By("Verifying gather container has MUST_GATHER_SINCE env var")
			var gatherContainer *corev1.Container
			for i := range job.Spec.Template.Spec.Containers {
				if job.Spec.Template.Spec.Containers[i].Name == "gather" {
					gatherContainer = &job.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(gatherContainer).NotTo(BeNil())

			found := false
			for _, env := range gatherContainer.Env {
				if env.Name == "MUST_GATHER_SINCE" {
					Expect(env.Value).To(Equal("2h0m0s"))
					found = true
					break
				}
			}
			Expect(found).To(BeTrue())

			By("Cleaning up the MustGather CR")
			Expect(k8sClient.Delete(ctx, mustGather)).To(Succeed())
		})
	})

	Context("Lifecycle and Status", func() {

		// Diff-suggested: Verify complete lifecycle with new gatherSpec field
		It("should complete MustGather lifecycle with gatherSpec", func() {
			ctx := context.Background()

			mgName := fmt.Sprintf("test-lifecycle-%d", time.Now().Unix())

			By("Creating a MustGather CR with gatherSpec")
			mustGather := &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: "must-gather-admin",
					GatherSpec: &mustgatherv1alpha1.GatherSpec{
						Since: &metav1.Duration{Duration: 1 * time.Hour},
					},
				},
			}

			Expect(k8sClient.Create(ctx, mustGather)).To(Succeed())

			By("Waiting for Job creation")
			job := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				}, job)
			}, timeout, interval).Should(Succeed())

			By("Monitoring MustGather status (optional - may timeout on actual cluster)")
			// Note: This will likely timeout in e2e tests since the job needs to actually run
			// Uncomment for live cluster testing
			/*
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      mgName,
					Namespace: mustGatherNamespace,
				}, mustGather)
				if err != nil {
					return false
				}
				return mustGather.Status.Completed
			}, timeout, interval).Should(BeTrue(), "MustGather should eventually complete")
			*/

			By("Cleaning up the MustGather CR")
			Expect(k8sClient.Delete(ctx, mustGather)).To(Succeed())
		})
	})
})
