package oap

import (
	"math/rand"
	"strings"
	"time"

	exutil "github.com/openshift/origin/test/extended/util"
	e2e "k8s.io/kubernetes/test/e2e/framework"
)

// getRandomString generates a random string of specified length
func getRandomString(digit int) string {
	chars := "abcdefghijklmnopqrstuvwxyz0123456789"
	seed := rand.New(rand.NewSource(time.Now().UnixNano()))
	buffer := make([]byte, digit)
	for index := range buffer {
		buffer[index] = chars[seed.Intn(len(chars))]
	}
	return string(buffer)
}

// IsDeploymentReady checks if a deployment is ready and available
func IsDeploymentReady(oc *exutil.CLI, namespace string, deploymentName string) bool {
	e2e.Logf("Checking readiness of deployment '%s' in namespace '%s'...", deploymentName, namespace)
	status, err := oc.AsAdmin().WithoutNamespace().Run("get").Args("deploy", deploymentName, "-n", namespace, `-o=jsonpath={.status.conditions[?(@.type=="Available")].status}`).Output()
	if err != nil {
		e2e.Logf("Failed to check deployment readiness: %v", err.Error())
		return false
	}
	if strings.TrimSpace(status) == "True" {
		e2e.Logf("Deployment '%s' is ready and available.", deploymentName)
		return true
	}
	e2e.Logf("Deployment '%s' is not ready. Status: '%s'", deploymentName, status)
	return false
}

// dumpResource dumps a Kubernetes resource for debugging
func dumpResource(oc *exutil.CLI, namespace, resourceType, resourceName, parameter string) {
	args := []string{resourceType, resourceName, parameter}
	if len(namespace) > 0 {
		args = append(args, "-n="+namespace)
	}

	output, _ := oc.AsAdmin().WithoutNamespace().Run("get").Args(args...).Output()
	e2e.Logf("Dumping the %s '%s' with parameter '%s':\n%s", resourceType, resourceName, parameter, output)
}
