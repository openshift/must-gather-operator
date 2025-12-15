//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/must-gather-operator/test/library"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const (
	jUnitOutputFilename = "junit-must-gather-operator-e2e-test.xml"
)

var (
	loader      library.DynamicResourceLoader
	cfg         *rest.Config
	clusterName string
	ns          *corev1.Namespace
)

func getTestDir() string {
	// test is running in an OpenShift CI Prow job
	if os.Getenv("OPENSHIFT_CI") == "true" {
		return os.Getenv("ARTIFACT_DIR")
	}
	// not running in a CI job
	return "/tmp"
}

// getClusterName returns the cluster name from the provided rest.Config
func getClusterName(cfg *rest.Config) string {
	// Extract cluster name from the API server URL
	if cfg.Host != "" {
		host := cfg.Host
		host = strings.TrimPrefix(host, "https://")
		host = strings.TrimPrefix(host, "http://")
		return host
	}

	return "unknown-cluster"
}

// TestMustGatherOperator is the test entrypoint for e2e tests.
func TestMustGatherOperator(t *testing.T) {
	RegisterFailHandler(Fail)
	testDir := getTestDir()
	suiteConfig, reporterConfig := GinkgoConfiguration()
	if _, ok := os.LookupEnv("DISABLE_JUNIT_REPORT"); !ok {
		reporterConfig.JUnitReport = filepath.Join(testDir, jUnitOutputFilename)
	}
	RunSpecs(t, "support-log-gather e2e suite", suiteConfig, reporterConfig)
}

var _ = BeforeSuite(func() {
	var err error
	cfg, err = config.GetConfig()
	Expect(err).NotTo(HaveOccurred())

	clusterName = getClusterName(cfg)
	By(fmt.Sprintf("using cluster: %s", clusterName))

	By("creating dynamic resources client")
	loader = library.NewDynamicResourceLoader(context.TODO(), &testing.T{})

})
