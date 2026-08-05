# ADR-0003: Extensible Upload Target via Union API

**Status**: Accepted
**Date**: 2025-08-18
**Component**: Must-Gather Operator

## Context

The original community operator had flat top-level fields for SFTP upload (`caseID`, `caseManagementAccountSecretRef`, `ftpHost`). This design made it impossible to add new upload types (S3, HTTP) without breaking the API or creating ambiguous field combinations.

## Decision

Refactor upload configuration into a discriminated union under `spec.uploadTarget`:

```go
type UploadTargetSpec struct {
    Type UploadType `json:"type"` // +unionDiscriminator, Enum=SFTP
    SFTP *SFTPSpec  `json:"sftp,omitempty"` // +unionMember
}
```

CEL validation enforces that `sftp` is required when `type=SFTP` and forbidden otherwise.

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

- `api/v1alpha1/mustgather_types.go:153-168` — UploadTargetSpec; `SFTPSpec` at lines 121-143
- [Extensible Upload Targets Enhancement](https://github.com/openshift/enhancements/blob/master/enhancements/support-log-gather/operator-upload-targets.md) (MG-53)
