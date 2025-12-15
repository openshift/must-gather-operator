//go:build e2e
// +build e2e

package library

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/restmapper"
)

type DynamicResourceLoader struct {
	KubeClient    kubernetes.Interface
	DynamicClient dynamic.Interface

	context context.Context
	t       *testing.T
}

type doFunc func(t *testing.T, unstructured *unstructured.Unstructured, dynamicResourceInterface dynamic.ResourceInterface)

func NewDynamicResourceLoader(context context.Context, t *testing.T) DynamicResourceLoader {
	k, d := NewClientsConfigForTest(t)
	return DynamicResourceLoader{
		KubeClient:    k,
		DynamicClient: d,
		context:       context,
		t:             t,
	}
}

func (d DynamicResourceLoader) noErrorSkipExists(err error) {
	if !k8serrors.IsAlreadyExists(err) {
		require.NoError(d.t, err)
	}
}

func (d DynamicResourceLoader) noErrorSkipNotExisting(err error) {
	if !k8serrors.IsNotFound(err) {
		require.NoError(d.t, err)
	}
}

// updateSubjectNamespaces sets the namespace on all subjects in a RoleBinding/ClusterRoleBinding
// that don't already have a namespace set. This is needed because ServiceAccount subjects
// require a namespace to be specified.
func (d DynamicResourceLoader) updateSubjectNamespaces(obj *unstructured.Unstructured, namespace string) {
	subjects, found, err := unstructured.NestedSlice(obj.Object, "subjects")
	if err != nil || !found {
		return
	}

	for i, subject := range subjects {
		subjectMap, ok := subject.(map[string]interface{})
		if !ok {
			continue
		}
		// Only set namespace if it's empty or not set
		if ns, exists := subjectMap["namespace"]; !exists || ns == "" {
			subjectMap["namespace"] = namespace
			subjects[i] = subjectMap
		}
	}

	_ = unstructured.SetNestedSlice(obj.Object, subjects, "subjects")
}

func (d DynamicResourceLoader) do(do doFunc, assetFunc func(name string) ([]byte, error), filename string, overrideNamespace string) {
	b, err := assetFunc(filename)
	require.NoError(d.t, err)

	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(b), 1024)
	var rawObj runtime.RawExtension
	err = decoder.Decode(&rawObj)
	require.NoError(d.t, err)

	obj, gvk, err := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme).Decode(rawObj.Raw, nil, nil)
	require.NoError(d.t, err)

	unstructuredMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	require.NoError(d.t, err)

	unstructuredObj := &unstructured.Unstructured{Object: unstructuredMap}

	gr, err := restmapper.GetAPIGroupResources(d.KubeClient.Discovery())
	require.NoError(d.t, err)

	mapper := restmapper.NewDiscoveryRESTMapper(gr)
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	require.NoError(d.t, err)

	var dri dynamic.ResourceInterface
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		if overrideNamespace != "" {
			unstructuredObj.SetNamespace(overrideNamespace)
		}
		require.NotEmpty(d.t, unstructuredObj.GetNamespace(), "Namespace can not be empty!")
		dri = d.DynamicClient.Resource(mapping.Resource).Namespace(unstructuredObj.GetNamespace())
	} else {
		// For cluster-scoped bindings (ClusterRoleBinding, RoleBinding), update subject namespaces
		if overrideNamespace != "" && (gvk.Kind == "ClusterRoleBinding" || gvk.Kind == "RoleBinding") {
			d.updateSubjectNamespaces(unstructuredObj, overrideNamespace)
		}
		dri = d.DynamicClient.Resource(mapping.Resource)
	}

	do(d.t, unstructuredObj, dri)
}

func (d DynamicResourceLoader) DeleteFromFile(assetFunc func(name string) ([]byte, error), filename string, overrideNamespace string) {
	d.t.Logf("Deleting resource %v\n", filename)
	deleteFunc := func(t *testing.T, unstructured *unstructured.Unstructured, dynamicResourceInterface dynamic.ResourceInterface) {
		err := dynamicResourceInterface.Delete(context.TODO(), unstructured.GetName(), metav1.DeleteOptions{})
		d.noErrorSkipNotExisting(err)
	}

	d.do(deleteFunc, assetFunc, filename, overrideNamespace)
	d.t.Logf("Resource %v deleted\n", filename)
}

func (d DynamicResourceLoader) CreateFromFile(assetFunc func(name string) ([]byte, error), filename string, overrideNamespace string) {
	d.t.Logf("Creating resource %v\n", filename)
	createFunc := func(t *testing.T, unstructured *unstructured.Unstructured, dynamicResourceInterface dynamic.ResourceInterface) {
		_, err := dynamicResourceInterface.Create(context.TODO(), unstructured, metav1.CreateOptions{})
		d.noErrorSkipExists(err)
	}

	d.do(createFunc, assetFunc, filename, overrideNamespace)
	d.t.Logf("Resource %v created\n", filename)
}
