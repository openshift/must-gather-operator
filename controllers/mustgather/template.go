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
	uploadCommand             = "count=0\nuntil [ $count -gt 4 ]\ndo\n  while `pgrep -a gather > /dev/null`\n  do\n    echo \"waiting for gathers to complete ...\"\n    sleep 120\n    count=0\n  done\n  echo \"no gather is running ($count / 4)\"\n  ((count++))\n  sleep 30\ndone\n/usr/local/bin/upload"

	// SSH directory and known hosts file
	sshDir         = "/tmp/must-gather-operator/.ssh"
	knownHostsFile = "/tmp/must-gather-operator/.ssh/known_hosts"
)

func outputSubPathExpr(storage *v1alpha1.Storage) (string, bool) {
	if storage == nil || storage.Type != v1alpha1.StorageTypePersistentVolume {
		return "", false
	}

	base := strings.TrimSpace(storage.PersistentVolume.SubPath)
	base = strings.Trim(base, "/")

	// Isolate each run using the pod name to avoid overwriting prior collections on the PVC.
	// When base is empty, path.Join("", ...) yields just the pod name expr, giving per-run isolation at PVC root.
	return path.Join(base, fmt.Sprintf("$(%s)", podNameEnvVar)), true
}

func podNameEnvVars() []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name: podNameEnvVar,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
			},
		},
	}
}

// timeNow exists to allow deterministic unit testing of time-based behavior.
var timeNow = time.Now

// GatherTimeFilter holds the time-based filtering options for log collection
type GatherTimeFilter struct {
	// Since is a relative duration (e.g., "2h", "30m")
	Since time.Duration
	// SinceTime is an absolute timestamp
	SinceTime *time.Time
}

func getJobTemplate(image string, operatorImage string, mustGather v1alpha1.MustGather, trustedCAConfigMapName string, clusterCreationTime *time.Time) *batchv1.Job {
	job := initializeJobTemplate(mustGather.Name, mustGather.Namespace, mustGather.Spec.ServiceAccountName, mustGather.Spec.Storage, trustedCAConfigMapName)

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

	job.Spec.Template.Spec.Containers = append(
		job.Spec.Template.Spec.Containers,
		getGatherContainer(image, audit, timeout, mustGather.Spec.Storage, trustedCAConfigMapName, timeFilter, clusterCreationTime, command, args),
	)

	// Add the upload container only if the upload target is specified
	if mustGather.Spec.UploadTarget != nil && mustGather.Spec.UploadTarget.Type == v1alpha1.UploadTypeSFTP {
		s := mustGather.Spec.UploadTarget.SFTP
		if s != nil && s.CaseID != "" && s.CaseManagementAccountSecretRef.Name != "" {
			job.Spec.Template.Spec.Containers = append(
				job.Spec.Template.Spec.Containers,
				getUploadContainer(
					operatorImage,
					s.CaseID,
					s.Host,
					s.InternalUser,
					mustGather.Spec.Storage,
					httpProxy,
					httpsProxy,
					noProxy,
					s.CaseManagementAccountSecretRef,
					trustedCAConfigMapName != "",
				),
			)
		}
	}

	return job
}

func initializeJobTemplate(name string, namespace string, serviceAccountRef string, storage *v1alpha1.Storage, trustedCAConfigMapName string) *batchv1.Job {
	outputVolume := corev1.Volume{
		Name:         outputVolumeName,
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	}

	if storage != nil && storage.Type == v1alpha1.StorageTypePersistentVolume {
		outputVolume.VolumeSource = corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: storage.PersistentVolume.Claim.Name,
			},
		}
	}

	volumes := []corev1.Volume{
		outputVolume,
		{
			Name:         uploadVolumeName,
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
	}

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

func getGatherContainer(image string, audit bool, timeout time.Duration, storage *v1alpha1.Storage, trustedCAConfigMapName string, timeFilter *GatherTimeFilter, clusterCreationTime *time.Time, command []string, args []string) corev1.Container {
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

	subPathExpr, hasSubPathExpr := outputSubPathExpr(storage)
	if hasSubPathExpr {
		volumeMount.SubPathExpr = subPathExpr
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
		container.Command = []string{
			"/bin/bash",
			"-c",
			fmt.Sprintf(gatherCommand, math.Ceil(timeout.Seconds()), commandBinary),
		}
	}

	if len(args) > 0 {
		container.Args = args
	}

	// Provide pod name env var only when SubPathExpr is used (PVC subPath is set).
	if hasSubPathExpr {
		container.Env = append(container.Env, podNameEnvVars()...)
	}
	// Add time filter environment variables if specified
	if timeFilter != nil {
		// Clamp the time filter so it never precedes cluster creation time.
		// - For Since (duration): ensure (now - since) >= clusterCreationTime by reducing Since to clusterAge.
		// - For SinceTime (absolute): ensure sinceTime >= clusterCreationTime by bumping it up.
		effectiveSince := timeFilter.Since
		effectiveSinceTime := timeFilter.SinceTime
		if clusterCreationTime != nil && !clusterCreationTime.IsZero() {
			now := timeNow()
			if effectiveSince > 0 {
				clusterAge := now.Sub(*clusterCreationTime)
				if clusterAge < 0 {
					clusterAge = 0
				}
				if effectiveSince > clusterAge {
					effectiveSince = clusterAge
				}
			}
			if effectiveSinceTime != nil && effectiveSinceTime.Before(*clusterCreationTime) {
				t := *clusterCreationTime
				effectiveSinceTime = &t
			}
		}

		if effectiveSince > 0 {
			container.Env = append(container.Env, corev1.EnvVar{
				Name:  gatherEnvSince,
				Value: effectiveSince.String(),
			})
		}
		if effectiveSinceTime != nil {
			container.Env = append(container.Env, corev1.EnvVar{
				Name:  gatherEnvSinceTime,
				Value: effectiveSinceTime.Format(time.RFC3339),
			})
		}
	}

	return container
}

func getUploadContainer(
	operatorImage string,
	caseId string,
	host string,
	internalUser bool,
	storage *v1alpha1.Storage,
	httpProxy string,
	httpsProxy string,
	noProxy string,
	secretKeyRefName corev1.LocalObjectReference,
	shouldMountTrustedCAConfigMap bool,
) corev1.Container {
	// Create the modified upload command that includes SSH setup
	uploadCommandWithSSH := fmt.Sprintf("mkdir -p %s; touch %s; chmod 700 %s; chmod 600 %s; %s",
		sshDir, knownHostsFile, sshDir, knownHostsFile, uploadCommand)

	outputMount := corev1.VolumeMount{
		MountPath: volumeMountPath,
		Name:      outputVolumeName,
	}
	subPathExpr, hasSubPathExpr := outputSubPathExpr(storage)
	if hasSubPathExpr {
		outputMount.SubPathExpr = subPathExpr
	}

	volumeMounts := []corev1.VolumeMount{
		outputMount,
		{
			MountPath: volumeUploadMountPath,
			Name:      uploadVolumeName,
		},
	}

	if shouldMountTrustedCAConfigMap {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      trustedCAVolumeName,
			MountPath: trustedCAMountPath,
			ReadOnly:  true,
		})
	}

	container := corev1.Container{
		Command: []string{
			"/bin/bash",
			"-c",
			uploadCommandWithSSH,
		},
		Image:        operatorImage,
		Name:         uploadContainerName,
		VolumeMounts: volumeMounts,
		Env: []corev1.EnvVar{
			{
				Name: uploadEnvUsername,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key:                  uploadEnvUsername,
						LocalObjectReference: secretKeyRefName,
					},
				},
			},
			{
				Name: uploadEnvPassword,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key:                  uploadEnvPassword,
						LocalObjectReference: secretKeyRefName,
					},
				},
			},
			{
				Name:  uploadEnvCaseId,
				Value: caseId,
			},
			{
				Name:  uploadEnvHost,
				Value: host,
			},
			{
				Name:  uploadEnvMustGatherOutput,
				Value: volumeMountPath,
			},
			{
				Name:  uploadEnvMustGatherUpload,
				Value: volumeUploadMountPath,
			},
			{
				Name:  uploadEnvInternalUser,
				Value: strconv.FormatBool(internalUser),
			},
		},
	}

	// Provide pod name env var only when SubPathExpr is used (PVC subPath is set).
	if hasSubPathExpr {
		container.Env = append(container.Env, podNameEnvVars()...)
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
