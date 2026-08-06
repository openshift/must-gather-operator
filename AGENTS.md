# Must-Gather Operator - Agentic Documentation

**Component**: Must-Gather Operator (MGO)
**Repository**: openshift/must-gather-operator

> **AI agents**: Read `harness-evals/harness-docs/domain/` first for API types, then `harness-evals/harness-docs/architecture/` for implementation patterns. Check `harness-evals/harness-docs/decisions/` before making architectural changes.
> **Platform Patterns**: See [openshift/enhancements/ai-docs/](https://github.com/openshift/enhancements/tree/master/ai-docs/) for operator patterns, testing, security, and cross-repo ADRs.

## What is Must-Gather Operator?

Automates diagnostic collection on OpenShift clusters. Creates a Kubernetes Job with a gather container and, when an upload target is configured, an upload container. Collects must-gather output and optionally uploads compressed archives to Red Hat SFTP for case management.

**Key Principle**: One CR = one gather operation. Spec is **immutable** after creation (CEL-enforced).

## Core Components

| Component | Location | Purpose |
|---|---|---|
| MustGather CRD | `api/v1alpha1/mustgather_types.go` | CR spec: SA, image, gather params, upload target, storage, timeout |
| Controller | `controllers/mustgather/mustgather_controller.go` | Single reconciler: Job lifecycle, cleanup, SFTP validation |
| Job Template | `controllers/mustgather/template.go` | Job builder (gather container + conditional upload container), volumes, affinity |
| Upload Script | `build/bin/upload` | Shell: compress + SFTP upload with proxy support |
| Predicates | `controllers/mustgather/predicates.go` | Event filters: generation/finalizer changes, Job status |
| Metrics | `pkg/localmetrics/localmetrics.go` | `must_gather_operator_must_gather_total`, `must_gather_operator_must_gather_errors` |

## Critical Patterns

1. **DO NOT assume secret replication** — secrets are referenced directly via SecretKeyRef from the CR namespace. Only trusted CA ConfigMaps are replicated to the CR namespace.
2. **DO NOT hand-edit generated files** — `zz_generated.deepcopy.go`, `zz_generated.openapi.go`, CRD YAML are all generated. Run `make generate` + `make manifests`.
3. **DO NOT use operator's own SA** — controller rejects its own ServiceAccount when CR is in the operator namespace (`mustgather_controller.go:158-168`).

## Documentation Structure

```text
harness-evals/harness-docs/
├── domain/mustgather.md           # MustGather CRD: fields, validation, lifecycle
├── architecture/components.md     # Repo layout, reconciliation flow, Job template, upload
├── decisions/
│   ├── adr-0001-immutable-spec.md # Why spec is immutable after creation
│   ├── adr-0002-two-container-job.md  # Gather + upload container design
│   └── adr-0003-extensible-upload-union.md  # Union API for upload targets
├── references/
│   ├── ecosystem.md               # Links to Platform patterns
│   └── enhancements.md            # 7 enhancement proposals (MG-5 through MG-293)
├── exec-plans/                    # Feature planning
├── MGO_DEVELOPMENT.md             # Build, common tasks, env vars, mistakes
└── MGO_TESTING.md                 # Unit (fake client + interceptClient), E2E (Ginkgo)
```

**AI Agent Path**: `harness-evals/harness-docs/domain/` → `harness-evals/harness-docs/architecture/` → `harness-evals/harness-docs/decisions/` → `harness-evals/harness-docs/MGO_DEVELOPMENT.md` or `harness-evals/harness-docs/MGO_TESTING.md` (as relevant)

## Quick Reference

| Action | Command |
|---|---|
| Build + test + lint | `make` |
| Unit tests | `make go-test` |
| E2E tests | `make test-e2e` |
| Generate code | `make generate` |
| Generate manifests | `make manifests` |
| Build image | `make docker-build` |

**Framework**: controller-runtime v0.21.0 | **Go**: 1.25.7 | **FIPS**: enabled (BoringCrypto)

## Knowledge Graph

```text
                         [AGENTS.md] ← Start here
                              │
              ┌───────────────┼───────────────┐
              │               │               │
  [harness-docs/domain/]  [harness-docs/architecture/] [harness-docs/decisions/]
     MustGather CRD         Reconcile flow               ADR history
       fields,CEL           Job template                  (3 ADRs)
              │                    │                    │
              └────────────────────┼────────────────────┘
                                   │
                 [harness-docs/MGO_DEVELOPMENT.md]
                 [harness-docs/MGO_TESTING.md]
                                   │
              [harness-docs/references/ecosystem]
                   Links to Platform
```

## External References

- [Enhancement Proposals](https://github.com/openshift/enhancements/tree/master/enhancements/support-log-gather/)
- [Product Docs](https://docs.openshift.com/)

---

**Platform Documentation**: [openshift/enhancements/ai-docs/](https://github.com/openshift/enhancements/tree/master/ai-docs/)
