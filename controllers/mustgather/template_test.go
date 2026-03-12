package mustgather

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
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
	tests := []struct {
		name            string
		audit           bool
		timeout         time.Duration
		mustGatherImage string
		storage         *mustgatherv1alpha1.Storage
		command         []string
		args            []string
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
			name:    "with PVC empty subPath does not set subPathExpr",
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
			name:    "with PVC whitespace subPath does not set subPathExpr",
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
			name:    "with PVC slash-only subPath does not set subPathExpr",
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
			timeout:         6*time.Hour + 5*time.Minute + 3*time.Second, // 6h5m3s
			mustGatherImage: "quay.io/foo/bar/must-gather:latest",
		},
		{
			name:            "custom command and args",
			timeout:         5 * time.Second,
			mustGatherImage: "quay.io/foo/bar/must-gather:latest",
			command:         []string{"/usr/bin/custom-gather"},
			args:            []string{"--verbose", "--subsystem=network"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			container := getGatherContainer(tt.mustGatherImage, tt.audit, tt.timeout, tt.storage, "", tt.command, tt.args)

			if len(tt.command) == 0 {
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

			if tt.storage != nil {
				if len(container.VolumeMounts) == 0 {
					t.Fatalf("expected at least one volume mount when storage is provided")
				}
				volumeMount := container.VolumeMounts[0]
				if volumeMount.Name != outputVolumeName {
					t.Fatalf("volume mount name was not correctly set. got %v, wanted %v", volumeMount.Name, outputVolumeName)
				}
				base := strings.Trim(strings.TrimSpace(tt.storage.PersistentVolume.SubPath), "/")
				if base == "" {
					if volumeMount.SubPathExpr != "" {
						t.Fatalf("did not expect volume mount subPathExpr to be set when base subPath is empty, got %q", volumeMount.SubPathExpr)
					}
				} else {
					wantExpr := fmt.Sprintf("%s/$(POD_NAME)", base)
					if volumeMount.SubPathExpr != wantExpr {
						t.Fatalf("volume mount subPathExpr was not correctly set. got %q, wanted %q", volumeMount.SubPathExpr, wantExpr)
					}
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
			subPathBase := ""
			if tt.storage != nil && tt.storage.Type == mustgatherv1alpha1.StorageTypePersistentVolume {
				subPathBase = strings.Trim(strings.TrimSpace(tt.storage.PersistentVolume.SubPath), "/")
			}
			if subPathBase == "" {
				if hasPodNameEnv {
					t.Fatalf("did not expect %s env var when PVC subPath is empty", podNameEnvVar)
				}
			} else {
				if !hasPodNameEnv {
					t.Fatalf("expected %s env var when PVC subPath is set (base=%q)", podNameEnvVar, subPathBase)
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
			name:          "With PVC empty subPath does not set subPathExpr",
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
			name:          "With PVC whitespace subPath does not set subPathExpr",
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
			name:          "With PVC slash-only subPath does not set subPathExpr",
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
				if base == "" {
					if outputMount.SubPathExpr != "" {
						t.Fatalf("did not expect output volume mount subPathExpr to be set when base subPath is empty, got %q", outputMount.SubPathExpr)
					}
				} else {
					wantExpr := fmt.Sprintf("%s/$(POD_NAME)", base)
					if outputMount.SubPathExpr != wantExpr {
						t.Fatalf("expected output volume mount subPathExpr %q but got %q", wantExpr, outputMount.SubPathExpr)
					}
				}
				if outputMount.SubPath != "" {
					t.Fatalf("did not expect output volume mount subPath to be set when using subPathExpr, got %q", outputMount.SubPath)
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
			subPathBase := ""
			if tt.storage != nil && tt.storage.Type == mustgatherv1alpha1.StorageTypePersistentVolume {
				subPathBase = strings.Trim(strings.TrimSpace(tt.storage.PersistentVolume.SubPath), "/")
			}
			if subPathBase == "" {
				if hasPodNameEnv {
					t.Fatalf("did not expect %s env var when PVC subPath is empty", podNameEnvVar)
				}
			} else {
				if !hasPodNameEnv {
					t.Fatalf("expected %s env var when PVC subPath is set (base=%q)", podNameEnvVar, subPathBase)
				}
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
