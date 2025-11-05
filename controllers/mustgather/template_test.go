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
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_initializeJobTemplate(t *testing.T) {
	testName := "testName"
	testNamespace := "testNamespace"
	testServiceAccountRef := "testServiceAccountRef"
	pvcClaimName := "test-pvc"
	pvcSubPath := "test-path"

	tests := []struct {
		name    string
		storage *mustgatherv1alpha1.Storage
	}{
		{
			name: "Without PVC",
		},
		{
			name: "With PVC",
			storage: &mustgatherv1alpha1.Storage{
				Type: mustgatherv1alpha1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1alpha1.PersistentVolumeConfig{
					Claim: mustgatherv1alpha1.PersistentVolumeClaimReference{
						Name: pvcClaimName,
					},
					SubPath: pvcSubPath,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := initializeJobTemplate(testName, testNamespace, testServiceAccountRef, tt.storage)

			if got := job.Name; got != testName {
				t.Fatalf("job name from initializeJobTemplate() was not correctly set. got %v, wanted %v", got, testName)
			}

			if got := job.Namespace; got != testNamespace {
				t.Fatalf("job namespace from initializeJobTemplate() was not correctly set. got %v, wanted %v", got, testNamespace)
			}

			if got := job.Spec.Template.Spec.ServiceAccountName; got != testServiceAccountRef {
				t.Fatalf("job service account name from initializeJobTemplate() was not correctly set. got %v, wanted %v", got, testServiceAccountRef)
			}

			if tt.storage != nil {
				if len(job.Spec.Template.Spec.Volumes) == 0 {
					t.Fatalf("expected at least one volume to be present")
				}
				volume := job.Spec.Template.Spec.Volumes[0]
				if volume.Name != outputVolumeName {
					t.Fatalf("volume name from initializeJobTemplate() was not correctly set. got %v, wanted %v", volume.Name, outputVolumeName)
				}
				if volume.PersistentVolumeClaim.ClaimName != pvcClaimName {
					t.Fatalf("pvc claim name from initializeJobTemplate() was not correctly set. got %v, wanted %v", volume.PersistentVolumeClaim.ClaimName, pvcClaimName)
				}
			}
		})
	}
}

func Test_getGatherContainer(t *testing.T) {

	testDefaultMustGatherImage := "quay.io/foo/bar/must-gather:latest"
	testAcmHcpMustGatherImage := "quay.io/acm/hcp/must-gather:latest"

	tests := []struct {
		name                 string
		audit                bool
		timeout              time.Duration
		mustGatherImage      string
		storage              *mustgatherv1alpha1.Storage
		hostedClusterOptions mustgatherv1alpha1.HostedClusterOptions
		expectedImage        string
		expectedArgs         string
	}{
		{
			name:            "no audit",
			timeout:         5 * time.Second,
			mustGatherImage: defaultMustGatherImageEnv,
			expectedImage:   testDefaultMustGatherImage,
		},
		{
			name:            "audit",
			audit:           true,
			timeout:         0 * time.Second,
			mustGatherImage: defaultMustGatherImageEnv,
			expectedImage:   testDefaultMustGatherImage,
		},
		{
			name:            "HCP default",
			timeout:         5 * time.Second,
			mustGatherImage: acmHcpMustGatherImage,
			hostedClusterOptions: mustgatherv1alpha1.HostedClusterOptions{
				HostedClusterName:      "foo",
				HostedClusterNamespace: "bar",
			},
			expectedImage: testAcmHcpMustGatherImage,
			expectedArgs:  "--hosted-cluster-namespace=bar --hosted-cluster-name=foo",
		},
		{
			name:            "with PVC",
			timeout:         5 * time.Second,
			mustGatherImage: defaultMustGatherImageEnv,
			expectedImage:   testDefaultMustGatherImage,
			storage: &mustgatherv1alpha1.Storage{
				Type: mustgatherv1alpha1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1alpha1.PersistentVolumeConfig{
					Claim: mustgatherv1alpha1.PersistentVolumeClaimReference{
						Name: "test-pvc",
					},
					SubPath: "test-path",
				},
			},
		},
		{
			name:            "robust timeout",
			timeout:         6*time.Hour + 5*time.Minute + 3*time.Second, // 6h5m3s
			mustGatherImage: defaultMustGatherImageEnv,
			expectedImage:   testDefaultMustGatherImage,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(defaultMustGatherImageEnv, testDefaultMustGatherImage)
			t.Setenv(acmHcpMustGatherImageEnv, testAcmHcpMustGatherImage)

			container := getGatherContainer(tt.audit, tt.timeout, tt.storage, tt.mustGatherImage, &tt.hostedClusterOptions)

			containerCommand := container.Command[2]
			if tt.audit && !strings.Contains(containerCommand, gatherCommandBinaryAudit) {
				t.Fatalf("gather container command expected with binary %v but it wasn't present", gatherCommandBinaryAudit)
			} else if !tt.audit && !strings.Contains(containerCommand, gatherCommandBinaryNoAudit) {
				t.Fatalf("gather container command expected with binary %v but it wasn't present", gatherCommandBinaryNoAudit)
			}

			timeoutInSeconds := int(tt.timeout.Seconds())
			if !strings.HasPrefix(containerCommand, fmt.Sprintf("timeout %d", timeoutInSeconds)) {
				t.Fatalf("the duration was not properly added to the container command, got %v but wanted %v", strings.Split(containerCommand, " ")[1], timeoutInSeconds)
			}

			if tt.expectedArgs != "" {
				if !strings.Contains(containerCommand, tt.expectedArgs) {
					t.Fatalf("command did not contain expected arguments: %v", tt.expectedArgs)

				}
			}

			if container.Image != tt.expectedImage {
				t.Fatalf("expected container image %v but got %v", tt.expectedImage, container.Image)
			}

			if tt.storage != nil {
				if len(container.VolumeMounts) == 0 {
					t.Fatalf("expected at least one volume mount when storage is provided")
				}
				volumeMount := container.VolumeMounts[0]
				if volumeMount.Name != outputVolumeName {
					t.Fatalf("volume mount name was not correctly set. got %v, wanted %v", volumeMount.Name, outputVolumeName)
				}
				if volumeMount.SubPath != tt.storage.PersistentVolume.SubPath {
					t.Fatalf("volume mount subpath was not correctly set. got %v, wanted %v", volumeMount.SubPath, tt.storage.PersistentVolume.SubPath)
				}
			}
		})
	}
}

func Test_getUploadContainer(t *testing.T) {
	tests := []struct {
		name             string
		operatorImage    string
		caseId           string
		host             string
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
			host:             "sftp.example.com",
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
			container := getUploadContainer(tt.operatorImage, tt.caseId, tt.host, tt.internalUser, tt.httpProxy, tt.httpsProxy, tt.noProxy, tt.secretKeyRefName)

			if container.Image != tt.operatorImage {
				t.Fatalf("expected container image %v but got %v", tt.operatorImage, container.Image)
			}

			for _, env := range container.Env {
				switch env.Name {
				case uploadEnvCaseId:
					if env.Value != tt.caseId {
						t.Fatalf("expected case ID envar %v but got %v", tt.caseId, env.Value)
					}
				case uploadEnvHost:
					if env.Value != tt.host {
						t.Fatalf("expected host envar %v but got %v", tt.host, env.Value)
					}
				case uploadEnvInternalUser:
					if env.Value != strconv.FormatBool(tt.internalUser) {
						t.Fatalf("expected internal user envar %v but got %v", tt.internalUser, env.Value)
					}
				case uploadEnvHttpProxy:
					if env.Value != tt.httpProxy {
						t.Fatalf("expected httpproxy envar %v but got %v", tt.httpProxy, env.Value)
					}
				case uploadEnvHttpsProxy:
					if env.Value != tt.httpsProxy {
						t.Fatalf("expected httpsproxy envar %v but got %v", tt.httpsProxy, env.Value)
					}
				case uploadEnvNoProxy:
					if env.Value != tt.noProxy {
						t.Fatalf("expected noproxy envar %v but got %v", tt.noProxy, env.Value)
					}
				case uploadEnvUsername, uploadEnvPassword:
					if !reflect.DeepEqual(env.ValueFrom.SecretKeyRef.LocalObjectReference, tt.secretKeyRefName) {
						t.Fatalf("expected %v envar to have secret key ref name %v but got %v", env.Name, tt.secretKeyRefName.Name, env.ValueFrom.SecretKeyRef.Name)
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
			ServiceAccountName: "default",
			MustGatherTimeout:  &metav1.Duration{Duration: 1 * time.Hour},
			ProxyConfig: &mustgatherv1alpha1.ProxySpec{
				NoProxy: "cr-no-proxy",
			},
			UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
				Type: mustgatherv1alpha1.UploadTypeSFTP,
				SFTP: &mustgatherv1alpha1.SFTPSpec{
					CaseID:                         "case",
					CaseManagementAccountSecretRef: v1.LocalObjectReference{Name: "sec"},
				},
			},
		},
	}

	job := getJobTemplate("img", mg)
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
			ServiceAccountName: "default",
			MustGatherTimeout:  &metav1.Duration{Duration: 1 * time.Hour},
			ProxyConfig: &mustgatherv1alpha1.ProxySpec{
				HTTPProxy:  "http://cr-http:8080",
				HTTPSProxy: "https://cr-https:8443",
				// NoProxy intentionally empty
			},
			UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
				Type: mustgatherv1alpha1.UploadTypeSFTP,
				SFTP: &mustgatherv1alpha1.SFTPSpec{
					CaseID:                         "case",
					CaseManagementAccountSecretRef: v1.LocalObjectReference{Name: "sec"},
				},
			},
		},
	}

	job := getJobTemplate("img", mg)
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
			ServiceAccountName: "default",
			MustGatherTimeout:  &metav1.Duration{Duration: 1 * time.Hour},
			ProxyConfig: &mustgatherv1alpha1.ProxySpec{
				HTTPSProxy: "https://cr-https:8443",
				// HTTPProxy empty to ensure fallback condition is false
			},
			UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
				Type: mustgatherv1alpha1.UploadTypeSFTP,
				SFTP: &mustgatherv1alpha1.SFTPSpec{
					CaseID:                         "case",
					CaseManagementAccountSecretRef: v1.LocalObjectReference{Name: "sec"},
				},
			},
		},
	}

	job := getJobTemplate("img", mg)
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
