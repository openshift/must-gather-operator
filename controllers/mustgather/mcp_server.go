/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package mustgather

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"time"

	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	mcpDeploymentName = "must-gather-mcp"
	mcpServiceName    = "must-gather-mcp"
	mcpContainerPort  = 8080
	mcpVolumeName     = "must-gather-data"
	mcpMountPath      = "/data"

	mcpLabelApp       = "must-gather-mcp"
	mcpLabelComponent = "mcp-server"
	mcpLabelPartOf    = "must-gather-operator"

	// mcpCurrentMustGatherAnnotation records which MustGather's PVC is
	// currently mounted by the shared MCP server Deployment. It is used to
	// guard against swapping the PVC out from under an in-flight IntelliAide
	// analysis when a second MustGather completes concurrently.
	mcpCurrentMustGatherAnnotation = "must-gather-operator.openshift.io/current-mustgather"

	// mcpServerBusyRequeueInterval is how long to wait before retrying a
	// deferred PVC swap when the shared MCP server is busy serving another
	// MustGather's in-flight analysis.
	mcpServerBusyRequeueInterval = 30 * time.Second
)

// errMCPServerBusy is returned by ensureMCPDeployment when the shared MCP
// server's PVC cannot be swapped yet because another MustGather's
// IntelliAide analysis is still reading from it.
var errMCPServerBusy = stderrors.New("shared MCP server is busy serving another MustGather's analysis")

// ensureMCPServer ensures the shared MCP server Deployment and Service exist
// in the operator namespace with the correct PVC. It creates them if missing,
// or updates the PVC claim name if it has changed.
func (r *MustGatherReconciler) ensureMCPServer(
	ctx context.Context,
	instance *mustgatherv1alpha1.MustGather,
) error {
	if instance.Spec.Storage == nil || instance.Spec.Storage.Type != mustgatherv1alpha1.StorageTypePersistentVolume {
		return nil
	}

	pvcName := instance.Spec.Storage.PersistentVolume.Claim.Name
	mcpImage := r.getMCPServerImage()

	if err := r.ensureMCPDeployment(ctx, pvcName, mcpImage, instance.Name); err != nil {
		return err
	}
	return r.ensureMCPService(ctx)
}

func (r *MustGatherReconciler) getMCPServerImage() string {
	if img := os.Getenv("MCP_SERVER_IMAGE"); img != "" {
		return img
	}
	return "registry.redhat.io/openshift-mcp-beta/openshift-mcp-server-rhel9:latest"
}

func (r *MustGatherReconciler) ensureMCPDeployment(
	ctx context.Context,
	pvcName string,
	mcpImage string,
	mustGatherName string,
) error {
	existing := &appsv1.Deployment{}
	err := r.GetClient().Get(ctx, types.NamespacedName{
		Name:      mcpDeploymentName,
		Namespace: r.OperatorNamespace,
	}, existing)

	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get MCP deployment: %w", err)
	}

	if errors.IsNotFound(err) {
		desired := r.buildMCPDeployment(pvcName, mcpImage, mustGatherName)
		log.Info("Creating shared MCP server Deployment",
			"name", mcpDeploymentName, "namespace", r.OperatorNamespace, "pvc", pvcName)
		if createErr := r.GetClient().Create(ctx, desired); createErr != nil {
			if errors.IsAlreadyExists(createErr) {
				log.Info("MCP Deployment already exists (concurrent create) — continuing",
					"name", mcpDeploymentName)
				return nil
			}
			return createErr
		}
		return nil
	}

	// Check if the PVC claim name needs updating
	volumes := existing.Spec.Template.Spec.Volumes
	for i := range volumes {
		if volumes[i].Name != mcpVolumeName || volumes[i].PersistentVolumeClaim == nil {
			continue
		}

		if volumes[i].PersistentVolumeClaim.ClaimName == pvcName {
			// Same PVC already mounted — just make sure the annotation
			// reflects the latest requester so a future swap check is
			// comparing against the right "current owner".
			if existing.Annotations[mcpCurrentMustGatherAnnotation] != mustGatherName {
				if existing.Annotations == nil {
					existing.Annotations = map[string]string{}
				}
				existing.Annotations[mcpCurrentMustGatherAnnotation] = mustGatherName
				return r.GetClient().Update(ctx, existing)
			}
			return nil
		}

		// A different MustGather's PVC is requesting the mount. Before
		// swapping it out, make sure the currently-mounted MustGather isn't
		// still being read by an in-flight IntelliAide analysis — otherwise
		// the swap would yank the data out from under the agent mid-run.
		currentOwner := existing.Annotations[mcpCurrentMustGatherAnnotation]
		if currentOwner != "" && currentOwner != mustGatherName {
			inFlight, checkErr := r.proposalAnalysisInFlight(ctx, currentOwner)
			if checkErr != nil {
				log.Info("failed to check in-flight analysis status for shared MCP server, deferring PVC swap to be safe",
					"currentOwner", currentOwner, "requested", mustGatherName, "error", checkErr)
				return errMCPServerBusy
			}
			if inFlight {
				log.Info("Deferring shared MCP server PVC swap — another MustGather's analysis is still in flight",
					"currentOwner", currentOwner, "requested", mustGatherName,
					"currentPVC", volumes[i].PersistentVolumeClaim.ClaimName, "requestedPVC", pvcName)
				return errMCPServerBusy
			}
		}

		log.Info("Updating MCP server Deployment PVC",
			"old", volumes[i].PersistentVolumeClaim.ClaimName, "new", pvcName)
		existing.Spec.Template.Spec.Volumes[i].PersistentVolumeClaim.ClaimName = pvcName
		if existing.Annotations == nil {
			existing.Annotations = map[string]string{}
		}
		existing.Annotations[mcpCurrentMustGatherAnnotation] = mustGatherName
		return r.GetClient().Update(ctx, existing)
	}

	return nil
}

func (r *MustGatherReconciler) buildMCPDeployment(pvcName, mcpImage, mustGatherName string) *appsv1.Deployment {
	replicas := int32(1)
	labels := map[string]string{
		"app":                          mcpLabelApp,
		"app.kubernetes.io/component":  mcpLabelComponent,
		"app.kubernetes.io/part-of":    mcpLabelPartOf,
		"app.kubernetes.io/managed-by": "must-gather-operator",
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mcpDeploymentName,
			Namespace: r.OperatorNamespace,
			Labels:    labels,
			Annotations: map[string]string{
				mcpCurrentMustGatherAnnotation: mustGatherName,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": mcpLabelApp},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "must-gather-operator",
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
								{
									Weight: 1,
									Preference: corev1.NodeSelectorTerm{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      "node-role.kubernetes.io/infra",
												Operator: corev1.NodeSelectorOpExists,
											},
										},
									},
								},
							},
						},
					},
					Tolerations: []corev1.Toleration{
						{
							Key:      "node-role.kubernetes.io/infra",
							Operator: corev1.TolerationOpExists,
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "mcp-server",
							Image: mcpImage,
							Args: []string{
								"--port", "8080",
								"--toolsets", "openshift/mustgather",
								"--cluster-provider", "disabled",
								"--stateless",
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "mcp",
									ContainerPort: mcpContainerPort,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										// /mcp only accepts POST (JSON-RPC) and returns 405 to GET
										// probes. /healthz is the dedicated health check endpoint.
										Path: "/healthz",
										Port: intstr.FromInt(mcpContainerPort),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt(mcpContainerPort),
									},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       30,
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("512Mi"),
									corev1.ResourceCPU:    resource.MustParse("200m"),
								},
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("128Mi"),
									corev1.ResourceCPU:    resource.MustParse("50m"),
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      mcpVolumeName,
									MountPath: mcpMountPath,
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: mcpVolumeName,
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: pvcName,
									ReadOnly:  true,
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *MustGatherReconciler) ensureMCPService(ctx context.Context) error {
	existing := &corev1.Service{}
	err := r.GetClient().Get(ctx, types.NamespacedName{
		Name:      mcpServiceName,
		Namespace: r.OperatorNamespace,
	}, existing)

	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get MCP service: %w", err)
	}

	if errors.IsNotFound(err) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mcpServiceName,
				Namespace: r.OperatorNamespace,
				Labels: map[string]string{
					"app":                          mcpLabelApp,
					"app.kubernetes.io/component":  mcpLabelComponent,
					"app.kubernetes.io/part-of":    mcpLabelPartOf,
					"app.kubernetes.io/managed-by": "must-gather-operator",
				},
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": mcpLabelApp},
				Ports: []corev1.ServicePort{
					{
						Name:       "mcp",
						Port:       mcpContainerPort,
						TargetPort: intstr.FromInt(mcpContainerPort),
						Protocol:   corev1.ProtocolTCP,
					},
				},
			},
		}
		log.Info("Creating shared MCP server Service",
			"name", mcpServiceName, "namespace", r.OperatorNamespace)
		if createErr := r.GetClient().Create(ctx, svc); createErr != nil {
			if errors.IsAlreadyExists(createErr) {
				log.Info("MCP Service already exists (concurrent create) — continuing",
					"name", mcpServiceName)
				return nil
			}
			return createErr
		}
		return nil
	}

	return nil
}
