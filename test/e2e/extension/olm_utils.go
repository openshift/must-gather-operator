package oap

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	o "github.com/onsi/gomega"
	compat_otp "github.com/openshift/origin/test/extended/util/compat_otp"
	exutil "github.com/openshift/origin/test/extended/util"
	g "github.com/onsi/ginkgo/v2"
	e2e "k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/apimachinery/pkg/util/wait"
)

type olmInstallConfig struct {
	mode                   string
	operatorNamespace      string
	buildPruningBaseDir    string
	subscriptionName       string // OLMv0
	catalogSourceName      string // OLMv0
	catalogSourceNamespace string // OLMv0
	channel                string
	packageName            string // OLMv1
	extensionName          string // OLMv1
	serviceAccountName     string // OLMv1
}

// create operator Namespace
func createOperatorNamespace(oc *exutil.CLI, buildPruningBaseDir string) {
	e2e.Logf("=========Create the operator namespace=========")
	namespaceFile := filepath.Join(buildPruningBaseDir, "namespace.yaml")
	output, err := oc.AsAdmin().WithoutNamespace().Run("apply").Args("-f", namespaceFile).Output()
	if strings.Contains(output, "being deleted") {
		g.Skip("skip the install process as the namespace is being terminated due to other env issue e.g. we ever hit such failures caused by OCPBUGS-31443")
	}
	if err != nil && !strings.Contains(output, "AlreadyExists") {
		e2e.Failf("Failed to apply namespace: %v", err)
	}
}

// install Via OLMv0
func installViaOLMv0(oc *exutil.CLI, operatorNamespace, buildPruningBaseDir, subscriptionName, catalogSourceName, catalogSourceNamespace, channel string) {
	e2e.Logf("=========Installing via OLMv0 (Subscription)=========")
	// Create operator group
	operatorGroupFile := filepath.Join(buildPruningBaseDir, "operatorgroup.yaml")
	output, err := oc.AsAdmin().WithoutNamespace().Run("apply").Args("-f", operatorGroupFile).Output()
	if err != nil && !strings.Contains(output, "AlreadyExists") {
		e2e.Failf("Error: %v", output)
	}

	e2e.Logf("=========Create the subscription=========")
	subscriptionTemplate := filepath.Join(buildPruningBaseDir, "subscription.yaml")
	params := []string{"-f", subscriptionTemplate, "-p", "NAME=" + subscriptionName, "SOURCE=" + catalogSourceName, "SOURCE_NAMESPACE=" + catalogSourceNamespace, "CHANNEL=" + channel}
	compat_otp.ApplyNsResourceFromTemplate(oc, operatorNamespace, params...)
	// Wait for subscription state to become AtLatestKnown
	err = wait.PollUntilContextTimeout(context.TODO(), 10*time.Second, 180*time.Second, true, func(context.Context) (bool, error) {
		output, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args("sub", subscriptionName, "-n", operatorNamespace, "-o=jsonpath={.status.state}").Output()
		if strings.Contains(output, "AtLatestKnown") {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		dumpResource(oc, operatorNamespace, "sub", subscriptionName, "-o=jsonpath={.status}")
	}
	compat_otp.AssertWaitPollNoErr(err, "timeout waiting for subscription state to become AtLatestKnown")

	e2e.Logf("=========retrieve the installed CSV name=========")
	csvName, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("sub", subscriptionName, "-n", operatorNamespace, "-o=jsonpath={.status.installedCSV}").Output()
	o.Expect(err).NotTo(o.HaveOccurred())
	o.Expect(csvName).NotTo(o.BeEmpty())
	// Wait for csv phase to become Succeeded
	err = wait.PollUntilContextTimeout(context.TODO(), 10*time.Second, 180*time.Second, true, func(context.Context) (bool, error) {
		output, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args("csv", csvName, "-n", operatorNamespace, "-o=jsonpath={.status.phase}").Output()
		if strings.Contains(output, "Succeeded") {
			e2e.Logf("csv '%s' installed successfully", csvName)
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		dumpResource(oc, operatorNamespace, "csv", csvName, "-o=jsonpath={.status}")
	}
	compat_otp.AssertWaitPollNoErr(err, "timeout waiting for csv phase to become Succeeded")
}

// install Via OLMv1
// Note: This is a simplified version. The must-gather tests use OLMv0.
// If OLMv1 support is needed, additional dependencies from origin/test/extended/util/compat_otp/olm/v1 are required.
func installViaOLMv1(oc *exutil.CLI, operatorNamespace, packageName, clusterextensionName, channel, saCrbName string) {
	e2e.Logf("=========Installing via OLMv1 (ClusterExtension)=========")
	e2e.Failf("OLMv1 installation is not implemented in this migration. The must-gather tests use OLMv0.")
}
