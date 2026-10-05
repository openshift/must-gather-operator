# Must-Gather Operator - Agentic Documentation

**Component**: Must-Gather Operator (MGO)
**Repository**: openshift/must-gather-operator

> **AI agents**: Read `harness-evals/harness-docs/domain/` first for API types, then `harness-evals/harness-docs/architecture/` for implementation patterns. Read `docs/design/` for preconditions, invariants, and rationale. Check `docs/adrs/` before making architectural changes. When you change API contracts, reconciler behavior, Job template, predicates, or upload/SFTP validation, you **must** review and update the matching design doc in `docs/design/`.
> **Platform Patterns**: See [openshift/enhancements/ai-docs/](https://github.com/openshift/enhancements/tree/master/ai-docs/) for operator patterns, testing, security, and cross-repo ADRs.

## What is Must-Gather Operator?

Automates diagnostic collection on OpenShift clusters. Creates a Kubernetes Job with a gather container and, when an upload target is configured, an upload container. Collects must-gather output and optionally uploads compressed archives to Red Hat SFTP for case management.

**Key Principle**: One CR = one gather operation. Spec is **immutable** after creation (CEL-enforced).

## Core Components

| Component | Location | Purpose |
|---|---|---|
| MustGather CRD | `api/v1/mustgather_types.go` (primary); `api/v1alpha1/` deprecated | CR spec: SA, image, gather params, upload target, storage, obfuscation, timeout |
| Controller | `controllers/mustgather/mustgather_controller.go` | Single reconciler: Job lifecycle, cleanup, SFTP validation |
| Job Template | `controllers/mustgather/template.go` | Job builder (gather container + conditional upload container), volumes, affinity |
| Upload Script | `build/bin/upload` | Shell: compress + SFTP upload with proxy support |
| Predicates | `controllers/mustgather/predicates.go` | Event filters: generation/finalizer changes, Job status |
| Metrics | `pkg/localmetrics/localmetrics.go` | `must_gather_operator_must_gather_total`, `must_gather_operator_must_gather_errors` |

## Critical Patterns

1. **DO NOT assume secret replication** — secrets are referenced directly via SecretKeyRef from the CR namespace. Only trusted CA ConfigMaps are replicated to the CR namespace.
2. **DO NOT hand-edit generated files** — `zz_generated.deepcopy.go`, `zz_generated.openapi.go`, CRD YAML are all generated. Run `make generate` + `make manifests`.
3. **DO NOT use operator's own SA** — controller rejects its own ServiceAccount when CR is in the operator namespace (`mustgather_controller.go:158-168`).

## Design documentation

Preconditions, invariants, and rationale for critical modules live in [`docs/design/`](docs/design/README.md). Implementation recipes stay in `harness-evals/harness-docs/`. ADRs stay in [`docs/adrs/`](docs/adrs/README.md).

**Required**: when modifying component boundaries, data flows, Job shape, or API contracts, review and update the corresponding design doc in `docs/design/` in the same change. The `check-design-docs` pre-commit hook and the `update-design-docs` skill enforce this. Do not skip the hook for architectural work.

## Documentation Structure

```text
docs/design/                       # Preconditions, invariants, rationale (required with arch changes)
docs/adrs/                         # Accepted architectural decisions (immutable after accept)
harness-evals/harness-docs/
├── domain/mustgather.md           # MustGather CRD: fields, validation, lifecycle
├── architecture/components.md     # Repo layout, reconciliation flow, Job template, upload
├── references/
│   ├── ecosystem.md               # Links to Platform patterns
│   └── enhancements.md            # 7 enhancement proposals (MG-5 through MG-293)
├── exec-plans/                    # Feature planning
├── MGO_DEVELOPMENT.md             # Build, common tasks, env vars, mistakes
└── MGO_TESTING.md                 # Unit (fake client + interceptClient), E2E (Ginkgo)
```

**AI Agent Path**: `docs/design/` → `docs/adrs/` → `harness-evals/harness-docs/domain/` → `harness-evals/harness-docs/architecture/` → `harness-evals/harness-docs/MGO_DEVELOPMENT.md` or `harness-evals/harness-docs/MGO_TESTING.md` (as relevant)

## Quick Reference

| Action | Command |
|---|---|
| Build + test + go-check | `make` |
| Full lint (kube-api + repo golangci + go-check) | `make lint` |
| Unit tests | `make go-test` |
| Unit tests with coverage | `make coverage-unit` |
| E2E tests | `make test-e2e` |
| Generate code | `make generate` |
| Generate manifests | `make manifests` |
| Build image | `make docker-build` |
| Lint one Go file | `golangci-lint run path/to/file.go` |
| Type-check one Go file | `go vet path/to/file.go` |
| Lint one YAML file | `yamllint path/to/file.yaml` |
| Lint / syntax-check one shell script | `shellcheck path/to/script.sh` / `bash -n path/to/script.sh` |

### Single-file lint and type-check

These run without a full build (`make`, image build, or `make lint`'s kube-api-linter plugin). Target: under 5 seconds per file once the Go module cache is warm.

```bash
# Go lint
golangci-lint run path/to/file.go
# Root .golangci.yml (errcheck, staticcheck, gosec, revive, depguard) is what
# `make lint` runs as golangci-repo-lint, plus kube-api-linter and boilerplate go-check.
# kube-api-linter uses .golangci-kube-api.yml and needs `make lint` (custom plugin).
# Broader go-check set (govet, unused, misspell, ...):
golangci-lint run -c boilerplate/openshift/golang-osd-operator/golangci.yml path/to/file.go

# Go type-check (does not write the operator binary)
go vet path/to/file.go
go vet ./path/to/package/

# YAML
yamllint path/to/file.yaml

# Shell
shellcheck path/to/script.sh
bash -n path/to/script.sh
```

**Framework**: controller-runtime v0.21.0 | **Go**: 1.26.0 | **FIPS**: enabled (BoringCrypto)

## Knowledge Graph

```text
                         [AGENTS.md] ← Start here
                              │
              ┌───────────────┴───────────────┐
              │                               │
     [docs/design/]                    [docs/adrs/]
  preconditions, invariants           ADR history (3)
              │
              ┌───────────────┴───────────────┐
              │                               │
  [harness-docs/domain/]          [harness-docs/architecture/]
     MustGather CRD                  Reconcile flow
       fields,CEL                    Job template
              │                               │
              └───────────────┬───────────────┘
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
