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
	testResultsDirectory = "./test-run-results"
	jUnitOutputFilename  = "junit-must-gather-operator.xml"
)

// TestMustGatherOperator is the test entrypoint for e2e tests.
func TestMustGatherOperator(t *testing.T) {
	RegisterFailHandler(Fail)
	suiteConfig, reporterConfig := GinkgoConfiguration()
	if _, ok := os.LookupEnv("DISABLE_JUNIT_REPORT"); !ok {
		reporterConfig.JUnitReport = filepath.Join(testResultsDirectory, jUnitOutputFilename)
	}
	RunSpecs(t, "Must Gather Operator", suiteConfig, reporterConfig)
}
