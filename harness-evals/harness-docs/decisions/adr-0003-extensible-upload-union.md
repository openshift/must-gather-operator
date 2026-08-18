# ADR-0003: Extensible Upload Target via Union API

**Status**: Accepted
**Date**: 2025-08-18
**Component**: Must-Gather Operator

## Context

The original community operator had flat top-level fields for SFTP upload (`caseID`, `caseManagementAccountSecretRef`, `ftpHost`). This design made it impossible to add new upload types (S3, HTTP) without breaking the API or creating ambiguous field combinations.

## Decision

Refactor upload configuration into a discriminated union under `spec.uploadTarget`:

```go
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
```

The XValidation CEL rule enforces that `sftp` is required when `type=SFTP` and forbidden otherwise.

## Rationale

- **Extensibility**: New upload types add a new enum value + union member without touching existing fields
- **CEL enforcement**: Invalid field combinations are rejected at API level
- **OpenShift API convention**: Follows the Kubernetes union pattern (`+union`, `+unionDiscriminator`, `+unionMember`)
- **Breaking change accepted**: The community API was pre-v1alpha1 adoption; this was the right time to restructure

## Consequences

### Positive
- Clean extension point for future upload targets (S3, HTTP, etc.)
- No ambiguous field combinations possible
- Upload target is optional — omitting it means gather-only (no upload)

### Negative
- Breaking change from community operator API
- Currently only one enum value (`SFTP`) — union feels heavy for a single type

## References

- `api/v1alpha1/mustgather_types.go:168-183` — UploadTargetSpec; `SFTPSpec` at lines 136-158
- [Extensible Upload Targets Enhancement](https://github.com/openshift/enhancements/blob/master/enhancements/support-log-gather/operator-upload-targets.md) (MG-53)
