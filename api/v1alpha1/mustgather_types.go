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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MustGatherSpec defines the desired state of MustGather
// +kubebuilder:validation:XValidation:rule="!(has(self.gatherSpec) && (size(self.gatherSpec.command) > 0 || size(self.gatherSpec.args) > 0)) || has(self.imageStreamRef)",message="command and args in gatherSpec can only be set when imageStreamRef is specified"
type MustGatherSpec struct {
	// serviceAccountName is the service account to use to run the must gather job pod, defaults to default
	// +optional
	// +default="default"
	ServiceAccountName *string `json:"serviceAccountName,omitempty"`

	// imageStreamRef specifies a custom image from the allowlist to be used for the
	// must-gather run.
	// +optional
	ImageStreamRef *ImageStreamTagRef `json:"imageStreamRef,omitempty"`

	// gatherSpec allows overriding the command and/or arguments for the custom must-gather image.
	// This field is ignored if ImageStreamRef is not specified.
	// +optional
	GatherSpec *GatherSpec `json:"gatherSpec,omitempty"`

	// mustGatherTimeout is a time limit for gather command to complete a floating point number with a suffix:
	// "s" for seconds, "m" for minutes, "h" for hours.
	// Will default to no time limit.
	// +optional
	// +kubebuilder:validation:Format=duration
	MustGatherTimeout *metav1.Duration `json:"mustGatherTimeout,omitempty"` //nolint:kubeapilinter // existing API field, changing type would break compatibility

	// uploadTarget is the target location for the must-gather bundle to be uploaded to.
	// If not specified, the bundle will not be uploaded.
	// +optional
	UploadTarget *UploadTargetSpec `json:"uploadTarget,omitempty"`

	// retainResourcesOnCompletion is a flag to specify if resources (secret, job, pods) should be retained when the MustGather completes.
	// If set to true, resources will be retained. If false or not set, resources will be deleted (default behavior).
	// +optional
	// +default=false
	RetainResourcesOnCompletion *bool `json:"retainResourcesOnCompletion,omitempty"`

	// storage is the storage configuration for persisting the collected must-gather tar archive.
	// If not specified, an ephemeral volume is used which will not persist
	// the tar archive on the cluster.
	// +optional
	Storage *Storage `json:"storage,omitempty"`
}

// GatherSpec allows specifying the execution details for a must-gather run.
type GatherSpec struct {
	// audit specifies whether to collect audit logs. This is translated to a signal
	// or command that can be respected by the default image
	// or any custom image designed to do so.
	// +optional
	Audit *bool `json:"audit,omitempty"`

	// command is a string array representing the entrypoint for the custom image.
	// This field is only honored when a custom image IS specified via imageStreamRef.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:Items:MaxLength=256
	Command []string `json:"command,omitempty"`

	// args is a string array of arguments passed to the custom image's command.
	// This field is only honored when a custom image IS specified via imageStreamRef.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:Items:MaxLength=256
	Args []string `json:"args,omitempty"`
}

// ImageStreamTagRef provides a structured reference to a specific tag within an ImageStream.
type ImageStreamTagRef struct {
	// name is the name of the ImageStream resource in the operator's namespace.
	// +required
	Name *string `json:"name,omitempty"`

	// tag is the name of the tag within the ImageStream.
	// +required
	Tag *string `json:"tag,omitempty"`
}

// SFTPSpec defines the desired state of SFTPSpec
// +kubebuilder:validation:XValidation:rule="size(self.caseID) > 0",message="caseID must not be empty"
// +kubebuilder:validation:XValidation:rule="size(self.caseManagementAccountSecretRef.name) > 0",message="caseManagementAccountSecretRef.name must not be empty"
type SFTPSpec struct {
	// caseID is the ID of the case this must gather will be uploaded to
	// +required
	CaseID *string `json:"caseID,omitempty"`

	// caseManagementAccountSecretRef is the secret containing a username and password field to be used to authenticate with red hat case management systems
	// +required
	CaseManagementAccountSecretRef corev1.LocalObjectReference `json:"caseManagementAccountSecretRef,omitempty"`

	// internalUser is a flag to specify if the upload user provided in the caseManagementAccountSecret is a RH internal user.
	// See documentation for further information.
	// +optional
	// +default=false
	InternalUser *bool `json:"internalUser,omitempty"`

	// host specifies the SFTP server hostname.
	// The host name of the SFTP server
	// +default="sftp.access.redhat.com"
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

	// sftp details for the upload.
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
	Name *string `json:"name,omitempty"`
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
	Status string `json:"status,omitempty"` //nolint:kubeapilinter // existing API field, changing to pointer would break compatibility
	// lastUpdate is the time of the last status update.
	// +optional
	LastUpdate metav1.Time `json:"lastUpdate,omitempty"` //nolint:kubeapilinter // existing API field, changing to pointer would break compatibility
	// reason provides additional detail about the current status.
	// +optional
	Reason string `json:"reason,omitempty"` //nolint:kubeapilinter // existing API field, changing to pointer would break compatibility
	// completed indicates whether the must-gather operation has finished.
	// +optional
	Completed bool `json:"completed"` //nolint:kubeapilinter // existing API field, changing to pointer would break compatibility
}

func (m *MustGather) GetConditions() []metav1.Condition {
	return m.Status.Conditions
}

func (m *MustGather) SetConditions(conditions []metav1.Condition) {
	m.Status.Conditions = conditions
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// MustGather is the Schema for the mustgathers API
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.spec) || self.spec == oldSelf.spec",message="spec values are immutable once set"
type MustGather struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of the must-gather operation.
	// +optional
	Spec MustGatherSpec `json:"spec,omitempty"` //nolint:kubeapilinter // Spec is conventionally not a pointer in Kubernetes API types
	// status defines the observed state of the must-gather operation.
	// +optional
	Status MustGatherStatus `json:"status,omitempty"` //nolint:kubeapilinter // Status is conventionally not a pointer in Kubernetes API types
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
