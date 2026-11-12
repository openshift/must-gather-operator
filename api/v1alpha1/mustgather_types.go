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
// +kubebuilder:validation:XValidation:rule="!(has(self.imageStreamRef) && has(self.gatherSpec) && has(self.gatherSpec.audit) && self.gatherSpec.audit)",message="audit mode is only supported with the default must-gather image"
// +kubebuilder:validation:XValidation:rule="!(!has(self.imageStreamRef) && has(self.gatherSpec) && has(self.gatherSpec.command) && size(self.gatherSpec.command) > 0 && has(self.gatherSpec.audit) && self.gatherSpec.audit)",message="audit mode cannot be combined with custom gather commands"
// +kubebuilder:validation:XValidation:rule="!(has(self.obfuscate) && has(self.obfuscate.enabled) && self.obfuscate.enabled && !(has(self.uploadTarget) || has(self.obfuscate.source) || has(self.storage)))",message="obfuscate.enabled requires uploadTarget, obfuscate.source, or storage"
// +kubebuilder:validation:XValidation:rule="!(has(self.obfuscate) && has(self.obfuscate.source) && (!has(self.obfuscate.enabled) || !self.obfuscate.enabled))",message="obfuscate.source requires obfuscate.enabled"
// +kubebuilder:validation:XValidation:rule="!(has(self.obfuscate) && has(self.obfuscate.source) && !has(self.uploadTarget))",message="obfuscate.source requires uploadTarget (obfuscated output is uploaded, not persisted on PVC)"
// +kubebuilder:validation:XValidation:rule="!(has(self.obfuscate) && has(self.obfuscate.source) && (has(self.imageStreamRef) || (has(self.gatherSpec) && (has(self.gatherSpec.command) || has(self.gatherSpec.audit) && self.gatherSpec.audit))))",message="obfuscate.source cannot be combined with imageStreamRef or gatherSpec.command/audit (gather is skipped)"
type MustGatherSpec struct {
	// ServiceAccountName is the name of the ServiceAccount to use for running the must-gather Job.
	// This field is required and must reference a ServiceAccount with sufficient RBAC permissions
	// to collect cluster data. The operator will verify the ServiceAccount exists before creating the Job.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ServiceAccountName string `json:"serviceAccountName"`

	// ImageStreamRef specifies a custom image from the allowlist to be used for the
	// must-gather run.
	// +kubebuilder:validation:Optional
	ImageStreamRef *ImageStreamTagRef `json:"imageStreamRef,omitempty"`

	// GatherSpec allows overriding the command and/or arguments for the must-gather container
	// (default or custom image from imageStreamRef) and configures time-based collection filters.
	// Time-based filters (since, sinceTime) apply regardless of imageStreamRef.
	// Audit is only allowed with the default image and default gather command (see CRD validation rules).
	// +kubebuilder:validation:Optional
	GatherSpec *GatherSpec `json:"gatherSpec,omitempty"`

	// A time limit for gather command to complete a floating point number with a suffix:
	// "s" for seconds, "m" for minutes, "h" for hours.
	// Will default to no time limit.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Format=duration
	MustGatherTimeout *metav1.Duration `json:"mustGatherTimeout,omitempty"`

	// The target location for the must-gather bundle to be uploaded to.
	// If not specified, the bundle will not be uploaded.
	// +kubebuilder:validation:Optional
	UploadTarget *UploadTargetSpec `json:"uploadTarget,omitempty"`

	// A flag to specify if resources (secret, job, pods) should be retained when the MustGather completes.
	// If set to true, resources will be retained. If false or not set, resources will be deleted (default behavior).
	// +kubebuilder:default:=false
	RetainResourcesOnCompletion *bool `json:"retainResourcesOnCompletion,omitempty"`

	// The storage configuration for persisting the collected must-gather tar archive.
	// Supported types are PersistentVolume (gather writes directly to a PVC) and
	// S3 (the archive is uploaded to an S3-compatible bucket after gathering).
	// storage can be combined with uploadTarget.
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
	//   - Gather + Obfuscate + PVC: enabled with PersistentVolume storage (cleaned output persisted, no upload)
	//   - Gather + Obfuscate + S3: enabled with S3 storage (cleaned output archived to the bucket)
	//   - Obfuscate + Upload: enabled with source and uploadTarget (redact existing bundle and upload)
	// +optional
	Obfuscate *ObfuscateConfig `json:"obfuscate,omitempty"`
}

// GatherSpec allows specifying the execution details for a must-gather run and the collection behavior.
// +kubebuilder:validation:XValidation:rule="!(has(self.since) && has(self.sinceTime))",message="only one of since or sinceTime may be specified"
type GatherSpec struct {
	// +kubebuilder:validation:Optional
	// Audit requests audit log collection via the default gather entrypoint.
	// It must be false when imageStreamRef is set or when gatherSpec.command is set without imageStreamRef.
	Audit bool `json:"audit,omitempty"`

	// +kubebuilder:validation:Optional
	// Command is a string array representing the container entrypoint.
	// When set, it replaces the default gather wrapper for both the default must-gather image and custom images.
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:Items:MaxLength=256
	Command []string `json:"command,omitempty"`

	// +kubebuilder:validation:Optional
	// Args is a string array of arguments passed to the container command.
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:Items:MaxLength=256
	Args []string `json:"args,omitempty"`

	// Since only returns logs newer than a relative duration like "2h" or "30m".
	// This is passed to the must-gather script to filter log collection.
	// Only one of since or sinceTime may be specified.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Format=duration
	Since *metav1.Duration `json:"since,omitempty"`

	// SinceTime only returns logs after a specific date/time (RFC3339 format).
	// This is passed to the must-gather script to filter log collection.
	// Only one of since or sinceTime may be specified.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Format=date-time
	SinceTime *metav1.Time `json:"sinceTime,omitempty"`
}

// ImageStreamTagRef provides a structured reference to a specific tag within an ImageStream.
type ImageStreamTagRef struct {
	// +kubebuilder:validation:Required
	// Name is the name of the ImageStream resource in the operator's namespace.
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	// Tag is the name of the tag within the ImageStream.
	Tag string `json:"tag"`
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

// StorageType defines the type of storage to use for the must-gather collection.
// +kubebuilder:validation:Enum=PersistentVolume;S3
type StorageType string

const (
	// StorageTypePersistentVolume corresponds to the PersistentVolume storage type.
	StorageTypePersistentVolume StorageType = "PersistentVolume"
	// StorageTypeS3 corresponds to the S3-compatible object storage type.
	StorageTypeS3 StorageType = "S3"
)

// Storage defines the desired state of Storage
// +kubebuilder:validation:XValidation:rule="self.type == 'PersistentVolume' ? has(self.persistentVolume) : !has(self.persistentVolume)",message="persistentVolume is required when type is PersistentVolume, and forbidden otherwise"
// +kubebuilder:validation:XValidation:rule="self.type == 'S3' ? has(self.s3) : !has(self.s3)",message="s3 is required when type is S3, and forbidden otherwise"
// +union
type Storage struct {
	// type defines the type of storage to use.
	// Available storage types are PersistentVolume and S3.
	// +unionDiscriminator
	// +required
	Type StorageType `json:"type"`
	// persistentVolume defines the configuration for a PersistentVolume.
	// Required when type is PersistentVolume, forbidden otherwise.
	// +unionMember
	// +optional
	PersistentVolume PersistentVolumeConfig `json:"persistentVolume,omitzero"`
	// s3 defines the configuration for an S3-compatible object storage bucket.
	// The collected must-gather is archived as <keyPrefix>/<directoryName>.tar.gz
	// and uploaded to the bucket once gathering (and obfuscation, if enabled) completes.
	// Required when type is S3, forbidden otherwise.
	// +unionMember
	// +optional
	S3 *S3Config `json:"s3,omitempty"`
}

// S3Config defines the configuration for an S3-compatible object storage bucket.
// +kubebuilder:validation:XValidation:rule="size(self.credentialsSecretRef.name) > 0",message="credentialsSecretRef.name must not be empty"
type S3Config struct {
	// bucket is the name of an existing bucket the must-gather archive is uploaded to.
	// +required
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=63
	Bucket string `json:"bucket"`

	// region is the region of the bucket.
	// When omitted, us-east-1 is used, which is also what most S3-compatible
	// stores (ODF/NooBaa, Ceph RGW, MinIO) expect.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	Region *string `json:"region,omitempty"`

	// endpoint is the URL of an S3-compatible service, for example
	// https://s3.openshift-storage.svc or https://minio.example.com:9000.
	// When omitted, the AWS S3 endpoint for the region is used.
	// When set, path-style addressing is used. Only https URLs are accepted.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="isURL(self) && url(self).getScheme() == 'https'",message="endpoint must be a valid https URL"
	Endpoint *string `json:"endpoint,omitempty"`

	// keyPrefix is prepended to the object key, for example "cluster-a/must-gathers".
	// It must not start with '/' and must not contain '..'.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('/') && !self.contains('..')",message="keyPrefix must not start with '/' or contain '..'"
	KeyPrefix *string `json:"keyPrefix,omitempty"`

	// credentialsSecretRef references a Secret in the same namespace as the MustGather CR
	// holding the keys AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY. This matches the
	// Secret generated for an ObjectBucketClaim, which can be referenced directly.
	// +required
	CredentialsSecretRef corev1.LocalObjectReference `json:"credentialsSecretRef"`
}

// PersistentVolumeConfig defines the configuration for a PersistentVolume.
// +kubebuilder:validation:XValidation:rule="!has(self.subPath) || !self.subPath.contains('..')",message="subPath must not contain '..'"
type PersistentVolumeConfig struct {
	// claim defines the PersistentVolumeClaim to use.
	// +required
	Claim PersistentVolumeClaimReference `json:"claim"`
	// subPath defines the path to a sub directory within the PersistentVolume to use.
	// +optional
	SubPath string `json:"subPath,omitempty"`
}

// PersistentVolumeClaimReference defines the reference to a PersistentVolumeClaim.
type PersistentVolumeClaimReference struct {
	// name defines the PersistentVolumeClaim to use,
	// should be already present in the same namespace.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="!format.dns1123Subdomain().validate(self).hasValue()",message="a lowercase RFC 1123 subdomain must consist of lower case alphanumeric characters, '-' or '.', and must start and end with an alphanumeric character."
	// +required
	Name string `json:"name"`
}

// ObfuscateConfig configures the obfuscation behavior for a MustGather run.
// +kubebuilder:validation:XValidation:rule="!has(self.obfuscationConfigRef) || size(self.obfuscationConfigRef.name) > 0",message="obfuscationConfigRef.name must not be empty"
type ObfuscateConfig struct {
	// enabled activates obfuscation of the must-gather bundle.
	// When true, the operator runs obfuscation on the collected or
	// referenced bundle before tarring and uploading.
	// +kubebuilder:default:=false
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
//+kubebuilder:deprecatedversion:warning="operator.openshift.io/v1alpha1 MustGather is deprecated; use operator.openshift.io/v1 MustGather"

// MustGather is the Schema for the mustgathers API
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.spec) || self.spec == oldSelf.spec",message="spec values are immutable once set"
type MustGather struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   MustGatherSpec   `json:"spec"`
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
