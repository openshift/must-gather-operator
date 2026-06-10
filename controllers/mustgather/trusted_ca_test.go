package mustgather

import (
	"context"
	"errors"
	"strings"
	"testing"

	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	testTrustedCAConfigMap = "trusted-ca-cert"
	testOperatorNamespace  = "must-gather-operator"
	testCRNamespace        = "customer-namespace"
)

func testMustGather(name, namespace string, uid types.UID) *mustgatherv1alpha1.MustGather {
	return &mustgatherv1alpha1.MustGather{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "operator.openshift.io/v1alpha1",
			Kind:       "MustGather",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       uid,
		},
	}
}

func testTrustedCASourceConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testTrustedCAConfigMap,
			Namespace: testOperatorNamespace,
			Labels: map[string]string{
				"config.openshift.io/inject-trusted-cabundle": "true",
			},
		},
		Data: map[string]string{
			"ca-bundle.crt": "-----BEGIN CERTIFICATE-----\ntest-ca\n-----END CERTIFICATE-----",
		},
	}
}

func newTrustedCAReconciler(t *testing.T, objects []client.Object, interceptor interceptClient) *MustGatherReconciler {
	t.Helper()

	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	if err := batchv1.AddToScheme(s); err != nil {
		t.Fatalf("add batchv1 to scheme: %v", err)
	}
	if err := mustgatherv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add mustgather to scheme: %v", err)
	}

	base := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()
	cl := client.Client(base)
	if interceptor.onGet != nil || interceptor.onList != nil || interceptor.onDelete != nil ||
		interceptor.onUpdate != nil || interceptor.onCreate != nil || interceptor.status != nil {
		interceptor.Client = base
		cl = interceptor
	}

	return &MustGatherReconciler{
		ReconcilerBase:         util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil),
		TrustedCAConfigMap:     testTrustedCAConfigMap,
		OperatorNamespace:      testOperatorNamespace,
		DefaultMustGatherImage: "test-must-gather-image",
	}
}

func getTrustedCAConfigMap(t *testing.T, cl client.Client, namespace string) *corev1.ConfigMap {
	t.Helper()

	cm := &corev1.ConfigMap{}
	if err := cl.Get(context.TODO(), types.NamespacedName{
		Name:      testTrustedCAConfigMap,
		Namespace: namespace,
	}, cm); err != nil {
		t.Fatalf("get trusted CA ConfigMap in %s: %v", namespace, err)
	}
	return cm
}

func TestEnsureTrustedCAConfigMap(t *testing.T) {
	instanceUID := types.UID("mustgather-uid-1")
	otherOwnerUID := types.UID("mustgather-uid-2")

	tests := []struct {
		name           string
		instance       *mustgatherv1alpha1.MustGather
		objects        []client.Object
		interceptor    interceptClient
		expectError    bool
		errorSubstring string
		postCheck      func(t *testing.T, cl client.Client)
	}{
		{
			name:     "skips when CR namespace matches operator namespace",
			instance: testMustGather("mg-same-ns", testOperatorNamespace, instanceUID),
			objects:  []client.Object{testTrustedCASourceConfigMap()},
			postCheck: func(t *testing.T, cl client.Client) {
				cm := &corev1.ConfigMap{}
				err := cl.Get(context.TODO(), types.NamespacedName{
					Name: testTrustedCAConfigMap, Namespace: testCRNamespace,
				}, cm)
				if !apierrors.IsNotFound(err) {
					t.Fatalf("expected no copy in CR namespace, got err=%v", err)
				}
			},
		},
		{
			name:     "creates ConfigMap in CR namespace when missing",
			instance: testMustGather("mg-create", testCRNamespace, instanceUID),
			objects:  []client.Object{testTrustedCASourceConfigMap()},
			postCheck: func(t *testing.T, cl client.Client) {
				cm := getTrustedCAConfigMap(t, cl, testCRNamespace)
				if cm.Data["ca-bundle.crt"] != testTrustedCASourceConfigMap().Data["ca-bundle.crt"] {
					t.Fatalf("copied CA data mismatch")
				}
				if len(cm.OwnerReferences) != 1 || cm.OwnerReferences[0].UID != instanceUID {
					t.Fatalf("expected single owner reference for instance, got %+v", cm.OwnerReferences)
				}
				if cm.Labels["config.openshift.io/inject-trusted-cabundle"] != "true" {
					t.Fatalf("expected source labels to be copied")
				}
			},
		},
		{
			name:     "adds owner reference to existing ConfigMap",
			instance: testMustGather("mg-update", testCRNamespace, instanceUID),
			objects: []client.Object{
				testTrustedCASourceConfigMap(),
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testTrustedCAConfigMap,
						Namespace: testCRNamespace,
					},
					Data: map[string]string{"ca-bundle.crt": "existing"},
				},
			},
			postCheck: func(t *testing.T, cl client.Client) {
				cm := getTrustedCAConfigMap(t, cl, testCRNamespace)
				if len(cm.OwnerReferences) != 1 || cm.OwnerReferences[0].UID != instanceUID {
					t.Fatalf("expected owner reference to be added, got %+v", cm.OwnerReferences)
				}
			},
		},
		{
			name:     "no-op when owner reference already exists",
			instance: testMustGather("mg-exists", testCRNamespace, instanceUID),
			objects: []client.Object{
				testTrustedCASourceConfigMap(),
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testTrustedCAConfigMap,
						Namespace: testCRNamespace,
						OwnerReferences: []metav1.OwnerReference{{
							APIVersion: "operator.openshift.io/v1alpha1",
							Kind:       "MustGather",
							Name:       "mg-exists",
							UID:        instanceUID,
						}},
					},
					Data: map[string]string{"ca-bundle.crt": "existing"},
				},
			},
			postCheck: func(t *testing.T, cl client.Client) {
				cm := getTrustedCAConfigMap(t, cl, testCRNamespace)
				if len(cm.OwnerReferences) != 1 {
					t.Fatalf("expected owner references to remain unchanged, got %+v", cm.OwnerReferences)
				}
			},
		},
		{
			name:     "appends owner reference when other owners exist",
			instance: testMustGather("mg-shared", testCRNamespace, instanceUID),
			objects: []client.Object{
				testTrustedCASourceConfigMap(),
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testTrustedCAConfigMap,
						Namespace: testCRNamespace,
						OwnerReferences: []metav1.OwnerReference{{
							APIVersion: "operator.openshift.io/v1alpha1",
							Kind:       "MustGather",
							Name:       "other-mg",
							UID:        otherOwnerUID,
						}},
					},
					Data: map[string]string{"ca-bundle.crt": "shared"},
				},
			},
			postCheck: func(t *testing.T, cl client.Client) {
				cm := getTrustedCAConfigMap(t, cl, testCRNamespace)
				if len(cm.OwnerReferences) != 2 {
					t.Fatalf("expected two owner references, got %+v", cm.OwnerReferences)
				}
			},
		},
		{
			name:     "emits warning and returns nil when source ConfigMap is missing",
			instance: testMustGather("mg-no-source", testCRNamespace, instanceUID),
			objects:  nil,
		},
		{
			name:     "returns error when checking existing ConfigMap fails",
			instance: testMustGather("mg-get-fail", testCRNamespace, instanceUID),
			objects:  []client.Object{testTrustedCASourceConfigMap()},
			interceptor: interceptClient{
				onGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
					if _, ok := obj.(*corev1.ConfigMap); ok &&
						key.Namespace == testCRNamespace && key.Name == testTrustedCAConfigMap {
						return errors.New("forced get error")
					}
					return nil
				},
			},
			expectError:    true,
			errorSubstring: "failed to check for existing ConfigMap",
		},
		{
			name:     "returns error when create fails",
			instance: testMustGather("mg-create-fail", testCRNamespace, instanceUID),
			objects:  []client.Object{testTrustedCASourceConfigMap()},
			interceptor: interceptClient{
				onCreate: func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
					if cm, ok := obj.(*corev1.ConfigMap); ok && cm.Namespace == testCRNamespace {
						return errors.New("forced create error")
					}
					return nil
				},
			},
			expectError:    true,
			errorSubstring: "failed to create trustedCA ConfigMap",
		},
		{
			name:     "returns error when update owner reference fails",
			instance: testMustGather("mg-update-fail", testCRNamespace, instanceUID),
			objects: []client.Object{
				testTrustedCASourceConfigMap(),
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testTrustedCAConfigMap,
						Namespace: testCRNamespace,
					},
				},
			},
			interceptor: interceptClient{
				onUpdate: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
					if cm, ok := obj.(*corev1.ConfigMap); ok && cm.Namespace == testCRNamespace {
						return errors.New("forced update error")
					}
					return nil
				},
			},
			expectError:    true,
			errorSubstring: "failed to update ownerReferences",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTrustedCAReconciler(t, tt.objects, tt.interceptor)
			err := r.ensureTrustedCAConfigMap(context.TODO(), logf.Log, tt.instance)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error but got none")
				}
				if tt.errorSubstring != "" && !strings.Contains(err.Error(), tt.errorSubstring) {
					t.Fatalf("expected error containing %q, got: %v", tt.errorSubstring, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.postCheck != nil {
				tt.postCheck(t, r.GetClient())
			}
		})
	}
}

func TestCleanupTrustedCAConfigMap(t *testing.T) {
	instanceUID := types.UID("mustgather-uid-cleanup")
	otherOwnerUID := types.UID("mustgather-uid-other")

	tests := []struct {
		name           string
		instance       *mustgatherv1alpha1.MustGather
		objects        []client.Object
		interceptor    interceptClient
		expectError    bool
		errorSubstring string
		postCheck      func(t *testing.T, cl client.Client)
	}{
		{
			name:     "skips when CR namespace matches operator namespace",
			instance: testMustGather("mg-same-ns", testOperatorNamespace, instanceUID),
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testTrustedCAConfigMap,
						Namespace: testOperatorNamespace,
						OwnerReferences: []metav1.OwnerReference{{
							UID: instanceUID,
						}},
					},
				},
			},
			postCheck: func(t *testing.T, cl client.Client) {
				getTrustedCAConfigMap(t, cl, testOperatorNamespace)
			},
		},
		{
			name:     "no-op when ConfigMap is not found",
			instance: testMustGather("mg-missing-cm", testCRNamespace, instanceUID),
			postCheck: func(t *testing.T, cl client.Client) {
				cm := &corev1.ConfigMap{}
				if err := cl.Get(context.TODO(), types.NamespacedName{
					Name: testTrustedCAConfigMap, Namespace: testCRNamespace,
				}, cm); !apierrors.IsNotFound(err) {
					t.Fatalf("expected ConfigMap to remain absent, got err=%v", err)
				}
			},
		},
		{
			name:     "deletes ConfigMap when instance is sole owner",
			instance: testMustGather("mg-delete", testCRNamespace, instanceUID),
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testTrustedCAConfigMap,
						Namespace: testCRNamespace,
						OwnerReferences: []metav1.OwnerReference{{
							UID: instanceUID,
						}},
					},
				},
			},
			postCheck: func(t *testing.T, cl client.Client) {
				cm := &corev1.ConfigMap{}
				if err := cl.Get(context.TODO(), types.NamespacedName{
					Name: testTrustedCAConfigMap, Namespace: testCRNamespace,
				}, cm); !apierrors.IsNotFound(err) {
					t.Fatalf("expected ConfigMap to be deleted, got err=%v", err)
				}
			},
		},
		{
			name:     "removes only this instance owner reference when others remain",
			instance: testMustGather("mg-shared-cleanup", testCRNamespace, instanceUID),
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testTrustedCAConfigMap,
						Namespace: testCRNamespace,
						OwnerReferences: []metav1.OwnerReference{
							{UID: instanceUID},
							{UID: otherOwnerUID},
						},
					},
				},
			},
			postCheck: func(t *testing.T, cl client.Client) {
				cm := getTrustedCAConfigMap(t, cl, testCRNamespace)
				if len(cm.OwnerReferences) != 1 || cm.OwnerReferences[0].UID != otherOwnerUID {
					t.Fatalf("expected remaining owner %s, got %+v", otherOwnerUID, cm.OwnerReferences)
				}
			},
		},
		{
			name:     "returns error when get ConfigMap fails",
			instance: testMustGather("mg-get-fail", testCRNamespace, instanceUID),
			interceptor: interceptClient{
				onGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
					if _, ok := obj.(*corev1.ConfigMap); ok &&
						key.Namespace == testCRNamespace && key.Name == testTrustedCAConfigMap {
						return errors.New("forced get error")
					}
					return nil
				},
			},
			expectError:    true,
			errorSubstring: "failed to get trustedCA ConfigMap",
		},
		{
			name:     "returns error when delete fails",
			instance: testMustGather("mg-delete-fail", testCRNamespace, instanceUID),
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testTrustedCAConfigMap,
						Namespace: testCRNamespace,
						OwnerReferences: []metav1.OwnerReference{{
							UID: instanceUID,
						}},
					},
				},
			},
			interceptor: interceptClient{
				onDelete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
					if cm, ok := obj.(*corev1.ConfigMap); ok && cm.Namespace == testCRNamespace {
						return errors.New("forced delete error")
					}
					return nil
				},
			},
			expectError:    true,
			errorSubstring: "failed to delete trustedCA ConfigMap",
		},
		{
			name:     "returns error when update owner references fails",
			instance: testMustGather("mg-update-fail", testCRNamespace, instanceUID),
			objects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testTrustedCAConfigMap,
						Namespace: testCRNamespace,
						OwnerReferences: []metav1.OwnerReference{
							{UID: instanceUID},
							{UID: otherOwnerUID},
						},
					},
				},
			},
			interceptor: interceptClient{
				onUpdate: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
					if cm, ok := obj.(*corev1.ConfigMap); ok && cm.Namespace == testCRNamespace {
						return errors.New("forced update error")
					}
					return nil
				},
			},
			expectError:    true,
			errorSubstring: "failed to update trustedCA ConfigMap owner references",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newTrustedCAReconciler(t, tt.objects, tt.interceptor)
			err := r.cleanupTrustedCAConfigMap(context.TODO(), logf.Log, tt.instance)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error but got none")
				}
				if tt.errorSubstring != "" && !strings.Contains(err.Error(), tt.errorSubstring) {
					t.Fatalf("expected error containing %q, got: %v", tt.errorSubstring, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.postCheck != nil {
				tt.postCheck(t, r.GetClient())
			}
		})
	}
}

func TestCleanupMustGatherResources_TrustedCA(t *testing.T) {
	instanceUID := types.UID("mustgather-uid-cleanup-resources")
	mg := testMustGather("example-mustgather", testCRNamespace, instanceUID)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: mg.Name, Namespace: testCRNamespace, UID: "job-uid",
			OwnerReferences: []metav1.OwnerReference{{
				UID: instanceUID,
			}},
		},
	}
	trustedCA := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testTrustedCAConfigMap,
			Namespace: testCRNamespace,
			OwnerReferences: []metav1.OwnerReference{{
				UID: instanceUID,
			}},
		},
	}

	r := newTrustedCAReconciler(t, []client.Object{mg, job, trustedCA}, interceptClient{})
	err := r.cleanupMustGatherResources(context.TODO(), logf.Log, mg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cm := &corev1.ConfigMap{}
	if getErr := r.GetClient().Get(context.TODO(), types.NamespacedName{
		Name: testTrustedCAConfigMap, Namespace: testCRNamespace,
	}, cm); !apierrors.IsNotFound(getErr) {
		t.Fatalf("expected trusted CA ConfigMap to be deleted during cleanup, got err=%v", getErr)
	}
}

func TestCleanupMustGatherResources_TrustedCAConfigMapCleanupError(t *testing.T) {
	instanceUID := types.UID("mustgather-uid-cleanup-fail")
	mg := testMustGather("mg-trusted-ca-fail", testCRNamespace, instanceUID)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: mg.Name, Namespace: testCRNamespace, UID: "job-uid",
			OwnerReferences: []metav1.OwnerReference{{
				UID: instanceUID,
			}},
		},
	}
	trustedCA := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testTrustedCAConfigMap,
			Namespace: testCRNamespace,
			OwnerReferences: []metav1.OwnerReference{{
				UID: instanceUID,
			}},
		},
	}

	interceptor := interceptClient{
		onDelete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
			if cm, ok := obj.(*corev1.ConfigMap); ok &&
				cm.Namespace == testCRNamespace && cm.Name == testTrustedCAConfigMap {
				return errors.New("forced trusted CA delete error")
			}
			return nil
		},
	}

	r := newTrustedCAReconciler(t, []client.Object{mg, job, trustedCA}, interceptor)
	err := r.cleanupMustGatherResources(context.TODO(), logf.Log, mg)
	if err == nil {
		t.Fatal("expected error when trusted CA ConfigMap cleanup fails")
	}
	if !strings.Contains(err.Error(), "failed to delete trustedCA ConfigMap") {
		t.Fatalf("expected error from cleanupTrustedCAConfigMap, got: %v", err)
	}

	// Job cleanup should have completed before trusted CA cleanup failed.
	chkJob := &batchv1.Job{}
	if getErr := r.GetClient().Get(context.TODO(), types.NamespacedName{
		Name: mg.Name, Namespace: testCRNamespace,
	}, chkJob); getErr == nil {
		t.Fatal("expected job to be deleted before trusted CA cleanup error")
	}

	// ConfigMap should remain because delete failed.
	getTrustedCAConfigMap(t, r.GetClient(), testCRNamespace)
}
