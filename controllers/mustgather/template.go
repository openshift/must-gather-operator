package mustgather

import (
	"fmt"
	"math"
	"path"
	"strconv"
	"strings"

	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/must-gather-operator/api/v1alpha1"

	"github.com/operator-framework/operator-lib/proxy"
)

const (
	infraNodeLabelKey     = "node-role.kubernetes.io/infra"
	outputVolumeName      = "must-gather-output"
	uploadVolumeName      = "must-gather-upload"
	trustedCAVolumeName   = "trusted-ca"
	volumeMountPath       = "/must-gather"
	volumeUploadMountPath = "/must-gather-upload"
	trustedCAMountPath    = "/etc/pki/tls/certs"

	gatherCommandBinaryAudit   = "gather_audit_logs"
	gatherCommandBinaryNoAudit = "gather"
	gatherCommand              = "timeout %v bash -x -c -- '/usr/bin/%v' 2>&1 | tee /must-gather/must-gather.log\n\nstatus=$?\nif [[ $status -eq 124 || $status -eq 137 ]]; then\n  echo \"Gather timed out.\"\n  exit 0\nfi | tee -a /must-gather/must-gather.log"
	gatherContainerName        = "gather"

	// Environment variables for time-based log filtering
	gatherEnvSince     = "MUST_GATHER_SINCE"
	gatherEnvSinceTime = "MUST_GATHER_SINCE_TIME"

	backoffLimit              = 3
	uploadContainerName       = "upload"
	uploadEnvUsername         = "username"
	uploadEnvPassword         = "password"
	uploadEnvCaseId           = "caseid"
	uploadEnvHost             = "host"
	uploadEnvInternalUser     = "internal_user"
	uploadEnvHttpProxy        = "http_proxy"
	uploadEnvHttpsProxy       = "https_proxy"
	uploadEnvNoProxy          = "no_proxy"
	uploadEnvMustGatherOutput = "must_gather_output"
	uploadEnvMustGatherUpload = "must_gather_upload"
	uploadEnvFilenamePrefix   = "FILENAME_PREFIX"
	uploadCommand             = "count=0\nuntil [ $count -gt 4 ]\ndo\n  while `pgrep -a gather > /dev/null`\n  do\n    echo \"waiting for gathers to complete ...\"\n    sleep 120\n    count=0\n  done\n  echo \"no gather is running ($count / 4)\"\n  ((count++))\n  sleep 30\ndone\n/usr/local/bin/upload"
	uploadCommandDirect       = "/usr/local/bin/upload"

	// SSH directory and known hosts file
	sshDir         = "/tmp/must-gather-operator/.ssh"
	knownHostsFile = "/tmp/must-gather-operator/.ssh/known_hosts"
)

func outputSubPath(storage *v1alpha1.Storage, directoryName string) (string, bool) {
	if storage == nil || storage.Type != v1alpha1.StorageTypePersistentVolume {
		return "", false
	}

	base := strings.TrimSpace(storage.PersistentVolume.SubPath)
	base = strings.Trim(base, "/")

	return path.Join(base, directoryName), true
}

// GatherTimeFilter holds the time-based filtering options for log collection
type GatherTimeFilter struct {
	// Since is a relative duration (e.g., "2h", "30m")
	Since time.Duration
	// SinceTime is an absolute timestamp
	SinceTime *time.Time
}

func isObfuscateEnabled(obfuscate *v1alpha1.ObfuscateConfig) bool {
	return obfuscate != nil && obfuscate.Enabled != nil && *obfuscate.Enabled
}

func shouldAppendObfuscateChown(obfuscate *v1alpha1.ObfuscateConfig) bool {
	return isObfuscateEnabled(obfuscate) && obfuscate.Source == nil
}

type uploadSFTPParams struct {
	caseID       string
	host         string
	internalUser bool
	secretRef    corev1.LocalObjectReference
}

func sftpUploadParams(mustGather v1alpha1.MustGather) *uploadSFTPParams {
	if mustGather.Spec.UploadTarget == nil || mustGather.Spec.UploadTarget.Type != v1alpha1.UploadTypeSFTP {
		return nil
	}
	s := mustGather.Spec.UploadTarget.SFTP
	if s == nil || s.CaseID == "" || s.CaseManagementAccountSecretRef.Name == "" {
		return nil
	}
	return &uploadSFTPParams{
		caseID:       s.CaseID,
		host:         s.Host,
		internalUser: s.InternalUser,
		secretRef:    s.CaseManagementAccountSecretRef,
	}
}

func shouldAddUploadContainer(mustGather v1alpha1.MustGather) bool {
	if isObfuscateEnabled(mustGather.Spec.Obfuscate) {
		return true
	}
	return sftpUploadParams(mustGather) != nil
}

func obfuscateConfigMapName(obfuscate *v1alpha1.ObfuscateConfig) string {
	if obfuscate != nil && obfuscate.ObfuscationConfigRef != nil {
		return obfuscate.ObfuscationConfigRef.Name
	}
	return ""
}

func hasObfuscateSource(obfuscate *v1alpha1.ObfuscateConfig) bool {
	return obfuscate != nil && obfuscate.Source != nil && obfuscate.Source.Claim.Name != ""
}

func getJobTemplate(image string, operatorImage string, mustGather v1alpha1.MustGather, trustedCAConfigMapName string, directoryName string) *batchv1.Job {
	job := initializeJobTemplate(
		mustGather.Name,
		mustGather.Namespace,
		mustGather.Spec.ServiceAccountName,
		mustGather.Spec.Storage,
		trustedCAConfigMapName,
		mustGather.Spec.Obfuscate,
	)

	var httpProxy, httpsProxy, noProxy string

	// Use operator's environment proxy variables
	envVars := proxy.ReadProxyVarsFromEnv()
	// the below loop should implicitly handle len(envVars) > 0
	for _, envVar := range envVars {
		switch envVar.Name {
		case "HTTP_PROXY":
			httpProxy = envVar.Value
		case "HTTPS_PROXY":
			httpsProxy = envVar.Value
		case "NO_PROXY":
			noProxy = envVar.Value
		}
	}

	var audit bool
	if mustGather.Spec.GatherSpec != nil {
		audit = mustGather.Spec.GatherSpec.Audit
	}

	timeout := time.Duration(0)
	if mustGather.Spec.MustGatherTimeout != nil {
		timeout = mustGather.Spec.MustGatherTimeout.Duration
	}

	// Build time filter from spec
	var timeFilter *GatherTimeFilter
	var command, args []string
	if mustGather.Spec.GatherSpec != nil {
		command = mustGather.Spec.GatherSpec.Command
		args = mustGather.Spec.GatherSpec.Args
		if mustGather.Spec.GatherSpec.Since != nil || mustGather.Spec.GatherSpec.SinceTime != nil {
			timeFilter = &GatherTimeFilter{}
			if mustGather.Spec.GatherSpec.Since != nil {
				timeFilter.Since = mustGather.Spec.GatherSpec.Since.Duration
			}
			if mustGather.Spec.GatherSpec.SinceTime != nil {
				t := mustGather.Spec.GatherSpec.SinceTime.Time
				timeFilter.SinceTime = &t
			}
		}
	}

	if !hasObfuscateSource(mustGather.Spec.Obfuscate) {
		job.Spec.Template.Spec.Containers = append(
			job.Spec.Template.Spec.Containers,
			getGatherContainer(image, audit, timeout, mustGather.Spec.Storage, trustedCAConfigMapName, timeFilter, command, args, directoryName, mustGather.Spec.Obfuscate),
		)
	}

	if shouldAddUploadContainer(mustGather) {
		job.Spec.Template.Spec.Containers = append(
			job.Spec.Template.Spec.Containers,
			getUploadContainer(
				operatorImage,
				mustGather.Spec.Storage,
				httpProxy,
				httpsProxy,
				noProxy,
				trustedCAConfigMapName != "",
				sftpUploadParams(mustGather),
				mustGather.Spec.Obfuscate,
				directoryName,
			),
		)
	}

	return job
}

func initializeJobTemplate(name string, namespace string, serviceAccountRef string, storage *v1alpha1.Storage, trustedCAConfigMapName string, obfuscate *v1alpha1.ObfuscateConfig) *batchv1.Job {
	outputVolume := corev1.Volume{
		Name:         outputVolumeName,
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	}

	if hasObfuscateSource(obfuscate) {
		outputVolume.VolumeSource = corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: obfuscate.Source.Claim.Name,
				ReadOnly:  true,
			},
		}
	} else if storage != nil && storage.Type == v1alpha1.StorageTypePersistentVolume {
		outputVolume.VolumeSource = corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: storage.PersistentVolume.Claim.Name,
			},
		}
	}

	uploadVolume := corev1.Volume{
		Name:         uploadVolumeName,
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	}
	if isObfuscateEnabled(obfuscate) && !hasObfuscateSource(obfuscate) &&
		storage != nil && storage.Type == v1alpha1.StorageTypePersistentVolume {
		uploadVolume.VolumeSource = corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: storage.PersistentVolume.Claim.Name,
			},
		}
	}

	volumes := []corev1.Volume{outputVolume, uploadVolume}

	// Add trusted CA volume if configmap name is provided
	if trustedCAConfigMapName != "" {
		volumes = append(volumes, corev1.Volume{
			Name: trustedCAVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: trustedCAConfigMapName,
					},
				},
			},
		})
	}

	if obfuscateConfigMapName(obfuscate) != "" {
		volumes = append(volumes, corev1.Volume{
			Name: obfuscateConfigVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: obfuscateConfigMapName(obfuscate),
					},
				},
			},
		})
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ToPtr(int32(backoffLimit)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
								{
									Preference: corev1.NodeSelectorTerm{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      infraNodeLabelKey,
												Operator: corev1.NodeSelectorOpExists,
											},
										},
									},
									Weight: 1,
								},
							},
						},
					},
					Tolerations: []corev1.Toleration{
						{
							Effect:   corev1.TaintEffectNoSchedule,
							Key:      infraNodeLabelKey,
							Operator: corev1.TolerationOpExists,
						},
					},
					RestartPolicy:         corev1.RestartPolicyNever,
					ShareProcessNamespace: ToPtr(true),
					Volumes:               volumes,
					ServiceAccountName:    serviceAccountRef,
				},
			},
		},
	}
}

func getGatherContainer(image string, audit bool, timeout time.Duration, storage *v1alpha1.Storage, trustedCAConfigMapName string, timeFilter *GatherTimeFilter, command []string, args []string, directoryName string, obfuscate *v1alpha1.ObfuscateConfig) corev1.Container {
	var commandBinary string
	if audit {
		commandBinary = gatherCommandBinaryAudit
	} else {
		commandBinary = gatherCommandBinaryNoAudit
	}

	volumeMount := corev1.VolumeMount{
		MountPath: volumeMountPath,
		Name:      outputVolumeName,
	}

	subPath, hasPVC := outputSubPath(storage, directoryName)
	if hasPVC {
		volumeMount.SubPath = subPath
	}

	volumeMounts := []corev1.VolumeMount{volumeMount}

	// Add trusted CA mount if configmap name is provided
	if trustedCAConfigMapName != "" {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      trustedCAVolumeName,
			MountPath: trustedCAMountPath,
			ReadOnly:  true,
		})
	}

	container := corev1.Container{
		Image:        image,
		Name:         gatherContainerName,
		VolumeMounts: volumeMounts,
	}

	if len(command) > 0 {
		container.Command = command
	} else {
		gatherCmd := fmt.Sprintf(gatherCommand, math.Ceil(timeout.Seconds()), commandBinary)
		if shouldAppendObfuscateChown(obfuscate) {
			gatherCmd += "\n" + obfuscateChownSuffix
		}
		container.Command = []string{
			"/bin/bash",
			"-c",
			gatherCmd,
		}
	}

	if len(args) > 0 {
		container.Args = args
	}

	// Add time filter environment variables if specified
	if timeFilter != nil {
		if timeFilter.Since > 0 {
			container.Env = append(container.Env, corev1.EnvVar{
				Name:  gatherEnvSince,
				Value: timeFilter.Since.String(),
			})
		}
		if timeFilter.SinceTime != nil {
			container.Env = append(container.Env, corev1.EnvVar{
				Name:  gatherEnvSinceTime,
				Value: timeFilter.SinceTime.Format(time.RFC3339),
			})
		}
	}

	return container
}

func getUploadContainer(
	operatorImage string,
	storage *v1alpha1.Storage,
	httpProxy string,
	httpsProxy string,
	noProxy string,
	shouldMountTrustedCAConfigMap bool,
	sftp *uploadSFTPParams,
	obfuscate *v1alpha1.ObfuscateConfig,
	directoryName string,
) corev1.Container {
	uploadCmd := uploadCommand
	if hasObfuscateSource(obfuscate) {
		uploadCmd = uploadCommandDirect
	}

	// Create the modified upload command that includes SSH setup
	uploadCommandWithSSH := fmt.Sprintf("mkdir -p %s; touch %s; chmod 700 %s; chmod 600 %s; %s",
		sshDir, knownHostsFile, sshDir, knownHostsFile, uploadCmd)

	outputMount := corev1.VolumeMount{
		MountPath: volumeMountPath,
		Name:      outputVolumeName,
	}
	if hasObfuscateSource(obfuscate) {
		outputMount.ReadOnly = true
		if subPath := strings.Trim(obfuscate.Source.SubPath, "/"); subPath != "" {
			outputMount.SubPath = subPath
		}
	} else {
		subPath, hasPVC := outputSubPath(storage, directoryName)
		if hasPVC {
			outputMount.SubPath = subPath
		}
	}

	uploadMount := corev1.VolumeMount{
		MountPath: volumeUploadMountPath,
		Name:      uploadVolumeName,
	}
	if isObfuscateEnabled(obfuscate) && !hasObfuscateSource(obfuscate) &&
		storage != nil && storage.Type == v1alpha1.StorageTypePersistentVolume {
		base := strings.TrimSpace(storage.PersistentVolume.SubPath)
		base = strings.Trim(base, "/")
		uploadMount.SubPath = path.Join(base, directoryName)
	}

	volumeMounts := []corev1.VolumeMount{outputMount, uploadMount}

	if shouldMountTrustedCAConfigMap {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      trustedCAVolumeName,
			MountPath: trustedCAMountPath,
			ReadOnly:  true,
		})
	}

	if obfuscateConfigMapName(obfuscate) != "" {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      obfuscateConfigVolumeName,
			MountPath: obfuscateConfigMountPath,
			SubPath:   obfuscateConfigMapKey,
			ReadOnly:  true,
		})
	}

	env := []corev1.EnvVar{
		{
			Name:  uploadEnvMustGatherOutput,
			Value: volumeMountPath,
		},
		{
			Name:  uploadEnvMustGatherUpload,
			Value: volumeUploadMountPath,
		},
	}
	if sftp != nil {
		env = append(env,
			corev1.EnvVar{
				Name: uploadEnvUsername,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key:                  uploadEnvUsername,
						LocalObjectReference: sftp.secretRef,
					},
				},
			},
			corev1.EnvVar{
				Name: uploadEnvPassword,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key:                  uploadEnvPassword,
						LocalObjectReference: sftp.secretRef,
					},
				},
			},
			corev1.EnvVar{
				Name:  uploadEnvCaseId,
				Value: sftp.caseID,
			},
			corev1.EnvVar{
				Name:  uploadEnvHost,
				Value: sftp.host,
			},
			corev1.EnvVar{
				Name:  uploadEnvInternalUser,
				Value: strconv.FormatBool(sftp.internalUser),
			},
		)
	}

	if isObfuscateEnabled(obfuscate) {
		env = append(env, corev1.EnvVar{
			Name:  obfuscateEnvEnabled,
			Value: "true",
		})
	}
	if obfuscateConfigMapName(obfuscate) != "" {
		env = append(env, corev1.EnvVar{
			Name:  obfuscateEnvConfig,
			Value: obfuscateConfigMountPath,
		})
	}

	env = append(env, corev1.EnvVar{
		Name:  uploadEnvFilenamePrefix,
		Value: directoryName,
	})

	container := corev1.Container{
		Command: []string{
			"/bin/bash",
			"-c",
			uploadCommandWithSSH,
		},
		Image:        operatorImage,
		Name:         uploadContainerName,
		VolumeMounts: volumeMounts,
		Env:          env,
	}

	if httpProxy != "" {
		container.Env = append(container.Env, corev1.EnvVar{Name: uploadEnvHttpProxy, Value: httpProxy})
	}
	if httpsProxy != "" {
		container.Env = append(container.Env, corev1.EnvVar{Name: uploadEnvHttpsProxy, Value: httpsProxy})
	}
	if noProxy != "" {
		container.Env = append(container.Env, corev1.EnvVar{Name: uploadEnvNoProxy, Value: noProxy})
	}

	return container
}

func ToPtr[T any](t T) *T { return &t }
