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
	"path"
	"strings"
	"time"

	mustgatherv1alpha1 "github.com/openshift/must-gather-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	intelliAideSkillsImage  = "image-registry.openshift-image-registry.svc:5000/openshift-lightspeed/lightspeed-skills:latest"
	intelliAideSkillsPath   = "/skills/intelliaide"
	proposalTargetNamespace = "openshift-lightspeed"
	proposalAnalysisAgent   = "smart"
	proposalTimeoutMinutes  = 60

	proposalAPIGroup   = "agentic.openshift.io"
	proposalAPIVersion = "agentic.openshift.io/v1alpha1"
	proposalKind       = "Proposal"
	proposalResource   = "proposals"

	mcpServerName       = "must-gather"
	mcpServerURL        = "http://must-gather-mcp.must-gather-operator.svc:8080/mcp"
	mcpServerTimeoutSec = 60
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

// proposalNameFor returns the name of the Proposal CR generated for a
// MustGather with the given name.
func proposalNameFor(mustGatherName string) string {
	return fmt.Sprintf("intelliaide-%s", mustGatherName)
}

// proposalAnalysisInFlight reports whether the Proposal generated for the
// given MustGather name still has an active (non-terminal) analysis running.
//
// It returns false ("not in flight", i.e. safe to proceed) when:
//   - the Proposal CRD isn't installed (Lightspeed absent/uninstalled)
//   - no Proposal was ever created for this MustGather (not found)
//   - the Proposal's "Analyzed" condition is already status=True
//   - the Proposal is older than its own analysis timeout plus a grace
//     period, i.e. it is treated as abandoned/stale rather than blocking
//     forever on an agent that crashed or never reported a final status
//
// It returns true only when a Proposal exists, is recent, and has not yet
// reported a terminal "Analyzed" condition — i.e. an agent may still be
// actively reading from the shared MCP server's mounted PVC.
func (r *MustGatherReconciler) proposalAnalysisInFlight(ctx context.Context, mustGatherName string) (bool, error) {
	proposal := &unstructured.Unstructured{}
	proposal.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   proposalAPIGroup,
		Version: "v1alpha1",
		Kind:    proposalKind,
	})
	err := r.GetClient().Get(ctx, types.NamespacedName{
		Name:      proposalNameFor(mustGatherName),
		Namespace: proposalTargetNamespace,
	}, proposal)
	if err != nil {
		if errors.IsNotFound(err) || apimeta.IsNoMatchError(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get proposal for mustgather %s: %w", mustGatherName, err)
	}

	// Stale guard: don't block a PVC swap forever if the previous analysis
	// never reported a terminal condition (e.g. the agent sandbox crashed).
	staleAfter := time.Duration(proposalTimeoutMinutes+15) * time.Minute
	if time.Since(proposal.GetCreationTimestamp().Time) > staleAfter {
		return false, nil
	}

	conditions, found, err := unstructured.NestedSlice(proposal.Object, "status", "conditions")
	if err != nil || !found {
		// No status reported yet — the agent hasn't finished, so treat as in flight.
		return true, nil
	}

	for _, c := range conditions {
		condMap, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if condMap["type"] == "Analyzed" && condMap["status"] == "True" {
			return false, nil
		}
	}
	return true, nil
}

// createIntelliAideProposal creates a Proposal CR so the Lightspeed agentic
// platform can run IntelliAide RCA using the must-gather MCP server to access
// the collected diagnostic data on the PVC.
//
// It is a no-op (returns nil) when any guard fails:
//   - agenticDebuggingEnabled is false/nil
//   - Lightspeed is not installed on the cluster
//   - a Proposal already exists for this MustGather
//   - storage is not configured (PVC required for MCP access)
func (r *MustGatherReconciler) createIntelliAideProposal(
	ctx context.Context,
	instance *mustgatherv1alpha1.MustGather,
) error {
	if instance.Spec.AgenticDebuggingEnabled == nil || !*instance.Spec.AgenticDebuggingEnabled {
		return nil
	}

	if instance.Spec.Storage == nil || instance.Spec.Storage.Type != mustgatherv1alpha1.StorageTypePersistentVolume {
		log.Info("PVC storage not configured — skipping Proposal (MCP server requires PVC-backed data)",
			"mustgather", instance.Name)
		return nil
	}

	if !r.isLightspeedInstalled() {
		log.Info("OpenShift Lightspeed Agentic is not installed — skipping Proposal creation",
			"mustgather", instance.Name)
		return nil
	}

	proposalName := proposalNameFor(instance.Name)

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
		if apimeta.IsNoMatchError(err) {
			log.Info("Proposal CRD not found — Lightspeed may have been uninstalled",
				"mustgather", instance.Name)
			return nil
		}
		return fmt.Errorf("failed to check existing proposal: %w", err)
	}

	// Resolve the must-gather data path on the PVC by finding the Job's pod name.
	mustGatherDataPath, err := r.resolveMustGatherDataPath(ctx, instance)
	if err != nil {
		return fmt.Errorf("failed to resolve must-gather data path: %w", err)
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
					"You MUST use the IntelliAide skill to perform root cause analysis.\n"+
						"Do NOT attempt your own analysis. Follow these instructions EXACTLY.\n\n"+
						"=== ARCHITECTURE ===\n"+
						"The must-gather data lives on a PVC mounted on the MCP server pod.\n"+
						"The Python scripts fetch data from the MCP server automatically via an\n"+
						"internal adapter — you do NOT need to fetch files manually.\n"+
						"Use \"/tmp/must-gather-cache\" as --data-dir for all Python scripts.\n\n"+
						"=== MANDATORY STEPS ===\n\n"+
						"STEP 0 — Bootstrap MCP (DO NOT SKIP):\n"+
						"  Call MCP tool: mustgather_use(\"/data/%s\")\n"+
						"  This tells the MCP server which collection to load.\n"+
						"  Verify with: mustgather_summary()\n\n"+
						"STEP 1 — Validate data source:\n"+
						"  mkdir -p /tmp/must-gather-cache\n"+
						"  python /app/skills/intelliaide/extract_cluster.py \\\n"+
						"    --query \"Perform comprehensive root cause analysis\" \\\n"+
						"    --data-dir /tmp/must-gather-cache\n"+
						"  Capture job_dir from output. Stop if success=false.\n\n"+
						"STEP 2 — Select files:\n"+
						"  python /app/skills/intelliaide/select_files.py --job-dir <job_dir>\n"+
						"  cat <prompt_path> from output, then write file_selection.json.\n\n"+
						"STEP 3 — Analyze (adapter fetches files from MCP automatically):\n"+
						"  python /app/skills/intelliaide/analyze_data.py --job-dir <job_dir> --priority high\n\n"+
						"STEP 4 — Chunk and analyze RCA:\n"+
						"  python /app/skills/intelliaide/perform_rca.py --job-dir <job_dir> --priority high\n"+
						"  Read each chunk file, write chunk summaries.\n\n"+
						"STEP 5 — Reduce and synthesize:\n"+
						"  python /app/skills/intelliaide/perform_rca.py --job-dir <job_dir> --priority high \\\n"+
						"    --mode reduce --level 1 --summary-files <chunk_summaries...>\n"+
						"  Repeat until is_final=true.\n\n"+
						"=== CRITICAL RULES ===\n"+
						"- NEVER skip Step 0. mustgather_use() MUST be called first.\n"+
						"- ALWAYS use \"/tmp/must-gather-cache\" as --data-dir.\n"+
						"- Do NOT manually fetch files via MCP tools — the Python scripts handle it.\n"+
						"- Execute scripts in order. Do NOT skip steps.\n"+
						"- Do NOT cat must-gather files directly (buffer overflow risk).\n\n"+
						"Triggered by MustGather %s/%s.",
					mustGatherDataPath, instance.Namespace, instance.Name,
				),
				"tools": map[string]interface{}{
					"skills": []interface{}{
						map[string]interface{}{
							"image": intelliAideSkillsImage,
							"paths": []interface{}{
								intelliAideSkillsPath,
							},
						},
					},
					"mcpServers": []interface{}{
						map[string]interface{}{
							"name":           mcpServerName,
							"url":            mcpServerURL,
							"timeoutSeconds": int64(mcpServerTimeoutSec),
						},
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
		"proposal", proposalName, "mustgather", instance.Name,
		"mcpDataPath", mustGatherDataPath)
	return nil
}

// resolveMustGatherDataPath determines the path within the PVC where the
// must-gather data was written. The path follows the pattern:
// {subPath}/{podName} (matching the SubPathExpr used in template.go).
func (r *MustGatherReconciler) resolveMustGatherDataPath(
	ctx context.Context,
	instance *mustgatherv1alpha1.MustGather,
) (string, error) {
	base := ""
	if instance.Spec.Storage != nil && instance.Spec.Storage.Type == mustgatherv1alpha1.StorageTypePersistentVolume {
		base = strings.TrimSpace(instance.Spec.Storage.PersistentVolume.SubPath)
		base = strings.Trim(base, "/")
	}

	// Find the pod name from the completed Job
	job := &batchv1.Job{}
	if err := r.GetClient().Get(ctx, types.NamespacedName{
		Name:      instance.Name,
		Namespace: instance.Namespace,
	}, job); err != nil {
		return "", fmt.Errorf("failed to get job %s: %w", instance.Name, err)
	}

	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(instance.Namespace),
		client.MatchingLabels{"controller-uid": string(job.UID)},
	}
	if err := r.GetClient().List(ctx, podList, listOpts...); err != nil {
		return "", fmt.Errorf("failed to list pods for job %s: %w", instance.Name, err)
	}

	if len(podList.Items) == 0 {
		return "", fmt.Errorf("no pods found for job %s", instance.Name)
	}

	podName := podList.Items[0].Name
	return path.Join(base, podName), nil
}
