package mustgather

import (
	"fmt"
	"github.com/openshift/must-gather-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strconv"
	"time"
)

import "github.com/operator-framework/operator-lib/proxy"

const (
	infraNodeLabelKey     = "node-role.kubernetes.io/infra"
	outputVolumeName      = "must-gather-output"
	uploadVolumeName      = "must-gather-upload"
	volumeMountPath       = "/must-gather"
	volumeUploadMountPath = "/must-gather-upload"

	gatherCommandBinaryAudit   = "gather_audit_logs"
	gatherCommandBinaryNoAudit = "gather"
	gatherCommand              = "timeout %v bash -x -c -- '/usr/bin/%v'\n\nstatus=$?\nif [[ $status -eq 124 || $status -eq 137 ]]; then\n  echo \"Gather timed out.\"\n  exit 0\nfi"
	mustGatherImage            = "quay.io/openshift/origin-must-gather"
	gatherContainerName        = "gather"

	uploadContainerName       = "upload"
	uploadEnvUsername         = "username"
	uploadEnvPassword         = "password"
	uploadEnvCaseId           = "caseid"
	uploadEnvInternalUser     = "internal_user"
	uploadEnvDisableUpload    = "disable_upload"
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

func getJobTemplate(operatorImage string, clusterVersion string, mustGather v1alpha1.MustGather) *batchv1.Job {
	job := initializeJobTemplate(mustGather.Name, mustGather.Namespace, mustGather.Spec.ServiceAccountRef.Name)

	var httpProxy, httpsProxy, noProxy string

	// Check if proxy configuration is provided in the CR
	if mustGather.Spec.ProxyConfig.HTTPProxy != "" || mustGather.Spec.ProxyConfig.HTTPSProxy != "" || mustGather.Spec.ProxyConfig.NoProxy != "" {
		// Use proxy configuration from CR
		httpProxy = mustGather.Spec.ProxyConfig.HTTPProxy
		httpsProxy = mustGather.Spec.ProxyConfig.HTTPSProxy
		noProxy = mustGather.Spec.ProxyConfig.NoProxy
	}

	// Fallback to operator's environment proxy variables only if not provided in the CR
	if httpProxy == "" && httpsProxy == "" {
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
	}

	job.Spec.Template.Spec.Containers = append(
		job.Spec.Template.Spec.Containers,
		getGatherContainer(mustGather.Spec.Audit, mustGather.Spec.MustGatherTimeout.Duration, clusterVersion),
		getUploadContainer(
			operatorImage,
			mustGather.Spec.CaseID,
			mustGather.Spec.InternalUser,
			mustGather.Spec.DisableUpload,
			httpProxy,
			httpsProxy,
			noProxy,
			mustGather.Spec.CaseManagementAccountSecretRef,
		),
	)
	return job
}

func initializeJobTemplate(name string, namespace string, serviceAccountRef string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: batchv1.JobSpec{
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
					RestartPolicy:         corev1.RestartPolicyOnFailure,
					ShareProcessNamespace: ToPtr(true),
					Volumes: []corev1.Volume{
						{
							Name:         outputVolumeName,
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
						{
							Name:         uploadVolumeName,
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
					},
					ServiceAccountName: serviceAccountRef,
				},
			},
		},
	}
}

func getGatherContainer(audit bool, timeout time.Duration, mustGatherImageVersion string) corev1.Container {
	var commandBinary string
	if audit {
		commandBinary = gatherCommandBinaryAudit
	} else {
		commandBinary = gatherCommandBinaryNoAudit
	}

	return corev1.Container{
		Command: []string{
			"/bin/bash",
			"-c",
			fmt.Sprintf(gatherCommand, timeout, commandBinary),
		},
		Image: fmt.Sprintf("%v:%v", mustGatherImage, mustGatherImageVersion),
		Name:  gatherContainerName,
		VolumeMounts: []corev1.VolumeMount{
			{
				MountPath: volumeMountPath,
				Name:      outputVolumeName,
			},
		},
	}
}

func getUploadContainer(
	operatorImage string,
	caseId string,
	internalUser bool,
	disableUpload bool,
	httpProxy string,
	httpsProxy string,
	noProxy string,
	secretKeyRefName corev1.LocalObjectReference,
) corev1.Container {
	// Create the modified upload command that includes SSH setup
	uploadCommandWithSSH := fmt.Sprintf("mkdir -p %s; touch %s; chmod 700 %s; chmod 600 %s; %s",
		sshDir, knownHostsFile, sshDir, knownHostsFile, uploadCommand)

	container := corev1.Container{
		Command: []string{
			"/bin/bash",
			"-c",
			uploadCommandWithSSH,
		},
		Image: operatorImage,
		Name:  uploadContainerName,
		VolumeMounts: []corev1.VolumeMount{
			{
				MountPath: volumeMountPath,
				Name:      outputVolumeName,
			},
			{
				MountPath: volumeUploadMountPath,
				Name:      uploadVolumeName,
			},
		},
		Env: []corev1.EnvVar{
			{
				Name:  uploadEnvCaseId,
				Value: caseId,
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
			{
				Name:  uploadEnvDisableUpload,
				Value: strconv.FormatBool(disableUpload),
			},
		},
	}

	// Add secret-based environment variables only when upload is enabled and secret is provided
	if !disableUpload && secretKeyRefName.Name != "" {
		container.Env = append(container.Env, []corev1.EnvVar{
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
		}...)
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
