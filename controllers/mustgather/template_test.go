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

	mustgatherv1 "github.com/openshift/must-gather-operator/api/v1"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
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
	pvcSubPath := ptr.To("test-path")

	tests := []struct {
		name        string
		storage     *mustgatherv1.Storage
		caConfigMap string
	}{
		{
			name: "Without PVC",
		},
		{
			name: "With PVC",
			storage: &mustgatherv1.Storage{
				Type: mustgatherv1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1.PersistentVolumeConfig{
					Claim: mustgatherv1.PersistentVolumeClaimReference{
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
			job := initializeJobTemplate(testName, testNamespace, testServiceAccountRef, tt.storage, tt.caConfigMap, nil)

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
	testDirName := "must-gather.local.456789abcdef.20260617T143025Z.042315"

	tests := []struct {
		name            string
		audit           bool
		timeout         time.Duration
		mustGatherImage string
		storage         *mustgatherv1.Storage
		command         []string
		args            []string
		caConfigMap     string
		timeFilter      *GatherTimeFilter
		directoryName   string
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
			name:    "with PVC and directory name",
			timeout: 5 * time.Second,
			storage: &mustgatherv1.Storage{
				Type: mustgatherv1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1.PersistentVolumeConfig{
					Claim: mustgatherv1.PersistentVolumeClaimReference{
						Name: "test-pvc",
					},
					SubPath: ptr.To("test-path"),
				},
			},
			directoryName: testDirName,
		},
		{
			name:    "with PVC empty subPath uses directory name only",
			timeout: 5 * time.Second,
			storage: &mustgatherv1.Storage{
				Type: mustgatherv1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1.PersistentVolumeConfig{
					Claim:   mustgatherv1.PersistentVolumeClaimReference{Name: "test-pvc"},
					SubPath: ptr.To(""),
				},
			},
			directoryName: testDirName,
		},
		{
			name:    "with PVC whitespace subPath uses directory name only",
			timeout: 5 * time.Second,
			storage: &mustgatherv1.Storage{
				Type: mustgatherv1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1.PersistentVolumeConfig{
					Claim:   mustgatherv1.PersistentVolumeClaimReference{Name: "test-pvc"},
					SubPath: ptr.To("   "),
				},
			},
			directoryName: testDirName,
		},
		{
			name:    "with PVC slash-only subPath uses directory name only",
			timeout: 5 * time.Second,
			storage: &mustgatherv1.Storage{
				Type: mustgatherv1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1.PersistentVolumeConfig{
					Claim:   mustgatherv1.PersistentVolumeClaimReference{Name: "test-pvc"},
					SubPath: ptr.To("/"),
				},
			},
			directoryName: testDirName,
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
			container := getGatherContainer(tt.mustGatherImage, tt.audit, tt.timeout, tt.storage, tt.caConfigMap, tt.timeFilter, tt.command, tt.args, tt.directoryName, nil, false)

			if len(tt.command) == 0 {
				containerCommand := container.Command[2]
				if tt.audit && !strings.Contains(containerCommand, gatherCommandBinaryAudit) {
					t.Fatalf("gather container command expected with binary %v but it wasn't present", gatherCommandBinaryAudit)
				} else if !tt.audit && !strings.Contains(containerCommand, gatherCommandBinaryNoAudit) {
					t.Fatalf("gather container command expected with binary %v but it wasn't present", gatherCommandBinaryNoAudit)
				}
				timeoutInSeconds := int(math.Ceil(tt.timeout.Seconds()))
				if !strings.Contains(containerCommand, fmt.Sprintf("timeout %d", timeoutInSeconds)) {
					t.Fatalf("the duration was not properly added to the container command, got %v but wanted %v", containerCommand, timeoutInSeconds)
				}
				if !strings.HasPrefix(containerCommand, "set -o pipefail") {
					t.Fatalf("expected gather command to start with pipefail, got %q", containerCommand)
				}
				if !strings.Contains(containerCommand, gatherExitCodeFile) {
					t.Fatalf("expected gather command to write exit code marker to %s", gatherExitCodeFile)
				}
				if !strings.Contains(containerCommand, "gather_rc=$?") {
					t.Fatalf("expected gather command to capture exit code via gather_rc=$?")
				}
				if !strings.Contains(containerCommand, "gather_rc=0") {
					t.Fatalf("expected gather command to normalize timeout exit code via gather_rc=0")
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
				base := strings.Trim(strings.TrimSpace(derefString(tt.storage.PersistentVolume.SubPath)), "/")
				wantSubPath := path.Join(base, tt.directoryName)
				if volumeMount.SubPath != wantSubPath {
					t.Fatalf("volume mount subPath was not correctly set. got %q, wanted %q", volumeMount.SubPath, wantSubPath)
				}
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
	testDirName := "must-gather.local.456789abcdef.20260617T143025Z.042315"

	tests := []struct {
		name             string
		operatorImage    string
		caseId           string
		host             *string
		internalUser     *bool
		storage          *mustgatherv1.Storage
		httpProxy        string
		httpsProxy       string
		noProxy          string
		mountCAConfigMap bool
		secretKeyRefName v1.LocalObjectReference
		directoryName    string
	}{
		{
			name:             "All fields present",
			operatorImage:    "testImage",
			caseId:           "1234",
			host:             ptr.To("sftp.example.com"),
			internalUser:     ptr.To(true),
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
			name:          "With PVC subPath and directory name",
			operatorImage: "testImage",
			caseId:        "1234",
			secretKeyRefName: v1.LocalObjectReference{
				Name: "testSecretKeyRefName",
			},
			storage: &mustgatherv1.Storage{
				Type: mustgatherv1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1.PersistentVolumeConfig{
					Claim: mustgatherv1.PersistentVolumeClaimReference{
						Name: "test-pvc",
					},
					SubPath: ptr.To("test-path"),
				},
			},
			directoryName: testDirName,
		},
		{
			name:          "With PVC empty subPath uses directory name only",
			operatorImage: "testImage",
			caseId:        "1234",
			secretKeyRefName: v1.LocalObjectReference{
				Name: "testSecretKeyRefName",
			},
			storage: &mustgatherv1.Storage{
				Type: mustgatherv1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1.PersistentVolumeConfig{
					Claim:   mustgatherv1.PersistentVolumeClaimReference{Name: "test-pvc"},
					SubPath: ptr.To(""),
				},
			},
			directoryName: testDirName,
		},
		{
			name:          "With PVC whitespace subPath uses directory name only",
			operatorImage: "testImage",
			caseId:        "1234",
			secretKeyRefName: v1.LocalObjectReference{
				Name: "testSecretKeyRefName",
			},
			storage: &mustgatherv1.Storage{
				Type: mustgatherv1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1.PersistentVolumeConfig{
					Claim:   mustgatherv1.PersistentVolumeClaimReference{Name: "test-pvc"},
					SubPath: ptr.To("   "),
				},
			},
			directoryName: testDirName,
		},
		{
			name:          "With PVC slash-only subPath uses directory name only",
			operatorImage: "testImage",
			caseId:        "1234",
			secretKeyRefName: v1.LocalObjectReference{
				Name: "testSecretKeyRefName",
			},
			storage: &mustgatherv1.Storage{
				Type: mustgatherv1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1.PersistentVolumeConfig{
					Claim:   mustgatherv1.PersistentVolumeClaimReference{Name: "test-pvc"},
					SubPath: ptr.To("/"),
				},
			},
			directoryName: testDirName,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testFailed := false
			sftp := &mustgatherv1.SFTPSpec{
				CaseID:                         tt.caseId,
				Host:                           tt.host,
				InternalUser:                   tt.internalUser,
				CaseManagementAccountSecretRef: tt.secretKeyRefName,
			}
			container := getUploadContainer(tt.operatorImage, tt.storage, tt.httpProxy, tt.httpsProxy, tt.noProxy, tt.mountCAConfigMap, sftp, nil, tt.directoryName)

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

			if tt.storage != nil && tt.storage.Type == mustgatherv1.StorageTypePersistentVolume {
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
				base := strings.Trim(strings.TrimSpace(derefString(tt.storage.PersistentVolume.SubPath)), "/")
				wantSubPath := path.Join(base, tt.directoryName)
				if outputMount.SubPath != wantSubPath {
					t.Fatalf("expected output volume mount subPath %q but got %q", wantSubPath, outputMount.SubPath)
				}
			}

			for _, env := range container.Env {
				switch env.Name {
				case uploadEnvCaseId:
					if env.Value != tt.caseId {
						t.Fatalf("expected case ID envar %v but got %v", tt.caseId, env.Value)
					}
				case uploadEnvHost:
					if env.Value != derefString(tt.host) {
						t.Fatalf("expected host envar %v but got %v", derefString(tt.host), env.Value)
					}
				case uploadEnvInternalUser:
					if env.Value != strconv.FormatBool(ptr.Deref(tt.internalUser, false)) {
						t.Fatalf("expected internal user envar %v but got %v", ptr.Deref(tt.internalUser, false), env.Value)
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
		gatherSpec  *mustgatherv1.GatherSpec
		wantSince   string
		wantSinceTs string
	}{
		{
			name: "no gatherSpec means no time filter env vars",
		},
		{
			name:       "gatherSpec with since builds timeFilter.Since",
			gatherSpec: &mustgatherv1.GatherSpec{Since: &metav1.Duration{Duration: 2 * time.Hour}},
			wantSince:  "2h0m0s",
		},
		{
			name:        "gatherSpec with sinceTime builds timeFilter.SinceTime",
			gatherSpec:  &mustgatherv1.GatherSpec{SinceTime: &sinceTime},
			wantSinceTs: "2026-01-07T10:11:12Z",
		},
		{
			name:       "gatherSpec present but with no since/sinceTime means no time filter env vars",
			gatherSpec: &mustgatherv1.GatherSpec{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mg := mustgatherv1.MustGather{
				ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns"},
				Spec: mustgatherv1.MustGatherSpec{
					ServiceAccountName: "default",
					GatherSpec:         tt.gatherSpec,
				},
			}

			job := getJobTemplate("img", "operator-image", mg, "", "must-gather.local.test.20240101T120000Z.000001")
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
		audit       *bool
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
			audit:       ptr.To(true),
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

			mg := mustgatherv1.MustGather{
				ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns"},
				Spec: mustgatherv1.MustGatherSpec{
					ServiceAccountName: "default",
					MustGatherTimeout:  tt.timeout,
					GatherSpec: &mustgatherv1.GatherSpec{
						Audit: tt.audit,
					},
					UploadTarget: &mustgatherv1.UploadTargetSpec{
						Type: mustgatherv1.UploadTypeSFTP,
						SFTP: &mustgatherv1.SFTPSpec{
							CaseID: "1234",
							Host:   ptr.To("sftp.example.com"),
							CaseManagementAccountSecretRef: v1.LocalObjectReference{
								Name: "case-mgmt-secret",
							},
						},
					},
				},
			}

			job := getJobTemplate("image", "operator-image", mg, "", "must-gather.local.test.20240101T120000Z.000001")

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
			if !strings.Contains(gatherCmd, tt.wantTimeout) {
				t.Fatalf("expected gather command to contain %q but got %q", tt.wantTimeout, gatherCmd)
			}
			if !strings.Contains(gatherCmd, "set -o pipefail") {
				t.Fatalf("expected gather command to start with pipefail, got %q", gatherCmd)
			}
			if !strings.Contains(gatherCmd, gatherExitCodeFile) {
				t.Fatalf("expected gather command to write exit code marker, got %q", gatherCmd)
			}
			if !strings.Contains(gatherCmd, "gather_rc=$?") {
				t.Fatalf("expected gather command to capture exit code, got %q", gatherCmd)
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

			uploadCmd := upload.Command[2]
			if !strings.Contains(uploadCmd, gatherExitCodeFile) {
				t.Fatalf("expected upload command to check gather exit code file, got %q", uploadCmd)
			}
		})
	}
}

func Test_getJobTemplate_FilenamePrefix(t *testing.T) {
	t.Setenv(DefaultMustGatherImageEnv, "quay.io/foo/bar/must-gather:latest")

	directoryName := "must-gather.local.456789abcdef.20260617T143025Z.042315"

	mg := mustgatherv1.MustGather{
		ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns"},
		Spec: mustgatherv1.MustGatherSpec{
			ServiceAccountName: "default",
			UploadTarget: &mustgatherv1.UploadTargetSpec{
				Type: mustgatherv1.UploadTypeSFTP,
				SFTP: &mustgatherv1.SFTPSpec{
					CaseID: "1234",
					Host:   ptr.To("sftp.example.com"),
					CaseManagementAccountSecretRef: v1.LocalObjectReference{
						Name: "case-mgmt-secret",
					},
				},
			},
		},
	}

	job := getJobTemplate("img", "operator-image", mg, "", directoryName)
	upload := findUploadContainerInJob(t, job)
	uploadEnv := envValues(upload)

	val, ok := uploadEnv[uploadEnvFilenamePrefix]
	if !ok {
		t.Fatalf("expected %s env var in upload container, not found", uploadEnvFilenamePrefix)
	}
	if val != directoryName {
		t.Fatalf("expected %s=%s, got %s", uploadEnvFilenamePrefix, directoryName, val)
	}
}

func Test_getJobTemplate_GatherObfuscatePVC(t *testing.T) {
	t.Setenv(DefaultMustGatherImageEnv, "quay.io/foo/bar/must-gather:latest")

	directoryName := "must-gather.local.abc123.20260722T120000Z.042315"

	tests := []struct {
		name    string
		storage *mustgatherv1.Storage
		subPath string
	}{
		{
			name: "Gather + Obfuscate + PVC with subPath",
			storage: &mustgatherv1.Storage{
				Type: mustgatherv1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1.PersistentVolumeConfig{
					Claim:   mustgatherv1.PersistentVolumeClaimReference{Name: "mg-pvc"},
					SubPath: ptr.To("collections"),
				},
			},
			subPath: "collections",
		},
		{
			name: "Gather + Obfuscate + PVC without subPath",
			storage: &mustgatherv1.Storage{
				Type: mustgatherv1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1.PersistentVolumeConfig{
					Claim: mustgatherv1.PersistentVolumeClaimReference{Name: "mg-pvc"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mg := mustgatherv1.MustGather{
				ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns"},
				Spec: mustgatherv1.MustGatherSpec{
					ServiceAccountName: "default",
					Storage:            tt.storage,
					Obfuscate: &mustgatherv1.ObfuscateConfig{
						Enabled: ToPtr(true),
					},
				},
			}

			job := getJobTemplate("img", "operator-image", mg, "", directoryName)

			// Gather container must be present (no obfuscate.source)
			gather := findGatherContainerInJob(t, job)
			gatherCmd := gather.Command[2]
			if !strings.Contains(gatherCmd, obfuscateChownSuffix) {
				t.Fatalf("expected gather command to contain chown suffix for obfuscation, got %q", gatherCmd)
			}

			// Upload container must be present (obfuscate.enabled)
			upload := findUploadContainerInJob(t, job)
			uploadEnv := envValues(upload)

			if uploadEnv[obfuscateEnvEnabled] != "true" {
				t.Fatalf("expected %s=true, got %q", obfuscateEnvEnabled, uploadEnv[obfuscateEnvEnabled])
			}

			// Upload volume should stay emptyDir (PVC is mounted via the output volume)
			var uploadVol *v1.Volume
			for i := range job.Spec.Template.Spec.Volumes {
				if job.Spec.Template.Spec.Volumes[i].Name == uploadVolumeName {
					uploadVol = &job.Spec.Template.Spec.Volumes[i]
					break
				}
			}
			if uploadVol == nil {
				t.Fatalf("expected upload volume %q", uploadVolumeName)
			}
			if uploadVol.EmptyDir == nil {
				t.Fatalf("expected upload volume to be emptyDir (PVC reuse is via mount, not volume)")
			}

			// Upload mount should reference the output volume (PVC-backed) with correct SubPath
			var uploadMount *v1.VolumeMount
			for i := range upload.VolumeMounts {
				if upload.VolumeMounts[i].MountPath == volumeUploadMountPath {
					uploadMount = &upload.VolumeMounts[i]
					break
				}
			}
			if uploadMount == nil {
				t.Fatalf("expected upload mount at %q", volumeUploadMountPath)
			}
			if uploadMount.Name != outputVolumeName {
				t.Fatalf("expected upload mount to reference %q (PVC-backed), got %q", outputVolumeName, uploadMount.Name)
			}
			base := strings.Trim(strings.TrimSpace(tt.subPath), "/")
			wantSubPath := path.Join(base, directoryName)
			if uploadMount.SubPath != wantSubPath {
				t.Fatalf("expected upload mount SubPath %q, got %q", wantSubPath, uploadMount.SubPath)
			}

			// No SFTP env vars should be set (no uploadTarget)
			if _, ok := uploadEnv[uploadEnvUsername]; ok {
				t.Fatalf("expected no %s env var in Gather+Obfuscate+PVC mode", uploadEnvUsername)
			}
			if _, ok := uploadEnv[uploadEnvCaseId]; ok {
				t.Fatalf("expected no %s env var in Gather+Obfuscate+PVC mode", uploadEnvCaseId)
			}
		})
	}
}

func Test_getJobTemplate_ObfuscateSourceSkipsGather(t *testing.T) {
	t.Setenv(DefaultMustGatherImageEnv, "quay.io/foo/bar/must-gather:latest")

	mg := mustgatherv1.MustGather{
		ObjectMeta: metav1.ObjectMeta{Name: "mg", Namespace: "ns"},
		Spec: mustgatherv1.MustGatherSpec{
			ServiceAccountName: "default",
			Obfuscate: &mustgatherv1.ObfuscateConfig{
				Enabled: ToPtr(true),
				Source: &mustgatherv1.PersistentVolumeConfig{
					Claim:   mustgatherv1.PersistentVolumeClaimReference{Name: "existing-pvc"},
					SubPath: ptr.To("bundles/run-1"),
				},
			},
		},
	}

	job := getJobTemplate("img", "operator-image", mg, "", "dir-name")

	// No gather container when source is provided
	for _, c := range job.Spec.Template.Spec.Containers {
		if c.Name == gatherContainerName {
			t.Fatalf("expected no gather container when obfuscate.source is set")
		}
	}

	// Upload container must be present
	upload := findUploadContainerInJob(t, job)

	// Output volume should be the source PVC (read-only)
	var outputVol *v1.Volume
	for i := range job.Spec.Template.Spec.Volumes {
		if job.Spec.Template.Spec.Volumes[i].Name == outputVolumeName {
			outputVol = &job.Spec.Template.Spec.Volumes[i]
			break
		}
	}
	if outputVol == nil || outputVol.PersistentVolumeClaim == nil {
		t.Fatalf("expected output volume to be backed by source PVC")
	}
	if outputVol.PersistentVolumeClaim.ClaimName != "existing-pvc" {
		t.Fatalf("expected output volume PVC claim %q, got %q", "existing-pvc", outputVol.PersistentVolumeClaim.ClaimName)
	}
	if !outputVol.PersistentVolumeClaim.ReadOnly {
		t.Fatalf("expected source PVC to be mounted read-only")
	}

	// Output mount should have SubPath and be read-only
	var outputMount *v1.VolumeMount
	for i := range upload.VolumeMounts {
		if upload.VolumeMounts[i].Name == outputVolumeName && upload.VolumeMounts[i].MountPath == volumeMountPath {
			outputMount = &upload.VolumeMounts[i]
			break
		}
	}
	if outputMount == nil {
		t.Fatalf("expected output volume mount")
	}
	if !outputMount.ReadOnly {
		t.Fatalf("expected output mount to be read-only")
	}
	if outputMount.SubPath != "bundles/run-1" {
		t.Fatalf("expected output mount SubPath %q, got %q", "bundles/run-1", outputMount.SubPath)
	}

	// Upload mount should stay on emptyDir (uploadVolumeName), NOT redirected to source PVC
	var uploadMount *v1.VolumeMount
	for i := range upload.VolumeMounts {
		if upload.VolumeMounts[i].MountPath == volumeUploadMountPath {
			uploadMount = &upload.VolumeMounts[i]
			break
		}
	}
	if uploadMount == nil {
		t.Fatalf("expected upload mount at %q", volumeUploadMountPath)
	}
	if uploadMount.Name != uploadVolumeName {
		t.Fatalf("expected upload mount to reference %q (emptyDir), got %q", uploadVolumeName, uploadMount.Name)
	}

	// Upload volume should be emptyDir
	var uploadVol *v1.Volume
	for i := range job.Spec.Template.Spec.Volumes {
		if job.Spec.Template.Spec.Volumes[i].Name == uploadVolumeName {
			uploadVol = &job.Spec.Template.Spec.Volumes[i]
			break
		}
	}
	if uploadVol == nil || uploadVol.EmptyDir == nil {
		t.Fatalf("upload volume should be emptyDir in source mode")
	}

	// Upload command should be direct (no gather polling)
	uploadCmd := upload.Command[2]
	if !strings.Contains(uploadCmd, uploadCommandDirect) {
		t.Fatalf("expected direct upload command for source mode, got %q", uploadCmd)
	}

	// No init containers needed (source PVC is read-only, upload goes to emptyDir)
	if len(job.Spec.Template.Spec.InitContainers) != 0 {
		t.Fatalf("source mode should not have init containers, got %d", len(job.Spec.Template.Spec.InitContainers))
	}
}

func Test_obfuscateHelpers(t *testing.T) {
	t.Run("isObfuscateEnabled", func(t *testing.T) {
		if isObfuscateEnabled(nil) {
			t.Fatal("expected false for nil")
		}
		if isObfuscateEnabled(&mustgatherv1.ObfuscateConfig{}) {
			t.Fatal("expected false when Enabled is nil")
		}
		if isObfuscateEnabled(&mustgatherv1.ObfuscateConfig{Enabled: ToPtr(false)}) {
			t.Fatal("expected false when Enabled is false")
		}
		if !isObfuscateEnabled(&mustgatherv1.ObfuscateConfig{Enabled: ToPtr(true)}) {
			t.Fatal("expected true when Enabled is true")
		}
	})

	t.Run("shouldAppendObfuscateChown", func(t *testing.T) {
		if shouldAppendObfuscateChown(nil) {
			t.Fatal("expected false for nil")
		}
		if shouldAppendObfuscateChown(&mustgatherv1.ObfuscateConfig{Enabled: ToPtr(true), Source: &mustgatherv1.PersistentVolumeConfig{Claim: mustgatherv1.PersistentVolumeClaimReference{Name: "pvc"}}}) {
			t.Fatal("expected false when source is set (no gather container)")
		}
		if !shouldAppendObfuscateChown(&mustgatherv1.ObfuscateConfig{Enabled: ToPtr(true)}) {
			t.Fatal("expected true when enabled and no source")
		}
	})

	t.Run("shouldAddUploadContainer", func(t *testing.T) {
		mg := mustgatherv1.MustGather{
			Spec: mustgatherv1.MustGatherSpec{
				Obfuscate: &mustgatherv1.ObfuscateConfig{Enabled: ToPtr(true)},
			},
		}
		if !shouldAddUploadContainer(mg) {
			t.Fatal("expected true when obfuscate enabled")
		}

		mgNoObfuscate := mustgatherv1.MustGather{
			Spec: mustgatherv1.MustGatherSpec{},
		}
		if shouldAddUploadContainer(mgNoObfuscate) {
			t.Fatal("expected false when no obfuscate and no upload target")
		}
	})

	t.Run("getObfuscateConfigMapRefName", func(t *testing.T) {
		if getObfuscateConfigMapRefName(nil) != "" {
			t.Fatal("expected empty for nil")
		}
		ref := &mustgatherv1.ObfuscateConfig{
			ObfuscationConfigRef: &v1.LocalObjectReference{Name: "my-config"},
		}
		if getObfuscateConfigMapRefName(ref) != "my-config" {
			t.Fatal("expected my-config")
		}
	})

	t.Run("hasObfuscateSource", func(t *testing.T) {
		if hasObfuscateSource(nil) {
			t.Fatal("expected false for nil")
		}
		src := &mustgatherv1.ObfuscateConfig{
			Source: &mustgatherv1.PersistentVolumeConfig{
				Claim: mustgatherv1.PersistentVolumeClaimReference{Name: "pvc"},
			},
		}
		if !hasObfuscateSource(src) {
			t.Fatal("expected true with valid source claim")
		}
	})
}

func Test_getGatherContainer_ChownSuffix(t *testing.T) {
	container := getGatherContainer("img", false, 5*time.Second, nil, "", nil, nil, nil, "", &mustgatherv1.ObfuscateConfig{Enabled: ToPtr(true)}, true)
	gatherCmd := container.Command[2]
	if !strings.Contains(gatherCmd, obfuscateChownSuffix) {
		t.Fatalf("expected chown suffix when obfuscate enabled, got %q", gatherCmd)
	}

	containerNoObfuscate := getGatherContainer("img", false, 5*time.Second, nil, "", nil, nil, nil, "", nil, false)
	gatherCmdNoObfuscate := containerNoObfuscate.Command[2]
	if strings.Contains(gatherCmdNoObfuscate, obfuscateChownSuffix) {
		t.Fatalf("expected no chown suffix without obfuscation, got %q", gatherCmdNoObfuscate)
	}

	containerCustomCmd := getGatherContainer("img", false, 5*time.Second, nil, "", nil, []string{"/custom"}, []string{"--flag"}, "", &mustgatherv1.ObfuscateConfig{Enabled: ToPtr(true)}, true)
	if len(containerCustomCmd.Command) != 4 || containerCustomCmd.Command[0] != "/bin/bash" {
		t.Fatalf("expected custom command to be wrapped in bash for chown, got %v", containerCustomCmd.Command)
	}
	wrappedScript := containerCustomCmd.Command[2]
	if !strings.Contains(wrappedScript, `"$@"`) {
		t.Fatalf("expected wrapped script to contain \"$@\" passthrough, got %q", wrappedScript)
	}
	if !strings.Contains(wrappedScript, obfuscateChownSuffix) {
		t.Fatalf("expected wrapped script to contain chown suffix, got %q", wrappedScript)
	}
	if !strings.Contains(wrappedScript, gatherExitCodeFile) {
		t.Fatalf("expected wrapped script to write exit code marker, got %q", wrappedScript)
	}
	if !reflect.DeepEqual(containerCustomCmd.Args, []string{"/custom", "--flag"}) {
		t.Fatalf("expected args [/custom --flag], got %v", containerCustomCmd.Args)
	}

	containerCustomCmdNoObfuscate := getGatherContainer("img", false, 5*time.Second, nil, "", nil, []string{"/custom"}, nil, "", nil, false)
	if len(containerCustomCmdNoObfuscate.Command) != 1 || containerCustomCmdNoObfuscate.Command[0] != "/custom" {
		t.Fatalf("expected custom command to be preserved without obfuscate, got %v", containerCustomCmdNoObfuscate.Command)
	}
}

func Test_getGatherContainer_ExitMarkerCustomCommand(t *testing.T) {
	container := getGatherContainer("img", false, 5*time.Second, nil, "", nil, []string{"/custom"}, []string{"--flag"}, "", nil, true)
	if len(container.Command) != 4 || container.Command[0] != "/bin/bash" {
		t.Fatalf("expected custom command to be wrapped in bash for exit marker, got %v", container.Command)
	}
	wrappedScript := container.Command[2]
	if !strings.Contains(wrappedScript, `"$@"`) {
		t.Fatalf("expected wrapped script to contain \"$@\" passthrough, got %q", wrappedScript)
	}
	if !strings.Contains(wrappedScript, gatherExitCodeFile) {
		t.Fatalf("expected wrapped script to write exit code marker, got %q", wrappedScript)
	}
	if strings.Contains(wrappedScript, "chown") {
		t.Fatalf("expected no chown in exit-marker-only wrapping, got %q", wrappedScript)
	}
	if !reflect.DeepEqual(container.Args, []string{"/custom", "--flag"}) {
		t.Fatalf("expected args [/custom --flag], got %v", container.Args)
	}

	containerNoMarker := getGatherContainer("img", false, 5*time.Second, nil, "", nil, []string{"/custom"}, nil, "", nil, false)
	if len(containerNoMarker.Command) != 1 || containerNoMarker.Command[0] != "/custom" {
		t.Fatalf("expected custom command to be preserved without exit marker, got %v", containerNoMarker.Command)
	}
}

func Test_getUploadContainer_GatherExitCodeCheck(t *testing.T) {
	sftp := &mustgatherv1.SFTPSpec{
		CaseID:                         "1234",
		Host:                           ptr.To("sftp.example.com"),
		CaseManagementAccountSecretRef: v1.LocalObjectReference{Name: "secret"},
	}
	container := getUploadContainer("img", nil, "", "", "", false, sftp, nil, "dir")

	uploadCmd := container.Command[2]
	if !strings.Contains(uploadCmd, gatherExitCodeFile) {
		t.Fatalf("expected upload command to check gather exit code file %s, got %q", gatherExitCodeFile, uploadCmd)
	}
	if !strings.Contains(uploadCmd, "Gather may have crashed") {
		t.Fatalf("expected upload command to handle missing exit code file, got %q", uploadCmd)
	}
	if !strings.Contains(uploadCmd, "gather failed with exit code") {
		t.Fatalf("expected upload command to handle non-zero exit code, got %q", uploadCmd)
	}
}

func Test_getUploadContainer_DirectModeSkipsExitCheck(t *testing.T) {
	obfuscate := &mustgatherv1.ObfuscateConfig{
		Enabled: ToPtr(true),
		Source: &mustgatherv1.PersistentVolumeConfig{
			Claim: mustgatherv1.PersistentVolumeClaimReference{Name: "existing-pvc"},
		},
	}
	container := getUploadContainer("img", nil, "", "", "", false, nil, obfuscate, "dir")

	uploadCmd := container.Command[2]
	if strings.Contains(uploadCmd, gatherExitCodeFile) {
		t.Fatalf("direct upload mode (obfuscate.source) should not check gather exit code, got %q", uploadCmd)
	}
	if !strings.Contains(uploadCmd, uploadCommandDirect) {
		t.Fatalf("expected direct upload command for source mode, got %q", uploadCmd)
	}
}

func Test_getUploadContainer_ObfuscateConfigMount(t *testing.T) {
	obfuscate := &mustgatherv1.ObfuscateConfig{
		Enabled:              ToPtr(true),
		ObfuscationConfigRef: &v1.LocalObjectReference{Name: "custom-rules"},
	}
	container := getUploadContainer("img", nil, "", "", "", false, nil, obfuscate, "dir")

	env := envValues(container)
	if env[obfuscateEnvEnabled] != "true" {
		t.Fatalf("expected %s=true", obfuscateEnvEnabled)
	}
	if env[obfuscateEnvConfig] != obfuscateConfigMountPath {
		t.Fatalf("expected %s=%s, got %s", obfuscateEnvConfig, obfuscateConfigMountPath, env[obfuscateEnvConfig])
	}

	foundConfigMount := false
	for _, vm := range container.VolumeMounts {
		if vm.Name == obfuscateConfigVolumeName {
			foundConfigMount = true
			if vm.SubPath != obfuscateConfigMapKey {
				t.Fatalf("expected config mount SubPath %q, got %q", obfuscateConfigMapKey, vm.SubPath)
			}
			if !vm.ReadOnly {
				t.Fatalf("expected config mount to be read-only")
			}
		}
	}
	if !foundConfigMount {
		t.Fatalf("expected obfuscate config volume mount")
	}
}

func Test_outputSubPath(t *testing.T) {
	tests := []struct {
		name          string
		storage       *mustgatherv1.Storage
		directoryName string
		wantPath      string
		wantOk        bool
	}{
		{
			name:     "nil storage returns empty",
			storage:  nil,
			wantPath: "",
			wantOk:   false,
		},
		{
			name: "PVC with subPath and directoryName",
			storage: &mustgatherv1.Storage{
				Type: mustgatherv1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1.PersistentVolumeConfig{
					Claim:   mustgatherv1.PersistentVolumeClaimReference{Name: "pvc"},
					SubPath: ptr.To("base-path"),
				},
			},
			directoryName: "must-gather.local.abc.20260101T000000Z.123456",
			wantPath:      "base-path/must-gather.local.abc.20260101T000000Z.123456",
			wantOk:        true,
		},
		{
			name: "PVC with empty subPath and directoryName",
			storage: &mustgatherv1.Storage{
				Type: mustgatherv1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1.PersistentVolumeConfig{
					Claim:   mustgatherv1.PersistentVolumeClaimReference{Name: "pvc"},
					SubPath: ptr.To(""),
				},
			},
			directoryName: "must-gather.local.abc.20260101T000000Z.123456",
			wantPath:      "must-gather.local.abc.20260101T000000Z.123456",
			wantOk:        true,
		},
		{
			name: "PVC with whitespace subPath and directoryName",
			storage: &mustgatherv1.Storage{
				Type: mustgatherv1.StorageTypePersistentVolume,
				PersistentVolume: mustgatherv1.PersistentVolumeConfig{
					Claim:   mustgatherv1.PersistentVolumeClaimReference{Name: "pvc"},
					SubPath: ptr.To("  / "),
				},
			},
			directoryName: "must-gather.local.abc.20260101T000000Z.123456",
			wantPath:      "must-gather.local.abc.20260101T000000Z.123456",
			wantOk:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotOk := outputSubPath(tt.storage, tt.directoryName)
			if gotOk != tt.wantOk {
				t.Fatalf("outputSubPath() ok = %v, want %v", gotOk, tt.wantOk)
			}
			if gotPath != tt.wantPath {
				t.Fatalf("outputSubPath() path = %q, want %q", gotPath, tt.wantPath)
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
