# Design intent

Module-level **preconditions**, **invariants**, and **rationale** for the Must-Gather Operator. Implementation detail lives in [architecture/components.md](../../harness-evals/harness-docs/architecture/components.md). Accepted ADRs live in [docs/adrs/](../adrs/README.md).

This directory is the source of truth for *why* a module behaves as it does. Agents and reviewers must update the matching file when they change component boundaries, data flows, or API contracts.

| Module | Sources | Design doc |
|---|---|---|
| MustGather API | `api/v1/mustgather_types.go` | [api.md](api.md) |
| Reconciler, cleanup, metrics, trusted CA | `controllers/mustgather/mustgather_controller.go` | [controller.md](controller.md) |
| Job spec | `controllers/mustgather/template.go` | [job-template.md](job-template.md) |
| Event filters | `controllers/mustgather/predicates.go` | [predicates.md](predicates.md) |
| SFTP validation and upload | `controllers/mustgather/validation.go`, `build/bin/upload` | [upload.md](upload.md) |

## When to update

Required when changing any of:

- `api/*/mustgather_types.go` (fields, CEL, union discriminators)
- `controllers/mustgather/mustgather_controller.go`
- `controllers/mustgather/template.go`
- `controllers/mustgather/predicates.go`
- `controllers/mustgather/validation.go`
- `build/bin/upload` or `build/bin/https-proxy-connect-util`

Pre-commit hook `check-design-docs` enforces that the **matching** design doc from the table above is staged with those sources (not an unrelated file under `docs/design/`). Skip only with `SKIP=check-design-docs`.
