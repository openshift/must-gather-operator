package mustgather

import (
	"context"
	"errors"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	batchv1 "k8s.io/api/batch/v1"
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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// interceptClient allows injecting failures for specific CRUD operations
type interceptClient struct {
	client.Client
	onGet    func(ctx context.Context, key client.ObjectKey, obj client.Object) error
	onList   func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
	onDelete func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error
	onUpdate func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error
	onCreate func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error
	status   client.StatusWriter
}

func (c interceptClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if c.onGet != nil {
		if err := c.onGet(ctx, key, obj); err != nil {
			return err
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}
func (c interceptClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if c.onList != nil {
		if err := c.onList(ctx, list, opts...); err != nil {
			return err
		}
	}
	return c.Client.List(ctx, list, opts...)
}
func (c interceptClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if c.onDelete != nil {
		if err := c.onDelete(ctx, obj, opts...); err != nil {
			return err
		}
	}
	return c.Client.Delete(ctx, obj, opts...)
}
func (c interceptClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if c.onUpdate != nil {
		if err := c.onUpdate(ctx, obj, opts...); err != nil {
			return err
		}
	}
	return c.Client.Update(ctx, obj, opts...)
}
func (c interceptClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if c.onCreate != nil {
		if err := c.onCreate(ctx, obj, opts...); err != nil {
			return err
		}
	}
	return c.Client.Create(ctx, obj, opts...)
}
func (c interceptClient) Status() client.StatusWriter {
	if c.status != nil {
		return c.status
	}
	return c.Client.Status()
}

// failingStatusWriter wraps a client.StatusWriter and forces Status().Update to return an error
type failingStatusWriter struct{ client.StatusWriter }

func (w failingStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return errors.New("forced status update error")
}

func TestCleanupMustGatherResources(t *testing.T) {
	targetNamespace := "foo-bar"

	tests := []struct {
		name           string
		setupObjects   func() []client.Object
		interceptors   func() interceptClient
		expectError    bool
		postTestChecks func(t *testing.T, cl client.Client)
	}{
		{
			name: "cleanup_success_all_resources_deleted",
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: targetNamespace},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
							Type: mustgatherv1alpha1.UploadTypeSFTP,
							SFTP: &mustgatherv1alpha1.SFTPSpec{
								CaseID:                         "12345678",
								CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "case-management-creds"},
							},
						},
					},
				}
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "case-management-creds", Namespace: targetNamespace}}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: targetNamespace, UID: "user-123"}}
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: targetNamespace, Labels: map[string]string{"controller-uid": string(job.UID)}}}
				return []client.Object{mg, secret, job, pod}
			},
			interceptors: func() interceptClient { return interceptClient{} },
			expectError:  false,
			postTestChecks: func(t *testing.T, cl client.Client) {
				// Verify job is deleted
				chkJob := &batchv1.Job{}
				if getErr := cl.Get(context.TODO(), types.NamespacedName{Namespace: targetNamespace, Name: "example-mustgather"}, chkJob); getErr == nil {
					t.Fatalf("expected job to be deleted")
				}
			},
		},
		{
			name: "cleanup_job_get_error_continues_successfully",
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: targetNamespace},
					Spec:       mustgatherv1alpha1.MustGatherSpec{},
				}
				return []client.Object{mg}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if _, ok := obj.(*batchv1.Job); ok && key.Name == "mg" {
							return errors.New("failed to get job")
						}
						return nil
					},
				}
			},
			expectError:    true,
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name: "cleanup_pod_list_error_leaves_job_intact",
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: targetNamespace},
					Spec:       mustgatherv1alpha1.MustGatherSpec{},
				}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: targetNamespace, UID: "u"}}
				return []client.Object{mg, job}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onList: func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
						if _, ok := list.(*corev1.PodList); ok {
							return errors.New("failed to list pods")
						}
						return nil
					},
				}
			},
			expectError: true,
			postTestChecks: func(t *testing.T, cl client.Client) {
				// Since cleanup failed due to pod list error, job should still exist
				chk := &batchv1.Job{}
				if e := cl.Get(context.TODO(), types.NamespacedName{Namespace: targetNamespace, Name: "mg"}, chk); e != nil {
					t.Fatalf("expected job to remain after failed cleanup, get err: %v", e)
				}
			},
		},
		{
			name: "cleanup_pod_delete_error_returns_error",
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: targetNamespace},
					Spec:       mustgatherv1alpha1.MustGatherSpec{},
				}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: targetNamespace, UID: "u"}}
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: targetNamespace, Labels: map[string]string{"controller-uid": string(job.UID)}}}
				return []client.Object{mg, job, pod}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onDelete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
						if _, ok := obj.(*corev1.Pod); ok {
							return errors.New("failed to delete pod")
						}
						return nil
					},
				}
			},
			expectError:    true,
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup scheme
			s := runtime.NewScheme()
			_ = corev1.AddToScheme(s)
			_ = batchv1.AddToScheme(s)
			_ = mustgatherv1alpha1.AddToScheme(s)

			// Setup objects and client
			objects := tt.setupObjects()
			base := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()

			// Setup interceptor if needed
			interceptor := tt.interceptors()
			var cl client.Client = base
			if interceptor.onGet != nil || interceptor.onList != nil || interceptor.onDelete != nil || interceptor.onUpdate != nil || interceptor.onCreate != nil || interceptor.status != nil {
				interceptor.Client = base
				cl = interceptor
			}

			// Create reconciler
			r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil)}

			// Get the MustGather object for the test
			var mg *mustgatherv1alpha1.MustGather
			for _, obj := range objects {
				if mgObj, ok := obj.(*mustgatherv1alpha1.MustGather); ok {
					mg = mgObj
					break
				}
			}

			// Execute
			err := r.cleanupMustGatherResources(context.TODO(), logf.Log, mg)

			// Assert error expectation
			if tt.expectError && err == nil {
				t.Fatalf("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Run post-test checks
			tt.postTestChecks(t, cl)
		})
	}
}

func TestReconcile(t *testing.T) {
	const operatorNs = "must-gather-operator"

	tests := []struct {
		name           string
		setupEnv       func(t *testing.T)
		setupObjects   func() []client.Object
		interceptors   func() interceptClient
		expectError    bool
		expectResult   reconcile.Result
		postTestChecks func(t *testing.T, cl client.Client)
	}{
		{
			name:     "reconcile_mustgather_not_found_returns_empty_result",
			setupEnv: func(t *testing.T) {},
			setupObjects: func() []client.Object {
				return []client.Object{}
			},
			interceptors:   func() interceptClient { return interceptClient{} },
			expectError:    false,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name:     "reconcile_mustgather_get_error_returns_error",
			setupEnv: func(t *testing.T) {},
			setupObjects: func() []client.Object {
				return []client.Object{}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						if _, ok := obj.(*mustgatherv1alpha1.MustGather); ok {
							return errors.New("failed to get mustgather")
						}
						return nil
					},
				}
			},
			expectError:    true,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name: "reconcile_initialize_mustgather_update_succeeds",
			setupEnv: func(t *testing.T) {
				t.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns"}}
				return []client.Object{mg}
			},
			interceptors:   func() interceptClient { return interceptClient{} },
			expectError:    false,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name:     "reconcile_initialize_mustgather_update_fails",
			setupEnv: func(t *testing.T) {},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns"}}
				return []client.Object{mg}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onUpdate: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
						if _, ok := obj.(*mustgatherv1alpha1.MustGather); ok {
							return errors.New("failed to update mustgather")
						}
						return nil
					},
				}
			},
			expectError:    true,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name: "reconcile_deletion_cleanup_and_finalizer_removal_success",
			setupEnv: func(t *testing.T) {
			},
			setupObjects: func() []client.Object {
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: operatorNs}}
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{
						Name: "example-mustgather", Namespace: operatorNs,
						Finalizers:        []string{mustGatherFinalizer},
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default",
						UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
							Type: mustgatherv1alpha1.UploadTypeSFTP,
							SFTP: &mustgatherv1alpha1.SFTPSpec{
								CaseID:                         "12345678",
								CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"},
							},
						},
					},
				}
				return []client.Object{mg, secret}
			},
			interceptors: func() interceptClient { return interceptClient{} },
			expectError:  false,
			expectResult: reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {
				out := &mustgatherv1alpha1.MustGather{}
				_ = cl.Get(context.TODO(), types.NamespacedName{Name: "example-mustgather", Namespace: operatorNs}, out)
				if contains(out.GetFinalizers(), mustGatherFinalizer) {
					t.Fatalf("expected finalizer removed")
				}
			},
		},
		{
			name: "reconcile_deletion_cleanup_resources_returns_error",
			setupEnv: func(t *testing.T) {
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{
						Name: "example-mustgather", Namespace: operatorNs,
						Finalizers:        []string{mustGatherFinalizer},
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default",
						UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
							Type: mustgatherv1alpha1.UploadTypeSFTP,
							SFTP: &mustgatherv1alpha1.SFTPSpec{
								CaseID:                         "12345678",
								CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"},
							},
						},
					},
				}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: operatorNs, UID: "user-123"}}
				return []client.Object{mg, job}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onDelete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
						if _, ok := obj.(*batchv1.Job); ok {
							return errors.New("failed to delete job")
						}
						return nil
					},
				}
			},
			expectError:    true,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name:     "reconcile_job_template_env_missing_returns_error",
			setupEnv: func(t *testing.T) {},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns"},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default",
					},
				}
				return []client.Object{mg}
			},
			interceptors:   func() interceptClient { return interceptClient{} },
			expectError:    true,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name: "reconcile_job_not_found_no_upload_target_creates_job_successfully",
			setupEnv: func(t *testing.T) {
				t.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default",
					},
				}
				return []client.Object{mg}
			},
			interceptors:   func() interceptClient { return interceptClient{} },
			expectError:    false,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name: "reconcile_job_not_found_creates_job_successfully",
			setupEnv: func(t *testing.T) {
				t.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default",
					},
				}
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, cv}
			},
			interceptors: func() interceptClient { return interceptClient{} },
			expectError:  false,
			expectResult: reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {
				// Verify job was created
				job := &batchv1.Job{}
				if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: "ns", Name: "example-mustgather"}, job); err != nil {
					t.Fatalf("expected job to be created, got error: %v", err)
				}
			},
		},
		{
			name: "reconcile_job_not_found_user_secret_not_found_calls_manage_error",
			setupEnv: func(t *testing.T) {
				t.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default",
						UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
							Type: mustgatherv1alpha1.UploadTypeSFTP,
							SFTP: &mustgatherv1alpha1.SFTPSpec{
								CaseID:                         "12345678",
								CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"},
							},
						},
					},
				}
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, cv}
			},
			interceptors: func() interceptClient { return interceptClient{} },
			expectError:  true,
			expectResult: reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {
				// Verify the MustGather status was updated with error condition
				out := &mustgatherv1alpha1.MustGather{}
				if getErr := cl.Get(context.TODO(), types.NamespacedName{Name: "example-mustgather", Namespace: "ns"}, out); getErr != nil {
					t.Fatalf("failed to get mustgather: %v", getErr)
				}
				// ManageError should have set ReconciliationStatus to "Error"
				if len(out.Status.Conditions) == 0 {
					t.Fatalf("expected error condition to be set on mustgather status")
				}
			},
		},
		{
			name: "reconcile_job_not_found_user_secret_get_error_returns_requeue",
			setupEnv: func(t *testing.T) {
				t.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default",
						UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
							Type: mustgatherv1alpha1.UploadTypeSFTP,
							SFTP: &mustgatherv1alpha1.SFTPSpec{
								CaseID:                         "12345678",
								CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"},
							},
						},
					},
				}
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, cv}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						// Return a non-NotFound error when getting user secret
						if _, ok := obj.(*corev1.Secret); ok && key.Namespace == "ns" && key.Name == "sec" {
							return errors.New("API server error - failed to get user secret")
						}
						return nil
					},
				}
			},
			expectError:  true,
			expectResult: reconcile.Result{Requeue: true},
			postTestChecks: func(t *testing.T, cl client.Client) {
				// Verify job was not created due to error
				job := &batchv1.Job{}
				if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: "ns", Name: "example-mustgather"}, job); err == nil {
					t.Fatalf("expected job to not be created when secret get fails")
				}
			},
		},
		{
			name: "reconcile_job_active_updates_status_running",
			setupEnv: func(t *testing.T) {
				t.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default",
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: "ns"}}
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns"}}
				job.Status.Active = 1
				return []client.Object{mg, userSecret, cv, job}
			},
			interceptors:   func() interceptClient { return interceptClient{} },
			expectError:    false,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name: "reconcile_job_succeeded_retain_resources_no_cleanup",
			setupEnv: func(t *testing.T) {
				t.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: operatorNs, Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName:          "default",
						RetainResourcesOnCompletion: ToPtr(true),
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: operatorNs}}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: operatorNs}}
				job.Status.Succeeded = 1
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, userSecret, cv, job}
			},
			interceptors: func() interceptClient { return interceptClient{} },
			expectError:  false,
			expectResult: reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {
				chk := &batchv1.Job{}
				if e := cl.Get(context.TODO(), types.NamespacedName{Namespace: operatorNs, Name: "example-mustgather"}, chk); e != nil {
					t.Fatalf("expected job to remain, err: %v", e)
				}
			},
		},
		{
			name: "reconcile_job_failed_cleanup_error_returns_error",
			setupEnv: func(t *testing.T) {
				t.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: operatorNs, Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default",
						UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
							Type: mustgatherv1alpha1.UploadTypeSFTP,
							SFTP: &mustgatherv1alpha1.SFTPSpec{
								CaseID:                         "12345678",
								CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"},
							},
						},
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: operatorNs}}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: operatorNs}}
				job.Status.Failed = 1
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, userSecret, cv, job}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onDelete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
						if _, ok := obj.(*batchv1.Job); ok {
							return errors.New("failed to delete job")
						}
						return nil
					},
				}
			},
			expectError:    true,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name: "reconcile_job_failed_retain_resources_no_cleanup",
			setupEnv: func(t *testing.T) {
				t.Setenv("OPERATOR_NAMESPACE", "bar")
				t.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName:          "default",
						RetainResourcesOnCompletion: ToPtr(true),
					},
				}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns"}}
				job.Status.Failed = 1
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, cv, job}
			},
			interceptors: func() interceptClient { return interceptClient{} },
			expectError:  false,
			expectResult: reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {
				out := &mustgatherv1alpha1.MustGather{}
				if getErr := cl.Get(context.TODO(), types.NamespacedName{Name: "example-mustgather", Namespace: "ns"}, out); getErr != nil {
					t.Fatalf("failed to get mustgather: %v", getErr)
				}
				if !out.Status.Completed || out.Status.Status != "Failed" || out.Status.Reason != "MustGather Job pods failed" {
					t.Fatalf("unexpected status after failed without cleanup: %+v", out.Status)
				}
			},
		},
		{
			name: "reconcile_job_succeeded_status_update_fails",
			setupEnv: func(t *testing.T) {
				t.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: operatorNs, Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default",
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: operatorNs}}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: operatorNs}}
				job.Status.Succeeded = 1
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, userSecret, cv, job}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					status: &failingStatusWriter{},
				}
			},
			expectError:    true,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name: "reconcile_job_failed_status_update_fails",
			setupEnv: func(t *testing.T) {
				t.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: operatorNs, Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default",
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: operatorNs}}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: operatorNs}}
				job.Status.Failed = 1
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, userSecret, cv, job}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					status: &failingStatusWriter{},
				}
			},
			expectError:    true,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name:     "reconcile_deletion_finalizer_removal_update_fails",
			setupEnv: func(t *testing.T) {},
			setupObjects: func() []client.Object {
				operatorNs := "must-gather-operator"
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret", Namespace: operatorNs}}
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{
						Name: "must-gather", Namespace: operatorNs,
						Finalizers:        []string{mustGatherFinalizer},
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default",
						UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
							Type: mustgatherv1alpha1.UploadTypeSFTP,
							SFTP: &mustgatherv1alpha1.SFTPSpec{
								CaseID:                         "12345678",
								CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "secret"},
							},
						},
					},
				}
				return []client.Object{mg, secret}
			},
			interceptors: func() interceptClient {
				updateCount := 0
				return interceptClient{
					onUpdate: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
						if mgObj, ok := obj.(*mustgatherv1alpha1.MustGather); ok {
							updateCount++
							// Fail the update when removing finalizer (after cleanup is done)
							if updateCount > 0 && !contains(mgObj.GetFinalizers(), mustGatherFinalizer) {
								return errors.New("failed to remove finalizer")
							}
						}
						return nil
					},
				}
			},
			expectError:    true,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name:     "reconcile_add_finalizer_fails",
			setupEnv: func(t *testing.T) {},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns"},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default", // Pre-initialized to skip IsInitialized update
					},
				}
				return []client.Object{mg}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onUpdate: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
						if mgObj, ok := obj.(*mustgatherv1alpha1.MustGather); ok {
							// Fail when trying to add the finalizer (when finalizer is present in the object)
							if contains(mgObj.GetFinalizers(), mustGatherFinalizer) {
								return errors.New("failed to add finalizer")
							}
						}
						return nil
					},
				}
			},
			expectError:    true,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name: "reconcile_job_not_found_create_job_fails",
			setupEnv: func(t *testing.T) {
				t.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: operatorNs, Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default",
						UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
							Type: mustgatherv1alpha1.UploadTypeSFTP,
							SFTP: &mustgatherv1alpha1.SFTPSpec{
								Host:                           "sftp.example.com",
								CaseID:                         "12345678",
								CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "secret"},
							},
						},
					},
				}
				userSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: "secret", Namespace: operatorNs},
					Data: map[string][]byte{
						"username": []byte("testuser"),
						"password": []byte("testpass"),
						"host_key": []byte("sftp.example.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC..."),
					},
				}
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, userSecret, cv}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onCreate: func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
						// Fail job creation
						if _, ok := obj.(*batchv1.Job); ok {
							return errors.New("failed to create job")
						}
						return nil
					},
				}
			},
			expectError:    true,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name: "reconcile_job_lookup_error_non_notfound",
			setupEnv: func(t *testing.T) {
				t.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default",
						UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
							Type: mustgatherv1alpha1.UploadTypeSFTP,
							SFTP: &mustgatherv1alpha1.SFTPSpec{
								CaseID:                         "12345678",
								CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "secret"},
							},
						},
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret", Namespace: "ns"}}
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, userSecret, cv}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						// Fail the initial job lookup with a non-NotFound error
						if _, ok := obj.(*batchv1.Job); ok && key.Name == "example-mustgather" && key.Namespace == "ns" {
							return errors.New("API server error - unable to look up job")
						}
						return nil
					},
				}
			},
			expectError:    true,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name: "reconcile_job_not_found_get_secret_returns_non_not_found_error",
			setupEnv: func(t *testing.T) {
				t.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountName: "default",
						UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
							Type: mustgatherv1alpha1.UploadTypeSFTP,
							SFTP: &mustgatherv1alpha1.SFTPSpec{
								CaseID:                         "12345678",
								CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "secret"},
							},
						},
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret", Namespace: "ns"}}
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, userSecret, cv}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
						// Return non-NotFound error when getting secret
						if _, ok := obj.(*corev1.Secret); ok && key.Namespace == "ns" && key.Name == "secret" {
							return errors.New("API server unavailable - failed to get operator secret")
						}
						return nil
					},
				}
			},
			expectError:    true,
			expectResult:   reconcile.Result{Requeue: true},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup environment
			tt.setupEnv(t)

			// Setup scheme
			s := runtime.NewScheme()
			_ = corev1.AddToScheme(s)
			_ = batchv1.AddToScheme(s)
			_ = mustgatherv1alpha1.AddToScheme(s)
			_ = configv1.AddToScheme(s)

			// Setup objects and client
			objects := tt.setupObjects()
			base := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).WithStatusSubresource(&mustgatherv1alpha1.MustGather{}).Build()

			// Setup interceptor if needed
			interceptor := tt.interceptors()
			var cl client.Client = base
			if interceptor.onGet != nil || interceptor.onList != nil || interceptor.onDelete != nil || interceptor.onUpdate != nil || interceptor.onCreate != nil || interceptor.status != nil {
				interceptor.Client = base
				cl = interceptor
			}

			// Mock sftpDialFunc to avoid real network calls in tests
			// Save original and restore after test
			originalSftpDialFunc := sftpDialFunc
			defer func() { sftpDialFunc = originalSftpDialFunc }()

			// Mock SFTP dial function to always succeed
			sftpDialFunc = func(ctx context.Context, username, password, host, hostKeyData string) error {
				return nil // Mock success - allows validation to pass and test job creation logic
			}

			// Create reconciler
			r := &MustGatherReconciler{ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil)}

			// Determine request based on test objects
			var req reconcile.Request
			for _, obj := range objects {
				if mgObj, ok := obj.(*mustgatherv1alpha1.MustGather); ok {
					req = reconcile.Request{NamespacedName: types.NamespacedName{Name: mgObj.Name, Namespace: mgObj.Namespace}}
					break
				}
			}
			// Default request if no MustGather object found
			if req.Name == "" {
				req = reconcile.Request{NamespacedName: types.NamespacedName{Name: "x", Namespace: "y"}}
			}

			// Execute
			res, err := r.Reconcile(context.TODO(), req)

			// Assert error expectation
			if tt.expectError && err == nil {
				t.Fatalf("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Assert result expectation
			if res != tt.expectResult {
				t.Fatalf("expected result %+v, got %+v", tt.expectResult, res)
			}

			// Run post-test checks
			tt.postTestChecks(t, cl)
		})
	}
}

// Helper functions for tests from HEAD branch
func TestMustGatherController(t *testing.T) {
	mgObj := createMustGatherObject()
	secObj := createMustGatherSecretObject()
	t.Setenv("OPERATOR_IMAGE", "test-image")

	objs := []runtime.Object{
		mgObj,
		secObj,
	}
	cl, s := generateFakeClient(objs...)
	eventRec := &record.FakeRecorder{}
	var cfg *rest.Config

	r := MustGatherReconciler{
		ReconcilerBase: util.NewReconcilerBase(cl, s, cfg, eventRec, nil),
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      mgObj.Name,
			Namespace: mgObj.Namespace,
		},
	}

	_, err := r.Reconcile(context.TODO(), req)
	if err != nil {
		t.Fatalf("reconcile: (%v)", err)
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

			job, err := r.getJobFromInstance(context.TODO(), tt.mustGather)
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
			Name:      "example-mustgather",
			Namespace: "openshift-must-gather-operator",
		},
		Spec: mustgatherv1alpha1.MustGatherSpec{},
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
			Kind: "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "case-management-creds",
			Namespace: "openshift-must-gather-operator",
		},
	}
}

func generateFakeClient(objs ...runtime.Object) (client.Client, *runtime.Scheme) {
	s := scheme.Scheme
	s.AddKnownTypes(mustgatherv1alpha1.GroupVersion, &mustgatherv1alpha1.MustGather{})
	cl := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).WithStatusSubresource(&mustgatherv1alpha1.MustGather{}).Build()
	return cl, s
}

// TestSFTPCredentialValidation tests the credential validation logic added in the controller
func TestSFTPCredentialValidation(t *testing.T) {
	// Setup scheme
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = mustgatherv1alpha1.AddToScheme(s)
	_ = batchv1.AddToScheme(s)

	tests := []struct {
		name                   string
		secret                 *corev1.Secret
		mustgather             *mustgatherv1alpha1.MustGather
		mockSFTPDialFunc       func(ctx context.Context, username, password, host, hostKeyData string) error
		expectError            bool
		expectRequeue          bool
		expectedStatus         string
		expectedCompleted      bool
		expectedReasonContains string
		checkLastUpdate        bool
	}{
		{
			name: "missing username field in secret",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-ns",
				},
				Data: map[string][]byte{
					"password": []byte("password123"),
				},
			},
			mustgather: &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-mg",
					Namespace:  "test-ns",
					Finalizers: []string{mustGatherFinalizer},
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
						Type: mustgatherv1alpha1.UploadTypeSFTP,
						SFTP: &mustgatherv1alpha1.SFTPSpec{
							CaseID:                         "12345678",
							CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "test-secret"},
							Host:                           "sftp.example.com",
						},
					},
				},
			},
			mockSFTPDialFunc:       nil, // Won't be called
			expectError:            false,
			expectRequeue:          false,
			expectedStatus:         "Failed",
			expectedCompleted:      true,
			expectedReasonContains: "missing required field 'username'",
			checkLastUpdate:        true,
		},
		{
			name: "empty username in secret",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-ns",
				},
				Data: map[string][]byte{
					"username": []byte(""),
					"password": []byte("password123"),
				},
			},
			mustgather: &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-mg",
					Namespace:  "test-ns",
					Finalizers: []string{mustGatherFinalizer},
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
						Type: mustgatherv1alpha1.UploadTypeSFTP,
						SFTP: &mustgatherv1alpha1.SFTPSpec{
							CaseID:                         "12345678",
							CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "test-secret"},
							Host:                           "sftp.example.com",
						},
					},
				},
			},
			mockSFTPDialFunc:       nil,
			expectError:            false,
			expectRequeue:          false,
			expectedStatus:         "Failed",
			expectedCompleted:      true,
			expectedReasonContains: "missing required field 'username'",
			checkLastUpdate:        true,
		},
		{
			name: "missing password field in secret",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-ns",
				},
				Data: map[string][]byte{
					"username": []byte("testuser"),
				},
			},
			mustgather: &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-mg",
					Namespace:  "test-ns",
					Finalizers: []string{mustGatherFinalizer},
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
						Type: mustgatherv1alpha1.UploadTypeSFTP,
						SFTP: &mustgatherv1alpha1.SFTPSpec{
							CaseID:                         "12345678",
							CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "test-secret"},
							Host:                           "sftp.example.com",
						},
					},
				},
			},
			mockSFTPDialFunc:       nil,
			expectError:            false,
			expectRequeue:          false,
			expectedStatus:         "Failed",
			expectedCompleted:      true,
			expectedReasonContains: "missing required field 'password'",
			checkLastUpdate:        true,
		},
		{
			name: "empty password in secret",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-ns",
				},
				Data: map[string][]byte{
					"username": []byte("testuser"),
					"password": []byte(""),
				},
			},
			mustgather: &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-mg",
					Namespace:  "test-ns",
					Finalizers: []string{mustGatherFinalizer},
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
						Type: mustgatherv1alpha1.UploadTypeSFTP,
						SFTP: &mustgatherv1alpha1.SFTPSpec{
							CaseID:                         "12345678",
							CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "test-secret"},
							Host:                           "sftp.example.com",
						},
					},
				},
			},
			mockSFTPDialFunc:       nil,
			expectError:            false,
			expectRequeue:          false,
			expectedStatus:         "Failed",
			expectedCompleted:      true,
			expectedReasonContains: "missing required field 'password'",
			checkLastUpdate:        true,
		},
		{
			name: "SFTP connection validation fails",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-ns",
				},
				Data: map[string][]byte{
					"username": []byte("testuser"),
					"password": []byte("password123"),
				},
			},
			mustgather: &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-mg",
					Namespace:  "test-ns",
					Finalizers: []string{mustGatherFinalizer},
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
						Type: mustgatherv1alpha1.UploadTypeSFTP,
						SFTP: &mustgatherv1alpha1.SFTPSpec{
							CaseID:                         "12345678",
							CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "test-secret"},
							Host:                           "sftp.example.com",
						},
					},
				},
			},
			mockSFTPDialFunc: func(ctx context.Context, username, password, host, hostKeyData string) error {
				return errors.New("SFTP connection failed: authentication failed")
			},
			expectError:            false,
			expectRequeue:          false,
			expectedStatus:         "Failed",
			expectedCompleted:      true,
			expectedReasonContains: "SFTP validation failed",
			checkLastUpdate:        true,
		},
		{
			name: "SFTP validation transient error triggers requeue",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-ns",
				},
				Data: map[string][]byte{
					"username": []byte("testuser"),
					"password": []byte("password123"),
				},
			},
			mustgather: &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-mg",
					Namespace:  "test-ns",
					Finalizers: []string{mustGatherFinalizer},
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
						Type: mustgatherv1alpha1.UploadTypeSFTP,
						SFTP: &mustgatherv1alpha1.SFTPSpec{
							CaseID:                         "12345678",
							CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "test-secret"},
							Host:                           "sftp.example.com",
						},
					},
				},
			},
			mockSFTPDialFunc: func(ctx context.Context, username, password, host, hostKeyData string) error {
				return &TransientError{Err: errors.New("network timeout")}
			},
			expectError:     true,
			expectRequeue:   true,
			checkLastUpdate: false,
		},
		{
			name: "valid credentials and successful SFTP validation",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-ns",
				},
				Data: map[string][]byte{
					"username": []byte("testuser"),
					"password": []byte("password123"),
					"host_key": []byte("sftp.example.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ..."),
				},
			},
			mustgather: &mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-mg",
					Namespace:  "test-ns",
					Finalizers: []string{mustGatherFinalizer},
				},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
						Type: mustgatherv1alpha1.UploadTypeSFTP,
						SFTP: &mustgatherv1alpha1.SFTPSpec{
							CaseID:                         "12345678",
							CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "test-secret"},
							Host:                           "sftp.example.com",
						},
					},
				},
			},
			mockSFTPDialFunc: func(ctx context.Context, username, password, host, hostKeyData string) error {
				return nil // Success
			},
			expectError:   false,
			expectRequeue: false,
			// When validation succeeds, job creation is attempted (not tested here)
			checkLastUpdate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup environment
			t.Setenv("OPERATOR_IMAGE", "test-image")

			// Create fake client with test objects
			objects := []client.Object{tt.mustgather, tt.secret}
			cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).WithStatusSubresource(&mustgatherv1alpha1.MustGather{}).Build()

			// Create reconciler
			r := &MustGatherReconciler{
				ReconcilerBase: util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil),
			}

			// Mock the SFTP dial function if provided
			if tt.mockSFTPDialFunc != nil {
				originalDialFunc := sftpDialFunc
				sftpDialFunc = tt.mockSFTPDialFunc
				defer func() { sftpDialFunc = originalDialFunc }()
			}

			// Execute reconcile
			result, err := r.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.mustgather.Name,
					Namespace: tt.mustgather.Namespace,
				},
			})

			// Check error expectation
			if tt.expectError && err == nil {
				t.Errorf("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// Check requeue expectation
			if tt.expectRequeue && result.RequeueAfter == 0 {
				t.Errorf("expected requeue but got: %+v", result)
			}

			// Get updated MustGather to check status
			updatedMG := &mustgatherv1alpha1.MustGather{}
			if getErr := cl.Get(context.Background(), types.NamespacedName{
				Name:      tt.mustgather.Name,
				Namespace: tt.mustgather.Namespace,
			}, updatedMG); getErr != nil {
				t.Fatalf("failed to get updated MustGather: %v", getErr)
			}

			// Check status fields if expected
			if tt.expectedStatus != "" {
				if updatedMG.Status.Status != tt.expectedStatus {
					t.Errorf("expected status %q, got %q", tt.expectedStatus, updatedMG.Status.Status)
				}
			}

			if updatedMG.Status.Completed != tt.expectedCompleted {
				t.Errorf("expected completed %v, got %v", tt.expectedCompleted, updatedMG.Status.Completed)
			}

			if tt.expectedReasonContains != "" {
				if updatedMG.Status.Reason == "" {
					t.Errorf("expected reason to contain %q, but reason is empty", tt.expectedReasonContains)
				} else if !contains([]string{updatedMG.Status.Reason}, tt.expectedReasonContains) {
					// Using simple substring check
					found := false
					for _, r := range []string{updatedMG.Status.Reason} {
						if len(r) >= len(tt.expectedReasonContains) {
							for i := 0; i <= len(r)-len(tt.expectedReasonContains); i++ {
								if r[i:i+len(tt.expectedReasonContains)] == tt.expectedReasonContains {
									found = true
									break
								}
							}
						}
					}
					if !found {
						t.Errorf("expected reason to contain %q, got %q", tt.expectedReasonContains, updatedMG.Status.Reason)
					}
				}
			}

			// Check LastUpdate was set
			if tt.checkLastUpdate {
				if updatedMG.Status.LastUpdate.IsZero() {
					t.Errorf("expected LastUpdate to be set, but it was zero")
				}
			}
		})
	}
}
