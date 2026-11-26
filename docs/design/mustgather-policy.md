# Design: MustGatherPolicy

**Module**: `api/v1/mustgatherpolicy_types.go`
**Enhancement**: [must-gather-operator-policy](https://github.com/openshift/enhancements/pull/2094)

## Preconditions

- The `MustGatherPolicy` CRD is installed and serves `operator.openshift.io/v1` (project convention; controllers import `api/v1` only).
- Phase 1 is a cluster-scoped singleton: `metadata.name` must be `cluster` (CEL-enforced).

## Invariants

- Creating a `MustGatherPolicy` is opt-in. With no policy object, MustGather upload behavior is unchanged.
- When `spec.uploadTargets` is omitted on an existing policy, upload destinations are not constrained by this API. Other policy fields (storage, resources, networkPolicy) may be present without affecting uploads until their controllers exist.
- When `spec.uploadTargets` is set, its shape is:
  - `allowedTypes` lists permitted upload methods (`SFTP` today).
  - `sftp.allowedHosts` lists permitted SFTP hostnames and may only be set when `SFTP` is in `allowedTypes` (CEL-enforced).
- `DefaultSFTPHost` (`sftp.access.redhat.com`) is the canonical default hostname for MustGather SFTP when host is unset; the kubebuilder `+default` on `SFTPSpec.Host` must stay identical.
- Storage, resources, and networkPolicy fields are part of the API surface for forward compatibility. This release does not reconcile or apply them.

## Rationale

- `allowedTypes` plus per-type sub-fields (`sftp.allowedHosts`) keep method allowlisting separate from destination allowlisting so future S3/HTTP targets can add an enum value and sibling struct without reinterpreting `allowedHosts`.
- Omitting `uploadTargets` while a policy exists avoids breaking SFTP when administrators create the singleton for storage or other future fields (MG-389 and siblings).

## Trade-offs

- Empty `allowedHosts` after opting into `uploadTargets` means no SFTP host is permitted by the API contract; administrators must list the default host explicitly if they want the managed endpoint.
- Runtime enforcement of `uploadTargets` is delivered in a follow-up change; this PR only introduces the API, CRD, and example.
