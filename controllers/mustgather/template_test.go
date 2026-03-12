package mustgather

import (
	"fmt"
	"math"
	"path"
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

const (
	// well-known dir for ca certificates to be mounted in a container,
	// canonical to `trustedCAMountPath`, de-coupled for test.
	wellKnownCADirForTest = "/etc/pki/tls/certs"
	// canonical to `outputVolumeName`, de-coupled for test.
	knownStorageVolumeMountNameForTest = "must-gather-output"
)

func Test_initializeJobTemplate(t *testing.T) {
	testName := "testName"
	testNamespace := "testNamespace"
	testServiceAccountRef := "testServiceAccountRef"
	pvcClaimName := "test-pvc"
	pvcSubPath := "test-path"

	tests := []struct {
		name        string
		storage     *mustgatherv1alpha1.Storage
		caConfigMap string
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
		{
			name:        "With CA config map",
			caConfigMap: "trusted-ca-cert-001",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := initializeJobTemplate(testName, testNamespace, testServiceAccountRef, tt.storage, tt.caConfigMap)

			if got := job.Name; got != testName {
				t.Fatalf("job name from initializeJobTemplate() was not correctly set. got %v, wanted %v", got, testName)
			}

			if got := job.Namespace; got != testNamespace {
				t.Fatalf("job namespace from initializeJobTemplate() was not correctly set. got %v, wanted %v", got, testNamespace)
			}

			if got := job.Spec.Template.Spec.ServiceAccountName; got != testServiceAccountRef {
				t.Fatalf("job service account name from initializeJobTemplate() was not correctly set. got %v, wanted %v", got, testServiceAccountRef)
			}

			if (tt.storage != nil || tt.caConfigMap != "") && len(job.Spec.Template.Spec.Volumes) == 0 {
				t.Fatalf("expected at least one volume to be present")
			}

			foundStorageVolume := false
			foundCAVolume := false
			for _, v := range job.Spec.Template.Spec.Volumes {
				if v.Name == knownStorageVolumeMountNameForTest {
					foundStorageVolume = true

					if tt.storage != nil && v.PersistentVolumeClaim.ClaimName != tt.storage.PersistentVolume.Claim.Name {
						t.Fatalf("pvc claim name from initializeJobTemplate() was not correctly set. got %v, wanted %v", v.PersistentVolumeClaim.ClaimName, tt.storage.PersistentVolume.Claim.Name)
					}
				}

				if v.ConfigMap != nil && v.ConfigMap.Name == tt.caConfigMap {
					foundCAVolume = true

					if v.ConfigMap.Name != tt.caConfigMap {
						t.Fatalf("config map CA from initializeJobTemplate() was not correctly set. got %v, wanted %v", v.ConfigMap.Name, tt.caConfigMap)
					}
				}
			}

			if tt.storage != nil && !foundStorageVolume {
				t.Fatalf("expected volumeMount for storage was not found got %v", job.Spec.Template.Spec.Volumes)
			}

			if tt.caConfigMap != "" && !foundCAVolume {
				t.Fatalf("expected volumeMount for CA was not found got %v", job.Spec.Template.Spec.Volumes)
			}
		})
	}
}

func Test_getGatherContainer(t *testing.T) {
	testSinceTime := time.Date(2026, 1, 7, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name            string
		audit           bool
		timeout         time.Duration
		mustGatherImage string
		storage         *mustgatherv1alpha1.Storage
		command         []string
		args            []string
		caConfigMap     string
		timeFilter      *GatherTimeFilter
	}{
		{
			name:            "no audit",
			timeout:         5 * time.Second,
			mustGatherImage: "quay.io/foo/bar/must-gather:latest",
		},
		{
			name:            "audit",
			audit:           true,
			timeout:         0 * time.Second,
			mustGatherImage: "quay.io/foo/bar/must-gather:latest",
		},
		{
			name:            "with trusted CA config map",
			timeout:         5 * time.Second,
			mustGatherImage: "quay.io/foo/bar/must-gather:latest",
			caConfigMap:     "trusted-ca-cert-001",
		},
		{
			name:    "with PVC",
			timeout: 5 * time.Second,
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
			name:    "with PVC empty subPath sets subPathExpr to POD_NAME only",
			timeout: 5 * time.Second,
			storage: &mustgatherv1alpha1.Storage{
				Type: mustgatherv1alpha1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1alpha1.PersistentVolumeConfig{
					Claim:   mustgatherv1alpha1.PersistentVolumeClaimReference{Name: "test-pvc"},
					SubPath: "",
				},
			},
		},
		{
			name:    "with PVC whitespace subPath sets subPathExpr to POD_NAME only",
			timeout: 5 * time.Second,
			storage: &mustgatherv1alpha1.Storage{
				Type: mustgatherv1alpha1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1alpha1.PersistentVolumeConfig{
					Claim:   mustgatherv1alpha1.PersistentVolumeClaimReference{Name: "test-pvc"},
					SubPath: "   ",
				},
			},
		},
		{
			name:    "with PVC slash-only subPath sets subPathExpr to POD_NAME only",
			timeout: 5 * time.Second,
			storage: &mustgatherv1alpha1.Storage{
				Type: mustgatherv1alpha1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1alpha1.PersistentVolumeConfig{
					Claim:   mustgatherv1alpha1.PersistentVolumeClaimReference{Name: "test-pvc"},
					SubPath: "/",
				},
			},
		},
		{
			name:            "robust timeout",
			timeout:         1500 * time.Millisecond,
			mustGatherImage: "quay.io/foo/bar/must-gather:latest",
		},
		{
			name:            "custom command and args",
			timeout:         5 * time.Second,
			mustGatherImage: "quay.io/foo/bar/must-gather:latest",
			command:         []string{"/usr/bin/custom-gather"},
			args:            []string{"--verbose", "--subsystem=network"},
		},
		{
			name:            "with since duration",
			timeout:         5 * time.Second,
			mustGatherImage: "quay.io/foo/bar/must-gather:latest",
			timeFilter: &GatherTimeFilter{
				Since: 2 * time.Hour,
			},
		},
		{
			name:            "with sinceTime",
			timeout:         5 * time.Second,
			mustGatherImage: "quay.io/foo/bar/must-gather:latest",
			timeFilter: &GatherTimeFilter{
				SinceTime: &testSinceTime,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			container := getGatherContainer(tt.mustGatherImage, tt.audit, tt.timeout, tt.storage, tt.caConfigMap, tt.timeFilter, tt.command, tt.args)

			if len(tt.command) == 0 {
				containerCommand := container.Command[2]
				if tt.audit && !strings.Contains(containerCommand, gatherCommandBinaryAudit) {
					t.Fatalf("gather container command expected with binary %v but it wasn't present", gatherCommandBinaryAudit)
				} else if !tt.audit && !strings.Contains(containerCommand, gatherCommandBinaryNoAudit) {
					t.Fatalf("gather container command expected with binary %v but it wasn't present", gatherCommandBinaryNoAudit)
				}
				timeoutInSeconds := int(math.Ceil(tt.timeout.Seconds()))
				if !strings.HasPrefix(containerCommand, fmt.Sprintf("timeout %d", timeoutInSeconds)) {
					t.Fatalf("the duration was not properly added to the container command, got %v but wanted %v", strings.Split(containerCommand, " ")[1], timeoutInSeconds)
				}
			} else {
				if !reflect.DeepEqual(container.Command, tt.command) {
					t.Fatalf("expected container command %v but got %v", tt.command, container.Command)
				}
				if !reflect.DeepEqual(container.Args, tt.args) {
					t.Fatalf("expected container args %v but got %v", tt.args, container.Args)
				}
			}

			if container.Image != tt.mustGatherImage {
				t.Fatalf("expected container image %v but got %v", tt.mustGatherImage, container.Image)
			}

			// Check trusted CA configmap volume mount behavior
			foundTrustedCAMount := false
			for _, vm := range container.VolumeMounts {
				if vm.Name == trustedCAVolumeName {
					foundTrustedCAMount = true
					if vm.MountPath != wellKnownCADirForTest {
						t.Fatalf("trusted CA volume mount path was not correctly set. got %v, wanted %v", vm.MountPath, wellKnownCADirForTest)
					}
					if !vm.ReadOnly {
						t.Fatalf("trusted CA volume mount expected to be read-only")
					}
				}
			}
			if tt.caConfigMap != "" && !foundTrustedCAMount {
				t.Fatalf("expected trusted CA volume mount to be present when caConfigMap is provided")
			}
			if tt.caConfigMap == "" && foundTrustedCAMount {
				t.Fatalf("did not expect trusted CA volume mount when caConfigMap is empty")
			}

			if tt.storage != nil {
				if len(container.VolumeMounts) == 0 {
					t.Fatalf("expected at least one volume mount when storage is provided")
				}
				volumeMount := container.VolumeMounts[0]
				if volumeMount.Name != outputVolumeName {
					t.Fatalf("volume mount name was not correctly set. got %v, wanted %v", volumeMount.Name, outputVolumeName)
				}
				base := strings.Trim(strings.TrimSpace(tt.storage.PersistentVolume.SubPath), "/")
				wantExpr := path.Join(base, fmt.Sprintf("$(%s)", podNameEnvVar))
				if volumeMount.SubPathExpr != wantExpr {
					t.Fatalf("volume mount subPathExpr was not correctly set. got %q, wanted %q", volumeMount.SubPathExpr, wantExpr)
				}
				if volumeMount.SubPath != "" {
					t.Fatalf("did not expect volume mount subPath to be set when using subPathExpr, got %q", volumeMount.SubPath)
				}
			}

			// POD_NAME env var should be present only when SubPathExpr is used.
			hasPodNameEnv := false
			for _, env := range container.Env {
				if env.Name == podNameEnvVar {
					hasPodNameEnv = true
					if env.ValueFrom == nil || env.ValueFrom.FieldRef == nil || env.ValueFrom.FieldRef.FieldPath != "metadata.name" {
						t.Fatalf("expected %s env var to be sourced from metadata.name via fieldRef, got %#v", podNameEnvVar, env)
					}
				}
			}
			// SubPathExpr is always set for PVC storage (for per-pod isolation), so POD_NAME env is always present.
			hasPVCStorage := tt.storage != nil && tt.storage.Type == mustgatherv1alpha1.StorageTypePersistentVolume
			if hasPVCStorage && !hasPodNameEnv {
				t.Fatalf("expected %s env var when PVC storage is used (SubPathExpr is set)", podNameEnvVar)
			}
			if !hasPVCStorage && hasPodNameEnv {
				t.Fatalf("did not expect %s env var when storage is not PVC", podNameEnvVar)
			}

			// Check time filter environment variables
			if tt.timeFilter != nil {
				envMap := envValues(container)
				if tt.timeFilter.Since > 0 {
					if envMap[gatherEnvSince] != tt.timeFilter.Since.String() {
						t.Fatalf("expected %s env var to be %v, got %v", gatherEnvSince, tt.timeFilter.Since.String(), envMap[gatherEnvSince])
					}
				}
				if tt.timeFilter.SinceTime != nil {
					expectedTime := tt.timeFilter.SinceTime.Format(time.RFC3339)
					if envMap[gatherEnvSinceTime] != expectedTime {
						t.Fatalf("expected %s env var to be %v, got %v", gatherEnvSinceTime, expectedTime, envMap[gatherEnvSinceTime])
					}
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
		storage          *mustgatherv1alpha1.Storage
		httpProxy        string
		httpsProxy       string
		noProxy          string
		mountCAConfigMap bool
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
		{
			name:             "With trusted CA config map",
			operatorImage:    "testImage",
			caseId:           "1234",
			httpProxy:        "testHttpProxy",
			httpsProxy:       "testHttpsProxy",
			secretKeyRefName: v1.LocalObjectReference{Name: "testSecretKeyRefName"},
			mountCAConfigMap: true,
		},
		{
			name:          "With PVC subPath",
			operatorImage: "testImage",
			caseId:        "1234",
			secretKeyRefName: v1.LocalObjectReference{
				Name: "testSecretKeyRefName",
			},
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
			name:          "With PVC empty subPath sets subPathExpr to POD_NAME only",
			operatorImage: "testImage",
			caseId:        "1234",
			secretKeyRefName: v1.LocalObjectReference{
				Name: "testSecretKeyRefName",
			},
			storage: &mustgatherv1alpha1.Storage{
				Type: mustgatherv1alpha1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1alpha1.PersistentVolumeConfig{
					Claim:   mustgatherv1alpha1.PersistentVolumeClaimReference{Name: "test-pvc"},
					SubPath: "",
				},
			},
		},
		{
			name:          "With PVC whitespace subPath sets subPathExpr to POD_NAME only",
			operatorImage: "testImage",
			caseId:        "1234",
			secretKeyRefName: v1.LocalObjectReference{
				Name: "testSecretKeyRefName",
			},
			storage: &mustgatherv1alpha1.Storage{
				Type: mustgatherv1alpha1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1alpha1.PersistentVolumeConfig{
					Claim:   mustgatherv1alpha1.PersistentVolumeClaimReference{Name: "test-pvc"},
					SubPath: "   ",
				},
			},
		},
		{
			name:          "With PVC slash-only subPath sets subPathExpr to POD_NAME only",
			operatorImage: "testImage",
			caseId:        "1234",
			secretKeyRefName: v1.LocalObjectReference{
				Name: "testSecretKeyRefName",
			},
			storage: &mustgatherv1alpha1.Storage{
				Type: mustgatherv1alpha1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1alpha1.PersistentVolumeConfig{
					Claim:   mustgatherv1alpha1.PersistentVolumeClaimReference{Name: "test-pvc"},
					SubPath: "/",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testFailed := false
			container := getUploadContainer(tt.operatorImage, tt.caseId, tt.host, tt.internalUser, tt.storage, tt.httpProxy, tt.httpsProxy, tt.noProxy, tt.secretKeyRefName, tt.mountCAConfigMap)

			if container.Image != tt.operatorImage {
				t.Fatalf("expected container image %v but got %v", tt.operatorImage, container.Image)
			}

			if tt.mountCAConfigMap {
				mountedCAExists := false
				for _, vm := range container.VolumeMounts {
					if vm.MountPath == wellKnownCADirForTest {
						mountedCAExists = true
					}
				}

				if !mountedCAExists {
					t.Fatalf("expected a CA cert volumeMount in upload container")
				}
			}

			if tt.storage != nil && tt.storage.Type == mustgatherv1alpha1.StorageTypePersistentVolume {
				var outputMount *v1.VolumeMount
				for i := range container.VolumeMounts {
					if container.VolumeMounts[i].Name == outputVolumeName {
						outputMount = &container.VolumeMounts[i]
						break
					}
				}
				if outputMount == nil {
					t.Fatalf("expected output volume mount %q to be present", outputVolumeName)
				}
				base := strings.Trim(strings.TrimSpace(tt.storage.PersistentVolume.SubPath), "/")
				wantExpr := path.Join(base, fmt.Sprintf("$(%s)", podNameEnvVar))
				if outputMount.SubPathExpr != wantExpr {
					t.Fatalf("expected output volume mount subPathExpr %q but got %q", wantExpr, outputMount.SubPathExpr)
				}
				if outputMount.SubPath != "" {
					t.Fatalf("did not expect output volume mount subPath to be set when using subPathExpr, got %q", outputMount.SubPath)
				}
			}

			// POD_NAME env var is present when SubPathExpr is used (always for PVC storage).
			hasPodNameEnv := false
			for _, env := range container.Env {
				if env.Name == podNameEnvVar {
					hasPodNameEnv = true
					if env.ValueFrom == nil || env.ValueFrom.FieldRef == nil || env.ValueFrom.FieldRef.FieldPath != "metadata.name" {
						t.Fatalf("expected %s env var to be sourced from metadata.name via fieldRef, got %#v", podNameEnvVar, env)
					}
				}
			}
			hasPVCStorage := tt.storage != nil && tt.storage.Type == mustgatherv1alpha1.StorageTypePersistentVolume
			if hasPVCStorage && !hasPodNameEnv {
				t.Fatalf("expected %s env var when PVC storage is used (SubPathExpr is set)", podNameEnvVar)
			}
			if !hasPVCStorage && hasPodNameEnv {
				t.Fatalf("did not expect %s env var when storage is not PVC", podNameEnvVar)
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

func Test_getJobTemplate_GatherSpec_BuildsTimeFilter(t *testing.T) {
	t.Setenv(DefaultMustGatherImageEnv, "quay.io/foo/bar/must-gather:latest")

	sinceTime := metav1.NewTime(time.Date(2026, 1, 7, 10, 11, 12, 0, time.UTC))

	tests := []struct {
		name        string
		gatherSpec  *mustgatherv1alpha1.GatherSpec
		wantSince   string
		wantSinceTs string
	}{
		{
			name: "no gatherSpec means no time filter env vars",
		},
		{
			name:       "gatherSpec with since builds timeFilter.Since",
			gatherSpec: &mustgatherv1alpha1.GatherSpec{Since: &metav1.Duration{Duration: 2 * time.Hour}},
			wantSince:  "2h0m0s",
		},
		{
			name:        "gatherSpec with sinceTime builds timeFilter.SinceTime",
			gatherSpec:  &mustgatherv1alpha1.GatherSpec{SinceTime: &sinceTime},
			wantSinceTs: "2026-01-07T10:11:12Z",
		},
		{
			name:       "gatherSpec present but with no since/sinceTime means no time filter env vars",
			gatherSpec: &mustgatherv1alpha1.GatherSpec{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mg := mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns"},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: "default",
					GatherSpec:         tt.gatherSpec,
				},
			}

			job := getJobTemplate("img", "operator-image", mg, "")
			gather := findGatherContainerInJob(t, job)
			got := envValues(gather)

			if tt.wantSince == "" {
				if _, ok := got[gatherEnvSince]; ok {
					t.Fatalf("did not expect %s env var, got %v", gatherEnvSince, got[gatherEnvSince])
				}
			} else if got[gatherEnvSince] != tt.wantSince {
				t.Fatalf("expected %s=%s, got %s", gatherEnvSince, tt.wantSince, got[gatherEnvSince])
			}

			if tt.wantSinceTs == "" {
				if _, ok := got[gatherEnvSinceTime]; ok {
					t.Fatalf("did not expect %s env var, got %v", gatherEnvSinceTime, got[gatherEnvSinceTime])
				}
			} else if got[gatherEnvSinceTime] != tt.wantSinceTs {
				t.Fatalf("expected %s=%s, got %s", gatherEnvSinceTime, tt.wantSinceTs, got[gatherEnvSinceTime])
			}
		})
	}
}

func Test_getJobTemplate_ProxyAuditTimeout(t *testing.T) {
	t.Setenv(DefaultMustGatherImageEnv, "quay.io/foo/bar/must-gather:latest")

	timeout := metav1.Duration{Duration: 5 * time.Second}

	tests := []struct {
		name        string
		audit       bool
		timeout     *metav1.Duration
		httpProxy   string
		httpsProxy  string
		noProxy     string
		wantAudit   bool
		wantTimeout string
		wantProxies bool
	}{
		{
			name:        "audit false and nil timeout default; no proxy env vars",
			wantAudit:   false,
			wantTimeout: "timeout 0",
			wantProxies: false,
		},
		{
			name:        "audit true and timeout set; proxy env vars propagate to upload container",
			audit:       true,
			timeout:     &timeout,
			httpProxy:   "http://proxy.example:8080",
			httpsProxy:  "https://proxy.example:8443",
			noProxy:     "127.0.0.1,localhost,.cluster.local",
			wantAudit:   true,
			wantTimeout: "timeout 5",
			wantProxies: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Always set proxy vars per test case to avoid leakage from host env.
			t.Setenv("HTTP_PROXY", tt.httpProxy)
			t.Setenv("HTTPS_PROXY", tt.httpsProxy)
			t.Setenv("NO_PROXY", tt.noProxy)

			mg := mustgatherv1alpha1.MustGather{
				ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns"},
				Spec: mustgatherv1alpha1.MustGatherSpec{
					ServiceAccountName: "default",
					MustGatherTimeout:  tt.timeout,
					GatherSpec: &mustgatherv1alpha1.GatherSpec{
						Audit: tt.audit,
					},
					UploadTarget: &mustgatherv1alpha1.UploadTargetSpec{
						Type: mustgatherv1alpha1.UploadTypeSFTP,
						SFTP: &mustgatherv1alpha1.SFTPSpec{
							CaseID: "1234",
							Host:   "sftp.example.com",
							CaseManagementAccountSecretRef: v1.LocalObjectReference{
								Name: "case-mgmt-secret",
							},
						},
					},
				},
			}

			job := getJobTemplate("image", "operator-image", mg, "")

			gather := findGatherContainerInJob(t, job)
			gatherCmd := gather.Command[2]
			if tt.wantAudit {
				if !strings.Contains(gatherCmd, gatherCommandBinaryAudit) {
					t.Fatalf("expected gather command to contain %v but got %v", gatherCommandBinaryAudit, gatherCmd)
				}
			} else {
				if !strings.Contains(gatherCmd, gatherCommandBinaryNoAudit) {
					t.Fatalf("expected gather command to contain %v but got %v", gatherCommandBinaryNoAudit, gatherCmd)
				}
			}
			if !strings.HasPrefix(gatherCmd, tt.wantTimeout) {
				t.Fatalf("expected gather command to start with %q but got %q", tt.wantTimeout, gatherCmd)
			}

			upload := findUploadContainerInJob(t, job)
			uploadEnv := envValues(upload)
			if tt.wantProxies {
				if uploadEnv[uploadEnvHttpProxy] != tt.httpProxy {
					t.Fatalf("expected %s=%v, got %v", uploadEnvHttpProxy, tt.httpProxy, uploadEnv[uploadEnvHttpProxy])
				}
				if uploadEnv[uploadEnvHttpsProxy] != tt.httpsProxy {
					t.Fatalf("expected %s=%v, got %v", uploadEnvHttpsProxy, tt.httpsProxy, uploadEnv[uploadEnvHttpsProxy])
				}
				if uploadEnv[uploadEnvNoProxy] != tt.noProxy {
					t.Fatalf("expected %s=%v, got %v", uploadEnvNoProxy, tt.noProxy, uploadEnv[uploadEnvNoProxy])
				}
			} else {
				if _, ok := uploadEnv[uploadEnvHttpProxy]; ok {
					t.Fatalf("did not expect %s env var, got %v", uploadEnvHttpProxy, uploadEnv[uploadEnvHttpProxy])
				}
				if _, ok := uploadEnv[uploadEnvHttpsProxy]; ok {
					t.Fatalf("did not expect %s env var, got %v", uploadEnvHttpsProxy, uploadEnv[uploadEnvHttpsProxy])
				}
				if _, ok := uploadEnv[uploadEnvNoProxy]; ok {
					t.Fatalf("did not expect %s env var, got %v", uploadEnvNoProxy, uploadEnv[uploadEnvNoProxy])
				}
			}
		})
	}
}

// helper to find gather container in a job
func findGatherContainerInJob(t *testing.T, job *batchv1.Job) v1.Container {
	t.Helper()
	for _, c := range job.Spec.Template.Spec.Containers {
		if c.Name == gatherContainerName {
			return c
		}
	}
	t.Fatalf("gather container not found in job")
	return v1.Container{}
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
