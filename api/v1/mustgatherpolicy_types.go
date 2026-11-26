/*
Copyright 2026.

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

package v1

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// MustGatherPolicyName is the singleton name for MustGatherPolicy in Phase 1.
	MustGatherPolicyName = "cluster"

	// DefaultSFTPHost is the canonical default SFTP hostname used when a
	// MustGather leaves uploadTarget.sftp.host unset or blank. Operator logic
	// (allowlist, dial, Job host env) uses this constant. The kubebuilder
	// +default on SFTPSpec.Host in mustgather_types.go (and the generated
	// MustGather CRD) must stay identical; markers cannot reference Go consts.
	DefaultSFTPHost = "sftp.access.redhat.com"
)

// MustGatherPolicySpec defines cluster-wide defaults and guardrails for
// must-gather runs.
type MustGatherPolicySpec struct {
	// storage defines the cluster default storage configuration and lifecycle
	// policy for the PVCs used to persist must-gather archives.
	// +optional
	Storage *PolicyStorage `json:"storage,omitempty"`

	// resources defines the default resource requests and limits applied to the
	// must-gather gather workload (Job pods).
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// uploadTargets is an allow list constraining where collected data may be
	// uploaded. When omitted, upload destinations are not constrained by policy
	// (MustGather keeps existing behavior). When set, allowedTypes and per-type
	// constraints are enforced.
	// +optional
	UploadTargets *UploadTargetPolicy `json:"uploadTargets,omitempty"`

	// networkPolicy, when set, is materialized as a NetworkPolicy applied to the
	// gather pods to constrain their traffic (typically egress to approved upload
	// endpoints). The operator owns and manages the podSelector.
	// +optional
	NetworkPolicy *NetworkPolicyProfile `json:"networkPolicy,omitempty"`
}

// PolicyStorage extends the run-time Storage config with lifecycle controls that
// only make sense for operator-managed (provisioned) PVCs.
type PolicyStorage struct {
	// type defines the type of storage to use. PersistentVolume only.
	// +kubebuilder:validation:Enum=PersistentVolume
	// +required
	Type StorageType `json:"type"`

	// persistentVolume defines the default PVC template and lifecycle policy.
	// +required
	PersistentVolume ManagedPersistentVolumeConfig `json:"persistentVolume"`
}

// ManagedPersistentVolumeConfig describes how the operator provisions and manages
// PVCs on behalf of MustGather runs.
type ManagedPersistentVolumeConfig struct {
	// storageClassName is the StorageClass used to provision PVCs.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// size is the requested capacity for provisioned PVCs.
	// +required
	Size resource.Quantity `json:"size"`

	// retentionPeriod is how long a provisioned PVC (and its data) is retained
	// after the MustGather run completes before the operator reclaims it.
	// +optional
	// +kubebuilder:validation:Format=duration
	RetentionPeriod *metav1.Duration `json:"retentionPeriod,omitempty"`
}

// UploadTargetPolicy constrains the upload destinations a MustGather may use.
//
// allowedTypes is the discriminator: it says which upload target types are
// permitted at all. Constraints specific to a type live in the sub-field named
// for that type (sftp today), so a new type is added by appending one enum value
// and one sub-field, without reinterpreting any existing field.
//
// +kubebuilder:validation:XValidation:rule="!has(self.sftp) || (has(self.allowedTypes) && 'SFTP' in self.allowedTypes)",message="sftp may only be set when SFTP is listed in allowedTypes"
type UploadTargetPolicy struct {
	// allowedTypes lists the upload target types permitted by this policy. When
	// omitted or empty, no upload target is permitted.
	// +optional
	// +listType=set
	// +kubebuilder:validation:items:Enum=SFTP
	AllowedTypes []UploadType `json:"allowedTypes,omitempty"`

	// sftp constrains SFTP upload targets. It may only be set when SFTP appears
	// in allowedTypes. When SFTP is allowed and this is omitted, no SFTP host is
	// permitted.
	// +optional
	SFTP *SFTPUploadTargetPolicy `json:"sftp,omitempty"`
}

// SFTPUploadTargetPolicy constrains SFTP upload destinations.
type SFTPUploadTargetPolicy struct {
	// allowedHosts is an allow list of SFTP destination hostnames permitted as
	// upload targets. When omitted or empty, no SFTP host is permitted.
	// +optional
	// +listType=set
	AllowedHosts []string `json:"allowedHosts,omitempty"`
}

// NetworkPolicyProfile carries a NetworkPolicy spec to apply to gather pods.
type NetworkPolicyProfile struct {
	// spec is the NetworkPolicySpec applied to the gather pods. The operator
	// injects/overwrites the podSelector so it targets only the gather pods it
	// creates; any user-supplied podSelector is ignored.
	// +required
	Spec networkingv1.NetworkPolicySpec `json:"spec"`
}

// MustGatherPolicyStatus defines the observed state of MustGatherPolicy.
type MustGatherPolicyStatus struct {
	// observedGeneration is the most recent generation observed for this policy.
	// Rollout for a given spec is complete when observedGeneration equals
	// metadata.generation and the Progressing condition is False.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the latest available observations of the policy rollout.
	//   Available:   the policy has been applied cluster-wide.
	//   Progressing: rollout to namespaces is in progress.
	//   Degraded:    one or more namespaces failed; the message carries the count,
	//                e.g. "2 of 42 namespaces failed".
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'",message="MustGatherPolicy is a singleton; metadata.name must be 'cluster'"

// MustGatherPolicy is the Schema for the mustgatherpolicies API.
// It is a cluster-scoped singleton (metadata.name must be "cluster") that
// carries administrator defaults and guardrails for MustGather runs.
type MustGatherPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +optional
	Spec MustGatherPolicySpec `json:"spec,omitempty"`
	// +optional
	Status MustGatherPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MustGatherPolicyList contains a list of MustGatherPolicy
type MustGatherPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MustGatherPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MustGatherPolicy{}, &MustGatherPolicyList{})
}
