package openshift

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/openshift/api"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/e2e-framework/klient/conf"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
)

type Client struct {
	*resources.Resources
	log logr.Logger
}

func New(logger logr.Logger) (*Client, error) {
	return NewFromKubeconfig("", logger)
}

func NewFromKubeconfig(filename string, logger logr.Logger) (*Client, error) {
	cfg, err := conf.New(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubernetes config: %w", err)
	}
	return NewFromRestConfig(cfg, logger)
}

func NewFromRestConfig(cfg *rest.Config, logger logr.Logger) (*Client, error) {
	client, err := resources.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to created dynamic client: %w", err)
	}
	if err = api.Install(client.GetScheme()); err != nil {
		return nil, fmt.Errorf("unable to register openshift api schemes: %w", err)
	}
	return &Client{client, logger}, nil
}

// Impersonate returns a copy of the client with a new ImpersonationConfig
// established on the underlying client, acting as the provided user
//
//	backplaneUser, _ := oc.Impersonate("test-user@redhat.com", "dedicated-admins")
func (c *Client) Impersonate(user string, groups ...string) (*Client, error) {
	if user != "" {
		// these groups are required for impersonating a user
		groups = append(groups, "system:authenticated", "system:authenticated:oauth")
	}

	client := *c
	newRestConfig := rest.CopyConfig(c.Resources.GetConfig())
	newRestConfig.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: groups}
	newResources, err := resources.New(newRestConfig)
	if err != nil {
		return nil, err
	}
	client.Resources = newResources

	if err = api.Install(client.GetScheme()); err != nil {
		return nil, fmt.Errorf("unable to register openshift api schemes: %w", err)
	}

	return &client, nil
}

// GetPodLogs fetches the logs of a pod's default container
func (c *Client) GetPodLogs(ctx context.Context, name, namespace string) (string, error) {
	clientSet, err := kubernetes.NewForConfig(c.GetConfig())
	if err != nil {
		return "", fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}
	logData, err := clientSet.CoreV1().Pods(namespace).GetLogs(name, &corev1.PodLogOptions{}).DoRaw(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get pod %s/%s logs: %w", name, namespace, err)
	}
	return string(logData), nil
}

// GetJobLogs fetches the logs of a job's first container
func (c *Client) GetJobLogs(ctx context.Context, name, namespace string) (string, error) {
	pods := new(corev1.PodList)
	err := c.List(ctx, pods, resources.WithLabelSelector(labels.FormatLabels(map[string]string{"job-name": name})))
	if err != nil {
		return "", fmt.Errorf("failed to list pods for job %s in %s namespace: %w", name, namespace, err)
	}
	// TODO: there may be a case where the first item isn't correct
	return c.GetPodLogs(ctx, pods.Items[0].GetName(), namespace)
}

// WatchJob function streams job events and returns nil on success.
// If the watched job fails, is disconnected, the watch produces an error, the
// watch channel closes, or the context is cancelled at timeout, it will return
// an error containing event in question.
func (c *Client) WatchJob(ctx context.Context, namespace string, name string) error {
	job := new(batchv1.Job)
	if err := c.Get(ctx, name, namespace, job); err != nil {
		return fmt.Errorf("failed to find job %s/%s: %w", namespace, name, err)
	}

	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			return nil
		}
	}

	clientSet, err := kubernetes.NewForConfig(c.GetConfig())
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}
	watcher, err := clientSet.BatchV1().Jobs(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + job.Name,
	})
	if err != nil {
		return fmt.Errorf("failed creating job watcher: %w", err)
	}
	defer watcher.Stop()
	for {
		select {
		case event, more := <-watcher.ResultChan():
			c.log.Info("Watching job", job.Name, event.Type)
			switch event.Type {
			case watch.Added:
				fallthrough
			case watch.Modified:
				job := event.Object.(*batchv1.Job)
				if job.Status.Succeeded > 0 {
					return nil
				}
				if job.Status.Failed > 0 {
					return fmt.Errorf("job failed")
				}
			case watch.Deleted:
				return fmt.Errorf("job deleted before becoming ready")
			case watch.Error:
				return fmt.Errorf("watch returned error event: %v", event)
			}
			if !more {
				return fmt.Errorf("job watch result channel closed prematurely with event: %T %v", event, event)
			}
		case <-ctx.Done():
			return fmt.Errorf("job watch context cancelled while still waiting for success")
		}
	}
}
