---
name: update-design-docs
description: >-
  Review and update docs/design when changing MustGather API types, the
  reconciler, Job template, predicates, SFTP validation, or the upload script.
  Use whenever architectural sources change so preconditions, invariants, and
  rationale stay accurate. Required before committing those files.
---

# Update design docs

When modifying component boundaries, data flows, Job shape, or API contracts, **must** review and update the matching file under `docs/design/`.

## Check

1. Identify the module from `docs/design/README.md`.
2. Ensure **Preconditions**, **Invariants**, and **Rationale** still hold.
3. Record new trade-offs instead of deleting ones that still apply.
4. If the change is an architectural decision, also add or update an ADR under `docs/adrs/`.
5. Stage the design doc in the same commit as the code. The `check-design-docs` pre-commit hook enforces this.

## Do not

- Hand-wave "see the code" in place of an invariant.
- Update only `CLAUDE.md` / `AGENTS.md` and skip `docs/design/`.
- Skip the hook with `SKIP=check-design-docs` for real architectural changes.
