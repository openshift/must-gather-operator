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
	"fmt"

	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

const (
	intelliAideSkillsImage  = "image-registry.openshift-image-registry.svc:5000/openshift-lightspeed/lightspeed-skills:latest"
	intelliAideSkillsPath   = "/skills/intelliaide"
	proposalTargetNamespace = "openshift-lightspeed"
	proposalAnalysisAgent   = "smart"
	proposalTimeoutMinutes  = 45

	proposalAPIGroup   = "agentic.openshift.io"
	proposalAPIVersion = "agentic.openshift.io/v1alpha1"
	proposalKind       = "Proposal"
	proposalResource   = "proposals"
)

var proposalGVR = schema.GroupVersionResource{
	Group:    proposalAPIGroup,
	Version:  "v1alpha1",
	Resource: proposalResource,
}

// isLightspeedInstalled checks whether the Lightspeed Agentic Operator is
// installed by querying the API server's discovery endpoint for the Proposal
// CRD. Returns false when the agentic.openshift.io API group is absent.
func (r *MustGatherReconciler) isLightspeedInstalled() bool {
	dc, err := r.GetDiscoveryClient()
	if err != nil {
		log.Info("unable to create discovery client, assuming Lightspeed not installed", "error", err)
		return false
	}
	resources, err := dc.ServerResourcesForGroupVersion(proposalAPIVersion)
	if err != nil {
		return false
	}
	for _, res := range resources.APIResources {
		if res.Kind == proposalKind {
			return true
		}
	}
	return false
}

// createIntelliAideProposal creates a Proposal CR targeting the must-gather
// PVC so the Lightspeed agentic platform can run IntelliAide RCA.
//
// It is a no-op (returns nil) when any guard fails:
//   - agenticDebuggingEnabled is false/nil
//   - spec.storage is not set (no PVC to point to)
//   - MustGather is not in the Proposal target namespace (PVCs are namespace-scoped)
//   - Lightspeed is not installed on the cluster
//   - a Proposal already exists for this MustGather
func (r *MustGatherReconciler) createIntelliAideProposal(
	ctx context.Context,
	instance *mustgatherv1alpha1.MustGather,
) error {
	if instance.Spec.AgenticDebuggingEnabled == nil || !*instance.Spec.AgenticDebuggingEnabled {
		return nil
	}

	if instance.Spec.Storage == nil {
		log.Info("agenticDebuggingEnabled is true but spec.storage is not set — skipping Proposal creation",
			"mustgather", instance.Name)
		return nil
	}

	if instance.Namespace != proposalTargetNamespace {
		log.Info("agenticDebuggingEnabled is true but MustGather is not in the Proposal namespace — skipping Proposal creation",
			"mustgather", instance.Name,
			"mustgatherNamespace", instance.Namespace,
			"requiredNamespace", proposalTargetNamespace)
		return nil
	}

	if !r.isLightspeedInstalled() {
		log.Info("OpenShift Lightspeed Agentic is not installed — skipping Proposal creation",
			"mustgather", instance.Name)
		return nil
	}

	pvcName := instance.Spec.Storage.PersistentVolume.Claim.Name
	proposalName := fmt.Sprintf("intelliaide-%s", instance.Name)

	// Idempotency: check if Proposal already exists
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   proposalAPIGroup,
		Version: "v1alpha1",
		Kind:    proposalKind,
	})
	err := r.GetClient().Get(ctx, types.NamespacedName{
		Name:      proposalName,
		Namespace: proposalTargetNamespace,
	}, existing)
	if err == nil {
		log.Info("Proposal already exists, skipping creation",
			"proposal", proposalName, "mustgather", instance.Name)
		return nil
	}
	if !errors.IsNotFound(err) {
		// If we get a NoMatch error here, Lightspeed was just uninstalled
		if apimeta.IsNoMatchError(err) {
			log.Info("Proposal CRD not found — Lightspeed may have been uninstalled",
				"mustgather", instance.Name)
			return nil
		}
		return fmt.Errorf("failed to check existing proposal: %w", err)
	}

	proposal := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": proposalAPIVersion,
			"kind":       proposalKind,
			"metadata": map[string]interface{}{
				"name":      proposalName,
				"namespace": proposalTargetNamespace,
				"labels": map[string]interface{}{
					"agentic.openshift.io/source":       "intelliaide",
					"agentic.openshift.io/mode":         "must-gather",
					"agentic.openshift.io/trigger":      "must-gather-operator",
					"operator.openshift.io/must-gather": instance.Name,
				},
			},
			"spec": map[string]interface{}{
				"request": fmt.Sprintf(
					"Perform root cause analysis (RCA) using IntelliAide Skill on the must-gather "+
						"bundle collected by MustGather %q.\n\n"+
						"Use must-gather data from PVC %q mounted at /data/input to investigate.",
					instance.Name, pvcName,
				),
				"targetNamespaces": []interface{}{
					proposalTargetNamespace,
				},
				"tools": map[string]interface{}{
					"skills": []interface{}{
						map[string]interface{}{
							"image": intelliAideSkillsImage,
							"paths": []interface{}{
								intelliAideSkillsPath,
							},
						},
					},
					"dataSource": map[string]interface{}{
						"claimName": pvcName,
					},
				},
				"analysis": map[string]interface{}{
					"agent":          proposalAnalysisAgent,
					"timeoutMinutes": int64(proposalTimeoutMinutes),
				},
			},
		},
	}

	if err := r.GetClient().Create(ctx, proposal); err != nil {
		if apimeta.IsNoMatchError(err) {
			log.Info("Proposal CRD not found on Create — Lightspeed may have been uninstalled",
				"mustgather", instance.Name)
			return nil
		}
		return fmt.Errorf("failed to create IntelliAide proposal: %w", err)
	}

	log.Info("Created IntelliAide Proposal",
		"proposal", proposalName, "pvc", pvcName, "mustgather", instance.Name)
	return nil
}
