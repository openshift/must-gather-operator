# Design: MustGather API

**Module**: `api/v1/mustgather_types.go` (storage); `api/v1alpha1/` deprecated but still served.
**ADRs**: [adr-0001](../adrs/adr-0001-immutable-spec.md), [adr-0003](../adrs/adr-0003-extensible-upload-union.md)
**Field catalog**: [domain/mustgather.md](../../harness-evals/harness-docs/domain/mustgather.md)

## Preconditions

- The CRD is installed and serves `operator.openshift.io/v1` as the storage version.
- New CRs use `v1`. `v1alpha1` exists only for existing objects.
- `serviceAccountName` is non-empty. The operator does not invent an SA.
- `uploadTarget.type` is set whenever `uploadTarget` is present; CEL requires `sftp` if and only if `type=SFTP`.
- `gatherSpec.since` and `gatherSpec.sinceTime` are mutually exclusive.

## Invariants

- Spec is immutable after create: `!has(oldSelf.spec) || self.spec == oldSelf.spec`. Correcting a typo means delete and recreate.
- One CR is one gather operation, not a desired-state loop. Status may change; spec must not.
- `uploadTarget` is a discriminated union (`+union` / `+unionDiscriminator` / `+unionMember`). Invalid type/member combinations never reach the controller.
- Generated files (`zz_generated.deepcopy.go`, `zz_generated.openapi.go`, CRD YAML) are produced by `make generate` and `make manifests`. They are never hand-edited.
- New required spec fields either have a default or are optional so existing immutable CRs remain valid.

## Rationale

- CEL spec immutability was chosen rather than controller-side diffing: the Job is already running with the original parameters, and Kubernetes cannot safely rewrite gather image, command, timeout, or upload target in place.
- The upload union replaced flat SFTP fields so a later S3 or HTTP target can add an enum value and member without ambiguous combinations. The breaking change was accepted before v1 GA.
- Dual-version CRD (`v1` + deprecated `v1alpha1`) keeps existing objects working while new work lands only on `v1`.

## Trade-offs

- Users cannot retry a failed gather with a different timeout on the same CR.
- A union with a single `SFTP` member is heavier than a flat struct; extensibility was the design decision.
