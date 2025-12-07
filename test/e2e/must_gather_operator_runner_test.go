//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	jUnitOutputFilename = "junit-must-gather-operator.xml"
)

func getTestDir() string {
	// test is running in an OpenShift CI Prow job
	if os.Getenv("OPENSHIFT_CI") == "true" {
		return os.Getenv("ARTIFACT_DIR")
	}
	// not running in a CI job
	return "/tmp"
}

// TestMustGatherOperator is the test entrypoint for e2e tests.
func TestMustGatherOperator(t *testing.T) {
	RegisterFailHandler(Fail)
	testDir := getTestDir()
	suiteConfig, reporterConfig := GinkgoConfiguration()
	if _, ok := os.LookupEnv("DISABLE_JUNIT_REPORT"); !ok {
		reporterConfig.JUnitReport = filepath.Join(testDir, jUnitOutputFilename)
	}
	RunSpecs(t, "Must Gather Operator", suiteConfig, reporterConfig)
}
