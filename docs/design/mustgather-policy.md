# Design: MustGatherPolicy

**Module**: `api/v1/mustgatherpolicy_types.go`, allowlist enforcement in `controllers/mustgather/policy_allowlist.go` and `mustgather_controller.go`
**Enhancement**: [must-gather-operator-policy](https://github.com/openshift/enhancements/pull/2094)

## Preconditions

- The `MustGatherPolicy` CRD is installed and serves `operator.openshift.io/v1` (project convention; controllers import `api/v1` only).
- Phase 1 is a cluster-scoped singleton: `metadata.name` must be `cluster` (CEL-enforced).
- The operator ServiceAccount can `get`/`list`/`watch` `mustgatherpolicies`.

## Invariants

- Creating a `MustGatherPolicy` is opt-in. With no policy object, MustGather upload behavior is unchanged (any SFTP host remains allowed).
- If the MustGatherPolicy CRD/kind is not yet served (`meta.IsNoMatchError` on Get), allowlist enforcement is skipped and hosts stay unconstrained. This covers operator-before-CRD / partial-apply upgrade edges without blocking SFTP. Transient Get failures still requeue.
- When `spec.uploadTargets` is omitted on an existing policy, upload destinations are not constrained. Other policy fields (storage, resources, networkPolicy) may be present without affecting uploads until their controllers exist.
- When `spec.uploadTargets` is set:
  - SFTP MustGather uploads are rejected unless `SFTP` is in `allowedTypes`.
  - When SFTP is allowed, `sftp.allowedHosts` must be non-empty and the effective host must match exactly.
- The effective SFTP host is `uploadTarget.sftp.host` when set/non-blank; otherwise `sftp.access.redhat.com` (CRD default). Allowlist checks, pre-flight dial, and the Job upload `host` env all use that same value (`effectiveSFTPHost`).
- Allowlist checks run before SFTP credential dial and before Job create. Failures are terminal via `setValidationFailureStatus`.
- Storage, resources, and networkPolicy fields are part of the API surface for forward compatibility. This release does not reconcile or apply them.

## Rationale

- `allowedTypes` plus per-type sub-fields (`sftp.allowedHosts`) keep method allowlisting separate from destination allowlisting so future S3/HTTP targets can add an enum value and sibling struct without reinterpreting `allowedHosts`.
- Omitting `uploadTargets` while a policy exists avoids breaking SFTP when administrators create the singleton for storage or other future fields (MG-389 and siblings).
- Application-level host checks (not NetworkPolicy hostname peers) are the enforceable control for custom SFTP hosts today.

## Trade-offs

- Empty `allowedHosts` after opting into `uploadTargets` denies all SFTP hosts; administrators must list the default host explicitly if they want the managed endpoint.
- Policy changes do not requeue in-flight MustGather Jobs; the MustGather spec is immutable and the allowlist is evaluated at Job create time only.
- Treating an unserved CRD as unconstrained prefers availability during upgrade over fail-closed allowlisting until the CRD appears.
