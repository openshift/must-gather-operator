package mustgather

import (
	"context"
	"errors"
	"strings"
	"testing"

	imagev1 "github.com/openshift/api/image/v1"
	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	testDefaultMustGatherImage = "quay.io/openshift/origin-must-gather:latest"
	testCustomMustGatherImage  = "registry.example.com/custom-must-gather:v1.0"
	testImageStreamName        = "custom-must-gather"
	testImageStreamTag         = "latest"
)

func newImageTestReconciler(t *testing.T, objects []client.Object, interceptor interceptClient) *MustGatherReconciler {
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
	if err := imagev1.Install(s); err != nil {
		t.Fatalf("add imagev1 to scheme: %v", err)
	}

	base := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objects...).
		WithStatusSubresource(&mustgatherv1alpha1.MustGather{}).
		Build()
	cl := client.Client(base)
	if interceptor.onGet != nil || interceptor.onList != nil || interceptor.onDelete != nil ||
		interceptor.onUpdate != nil || interceptor.onCreate != nil || interceptor.status != nil {
		interceptor.Client = base
		cl = interceptor
	}

	return &MustGatherReconciler{
		ReconcilerBase:         util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil),
		DefaultMustGatherImage: testDefaultMustGatherImage,
		OperatorNamespace:      testOperatorNamespace,
	}
}

func mustGatherWithImageStreamRef(name, namespace string, ref *mustgatherv1alpha1.ImageStreamTagRef) *mustgatherv1alpha1.MustGather {
	return &mustgatherv1alpha1.MustGather{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: mustgatherv1alpha1.MustGatherSpec{
			ImageStreamRef: ref,
		},
	}
}

func imageStreamWithTag(tag, dockerImageRef string) *imagev1.ImageStream {
	is := &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testImageStreamName,
			Namespace: testOperatorNamespace,
		},
	}
	if tag != "" {
		tagEvent := imagev1.TagEvent{DockerImageReference: dockerImageRef}
		is.Status.Tags = []imagev1.NamedTagEventList{{
			Tag:   tag,
			Items: []imagev1.TagEvent{tagEvent},
		}}
	}
	return is
}

func TestGetMustGatherImage(t *testing.T) {
	imageStreamRef := &mustgatherv1alpha1.ImageStreamTagRef{
		Name: testImageStreamName,
		Tag:  testImageStreamTag,
	}

	tests := []struct {
		name           string
		instance       *mustgatherv1alpha1.MustGather
		objects        []client.Object
		interceptor    interceptClient
		expectError    bool
		errorSubstring string
		expectedImage  string
	}{
		{
			name:          "returns default image when ImageStreamRef is nil",
			instance:      mustGatherWithImageStreamRef("mg-default", testCRNamespace, nil),
			expectedImage: testDefaultMustGatherImage,
		},
		{
			name:     "returns docker image reference from matching ImageStream tag",
			instance: mustGatherWithImageStreamRef("mg-custom", testCRNamespace, imageStreamRef),
			objects: []client.Object{
				imageStreamWithTag(testImageStreamTag, testCustomMustGatherImage),
			},
			expectedImage: testCustomMustGatherImage,
		},
		{
			name:           "returns error when ImageStream is not found",
			instance:       mustGatherWithImageStreamRef("mg-missing-is", testCRNamespace, imageStreamRef),
			objects:        nil,
			expectError:    true,
			errorSubstring: "failed to get imagestream " + testImageStreamName,
		},
		{
			name:     "returns error when ImageStream get fails",
			instance: mustGatherWithImageStreamRef("mg-get-fail", testCRNamespace, imageStreamRef),
			objects: []client.Object{
				imageStreamWithTag(testImageStreamTag, testCustomMustGatherImage),
			},
			interceptor: interceptClient{
				onGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
					if _, ok := obj.(*imagev1.ImageStream); ok && key.Name == testImageStreamName {
						return errors.New("forced imagestream get error")
					}
					return nil
				},
			},
			expectError:    true,
			errorSubstring: "failed to get imagestream",
		},
		{
			name:     "returns error when tag is not found in ImageStream",
			instance: mustGatherWithImageStreamRef("mg-missing-tag", testCRNamespace, imageStreamRef),
			objects: []client.Object{
				imageStreamWithTag("other-tag", testCustomMustGatherImage),
			},
			expectError:    true,
			errorSubstring: "imagestream tag " + testImageStreamTag + " not found",
		},
		{
			name:     "returns error when tag has no items",
			instance: mustGatherWithImageStreamRef("mg-empty-items", testCRNamespace, imageStreamRef),
			objects: []client.Object{
				&imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testImageStreamName,
						Namespace: testOperatorNamespace,
					},
					Status: imagev1.ImageStreamStatus{
						Tags: []imagev1.NamedTagEventList{{
							Tag:   testImageStreamTag,
							Items: nil,
						}},
					},
				},
			},
			expectError:    true,
			errorSubstring: "is not pullable",
		},
		{
			name:     "returns error when tag has empty docker image reference",
			instance: mustGatherWithImageStreamRef("mg-empty-ref", testCRNamespace, imageStreamRef),
			objects: []client.Object{
				imageStreamWithTag(testImageStreamTag, ""),
			},
			expectError:    true,
			errorSubstring: "is not pullable",
		},
		{
			name:     "uses first tag item when multiple items exist",
			instance: mustGatherWithImageStreamRef("mg-multi-items", testCRNamespace, imageStreamRef),
			objects: []client.Object{
				&imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name:      testImageStreamName,
						Namespace: testOperatorNamespace,
					},
					Status: imagev1.ImageStreamStatus{
						Tags: []imagev1.NamedTagEventList{{
							Tag: testImageStreamTag,
							Items: []imagev1.TagEvent{
								{DockerImageReference: testCustomMustGatherImage},
								{DockerImageReference: "registry.example.com/older:old"},
							},
						}},
					},
				},
			},
			expectedImage: testCustomMustGatherImage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newImageTestReconciler(t, tt.objects, tt.interceptor)
			image, err := r.getMustGatherImage(context.TODO(), tt.instance)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error but got none")
				}
				if tt.errorSubstring != "" && !strings.Contains(err.Error(), tt.errorSubstring) {
					t.Fatalf("expected error containing %q, got: %v", tt.errorSubstring, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if image != tt.expectedImage {
				t.Fatalf("expected image %q, got %q", tt.expectedImage, image)
			}
		})
	}
}

func Test_getJobFromInstance_ImageStreamValidationErrors(t *testing.T) {
	imageStreamRef := &mustgatherv1alpha1.ImageStreamTagRef{
		Name: testImageStreamName,
		Tag:  testImageStreamTag,
	}
	instance := mustGatherWithImageStreamRef("mg-image-err", testCRNamespace, imageStreamRef)

	t.Run("returns original image error when validation status update succeeds", func(t *testing.T) {
		t.Setenv("OPERATOR_IMAGE", "quay.io/operator:latest")

		r := newImageTestReconciler(t, []client.Object{instance}, interceptClient{})
		_, err := r.getJobFromInstance(context.TODO(), logf.Log, instance)
		if err == nil {
			t.Fatal("expected error but got none")
		}
		if strings.Contains(err.Error(), "failed to set validation failure status") {
			t.Fatalf("expected original image error only, got: %v", err)
		}
		if !strings.Contains(err.Error(), "failed to get imagestream") {
			t.Fatalf("expected imagestream error, got: %v", err)
		}
	})

	t.Run("returns image validation error even with failing status writer", func(t *testing.T) {
		t.Setenv("OPERATOR_IMAGE", "quay.io/operator:latest")

		r := newImageTestReconciler(t, []client.Object{instance}, interceptClient{
			status: &failingStatusWriter{},
		})
		_, err := r.getJobFromInstance(context.TODO(), logf.Log, instance)
		if err == nil {
			t.Fatal("expected error but got none")
		}
		if !strings.Contains(err.Error(), "image validation failed") {
			t.Fatalf("expected image validation error, got: %v", err)
		}
		if !strings.Contains(err.Error(), "failed to get imagestream") {
			t.Fatalf("expected original image error in wrap, got: %v", err)
		}
	})
}
