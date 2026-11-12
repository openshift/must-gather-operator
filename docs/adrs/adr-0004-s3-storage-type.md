# ADR-0004: S3 as a Storage Type, Not an Upload Target

**Status**: Accepted
**Date**: 2026-11-12
**Deciders**: Must-Gather Operator maintainers
**Component**: Must-Gather Operator

## Context

Users want to keep must-gather archives in their own S3-compatible bucket (AWS S3, ODF/NooBaa, Ceph RGW, MinIO) instead of, or in addition to, a PVC. [ADR-0003](adr-0003-extensible-upload-union.md) made `spec.uploadTarget` a union and named S3 as a likely future member.

`spec.uploadTarget` holds one target per CR. `spec.storage` and `spec.uploadTarget` can already be combined (PVC + SFTP). The common request is "archive in our bucket **and** send to the Red Hat case", which a single-member `uploadTarget` union cannot express.

## Decision

Add `S3` to the `spec.storage` union next to `PersistentVolume`:

```go
// +kubebuilder:validation:XValidation:rule="self.type == 'PersistentVolume' ? has(self.persistentVolume) : !has(self.persistentVolume)",message="..."
// +kubebuilder:validation:XValidation:rule="self.type == 'S3' ? has(self.s3) : !has(self.s3)",message="..."
// +union
type Storage struct {
	Type             StorageType            `json:"type,omitempty"`             // +unionDiscriminator, Enum=PersistentVolume;S3
	PersistentVolume PersistentVolumeConfig `json:"persistentVolume,omitzero"` // +unionMember
	S3               *S3Config              `json:"s3,omitempty"`              // +unionMember
}
```

`S3Config` holds `bucket`, optional `region`, `endpoint` (https only), `keyPrefix`, and `credentialsSecretRef` (Secret with `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`).

The upload container runs a new `must-gather-operator s3-upload` subcommand built on aws-sdk-go-v2 (`service/s3` + `feature/s3/transfermanager`).

## Rationale

- **Combinable with SFTP**: storage and upload target are separate fields, so one CR can archive to S3 and upload to a case.
- **Matches user intent**: S3 here replaces the PVC as the place the bundle is kept. It is storage from the user's point of view, even though it is written after gather rather than mounted.
- **Union pattern**: the same discriminated union shape and CEL rules as `uploadTarget`; invalid combinations are rejected at admission.
- **aws-sdk-go-v2**: official SDK. Multipart upload for large (audit) bundles, retries, custom endpoints with path-style addressing, proxy and system trust store through the standard HTTP client, and a path to STS / web identity later. Credentials stay in env, never on a command line.
- **OBC compatibility**: the Secret key names match ObjectBucketClaim Secrets, so ODF users reference the generated Secret directly.

## Consequences

### Positive
- PVC and S3 are interchangeable from the user's view; both can be combined with SFTP.
- No PVC or CSI driver is needed to keep an archive on-cluster-adjacent storage.
- Upload code is Go and unit-tested against a fake S3 server.

### Negative
- Unlike a PVC, S3 is not mounted: gather writes to emptyDir and the upload container must run to persist anything. An upload failure retries the whole Pod, including gather (same as SFTP).
- About 15 aws-sdk-go-v2 modules are vendored. `transfermanager` is pre-1.0; only `UploadObject` is used to limit churn.
- `storage.persistentVolume` became optional (union member). It stays a non-pointer to avoid a Go API break, with a `nolint:kubeapilinter` reason.
- `uploadTarget` stays SFTP-only. A future "send to someone else's bucket" upload target would be a separate decision.

## Alternatives Considered

### `uploadTarget.type: S3`
**Description**: Follow ADR-0003 literally and add S3 as a second `uploadTarget` member.
**Rejected because**: SFTP and S3 become mutually exclusive per CR; users who must send to Red Hat lose their own archive.

### Shell upload with `curl --aws-sigv4`
**Description**: No Go code; `build/bin/upload` signs a single PUT with curl.
**Rejected because**: single PUT caps objects at 5 GiB, no retries or multipart, and curl would have to be added to the image.

### minio-go
**Description**: Lighter S3 client.
**Rejected because**: maintainers preferred the official AWS SDK.

## References

- `api/v1/mustgather_types.go`: `Storage`, `S3Config` (mirrored in `api/v1alpha1/`)
- `pkg/s3upload/s3upload.go`: `s3-upload` subcommand
- [ADR-0003](adr-0003-extensible-upload-union.md)
