package mustgather

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	"github.com/openshift/must-gather-operator/pkg/helpers"
	"github.com/redhat-cop/operator-utils/pkg/util"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
	operatorNs := "must-gather-operator"

	tests := []struct {
		name           string
		setupObjects   func() []client.Object
		interceptors   func() interceptClient
		expectError    bool
		postTestChecks func(t *testing.T, cl client.Client)
	}{
		{
			name: "cleanup_success_secret_in_same_namespace_not_deleted",
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: operatorNs},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "case-management-creds"},
					},
				}
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "case-management-creds", Namespace: operatorNs}}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: operatorNs, UID: "user-123"}}
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: operatorNs, Labels: map[string]string{"controller-uid": string(job.UID)}}}
				return []client.Object{mg, secret, job, pod}
			},
			interceptors: func() interceptClient { return interceptClient{} },
			expectError:  false,
			postTestChecks: func(t *testing.T, cl client.Client) {
				// Verify secret is NOT deleted (same namespace as CR)
				chkSecret := &corev1.Secret{}
				if getErr := cl.Get(context.TODO(), types.NamespacedName{Namespace: operatorNs, Name: "case-management-creds"}, chkSecret); getErr != nil {
					t.Fatalf("expected secret to remain (same namespace), got error: %v", getErr)
				}
				// Verify job is deleted
				chkJob := &batchv1.Job{}
				if getErr := cl.Get(context.TODO(), types.NamespacedName{Namespace: operatorNs, Name: "example-mustgather"}, chkJob); getErr == nil {
					t.Fatalf("expected job to be deleted")
				}
			},
		},
		{
			name: "cleanup_success_copied_secret_deleted",
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "user-namespace"},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "case-management-creds"},
					},
				}
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "case-management-creds", Namespace: operatorNs}}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: operatorNs, UID: "user-123"}}
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: operatorNs, Labels: map[string]string{"controller-uid": string(job.UID)}}}
				return []client.Object{mg, secret, job, pod}
			},
			interceptors: func() interceptClient { return interceptClient{} },
			expectError:  false,
			postTestChecks: func(t *testing.T, cl client.Client) {
				// Verify copied secret is deleted
				chkSecret := &corev1.Secret{}
				if getErr := cl.Get(context.TODO(), types.NamespacedName{Namespace: operatorNs, Name: "case-management-creds"}, chkSecret); getErr == nil {
					t.Fatalf("expected copied secret to be deleted")
				}
				// Verify job is deleted
				chkJob := &batchv1.Job{}
				if getErr := cl.Get(context.TODO(), types.NamespacedName{Namespace: operatorNs, Name: "example-mustgather"}, chkJob); getErr == nil {
					t.Fatalf("expected job to be deleted")
				}
			},
		},
		{
			name: "cleanup_job_get_error_continues_successfully",
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"},
					},
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
					ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"},
					},
				}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: operatorNs, UID: "u"}}
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
				if e := cl.Get(context.TODO(), types.NamespacedName{Namespace: operatorNs, Name: "mg"}, chk); e != nil {
					t.Fatalf("expected job to remain after failed cleanup, get err: %v", e)
				}
			},
		},
		{
			name: "cleanup_pod_delete_error_returns_error",
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: operatorNs},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"},
					},
				}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: mg.Name, Namespace: operatorNs, UID: "u"}}
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: operatorNs, Labels: map[string]string{"controller-uid": string(job.UID)}}}
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
			err := r.cleanupMustGatherResources(context.TODO(), logf.Log, mg, operatorNs)

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
			name:     "reconcile_initialize_mustgather_update_succeeds",
			setupEnv: func(t *testing.T) {},
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
			name:     "reconcile_deletion_cleanup_and_finalizer_removal_success",
			setupEnv: func(t *testing.T) {},
			setupObjects: func() []client.Object {
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: operatorNs}}
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{
						Name: "example-mustgather", Namespace: operatorNs,
						Finalizers:        []string{mustGatherFinalizer},
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
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
				if helpers.Contains(out.GetFinalizers(), mustGatherFinalizer) {
					t.Fatalf("expected finalizer removed")
				}
			},
		},
		{
			name: "reconcile_deletion_cleanup_resources_returns_error",
			setupEnv: func(t *testing.T) {
				t.Setenv("OSDK_FORCE_RUN_MODE", "local")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{
						Name: "example-mustgather", Namespace: "user-ns",
						Finalizers:        []string{mustGatherFinalizer},
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
					},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "s"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
					},
				}
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: operatorNs}}
				return []client.Object{mg, secret}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onDelete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
						if _, ok := obj.(*corev1.Secret); ok {
							return errors.New("failed to delete secret")
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
			name: "reconcile_job_template_env_missing_returns_error",
			setupEnv: func(t *testing.T) {
				osUnset := os.Getenv("OPERATOR_IMAGE")
				_ = os.Unsetenv("OPERATOR_IMAGE")
				t.Cleanup(func() { os.Setenv("OPERATOR_IMAGE", osUnset) })
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns"},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountRef: corev1.LocalObjectReference{Name: "default"},
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
			name: "reconcile_job_template_cluster_version_missing_returns_error",
			setupEnv: func(t *testing.T) {
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns"},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						ServiceAccountRef: corev1.LocalObjectReference{Name: "default"},
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
			name: "reconcile_job_not_found_creates_job_successfully",
			setupEnv: func(t *testing.T) {
				t.Setenv("OSDK_FORCE_RUN_MODE", "local")
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "secret"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
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
			interceptors: func() interceptClient { return interceptClient{} },
			expectError:  false,
			expectResult: reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {
				// Verify secret was created in operator namespace without owner reference (retainResourcesOnCompletion=false)
				operatorSecret := &corev1.Secret{}
				if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: defaultMustGatherNamespace, Name: "secret"}, operatorSecret); err != nil {
					t.Fatalf("expected secret to be created in operator namespace, got error: %v", err)
				}
				if len(operatorSecret.OwnerReferences) > 0 {
					t.Fatalf("expected no owner reference on copied secret (retainResourcesOnCompletion=false)")
				}

				// Verify job was created
				job := &batchv1.Job{}
				if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: defaultMustGatherNamespace, Name: "example-mustgather"}, job); err != nil {
					t.Fatalf("expected job to be created, got error: %v", err)
				}
			},
		},
		{
			name: "reconcile_job_not_found_creates_secret_with_owner_reference_when_retain_resources",
			setupEnv: func(t *testing.T) {
				t.Setenv("OSDK_FORCE_RUN_MODE", "local")
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "example-mustgather",
						Namespace:  "user-ns",
						Finalizers: []string{mustGatherFinalizer},
						UID:        "mg-uid-123",
					},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "secret"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
						RetainResourcesOnCompletion:    true,
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret", Namespace: "user-ns"}}
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, userSecret, cv}
			},
			interceptors: func() interceptClient { return interceptClient{} },
			expectError:  false,
			expectResult: reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {
				// Verify secret was created in operator namespace with owner reference
				operatorSecret := &corev1.Secret{}
				if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: defaultMustGatherNamespace, Name: "secret"}, operatorSecret); err != nil {
					t.Fatalf("expected secret to be created in operator namespace, got error: %v", err)
				}
				if len(operatorSecret.OwnerReferences) != 1 {
					t.Fatalf("expected one owner reference on copied secret, got %d", len(operatorSecret.OwnerReferences))
				}
				if operatorSecret.OwnerReferences[0].UID != "mg-uid-123" {
					t.Fatalf("expected owner reference UID to be mg-uid-123, got %s", operatorSecret.OwnerReferences[0].UID)
				}

				// Verify job was created
				job := &batchv1.Job{}
				if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: defaultMustGatherNamespace, Name: "example-mustgather"}, job); err != nil {
					t.Fatalf("expected job to be created, got error: %v", err)
				}
			},
		},
		{
			name: "reconcile_secret_in_same_namespace_no_copy_needed",
			setupEnv: func(t *testing.T) {
				t.Setenv("OSDK_FORCE_RUN_MODE", "local")
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "example-mustgather",
						Namespace:  defaultMustGatherNamespace,
						Finalizers: []string{mustGatherFinalizer},
					},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "secret"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret", Namespace: defaultMustGatherNamespace}}
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, userSecret, cv}
			},
			interceptors: func() interceptClient { return interceptClient{} },
			expectError:  false,
			expectResult: reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {
				// Verify secret exists and has no owner reference
				secret := &corev1.Secret{}
				if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: defaultMustGatherNamespace, Name: "secret"}, secret); err != nil {
					t.Fatalf("expected secret to exist, got error: %v", err)
				}
				if len(secret.OwnerReferences) > 0 {
					t.Fatalf("expected no owner reference on secret in same namespace as CR")
				}

				// Verify job was created
				job := &batchv1.Job{}
				if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: defaultMustGatherNamespace, Name: "example-mustgather"}, job); err != nil {
					t.Fatalf("expected job to be created, got error: %v", err)
				}
			},
		},
		{
			name: "reconcile_job_not_found_user_secret_missing_error",
			setupEnv: func(t *testing.T) {
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
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
			interceptors:   func() interceptClient { return interceptClient{} },
			expectError:    true,
			expectResult:   reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {},
		},
		{
			name: "reconcile_job_active_updates_status_running",
			setupEnv: func(t *testing.T) {
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
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
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: operatorNs, Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
						InternalUser:                   true,
						RetainResourcesOnCompletion:    true,
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
			name: "reconcile_job_succeeded_cleanup_error_returns_error",
			setupEnv: func(t *testing.T) {
				t.Setenv("OSDK_FORCE_RUN_MODE", "local")
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "user-ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: "user-ns"}}
				operatorSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: defaultMustGatherNamespace}}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "user-ns"}}
				job.Status.Succeeded = 1
				// Job also needs to be in operator namespace for cleanup to find it
				operatorJob := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: defaultMustGatherNamespace, UID: "job-123"}}
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, userSecret, operatorSecret, cv, job, operatorJob}
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
			name: "reconcile_job_failed_cleanup_error_returns_error",
			setupEnv: func(t *testing.T) {
				t.Setenv("OSDK_FORCE_RUN_MODE", "local")
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "user-ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: "user-ns"}}
				operatorSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sec", Namespace: defaultMustGatherNamespace}}
				job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "user-ns"}}
				job.Status.Failed = 1
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, userSecret, operatorSecret, cv, job}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onDelete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
						if _, ok := obj.(*corev1.Secret); ok {
							return errors.New("failed to delete secret")
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
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
						RetainResourcesOnCompletion:    true,
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
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: operatorNs, Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
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
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: operatorNs, Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "sec"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
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
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "secret"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
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
							if updateCount > 0 && !helpers.Contains(mgObj.GetFinalizers(), mustGatherFinalizer) {
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
						ServiceAccountRef: corev1.LocalObjectReference{Name: "default"}, // Pre-initialized to skip IsInitialized update
					},
				}
				return []client.Object{mg}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onUpdate: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
						if mgObj, ok := obj.(*mustgatherv1alpha1.MustGather); ok {
							// Fail when trying to add the finalizer (when finalizer is present in the object)
							if helpers.Contains(mgObj.GetFinalizers(), mustGatherFinalizer) {
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
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: operatorNs, Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "secret"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret", Namespace: operatorNs}}
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
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "secret"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
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
			name: "reconcile_job_not_found_get_secret_returns_non_not_found_error_in_operator_ns",
			setupEnv: func(t *testing.T) {
				t.Setenv("OSDK_FORCE_RUN_MODE", "local")
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "secret"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
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
						// Return non-NotFound error when getting secret in operator namespace
						if _, ok := obj.(*corev1.Secret); ok && key.Namespace == defaultMustGatherNamespace && key.Name == "secret" {
							return errors.New("API server unavailable - failed to get operator secret")
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
			name: "reconcile_job_not_found_operator_secret_create_fails",
			setupEnv: func(t *testing.T) {
				t.Setenv("OSDK_FORCE_RUN_MODE", "local")
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{Name: "example-mustgather", Namespace: "ns", Finalizers: []string{mustGatherFinalizer}},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "secret"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
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
					onCreate: func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
						// Fail secret creation
						if _, ok := obj.(*corev1.Secret); ok {
							return errors.New("failed to create secret")
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
			name: "reconcile_existing_secret_adds_owner_reference_when_retain_resources_true",
			setupEnv: func(t *testing.T) {
				t.Setenv("OSDK_FORCE_RUN_MODE", "local")
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "example-mustgather",
						Namespace:  "user-ns",
						Finalizers: []string{mustGatherFinalizer},
						UID:        "mg-uid-456",
					},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "existing-secret"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
						RetainResourcesOnCompletion:    true,
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "existing-secret", Namespace: "user-ns"}}
				// Secret already exists in operator namespace without owner reference
				operatorSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "existing-secret", Namespace: defaultMustGatherNamespace}}
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, userSecret, operatorSecret, cv}
			},
			interceptors: func() interceptClient { return interceptClient{} },
			expectError:  false,
			expectResult: reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {
				// Verify owner reference was added to existing secret
				operatorSecret := &corev1.Secret{}
				if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: defaultMustGatherNamespace, Name: "existing-secret"}, operatorSecret); err != nil {
					t.Fatalf("expected secret to exist in operator namespace, got error: %v", err)
				}
				if len(operatorSecret.OwnerReferences) != 1 {
					t.Fatalf("expected one owner reference on existing secret, got %d", len(operatorSecret.OwnerReferences))
				}
				if operatorSecret.OwnerReferences[0].UID != "mg-uid-456" {
					t.Fatalf("expected owner reference UID to be mg-uid-456, got %s", operatorSecret.OwnerReferences[0].UID)
				}
			},
		},
		{
			name: "reconcile_existing_secret_skips_owner_reference_when_already_exists",
			setupEnv: func(t *testing.T) {
				t.Setenv("OSDK_FORCE_RUN_MODE", "local")
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "example-mustgather",
						Namespace:  "user-ns",
						Finalizers: []string{mustGatherFinalizer},
						UID:        "mg-uid-789",
					},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "existing-secret"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
						RetainResourcesOnCompletion:    true,
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "existing-secret", Namespace: "user-ns"}}
				// Secret already exists with owner reference
				operatorSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-secret",
						Namespace: defaultMustGatherNamespace,
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "managed.openshift.io/v1alpha1",
								Kind:       "MustGather",
								Name:       "example-mustgather",
								UID:        "mg-uid-789",
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
				return []client.Object{mg, userSecret, operatorSecret, cv}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onUpdate: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
						// Fail if trying to update secret (should not happen)
						if secret, ok := obj.(*corev1.Secret); ok && secret.Name == "existing-secret" {
							return errors.New("unexpected update to secret that already has owner reference")
						}
						return nil
					},
				}
			},
			expectError:  false,
			expectResult: reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {
				// Verify owner reference still exists (no duplicate added)
				operatorSecret := &corev1.Secret{}
				if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: defaultMustGatherNamespace, Name: "existing-secret"}, operatorSecret); err != nil {
					t.Fatalf("expected secret to exist in operator namespace, got error: %v", err)
				}
				if len(operatorSecret.OwnerReferences) != 1 {
					t.Fatalf("expected exactly one owner reference, got %d", len(operatorSecret.OwnerReferences))
				}
			},
		},
		{
			name: "reconcile_existing_secret_skips_owner_reference_when_retain_resources_false",
			setupEnv: func(t *testing.T) {
				t.Setenv("OSDK_FORCE_RUN_MODE", "local")
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "example-mustgather",
						Namespace:  "user-ns",
						Finalizers: []string{mustGatherFinalizer},
						UID:        "mg-uid-101",
					},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "existing-secret"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
						RetainResourcesOnCompletion:    false,
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "existing-secret", Namespace: "user-ns"}}
				// Secret already exists without owner reference
				operatorSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "existing-secret", Namespace: defaultMustGatherNamespace}}
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, userSecret, operatorSecret, cv}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onUpdate: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
						// Fail if trying to update secret (should not happen when retainResourcesOnCompletion is false)
						if secret, ok := obj.(*corev1.Secret); ok && secret.Name == "existing-secret" {
							return errors.New("unexpected update to secret when retainResourcesOnCompletion is false")
						}
						return nil
					},
				}
			},
			expectError:  false,
			expectResult: reconcile.Result{},
			postTestChecks: func(t *testing.T, cl client.Client) {
				// Verify no owner reference was added
				operatorSecret := &corev1.Secret{}
				if err := cl.Get(context.TODO(), types.NamespacedName{Namespace: defaultMustGatherNamespace, Name: "existing-secret"}, operatorSecret); err != nil {
					t.Fatalf("expected secret to exist in operator namespace, got error: %v", err)
				}
				if len(operatorSecret.OwnerReferences) != 0 {
					t.Fatalf("expected no owner references when retainResourcesOnCompletion is false, got %d", len(operatorSecret.OwnerReferences))
				}
			},
		},
		{
			name: "reconcile_existing_secret_owner_reference_update_fails",
			setupEnv: func(t *testing.T) {
				t.Setenv("OSDK_FORCE_RUN_MODE", "local")
				os.Setenv("OPERATOR_IMAGE", "img")
			},
			setupObjects: func() []client.Object {
				mg := &mustgatherv1alpha1.MustGather{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "example-mustgather",
						Namespace:  "user-ns",
						Finalizers: []string{mustGatherFinalizer},
						UID:        "mg-uid-202",
					},
					Spec: mustgatherv1alpha1.MustGatherSpec{
						CaseManagementAccountSecretRef: corev1.LocalObjectReference{Name: "existing-secret"},
						ServiceAccountRef:              corev1.LocalObjectReference{Name: "default"},
						RetainResourcesOnCompletion:    true,
					},
				}
				userSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "existing-secret", Namespace: "user-ns"}}
				// Secret already exists without owner reference
				operatorSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "existing-secret", Namespace: defaultMustGatherNamespace}}
				cv := &configv1.ClusterVersion{
					ObjectMeta: metav1.ObjectMeta{Name: "version"},
					Status: configv1.ClusterVersionStatus{
						History: []configv1.UpdateHistory{{State: "Completed", Version: "1.2.3"}},
					},
				}
				return []client.Object{mg, userSecret, operatorSecret, cv}
			},
			interceptors: func() interceptClient {
				return interceptClient{
					onUpdate: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
						// Fail when trying to update secret with owner reference
						if secret, ok := obj.(*corev1.Secret); ok && secret.Name == "existing-secret" {
							return errors.New("failed to update secret with owner reference")
						}
						return nil
					},
				}
			},
			expectError:    true,
			expectResult:   reconcile.Result{},
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
