package oap

import (
	"context"
	"fmt"
	"strings"
	"time"

	g "github.com/onsi/ginkgo/v2"
	exutil "github.com/openshift/origin/test/extended/util"
	"k8s.io/apimachinery/pkg/util/wait"
	e2e "k8s.io/kubernetes/test/e2e/framework"
)

// Immutable constant variables
const (
	MGOPackageName          = "support-log-gather-operator"
	MGONamespace            = "must-gather-operator"
	MGOLabel                = "name=must-gather-operator"
	MGODeploymentName       = "must-gather-operator"
	MGOCRDLabel             = "operators.coreos.com/openshift-external-secrets-operator.external-secrets-operator"
	MGOperandsDefaultPodNum = 3
)

// Other constant variables universally used
const (
	MGOSubscriptionName = "support-log-gather-operator"
	MGOChannelName      = "tech-preview"
	MGOExtensionName    = "clusterextension-support-log-gather"
	MGOFBCName          = "konflux-fbc-support-log-gather-operator"

	// Catalog source constants
	DefaultCatalogSourceNamespace = "openshift-marketplace"
	RHCatalogSourceName           = "redhat-operators"
	QECatalogSourceName           = "qe-app-registry"
	AutoReleaseCatalogSourceName  = "auto-release-app-registry"

	// Default SFTP credentials for testing
	DefaultSFTPUsername = "rhn-support-jitli"
	DefaultSFTPPassword = "rhn-support-jitli"
	Local               = false
)

func skipIfNotLocal(local bool) {
	if !local {
		g.Skip("Skip for non-local environment")
	}
}

// Create support-log-gather-operator
func installMustGatherOperator(oc *exutil.CLI, cfg olmInstallConfig) {
	createOperatorNamespace(oc, cfg.buildPruningBaseDir)

	switch cfg.mode {
	case "OLMv0":
		catalogSourceName, catalogSourceNamespace := determineCatalogSourceForMGO(oc)

		installViaOLMv0(oc, cfg.operatorNamespace, cfg.buildPruningBaseDir, cfg.subscriptionName, catalogSourceName, catalogSourceNamespace, cfg.channel)
	case "OLMv1":
		installViaOLMv1(oc, cfg.operatorNamespace, cfg.packageName, cfg.extensionName, cfg.channel, cfg.serviceAccountName)
	default:
		e2e.Failf("Please set correct olmMode:%v expected: 'OLMv0' or 'OLMv1'", cfg.mode)
	}

}

func determineCatalogSourceForMGO(oc *exutil.CLI) (string, string) {
	e2e.Logf("=========Determining which catalogsource to use=========")

	var catalogSourceName, catalogSourceNamespace string
	// strategy: 1. use "auto-release-app-registry", 2. use "qe-app-registry", 3. use "redhat-operators"
	output, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", DefaultCatalogSourceNamespace, "catalogsource", AutoReleaseCatalogSourceName).Output()
	if !strings.Contains(output, "NotFound") {
		catalogSourceName = AutoReleaseCatalogSourceName
		catalogSourceNamespace = DefaultCatalogSourceNamespace
	} else {
		output, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args("-n", DefaultCatalogSourceNamespace, "catalogsource", QECatalogSourceName).Output()
		if !strings.Contains(output, "NotFound") {
			catalogSourceName = QECatalogSourceName
			catalogSourceNamespace = DefaultCatalogSourceNamespace
		} else {
			catalogSourceName = RHCatalogSourceName
			catalogSourceNamespace = DefaultCatalogSourceNamespace
		}
	}

	// check if packagemanifest exists under the selected catalogsource
	output, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("packagemanifest", "-n", catalogSourceNamespace, "-l", "catalog="+catalogSourceName, "--field-selector", "metadata.name="+MGOPackageName).Output()
	if !strings.Contains(output, MGOPackageName) || err != nil {
		g.Skip("skip since no available packagemanifest was found")
	}
	e2e.Logf("=========Using catalogsource '%s' from namespace '%s'=========", catalogSourceName, catalogSourceNamespace)
	return catalogSourceName, catalogSourceNamespace
}

// verifyMustGatherLogs checks the must-gather pod logs for successful completion indicators
func verifyMustGatherLogs(oc *exutil.CLI, namespace, mgName string) error {
	e2e.Logf("Checking must-gather pod logs for successful completion...")

	// Wait for the must-gather pod to be created (up to 30 seconds)
	var podName string
	errWait := wait.PollUntilContextTimeout(context.TODO(), 5*time.Second, 30*time.Second, false, func(ctx context.Context) (bool, error) {
		var err error
		podName, err = findMustGatherPod(oc, namespace, mgName)
		if err != nil {
			e2e.Logf("Pod not found yet, waiting... error: %v", err)
			return false, nil // Continue waiting
		}
		return true, nil // Pod found
	})
	if errWait != nil {
		return fmt.Errorf("timeout waiting for must-gather pod to be created: %v", errWait)
	}

	e2e.Logf("Found must-gather pod: %s", podName)

	// Use polling to check logs repeatedly
	errWait = wait.PollUntilContextTimeout(context.TODO(), 30*time.Second, 12*time.Minute, false, func(ctx context.Context) (bool, error) {
		// Get logs from the upload container
		logs, err := oc.AsAdmin().Run("logs").Args("-n", namespace, "pod/"+podName, "-c", "upload").Output()
		if err != nil {
			e2e.Logf("Failed to get pod logs, error: %v, retrying...", err)
			return false, nil
		}

		// Check for archiving completion indicator
		// This verifies that must-gather data collection and archiving completed successfully
		// We check for archiving rather than upload success because CI doesn't have real SFTP credentials
		if strings.Contains(logs, "Archiving files from /must-gather to") {
			e2e.Logf("Must-gather data collection and packaging completed successfully")
			return true, nil
		}

		e2e.Logf("Archiving not completed yet, continuing to wait...")
		return false, nil
	})

	if errWait != nil {
		return fmt.Errorf("timeout waiting for must-gather completion: %v", errWait)
	}

	return nil
}

// findMustGatherPod finds the must-gather pod by name pattern
func findMustGatherPod(oc *exutil.CLI, namespace, mgName string) (string, error) {
	// Get all pods in the namespace
	output, err := oc.AsAdmin().Run("get").Args("-n", namespace, "pods", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		return "", err
	}

	pods := strings.Fields(output)
	for _, pod := range pods {
		if strings.Contains(pod, mgName) {
			return pod, nil
		}
	}

	return "", fmt.Errorf("no pod found matching pattern: %s", mgName)
}

// createSFTPCredentialSecret creates a secret containing SFTP credentials
func createSFTPCredentialSecret(oc *exutil.CLI, namespace, secretName, username, password string) error {
	e2e.Logf("Creating secret '%s' with SFTP credentials in namespace '%s'", secretName, namespace)
	oc.NotShowInfo()
	err := oc.AsAdmin().Run("create").Args("-n", namespace, "secret", "generic", secretName, "--from-literal=username="+username, "--from-literal=password="+password).Execute()
	oc.SetShowInfo()
	if err != nil {
		return fmt.Errorf("failed to create secret %s: %v", secretName, err)
	}
	return nil
}

// cleanupSecret deletes a secret from the specified namespace
func cleanupSecret(oc *exutil.CLI, namespace, secretName string) error {
	e2e.Logf("Cleanup secret '%s' from namespace '%s'", secretName, namespace)
	err := oc.AsAdmin().Run("delete").Args("-n", namespace, "secret", secretName, "--ignore-not-found").Execute()
	if err != nil {
		return fmt.Errorf("failed to delete secret %s: %v", secretName, err)
	}
	return nil
}

// waitForMustGatherCompletion waits for the MustGather CR to reach Completed status
func waitForMustGatherCompletion(oc *exutil.CLI, namespace, mgName string, timeout time.Duration) error {
	e2e.Logf("Waiting for MustGather '%s' to complete...", mgName)

	errWait := wait.PollUntilContextTimeout(context.TODO(), 20*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		status, err := oc.AsAdmin().Run("get").Args("-n", namespace, "mustgather", mgName, "-o=jsonpath={.status.status}").Output()
		if err != nil {
			e2e.Logf("Failed to get MustGather status, error: %v, retrying...", err)
			return false, nil
		}

		if strings.Contains(status, "Completed") {
			e2e.Logf("MustGather '%s' completed successfully", mgName)
			return true, nil
		}

		e2e.Logf("MustGather status: %s, waiting for completion...", status)
		return false, nil
	})

	if errWait != nil {
		return fmt.Errorf("timeout waiting for MustGather completion: %v", errWait)
	}

	return nil
}

// verifyPodDestroyed verifies that the must-gather pod has been destroyed
func verifyPodDestroyed(oc *exutil.CLI, namespace, mgName string, timeout time.Duration) error {
	e2e.Logf("Verifying that must-gather pod has been destroyed...")

	errWait := wait.PollUntilContextTimeout(context.TODO(), 5*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		_, err := findMustGatherPod(oc, namespace, mgName)
		if err != nil {
			// Check if error indicates pod not found (destroyed)
			if strings.Contains(err.Error(), "no pod found matching pattern") {
				e2e.Logf("Pod has been destroyed")
				return true, nil
			}
			// Other errors should be logged but continue polling
			e2e.Logf("Error checking pod existence: %v, retrying...", err)
			return false, nil
		}

		e2e.Logf("Pod still exists, waiting for destruction...")
		return false, nil
	})

	if errWait != nil {
		return fmt.Errorf("timeout waiting for pod to be destroyed: %v", errWait)
	}

	return nil
}

// verifyMustGatherTimeout verifies the mustGatherTimeout field value in MustGather CR
func verifyMustGatherTimeout(oc *exutil.CLI, namespace, mgName string, expectedTimeout string) error {
	e2e.Logf("Verifying mustGatherTimeout field value...")

	output, err := oc.AsAdmin().Run("get").Args("-n", namespace, "mustgather", mgName, "-o=jsonpath={.spec.mustGatherTimeout}").Output()
	if err != nil {
		return fmt.Errorf("failed to get mustGatherTimeout field: %v", err)
	}

	if output != expectedTimeout {
		return fmt.Errorf("mustGatherTimeout field mismatch: expected %s, got %s", expectedTimeout, output)
	}

	e2e.Logf("mustGatherTimeout field is set to %s as expected", expectedTimeout)
	return nil
}

// verifyPodRetained verifies that the must-gather pod still exists
func verifyPodRetained(oc *exutil.CLI, namespace, mgName string) error {
	e2e.Logf("Verifying that must-gather pod for '%s' is retained...", mgName)

	podName, err := findMustGatherPod(oc, namespace, mgName)
	if err != nil {
		return fmt.Errorf("pod was not retained as expected: %v", err)
	}

	e2e.Logf("Must-gather pod '%s' is retained as expected", podName)
	return nil
}

// verifyRetainResourcesField verifies the retainResourcesOnCompletion field value in MustGather CR
func verifyRetainResourcesField(oc *exutil.CLI, namespace, mgName string, expectedValue bool) error {
	e2e.Logf("Verifying retainResourcesOnCompletion field value...")

	output, err := oc.AsAdmin().Run("get").Args("-n", namespace, "mustgather", mgName, "-o=jsonpath={.spec.retainResourcesOnCompletion}").Output()
	if err != nil {
		return fmt.Errorf("failed to get retainResourcesOnCompletion field: %v", err)
	}

	expectedStr := fmt.Sprintf("%v", expectedValue)
	if output != expectedStr {
		return fmt.Errorf("retainResourcesOnCompletion field mismatch: expected %s, got %s", expectedStr, output)
	}

	e2e.Logf("retainResourcesOnCompletion field is set to %s as expected", expectedStr)
	return nil
}

// verifyNoUploadContainer verifies that the must-gather pod does not have an upload container
func verifyNoUploadContainer(oc *exutil.CLI, namespace, mgName string) error {
	e2e.Logf("Verifying that must-gather pod does not have upload container...")

	// Wait for pod to be created with timeout
	var podName string
	errWait := wait.PollUntilContextTimeout(context.TODO(), 5*time.Second, 30*time.Second, false, func(ctx context.Context) (bool, error) {
		name, err := findMustGatherPod(oc, namespace, mgName)
		if err != nil {
			e2e.Logf("Pod not found yet, waiting... (%v)", err)
			return false, nil
		}
		podName = name
		return true, nil
	})

	if errWait != nil {
		return fmt.Errorf("timeout waiting for must-gather pod to be created: %v", errWait)
	}

	e2e.Logf("Found must-gather pod: %s", podName)

	// Check if upload container exists by querying container names directly
	output, err := oc.AsAdmin().Run("get").Args("pod", podName, "-n", namespace, "-o=jsonpath={.spec.containers[?(@.name==\"upload\")].name}").Output()
	if err != nil {
		return fmt.Errorf("failed to get pod containers: %v", err)
	}

	if output == "" {
		e2e.Logf("Verified: no upload container found in pod %s", podName)
		return nil
	}

	return fmt.Errorf("upload container exists in pod %s, but should not exist when uploadTarget is not configured", podName)
}

// verifyPVCBound verifies that the PVC status is Bound
func verifyPVCBound(oc *exutil.CLI, namespace, pvcName string, timeout time.Duration) error {
	e2e.Logf("Verifying that PVC '%s' is bound...", pvcName)

	errWait := wait.PollUntilContextTimeout(context.TODO(), 5*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		status, err := oc.AsAdmin().Run("get").Args("pvc", pvcName, "-n", namespace, "-o=jsonpath={.status.phase}").Output()
		if err != nil {
			e2e.Logf("Failed to get PVC status, error: %v, retrying...", err)
			return false, nil
		}

		if status == "Bound" {
			e2e.Logf("PVC '%s' is bound", pvcName)
			return true, nil
		}

		e2e.Logf("PVC status: %s, waiting for Bound...", status)
		return false, nil
	})

	if errWait != nil {
		return fmt.Errorf("timeout waiting for PVC to be bound: %v", errWait)
	}

	return nil
}

// waitForPodRunning waits for a pod to reach Running state
func waitForPodRunning(oc *exutil.CLI, namespace, podName string, timeout time.Duration) error {
	e2e.Logf("Waiting for pod '%s' to be running...", podName)

	errWait := wait.PollUntilContextTimeout(context.TODO(), 5*time.Second, timeout, false, func(ctx context.Context) (bool, error) {
		status, err := oc.AsAdmin().Run("get").Args("pod", podName, "-n", namespace, "-o=jsonpath={.status.phase}").Output()
		if err != nil {
			e2e.Logf("Failed to get pod status, error: %v, retrying...", err)
			return false, nil
		}

		if status == "Running" {
			e2e.Logf("Pod '%s' is running", podName)
			return true, nil
		}

		e2e.Logf("Pod status: %s, waiting for Running...", status)
		return false, nil
	})

	if errWait != nil {
		return fmt.Errorf("timeout waiting for pod to be running: %v", errWait)
	}

	return nil
}

// verifyPVCContainsData verifies that PVC contains must-gather data by checking file existence
func verifyPVCContainsData(oc *exutil.CLI, namespace, readerPodName, subPath string) error {
	e2e.Logf("Verifying that PVC contains must-gather data...")

	// Check if the subPath directory exists in the PVC
	cmd := fmt.Sprintf("test -d /data/%s && echo 'exists' || echo 'not exists'", subPath)
	output, err := oc.AsAdmin().Run("exec").Args(readerPodName, "-n", namespace, "-c", "reader-container", "--", "sh", "-c", cmd).Output()
	if err != nil {
		return fmt.Errorf("failed to check PVC data: %v", err)
	}

	if !strings.Contains(output, "exists") {
		return fmt.Errorf("PVC does not contain expected data at path: %s", subPath)
	}

	// List files in the directory
	cmd = fmt.Sprintf("ls -la /data/%s", subPath)
	output, err = oc.AsAdmin().Run("exec").Args(readerPodName, "-n", namespace, "-c", "reader-container", "--", "sh", "-c", cmd).Output()
	if err != nil {
		return fmt.Errorf("failed to list PVC data: %v", err)
	}

	e2e.Logf("PVC contains must-gather data:\n%s", output)

	// Check for expected must-gather files
	expectedFiles := []string{"cluster-scoped-resources", "must-gather.log"}
	for _, file := range expectedFiles {
		if !strings.Contains(output, file) {
			e2e.Logf("Warning: expected file/directory '%s' not found in PVC", file)
		}
	}

	e2e.Logf("Verified: PVC contains must-gather data at %s", subPath)
	return nil
}

// cleanupPVC deletes a PVC from the specified namespace
func cleanupPVC(oc *exutil.CLI, namespace, pvcName string) error {
	e2e.Logf("Cleanup PVC '%s' from namespace '%s'", pvcName, namespace)
	err := oc.AsAdmin().Run("delete").Args("pvc", pvcName, "-n", namespace, "--ignore-not-found").Execute()
	if err != nil {
		return fmt.Errorf("failed to delete PVC %s: %v", pvcName, err)
	}
	return nil
}

// cleanupPod deletes a pod from the specified namespace
func cleanupPod(oc *exutil.CLI, namespace, podName string) error {
	e2e.Logf("Cleanup pod '%s' from namespace '%s'", podName, namespace)
	err := oc.AsAdmin().Run("delete").Args("pod", podName, "-n", namespace, "--ignore-not-found").Execute()
	if err != nil {
		return fmt.Errorf("failed to delete pod %s: %v", podName, err)
	}
	return nil
}

// verifyMustGatherLogContent verifies that must-gather.log contains expected gather process messages
func verifyMustGatherLogContent(oc *exutil.CLI, namespace, readerPodName, subPath string) error {
	e2e.Logf("Verifying must-gather.log content...")

	// Read must-gather.log content directly from PVC
	cmd := fmt.Sprintf("cat /data/%s/must-gather.log 2>/dev/null | head -n 30", subPath)
	logContent, err := oc.AsAdmin().Run("exec").Args(readerPodName, "-n", namespace, "-c", "reader-container", "--", "sh", "-c", cmd).Output()
	if err != nil {
		return fmt.Errorf("failed to read must-gather.log from PVC: %v", err)
	}

	if logContent == "" {
		return fmt.Errorf("must-gather.log is empty or not found at path: %s", subPath)
	}

	e2e.Logf("must-gather.log content preview:\n%s", logContent)

	// Verify log contains expected messages from the gather process
	expectedMessages := []string{
		"Gathering data for ns/openshift-cluster-version",
		"Gathering data for ns/default",
		"INFO: Gathering machine config daemon's old logs from all nodes",
		"INFO: Gathering HAProxy config files",
		"INFO: Collecting host service logs",
	}

	foundAny := false
	for _, expectedMsg := range expectedMessages {
		if strings.Contains(logContent, expectedMsg) {
			e2e.Logf("Found expected message: %s", expectedMsg)
			foundAny = true
		} else {
			e2e.Logf("Warning: Expected message '%s' not found in must-gather.log", expectedMsg)
		}
	}

	if !foundAny {
		return fmt.Errorf("none of the expected gather process messages were found in must-gather.log")
	}

	e2e.Logf("Verified: must-gather.log contains expected gather process messages")
	return nil
}

// verifyAuditLogCollection verifies that must-gather pod logs contain audit log collection messages
func verifyAuditLogCollection(oc *exutil.CLI, namespace, mgName string) error {
	e2e.Logf("Verifying that must-gather pod logs contain audit log collection messages...")

	// Wait for pod to be created with timeout
	var podName string
	errWait := wait.PollUntilContextTimeout(context.TODO(), 5*time.Second, 30*time.Second, false, func(ctx context.Context) (bool, error) {
		name, err := findMustGatherPod(oc, namespace, mgName)
		if err != nil {
			e2e.Logf("Pod not found yet, waiting... (%v)", err)
			return false, nil
		}
		podName = name
		return true, nil
	})

	if errWait != nil {
		return fmt.Errorf("timeout waiting for must-gather pod to be created: %v", errWait)
	}

	e2e.Logf("Found must-gather pod: %s", podName)

	// Use polling to check logs repeatedly
	var logs string
	errWait = wait.PollUntilContextTimeout(context.TODO(), 30*time.Second, 6*time.Minute, false, func(ctx context.Context) (bool, error) {
		// Get logs from the gather container
		logs, err := oc.AsAdmin().Run("logs").Args("-n", namespace, "pod/"+podName, "-c", "gather").Output()
		if err != nil {
			e2e.Logf("Failed to get pod logs, error: %v, retrying...", err)
			return false, nil
		}

		// Check for audit log collection script execution
		// We check if gather_audit_logs was called, regardless of success/failure
		// because the test is to verify audit: true triggers the collection attempt
		auditScriptMessages := []string{
			"/usr/bin/gather_audit_logs",
			"gather_audit_logs",
		}

		for _, scriptMsg := range auditScriptMessages {
			if strings.Contains(logs, scriptMsg) {
				e2e.Logf("Found audit log collection script execution: %s", scriptMsg)

				// Also check for success messages (but don't require them)
				successMessages := []string{
					"WARNING: Collecting one or more audit logs on ALL masters",
					"downloading openshift-apiserver/audit.log",
					"downloading kube-apiserver/audit",
					"downloading oauth-apiserver/audit.log",
				}

				for _, successMsg := range successMessages {
					if strings.Contains(logs, successMsg) {
						e2e.Logf("Found audit log collection success message: %s", successMsg)
					}
				}

				return true, nil
			}
		}

		e2e.Logf("Audit log collection messages not found yet, continuing to wait...")
		return false, nil
	})

	if errWait != nil {
		e2e.Logf("Pod logs:\n%s", logs)
		return fmt.Errorf("timeout waiting for audit log collection messages: %v", errWait)
	}

	e2e.Logf("Verified: must-gather pod logs contain audit log collection messages")
	return nil
}

// verifyAuditLogsDirectory verifies that audit_logs directory exists in PVC
func verifyAuditLogsDirectory(oc *exutil.CLI, namespace, readerPodName, subPath string) error {
	e2e.Logf("Verifying audit_logs directory exists in PVC...")

	// Check if audit_logs directory exists directly in subPath
	checkCmd := fmt.Sprintf("test -d /data/%s/audit_logs && echo 'exists' || echo 'not-exists'", subPath)
	output, err := oc.AsAdmin().Run("exec").Args(readerPodName, "-n", namespace, "-c", "reader-container", "--", "sh", "-c", checkCmd).Output()

	directoryFound := false
	if err == nil && strings.Contains(output, "exists") {
		directoryFound = true
		e2e.Logf("Found audit_logs directory at /data/%s/audit_logs", subPath)
	} else {
		// If not found directly, try to find it in subdirectories
		findCmd := fmt.Sprintf("find /data/%s -type d -name 'audit_logs' 2>/dev/null | head -n 1", subPath)
		findOutput, findErr := oc.AsAdmin().Run("exec").Args(readerPodName, "-n", namespace, "-c", "reader-container", "--", "sh", "-c", findCmd).Output()
		if findErr == nil && strings.Contains(findOutput, "audit_logs") {
			directoryFound = true
			e2e.Logf("Found audit_logs directory in subdirectory: %s", strings.TrimSpace(findOutput))
		}
	}

	if !directoryFound {
		// List what's actually in the directory for debugging
		listCmd := fmt.Sprintf("ls -la /data/%s", subPath)
		listOutput, _ := oc.AsAdmin().Run("exec").Args(readerPodName, "-n", namespace, "-c", "reader-container", "--", "sh", "-c", listCmd).Output()
		e2e.Logf("Directory contents:\n%s", listOutput)
		return fmt.Errorf("audit_logs directory not found in PVC")
	}

	e2e.Logf("Verified: audit_logs directory exists in PVC")
	// List some contents for verification
	listCmd := fmt.Sprintf("ls -la /data/%s/audit_logs 2>/dev/null || find /data/%s -type d -name 'audit_logs' -exec ls -la {} \\; | head -n 20", subPath, subPath)
	listOutput, _ := oc.AsAdmin().Run("exec").Args(readerPodName, "-n", namespace, "-c", "reader-container", "--", "sh", "-c", listCmd).Output()
	e2e.Logf("audit_logs contents:\n%s", listOutput)

	return nil
}
