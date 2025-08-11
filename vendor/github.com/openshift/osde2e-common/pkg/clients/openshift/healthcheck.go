package openshift

import (
	"context"
	"fmt"
	"os"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/e2e-framework/klient/wait"
)

const (
	osdClusterReadyNamespace = "openshift-monitoring"
	jobNameLoggerKey         = "job_name"
	timeoutLoggerKey         = "timeout"
)

// OSDClusterHealthy waits for the cluster to be in a healthy "ready" state
// by confirming the osd-ready-job finishes successfully
func (c *Client) OSDClusterHealthy(ctx context.Context, jobName, reportDir string, timeout time.Duration) error {
	if err := wait.For(func(ctx context.Context) (bool, error) {
		job := new(batchv1.Job)
		if err := c.Get(ctx, jobName, osdClusterReadyNamespace, job); err != nil {
			c.log.Error(err, fmt.Sprintf("failed to get job %s/%s", osdClusterReadyNamespace, jobName))
			if isRetryableAPIError(err) || apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	}, wait.WithTimeout(timeout)); err != nil {
		c.log.Error(err, "failed waiting for healthcheck job to finish")
		logs, err := c.GetJobLogs(ctx, jobName, osdClusterReadyNamespace)
		if err != nil {
			return fmt.Errorf("unable to get job logs for %s/%s: %w", osdClusterReadyNamespace, jobName, err)
		}
		jobLogsFile := fmt.Sprintf("%s/%s.log", reportDir, jobName)
		if err = os.WriteFile(jobLogsFile, []byte(logs), os.FileMode(0o644)); err != nil {
			return fmt.Errorf("failed to write job %s logs to file: %w", jobName, err)
		}
		return fmt.Errorf("%s/%s failed to complete (check %s for more info): %w", osdClusterReadyNamespace, jobName, jobLogsFile, err)
	}

	c.log.Info("Cluster job finished successfully!", jobNameLoggerKey, jobName)

	return nil
}

// HCPClusterHealthy waits for the cluster to be in a health "ready" state
// by confirming nodes are available
func (c *Client) HCPClusterHealthy(ctx context.Context, computeNodes int, timeout time.Duration) error {
	c.log.Info("Waiting for hosted control plane cluster to healthy", timeoutLoggerKey, timeout.Round(time.Second).String())

	err := wait.For(func(ctx context.Context) (bool, error) {
		var nodes corev1.NodeList
		err := c.List(ctx, &nodes)
		if err != nil {
			if os.IsTimeout(err) {
				c.log.Error(err, "timeout occurred contacting api server")
				return false, nil
			}
			return false, err
		}

		if len(nodes.Items) == 0 {
			return false, nil
		}

		for _, node := range nodes.Items {
			for _, condition := range node.Status.Conditions {
				if condition.Type == corev1.NodeReady && condition.Status != corev1.ConditionTrue {
					return false, nil
				}
			}
		}

		return len(nodes.Items) == computeNodes, nil
	}, wait.WithTimeout(timeout))
	if err != nil {
		return fmt.Errorf("hosted control plane cluster health check failed: %w", err)
	}

	c.log.Info("Hosted control plane cluster health check finished successfully!")

	return nil
}
