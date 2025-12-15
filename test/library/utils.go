//go:build e2e
// +build e2e

package library

import (
	"context"
	"fmt"
	"log"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

func (d DynamicResourceLoader) CreateTestingNS(namespacePrefix string, noSuffix bool) (*corev1.Namespace, error) {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"e2e-test": "true",
				"operator": "openshift-must-gather-operator",
			},
		},
	}

	if noSuffix {
		namespace.Name = namespacePrefix
	} else {
		namespace.GenerateName = fmt.Sprintf("%v-", namespacePrefix)
	}

	var got *corev1.Namespace
	if err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 30*time.Second, true, func(context.Context) (bool, error) {
		var err error
		got, err = d.KubeClient.CoreV1().Namespaces().Create(context.Background(), namespace, metav1.CreateOptions{})
		if err != nil {
			log.Printf("Error creating namespace: %v", err)
			return false, nil
		}
		return true, nil
	}); err != nil {
		return nil, err
	}
	return got, nil
}

func (d DynamicResourceLoader) DeleteTestingNS(name string, shouldDumpEvents func() bool) (bool, error) {
	ctx := context.Background()
	if shouldDumpEvents() {
		d.DumpEventsInNamespace(name)
	}

	err := d.KubeClient.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		log.Printf("Error deleting namespace %v, err: %v", name, err)
	}

	if err := wait.PollUntilContextTimeout(context.TODO(), 1*time.Second, 30*time.Second, true, func(context.Context) (bool, error) {
		// Poll until namespace is deleted
		_, err := d.KubeClient.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
		if err != nil && k8serrors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	}); err != nil {
		log.Printf("Timed out after 30s waiting for namespace %v to become deleted", name)
		return false, err
	}
	return false, nil
}

func (d DynamicResourceLoader) DumpEventsInNamespace(name string) {
	log.Printf("Dumping events in namespace %s...", name)
	events, err := d.KubeClient.CoreV1().Events(name).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Printf("Error listing events in namespace %s: %v", name, err)
		return
	}

	for _, e := range events.Items {
		log.Printf("At %v - event for %v %v: %v %v: %v", e.FirstTimestamp, e.InvolvedObject.Kind, e.InvolvedObject.Name, e.Source, e.Reason, e.Message)
	}
}
