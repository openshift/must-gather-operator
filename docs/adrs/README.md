# Architecture Decision Records

Dated, accepted decisions for the Must-Gather Operator. These records are the decision log. Living module contracts (preconditions, invariants, rationale) stay in [docs/design/](../design/README.md).

Do not rewrite an accepted ADR when code changes. Update the matching design doc instead. Add a new ADR (or mark one Deprecated/Superseded) only when the architectural choice itself changes.

Copy [adr-template.md](adr-template.md) for new records.

| ADR | Decision |
|---|---|
| [ADR-0001](adr-0001-immutable-spec.md) | MustGather spec is immutable after create (CEL) |
| [ADR-0002](adr-0002-two-container-job.md) | One Job with gather + conditional upload containers |
| [ADR-0003](adr-0003-extensible-upload-union.md) | Upload target is a discriminated union |
