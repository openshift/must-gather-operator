package mustgather

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/api/core/v1"
	batchv1 "k8s.io/api/batch/v1"
)

func Test_initializeJobTemplate(t *testing.T) {
	testFailed := false
	testName := "testName"
	testNamespace := "testNamespace"
	testServiceAccountRef := "testServiceAccountRef"
	job := initializeJobTemplate(testName, testNamespace, testServiceAccountRef)

	if got := job.Name; got != testName {
		t.Logf("job name from initializeJobTemplate() was not correctly set. got %v, wanted %v", got, testName)
		testFailed = true
	}

	if got := job.Namespace; got != testNamespace {
		t.Logf("job namespace from initializeJobTemplate() was not correctly set. got %v, wanted %v", got, testNamespace)
		testFailed = true
	}

	if got := job.Spec.Template.Spec.ServiceAccountName; got != testServiceAccountRef {
		t.Logf("job service account name from initializeJobTemplate() was not correctly set. got %v, wanted %v", got, testServiceAccountRef)
		testFailed = true
	}

	if testFailed == true {
		t.Error()
	}
}

func Test_getGatherContainer(t *testing.T) {
	tests := []struct {
		name                   string
		audit                  bool
		timeout                time.Duration
		mustGatherImageVersion string
	}{
		{
			name:                   "no audit",
			timeout:                5 * time.Second,
			mustGatherImageVersion: "1.2.3",
		},
		{
			name:                   "audit",
			audit:                  true,
			timeout:                0 * time.Second,
			mustGatherImageVersion: "1.2.3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testFailed := false

			container := getGatherContainer(tt.audit, tt.timeout, tt.mustGatherImageVersion)

			containerCommand := container.Command[2]
			if tt.audit && !strings.Contains(containerCommand, gatherCommandBinaryAudit) {
				t.Logf("gather container command expected with binary %v but it wasn't present", gatherCommandBinaryAudit)
				testFailed = true
			} else if !tt.audit && !strings.Contains(containerCommand, gatherCommandBinaryNoAudit) {
				t.Logf("gather container command expected with binary %v but it wasn't present", gatherCommandBinaryNoAudit)
				testFailed = true
			}

			if !strings.HasPrefix(containerCommand, fmt.Sprintf("timeout %v", tt.timeout)) {
				t.Logf("the duration was not properly added to the container command, got %v but wanted %v", strings.Split(containerCommand, " ")[1], tt.timeout.String())
				testFailed = true
			}

			if expectedImage := fmt.Sprintf("%v:%v", mustGatherImage, tt.mustGatherImageVersion); container.Image != expectedImage {
				t.Logf("expected container image %v but got %v", expectedImage, container.Image)
				testFailed = true
			}

			if testFailed {
				t.Error()
			}
		})
	}
}

func Test_getUploadContainer(t *testing.T) {
	tests := []struct {
		name             string
		operatorImage    string
		caseId           string
		internalUser     bool
		httpProxy        string
		httpsProxy       string
		noProxy          string
		secretKeyRefName v1.LocalObjectReference
	}{
		{
			name:             "All fields present",
			operatorImage:    "testImage",
			caseId:           "1234",
			internalUser:     true,
			httpProxy:        "testHttpProxy",
			httpsProxy:       "testHttpsProxy",
			noProxy:          "testNoProxy",
			secretKeyRefName: v1.LocalObjectReference{Name: "testSecretKeyRefName"},
		},
		{
			name:             "Non-internal user",
			operatorImage:    "testImage",
			caseId:           "1234",
			httpProxy:        "testHttpProxy",
			httpsProxy:       "testHttpsProxy",
			noProxy:          "testNoProxy",
			secretKeyRefName: v1.LocalObjectReference{Name: "testSecretKeyRefName"},
		},
		{
			name:             "No http proxy envar",
			operatorImage:    "testImage",
			caseId:           "1234",
			httpsProxy:       "testHttpsProxy",
			noProxy:          "testNoProxy",
			secretKeyRefName: v1.LocalObjectReference{Name: "testSecretKeyRefName"},
		},
		{
			name:             "No https proxy envar",
			operatorImage:    "testImage",
			caseId:           "1234",
			httpProxy:        "testHttpProxy",
			noProxy:          "testNoProxy",
			secretKeyRefName: v1.LocalObjectReference{Name: "testSecretKeyRefName"},
		},
		{
			name:             "No noproxy envar",
			operatorImage:    "testImage",
			caseId:           "1234",
			httpProxy:        "testHttpProxy",
			httpsProxy:       "testHttpsProxy",
			secretKeyRefName: v1.LocalObjectReference{Name: "testSecretKeyRefName"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testFailed := false
			container := getUploadContainer(tt.operatorImage, tt.caseId, tt.internalUser, tt.httpProxy, tt.httpsProxy, tt.noProxy, tt.secretKeyRefName)

			if container.Image != tt.operatorImage {
				t.Logf("expected container image %v but got %v", tt.operatorImage, container.Image)
				testFailed = true
			}

			for _, env := range container.Env {
				switch env.Name {
				case uploadEnvCaseId:
					if env.Value != tt.caseId {
						t.Logf("expected case ID envar %v but got %v", tt.caseId, env.Value)
						testFailed = true
					}
				case uploadEnvInternalUser:
					if env.Value != strconv.FormatBool(tt.internalUser) {
						t.Logf("expected internal user envar %v but got %v", tt.internalUser, env.Value)
						testFailed = true
					}
				case uploadEnvHttpProxy:
					if env.Value != tt.httpProxy {
						t.Logf("expected httpproxy envar %v but got %v", tt.httpProxy, tt.httpProxy)
						testFailed = true
					}
				case uploadEnvHttpsProxy:
					if env.Value != tt.httpsProxy {
						t.Logf("expected httpsproxy envar %v but got %v", tt.httpsProxy, tt.httpsProxy)
						testFailed = true
					}
				case uploadEnvNoProxy:
					if env.Value != tt.noProxy {
						t.Logf("expected noproxy envar %v but got %v", tt.noProxy, tt.noProxy)
					}
				case uploadEnvUsername, uploadEnvPassword:
					if !reflect.DeepEqual(env.ValueFrom.SecretKeyRef.LocalObjectReference, tt.secretKeyRefName) {
						t.Logf("expected %v envar to have secret key ref name %v but got %v", env.Name, tt.secretKeyRefName.Name, env.ValueFrom.SecretKeyRef.Name)
						testFailed = true
					}
				}

				if testFailed {
					t.Error()
				}
			}
		})
	}
}

// helper to find upload container in a job
func findUploadContainerInJob(t *testing.T, job *batchv1.Job) v1.Container {
	t.Helper()
	for _, c := range job.Spec.Template.Spec.Containers {
		if c.Name == uploadContainerName {
			return c
		}
	}
	t.Fatalf("upload container not found in job")
	return v1.Container{}
}

// helper to map env name->value
func envValues(container v1.Container) map[string]string {
	m := make(map[string]string)
	for _, e := range container.Env {
		m[e.Name] = e.Value
	}
	return m
}

func Test_getJobTemplate_FallbackWhenOnlyNoProxyProvidedInCR(t *testing.T) {
	_ = os.Setenv("HTTP_PROXY", "http://env-http:8080")
	_ = os.Setenv("HTTPS_PROXY", "https://env-https:8443")
	_ = os.Setenv("NO_PROXY", "env-no-proxy")
	defer func() {
		_ = os.Unsetenv("HTTP_PROXY")
		_ = os.Unsetenv("HTTPS_PROXY")
		_ = os.Unsetenv("NO_PROXY")
	}()

	mg := mustgatherv1alpha1.MustGather{
		ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns"},
		Spec: mustgatherv1alpha1.MustGatherSpec{
			CaseID:                         "case",
			CaseManagementAccountSecretRef: v1.LocalObjectReference{Name: "sec"},
			ServiceAccountRef:              v1.LocalObjectReference{Name: "sa"},
			ProxyConfig: mustgatherv1alpha1.ProxySpec{
				NoProxy: "cr-no-proxy",
			},
		},
	}

	job := getJobTemplate("img", "4.14.0", mg)
	upload := findUploadContainerInJob(t, job)
	got := envValues(upload)

	if got[uploadEnvHttpProxy] != "http://env-http:8080" {
		t.Fatalf("expected %s from env, got %s", uploadEnvHttpProxy, got[uploadEnvHttpProxy])
	}
	if got[uploadEnvHttpsProxy] != "https://env-https:8443" {
		t.Fatalf("expected %s from env, got %s", uploadEnvHttpsProxy, got[uploadEnvHttpsProxy])
	}
	if got[uploadEnvNoProxy] != "env-no-proxy" {
		t.Fatalf("expected %s from env, got %s", uploadEnvNoProxy, got[uploadEnvNoProxy])
	}
}

func Test_getJobTemplate_NoFallbackWhenHttpAndHttpsProvidedInCR(t *testing.T) {
	_ = os.Setenv("HTTP_PROXY", "http://env-http:8080")
	_ = os.Setenv("HTTPS_PROXY", "https://env-https:8443")
	_ = os.Setenv("NO_PROXY", "env-no-proxy")
	defer func() {
		_ = os.Unsetenv("HTTP_PROXY")
		_ = os.Unsetenv("HTTPS_PROXY")
		_ = os.Unsetenv("NO_PROXY")
	}()

	mg := mustgatherv1alpha1.MustGather{
		ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns"},
		Spec: mustgatherv1alpha1.MustGatherSpec{
			CaseID:                         "case",
			CaseManagementAccountSecretRef: v1.LocalObjectReference{Name: "sec"},
			ServiceAccountRef:              v1.LocalObjectReference{Name: "sa"},
			ProxyConfig: mustgatherv1alpha1.ProxySpec{
				HTTPProxy:  "http://cr-http:8080",
				HTTPSProxy: "https://cr-https:8443",
				// NoProxy intentionally empty
			},
		},
	}

	job := getJobTemplate("img", "4.14.0", mg)
	upload := findUploadContainerInJob(t, job)
	got := envValues(upload)

	if got[uploadEnvHttpProxy] != "http://cr-http:8080" {
		t.Fatalf("expected %s to be CR value, got %s", uploadEnvHttpProxy, got[uploadEnvHttpProxy])
	}
	if got[uploadEnvHttpsProxy] != "https://cr-https:8443" {
		t.Fatalf("expected %s to be CR value, got %s", uploadEnvHttpsProxy, got[uploadEnvHttpsProxy])
	}
	if _, ok := got[uploadEnvNoProxy]; ok {
		t.Fatalf("did not expect %s when CR NoProxy is empty", uploadEnvNoProxy)
	}
}

func Test_getJobTemplate_NoFallbackIfHttpsProvidedButHttpMissing(t *testing.T) {
	_ = os.Setenv("HTTP_PROXY", "http://env-http:8080")
	_ = os.Setenv("HTTPS_PROXY", "https://env-https:8443")
	_ = os.Setenv("NO_PROXY", "env-no-proxy")
	defer func() {
		_ = os.Unsetenv("HTTP_PROXY")
		_ = os.Unsetenv("HTTPS_PROXY")
		_ = os.Unsetenv("NO_PROXY")
	}()

	mg := mustgatherv1alpha1.MustGather{
		ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns"},
		Spec: mustgatherv1alpha1.MustGatherSpec{
			CaseID:                         "case",
			CaseManagementAccountSecretRef: v1.LocalObjectReference{Name: "sec"},
			ServiceAccountRef:              v1.LocalObjectReference{Name: "sa"},
			ProxyConfig: mustgatherv1alpha1.ProxySpec{
				HTTPSProxy: "https://cr-https:8443",
				// HTTPProxy empty to ensure fallback condition is false
			},
		},
	}

	job := getJobTemplate("img", "4.14.0", mg)
	upload := findUploadContainerInJob(t, job)
	got := envValues(upload)

	// http proxy should not be present (no fallback)
	if _, ok := got[uploadEnvHttpProxy]; ok {
		t.Fatalf("did not expect %s when only HTTPS proxy is provided in CR", uploadEnvHttpProxy)
	}
	// https proxy should be from CR
	if got[uploadEnvHttpsProxy] != "https://cr-https:8443" {
		t.Fatalf("expected %s to be CR value, got %s", uploadEnvHttpsProxy, got[uploadEnvHttpsProxy])
	}
}
