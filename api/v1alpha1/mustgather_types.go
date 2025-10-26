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
type MustGatherSpec struct {
	// the service account to use to run the must gather job pod, defaults to default
	// +kubebuilder:validation:Optional
	/* +kubebuilder:default:="{Name:default}" */
	ServiceAccountRef corev1.LocalObjectReference `json:"serviceAccountRef,omitempty"`

	// A flag to specify if audit logs must be collected
	// See documentation for further information.
	// +kubebuilder:default:=false
	Audit bool `json:"audit,omitempty"`

	// This represents the proxy configuration to be used. If left empty it will default to the cluster-level proxy configuration.
	// +kubebuilder:validation:Optional
	ProxyConfig ProxySpec `json:"proxyConfig,omitempty"`

	// A time limit for gather command to complete a floating point number with a suffix:
	// "s" for seconds, "m" for minutes, "h" for hours, or "d" for days.
	// Will default to no time limit.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Format=duration
	MustGatherTimeout metav1.Duration `json:"mustGatherTimeout,omitempty"`

	// The target location for the must-gather bundle to be uploaded to.
	// If not specified, the bundle will not be uploaded.
	// +kubebuilder:validation:Optional
	UploadTarget *UploadTargetSpec `json:"uploadTarget,omitempty"`

	// A flag to specify if resources (secret, job, pods) should be retained when the MustGather completes.
	// If set to true, resources will be retained. If false or not set, resources will be deleted (default behavior).
	// +kubebuilder:default:=false
	RetainResourcesOnCompletion bool `json:"retainResourcesOnCompletion,omitempty"`

	// The storage configuration for the must-gather execution.
	// If not specified, an ephemeral volume is used.
	// +optional
	Storage *Storage `json:"storage,omitempty"`
}

// SFTPSpec defines the desired state of SFTPSpec
// +kubebuilder:validation:XValidation:rule="size(self.caseID) > 0",message="caseID must not be empty"
// +kubebuilder:validation:XValidation:rule="size(self.caseManagementAccountSecretRef.name) > 0",message="caseManagementAccountSecretRef.name must not be empty"
type SFTPSpec struct {
	// The ID of the case this must gather will be uploaded to
	// +kubebuilder:validation:Required
	CaseID string `json:"caseID"`

	// the secret container a username and password field to be used to authenticate with red hat case management systems
	// +kubebuilder:validation:Required
	CaseManagementAccountSecretRef corev1.LocalObjectReference `json:"caseManagementAccountSecretRef"`

	// A flag to specify if the upload user provided in the caseManagementAccountSecret is a RH internal user.
	// See documentation for further information.
	// +kubebuilder:default:=false
	InternalUser bool `json:"internalUser,omitempty"`

	// host specifies the SFTP server hostname.
	// The host name of the SFTP server
	// +kubebuilder:default:="sftp.access.redhat.com"
	// +optional
	Host string `json:"host,omitempty"`
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
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=SFTP
	// +required
	Type UploadType `json:"type"`

	// SFTP details for the upload.
	// +unionMember
	// +optional
	SFTP *SFTPSpec `json:"sftp,omitempty"`
}

// StorageType defines the type of storage to use for the must-gather execution.
// +kubebuilder:validation:Enum=PersistentVolume
type StorageType string

const (
	// StorageTypePersistentVolume corresponds to the PersistentVolume storage type.
	StorageTypePersistentVolume StorageType = "PersistentVolume"
)

// Storage defines the desired state of Storage
type Storage struct {
	// type defines the type of storage to use.
	// +required
	Type StorageType `json:"type"`
	// persistentVolume defines the configuration for a PersistentVolume.
	// +required
	PersistentVolume PersistentVolumeConfig `json:"persistentVolume"`
}

// PersistentVolumeConfig defines the configuration for a PersistentVolume.
type PersistentVolumeConfig struct {
	// claim defines the PersistentVolumeClaim to use.
	// +required
	Claim PersistentVolumeClaimReference `json:"claim"`
	// subPath defines the sub-path within the PersistentVolume to use.
	// +optional
	SubPath string `json:"subPath,omitempty"`
}

// PersistentVolumeClaimReference defines the reference to a PersistentVolumeClaim.
type PersistentVolumeClaimReference struct {
	// name defines the name of the PersistentVolumeClaim.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="!format.dns1123Subdomain().validate(self).hasValue()",message="a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters, '-' or '.', and must start and end with an alphanumeric character."
	// +required
	Name string `json:"name"`
}

// +k8s:openapi-gen=true
type ProxySpec struct {
	// httpProxy is the URL of the proxy for HTTP requests.
	// +kubebuilder:validation:Required
	HTTPProxy string `json:"httpProxy"`

	// httpsProxy is the URL of the proxy for HTTPS requests.
	// +kubebuilder:validation:Required
	HTTPSProxy string `json:"httpsProxy"`

	// noProxy is the list of domains for which the proxy should not be used.  Empty means unset and will not result in an env var.
	// +optional
	NoProxy string `json:"noProxy,omitempty"`
}

// MustGatherStatus defines the observed state of MustGather
type MustGatherStatus struct {
	Status     string             `json:"status,omitempty"`
	LastUpdate metav1.Time        `json:"lastUpdate,omitempty"`
	Reason     string             `json:"reason,omitempty"`
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
	Completed  bool               `json:"completed"`
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
type MustGather struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MustGatherSpec   `json:"spec,omitempty"`
	Status MustGatherStatus `json:"status,omitempty"`
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
