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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MustGatherSpec defines the desired state of MustGather
// +kubebuilder:validation:XValidation:rule="!(has(self.imageStreamRef) && has(self.gatherSpec) && has(self.gatherSpec.audit) && self.gatherSpec.audit)",message="audit mode is only supported with the default must-gather image"
// +kubebuilder:validation:XValidation:rule="!(!has(self.imageStreamRef) && has(self.gatherSpec) && has(self.gatherSpec.command) && size(self.gatherSpec.command) > 0 && has(self.gatherSpec.audit) && self.gatherSpec.audit)",message="audit mode cannot be combined with custom gather commands"
// +kubebuilder:validation:XValidation:rule="!(has(self.obfuscate) && has(self.obfuscate.enabled) && self.obfuscate.enabled && !(has(self.uploadTarget) || has(self.obfuscate.source) || has(self.storage)))",message="obfuscate.enabled requires uploadTarget, obfuscate.source, or storage"
// +kubebuilder:validation:XValidation:rule="!(has(self.obfuscate) && has(self.obfuscate.source) && (!has(self.obfuscate.enabled) || !self.obfuscate.enabled))",message="obfuscate.source requires obfuscate.enabled"
// +kubebuilder:validation:XValidation:rule="!(has(self.obfuscate) && has(self.obfuscate.source) && !has(self.uploadTarget))",message="obfuscate.source requires uploadTarget (obfuscated output is uploaded, not persisted on PVC)"
// +kubebuilder:validation:XValidation:rule="!(has(self.obfuscate) && has(self.obfuscate.source) && (has(self.imageStreamRef) || (has(self.gatherSpec) && (has(self.gatherSpec.command) || has(self.gatherSpec.audit) && self.gatherSpec.audit))))",message="obfuscate.source cannot be combined with imageStreamRef or gatherSpec.command/audit (gather is skipped)"
type MustGatherSpec struct {
	// serviceAccountName is the name of the ServiceAccount to use for running the must-gather Job.
	// This field is required and must reference a ServiceAccount with sufficient RBAC permissions
	// to collect cluster data. The operator will verify the ServiceAccount exists before creating the Job.
	// +required
	// +kubebuilder:validation:MinLength=1
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// imageStreamRef specifies a custom image from the allowlist to be used for the
	// must-gather run.
	// +optional
	ImageStreamRef *ImageStreamTagRef `json:"imageStreamRef,omitempty"`

	// gatherSpec allows overriding the command and/or arguments for the must-gather container
	// (default or custom image from imageStreamRef) and configures time-based collection filters.
	// Time-based filters (since, sinceTime) apply regardless of imageStreamRef.
	// Audit is only allowed with the default image and default gather command (see CRD validation rules).
	// +optional
	GatherSpec *GatherSpec `json:"gatherSpec,omitempty"`

	//nolint:kubeapilinter //reason: changing Duration to int would be an API-breaking change
	// mustGatherTimeout is a time limit for gather command to complete, a floating point number with a suffix:
	// "s" for seconds, "m" for minutes, "h" for hours.
	// Will default to no time limit.
	// +optional
	// +kubebuilder:validation:Format=duration
	MustGatherTimeout *metav1.Duration `json:"mustGatherTimeout,omitempty"`

	// uploadTarget is the target location for the must-gather bundle to be uploaded to.
	// If not specified, the bundle will not be uploaded.
	// +optional
	UploadTarget *UploadTargetSpec `json:"uploadTarget,omitempty"`

	// retainResourcesOnCompletion is a flag to specify if resources (secret, job, pods) should be
	// retained when the MustGather completes. If set to true, resources will be retained.
	// If false or not set, resources will be deleted (default behavior).
	// +default:=false
	// +optional
	RetainResourcesOnCompletion *bool `json:"retainResourcesOnCompletion,omitempty"`

	// storage is the storage configuration for persisting the collected must-gather tar archive.
	// If not specified, an ephemeral volume is used which will not persist
	// the tar archive on the cluster.
	// +optional
	Storage *Storage `json:"storage,omitempty"`

	// obfuscate configures post-gather obfuscation of sensitive data
	// (IPs, MACs, Secrets, ConfigMaps) before upload using must-gather-clean.
	// When obfuscate.enabled is true, the operator runs obfuscation on the
	// collected or referenced bundle before tarring and uploading.
	// Supported operational modes:
	//   - Gather + Obfuscate + Upload: enabled with uploadTarget (full pipeline)
	//   - Gather + Obfuscate + PVC: enabled with storage (cleaned output persisted, no upload)
	//   - Obfuscate + Upload: enabled with source and uploadTarget (redact existing bundle and upload)
	// +optional
	Obfuscate *ObfuscateConfig `json:"obfuscate,omitempty"`
}

// GatherSpec allows specifying the execution details for a must-gather run and the collection behavior.
// +kubebuilder:validation:XValidation:rule="!(has(self.since) && has(self.sinceTime))",message="only one of since or sinceTime may be specified"
type GatherSpec struct {
	// audit requests audit log collection via the default gather entrypoint.
	// It must be false when imageStreamRef is set or when gatherSpec.command is set without imageStreamRef.
	// +optional
	Audit *bool `json:"audit,omitempty"`

	// command is a string array representing the container entrypoint.
	// When set, it replaces the default gather wrapper for both the default must-gather image and custom images.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:Items:MaxLength=256
	Command []string `json:"command,omitempty"`

	// args is a string array of arguments passed to the container command.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:Items:MaxLength=256
	Args []string `json:"args,omitempty"`

	//nolint:kubeapilinter //reason: changing Duration to int would be an API-breaking change
	// since only returns logs newer than a relative duration like "2h" or "30m".
	// This is passed to the must-gather script to filter log collection.
	// Only one of since or sinceTime may be specified.
	// +optional
	// +kubebuilder:validation:Format=duration
	Since *metav1.Duration `json:"since,omitempty"`

	// sinceTime only returns logs after a specific date/time (RFC3339 format).
	// This is passed to the must-gather script to filter log collection.
	// Only one of since or sinceTime may be specified.
	// +optional
	// +kubebuilder:validation:Format=date-time
	SinceTime *metav1.Time `json:"sinceTime,omitempty"`
}

// ImageStreamTagRef provides a structured reference to a specific tag within an ImageStream.
type ImageStreamTagRef struct {
	// name is the name of the ImageStream resource in the operator's namespace.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name,omitempty"`

	// tag is the name of the tag within the ImageStream.
	// +required
	// +kubebuilder:validation:MinLength=1
	Tag string `json:"tag,omitempty"`
}

// SFTPSpec defines the desired state of SFTPSpec
// +kubebuilder:validation:XValidation:rule="size(self.caseID) > 0",message="caseID must not be empty"
// +kubebuilder:validation:XValidation:rule="size(self.caseManagementAccountSecretRef.name) > 0",message="caseManagementAccountSecretRef.name must not be empty"
type SFTPSpec struct {
	// caseID is the ID of the case this must gather will be uploaded to.
	// +required
	// +kubebuilder:validation:MinLength=1
	CaseID string `json:"caseID,omitempty"`

	// caseManagementAccountSecretRef is the secret containing a username and password field
	// to be used to authenticate with Red Hat case management systems.
	// +required
	CaseManagementAccountSecretRef corev1.LocalObjectReference `json:"caseManagementAccountSecretRef,omitempty"`

	// internalUser is a flag to specify if the upload user provided in the
	// caseManagementAccountSecret is a Red Hat internal user. See documentation for further information.
	// +default:=false
	// +optional
	InternalUser *bool `json:"internalUser,omitempty"`

	// host specifies the SFTP server hostname.
	// The host name of the SFTP server
	// +default:="sftp.access.redhat.com"
	// +optional
	Host *string `json:"host,omitempty"`
}

// UploadType defines the type of upload target.
type UploadType string

const (
	// UploadTypeSFTP corresponds to the SFTP upload type.
	UploadTypeSFTP UploadType = "SFTP"
)

// UploadTargetSpec defines the desired state of UploadTargetSpec
// +kubebuilder:validation:XValidation:rule="has(self.type) && self.type == 'SFTP' ? has(self.sftp) : !has(self.sftp)",message="sftp upload target config is required when upload type is SFTP, and forbidden otherwise"
// +union
type UploadTargetSpec struct {
	// type defines the method used for uploading to a specific target.
	// +unionDiscriminator
	// +kubebuilder:validation:Enum=SFTP
	// +required
	Type UploadType `json:"type,omitempty"`

	// sftp is the SFTP details for the upload.
	// +unionMember
	// +optional
	SFTP *SFTPSpec `json:"sftp,omitempty"`
}

// StorageType defines the type of storage to use for the must-gather collection.
// +kubebuilder:validation:Enum=PersistentVolume
type StorageType string

const (
	// StorageTypePersistentVolume corresponds to the PersistentVolume storage type.
	StorageTypePersistentVolume StorageType = "PersistentVolume"
)

// Storage defines the desired state of Storage
type Storage struct {
	// type defines the type of storage to use.
	// Available storage types are PersistentVolume only.
	// +required
	Type StorageType `json:"type,omitempty"`
	// persistentVolume defines the configuration for a PersistentVolume.
	// +required
	PersistentVolume PersistentVolumeConfig `json:"persistentVolume,omitzero"`
}

// PersistentVolumeConfig defines the configuration for a PersistentVolume.
// +kubebuilder:validation:XValidation:rule="!has(self.subPath) || !self.subPath.contains('..')",message="subPath must not contain '..'"
type PersistentVolumeConfig struct {
	// claim defines the PersistentVolumeClaim to use.
	// +required
	Claim PersistentVolumeClaimReference `json:"claim,omitzero"`
	// subPath defines the path to a sub directory within the PersistentVolume to use.
	// +optional
	SubPath *string `json:"subPath,omitempty"`
}

// PersistentVolumeClaimReference defines the reference to a PersistentVolumeClaim.
type PersistentVolumeClaimReference struct {
	// name defines the PersistentVolumeClaim to use,
	// should be already present in the same namespace.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="!format.dns1123Subdomain().validate(self).hasValue()",message="a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters, '-' or '.', and must start and end with an alphanumeric character."
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name,omitempty"`
}

// ObfuscateConfig configures the obfuscation behavior for a MustGather run.
// +kubebuilder:validation:XValidation:rule="!has(self.obfuscationConfigRef) || size(self.obfuscationConfigRef.name) > 0",message="obfuscationConfigRef.name must not be empty"
type ObfuscateConfig struct {
	// enabled activates obfuscation of the must-gather bundle.
	// When true, the operator runs obfuscation on the collected or
	// referenced bundle before tarring and uploading.
	// +default:=false
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// obfuscationConfigRef references a ConfigMap in the same namespace as
	// the MustGather CR containing a must-gather-clean configuration file.
	// The ConfigMap must have a key named "config.yaml" whose value is a
	// valid must-gather-clean obfuscation config.
	// If omitted, the operator uses the built-in default config which
	// consistently replaces IPs and MACs, and omits Secrets and ConfigMaps.
	// +optional
	ObfuscationConfigRef *corev1.LocalObjectReference `json:"obfuscationConfigRef,omitempty"`

	// source references an existing must-gather bundle on a PVC
	// for obfuscation without running a new gather.
	// When set, the operator skips the gather step and runs obfuscation
	// directly on the referenced PVC contents (mounted read-only).
	// The obfuscated output is written to a temporary volume and uploaded
	// via SFTP. Requires uploadTarget to be set.
	// The PVC must be in the same namespace as the MustGather CR.
	// +optional
	Source *PersistentVolumeConfig `json:"source,omitempty"`
}

// MustGatherStatus defines the observed state of MustGather
type MustGatherStatus struct {
	// conditions represent the latest available observations of the must-gather's state.
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
	// status is the current status of the must-gather operation.
	// +optional
	Status *string `json:"status,omitempty"`
	// lastUpdate is the timestamp of the last status update.
	// +optional
	LastUpdate *metav1.Time `json:"lastUpdate,omitempty"`
	// reason is a human-readable message indicating details about the current status.
	// +optional
	Reason *string `json:"reason,omitempty"`
	// completed indicates whether the must-gather operation has finished.
	// +optional
	Completed *bool `json:"completed,omitempty"`
}

func (m *MustGather) GetConditions() []metav1.Condition {
	if m.Status == nil {
		return nil
	}
	return m.Status.Conditions
}

func (m *MustGather) SetConditions(conditions []metav1.Condition) {
	if m.Status == nil {
		m.Status = &MustGatherStatus{}
	}
	m.Status.Conditions = conditions
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:storageversion

// MustGather is the Schema for the mustgathers API
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.spec) || self.spec == oldSelf.spec",message="spec values are immutable once set"
type MustGather struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired configuration for a must-gather operation.
	// +required
	Spec MustGatherSpec `json:"spec,omitzero"`
	// status is the observed state of the must-gather operation.
	// +optional
	Status *MustGatherStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// MustGatherList contains a list of MustGather
type MustGatherList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MustGather `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MustGather{}, &MustGatherList{})
}
