# Contributing to Must Gather Operator

Thank you for contributing to [openshift/must-gather-operator](https://github.com/openshift/must-gather-operator). This operator automates must-gather collection on OpenShift and optional upload of the archive to Red Hat case management.

One MustGather custom resource (CR) is one gather operation. The spec is immutable after creation (CEL-enforced).

## Code of Conduct

Please read and follow our [Code of Conduct](CODE_OF_CONDUCT.md).

## Getting started

### Prerequisites

- Go **1.26.0** (see `go.mod`)
- Git
- [`oc`](https://docs.openshift.com/container-platform/latest/cli_reference/openshift_cli/getting-started-cli.html) (for cluster and local-operator work)
- [operator-sdk](https://github.com/operator-framework/operator-sdk) (for running the operator locally)
- Access to an OpenShift cluster with `cluster-admin` (recommended for local runs and required for E2E)
- Podman or Docker (only if you build images)

The default Makefile build is FIPS-enabled (`FIPS_ENABLED=true` / BoringCrypto). That toolchain is generally available in the project Dockerfile and CI, not on a typical laptop. For local iteration, use `make go-test` and `make go-build` as needed; see the [development guide](harness-evals/harness-docs/MGO_DEVELOPMENT.md).

### Fork and clone

1. Fork [openshift/must-gather-operator](https://github.com/openshift/must-gather-operator).
2. Clone your fork and add `upstream`:

   ```bash
   git clone https://github.com/YOUR_USERNAME/must-gather-operator.git
   cd must-gather-operator
   git remote add upstream https://github.com/openshift/must-gather-operator.git
   ```

3. Download modules:

   ```bash
   go mod download
   ```

4. Install pre-commit hooks (applies `gofmt -s -w`, basic file checks, matching `docs/design/` updates, and [gitleaks](https://github.com/gitleaks/gitleaks) secret scanning):

   ```bash
   pre-commit install
   ```

## Development workflow

### Branch from `master`

The default branch is **`master`**, not `main`.

```bash
git fetch upstream
git checkout -b MG-123-short-description upstream/master
```

Include the Jira key when you have one:

- `MG-361-sanitize-sftp-errors`
- `OAPE-886-operator-evals`
- `OCPBUGS-104849-network-policy-rbac`

### Making changes

- Prefer `make` targets over raw `go test` / `golangci-lint` so boilerplate, FIPS flags, and kube-api-linter stay consistent with CI.
- Add or update tests with the change (controller/template unit tests; E2E when the behavior is user-visible).
- Update docs and `examples/` when you change the CR API or operator behavior.
- Do **not** hand-edit generated files: `zz_generated.deepcopy.go`, `zz_generated.openapi.go`, and CRD YAML. After API changes run `make generate` and `make manifests`.
- If you change `go.mod` / `go.sum`, run `go mod vendor` and commit `vendor/` (Dependabot does not update it).
- Put new MustGather fields on `api/v1/mustgather_types.go`. Update `api/v1alpha1/` only if the field must still be served on the deprecated version.
- Do not assume secrets are copied into the CR namespace. Secrets are referenced in place via `SecretKeyRef`. Only trusted CA ConfigMaps are replicated.
- Do not use the operator's own ServiceAccount on a MustGather CR in the operator namespace; the controller rejects that.

Details for common tasks (new CR fields, Job template, upload script) are in the [development guide](harness-evals/harness-docs/MGO_DEVELOPMENT.md).

### Build, test, and lint

```bash
# Boilerplate go-check, unit tests, and compile (default target)
make

# Unit tests only (fake client / envtest)
make go-test

# E2E tests (Ginkgo; needs a cluster; uses -tags e2e)
make test-e2e

# kube-api-linter, root .golangci.yml (depguard, revive, errcheck, gosec, staticcheck),
# and boilerplate go-check (govet, unused, misspell, ...)
make lint

# After changing API types
make generate
make manifests
```

Single-package checks without a full `make`:

```bash
go vet ./controllers/mustgather/
golangci-lint run controllers/mustgather/mustgather_controller.go
```

### Running the operator locally

```bash
oc apply -f deploy/crds/operator.openshift.io_mustgathers.yaml
oc new-project must-gather-operator
export DEFAULT_MUST_GATHER_IMAGE='quay.io/openshift/origin-must-gather:latest'
export OPERATOR_IMAGE='<your-operator-image>'
OPERATOR_NAME=must-gather-operator operator-sdk run --verbose --local --namespace ''
```

`OPERATOR_IMAGE` is required for the upload container. Example CRs live in `examples/`. Full local-run notes are in the [README](README.md#local-development).

### Commit messages

Prefix the subject with a Jira key when one exists. This repo does **not** use Conventional Commits.

```text
MG-361: Sanitize SFTP validation errors in CR status

Keep the detailed dial error in operator logs and write a fixed
message to MustGather status and events.
```

Other valid prefixes: `OAPE-886:`, `OCPBUGS-104849:`, `NO-JIRA:` (for work with no ticket).

## Pull requests

1. Rebase onto current `upstream/master`:

   ```bash
   git fetch upstream
   git rebase upstream/master
   ```

2. Push your branch and open a PR against **`openshift/must-gather-operator`**, base branch **`master`**.
3. Fill in the PR template. Link the Jira issue in the description.
4. Reviewers come from [`OWNERS`](OWNERS) (and [`OWNERS_ALIASES`](OWNERS_ALIASES)). Prow/OWNERS assign reviewers; you do not need to `@` people unless you want a specific look.
5. Address review comments on the same branch. Do not force-push over a shared branch unless reviewers ask you to rebase.
6. Merge requires OWNER approval and green CI. Do not merge with conflicts.

## Testing

- Unit tests: fake client and `interceptClient` in `controllers/mustgather/` — `make go-test`.
- E2E: Ginkgo in `test/e2e/` — `make test-e2e` (cluster required). `go test ./...` does **not** run E2E (those packages use `-tags e2e`).
- Cover success and failure paths (missing ServiceAccount, bad SFTP secret, Job success/failure, cleanup vs `retainResourcesOnCompletion`).
- After Job template changes, check both gather-only and gather+upload Jobs.

See the [testing guide](harness-evals/harness-docs/MGO_TESTING.md).

## Documentation

| Change | Update |
|---|---|
| User-facing CR or install behavior | [README.md](README.md) and `examples/` |
| API fields, CEL, lifecycle | [domain/mustgather.md](harness-evals/harness-docs/domain/mustgather.md) |
| Preconditions, invariants, rationale | Matching file under [docs/design/](docs/design/README.md) (required with architectural source changes; pre-commit `check-design-docs` requires that specific file) |
| Reconcile / Job / upload implementation | [architecture/components.md](harness-evals/harness-docs/architecture/components.md) |
| New architectural decision | New ADR under [docs/adrs/](docs/adrs/README.md) |
| Godoc | Exported types and functions in `api/` and controllers |

## Security

Report vulnerabilities privately. See [SECURITY.md](SECURITY.md). Do not file a public GitHub issue for security bugs.

Local and CI scanning:

- Dependabot (`.github/dependabot.yml`) — Go modules, Docker, and GitHub Actions. `vendor/` is tracked; on gomod PRs run `go mod vendor` and commit `vendor/`.
- CodeQL (`.github/workflows/codeql.yml`) — SAST on Go
- gosec and govulncheck (`.github/workflows/security.yml`)
- gitleaks (pre-commit hook and `.github/workflows/secret-scan.yml`) — secret detection

## Reporting issues

Prefer a Jira issue (`MG`, `OAPE`, or `OCPBUGS`) and link it from the PR.

Include:

- OpenShift version, operator version or image, and namespace
- The MustGather CR (**redact** usernames, passwords, and other secret data)
- Expected vs actual behavior
- Operator and Job/pod logs
- How to reproduce

## License

This project is licensed under the [Apache License 2.0](LICENSE).

## Further reading

- [README.md](README.md) — usage, deploy, local run
- [AGENTS.md](AGENTS.md) — architecture, critical patterns, agent path
- [Design intent](docs/design/README.md) — preconditions, invariants, rationale
- [Decision records](docs/adrs/README.md) — accepted ADRs
- [Development guide](harness-evals/harness-docs/MGO_DEVELOPMENT.md) — env vars, common tasks, mistakes
- [Testing guide](harness-evals/harness-docs/MGO_TESTING.md) — unit and E2E patterns
- [Platform operator patterns](https://github.com/openshift/enhancements/tree/master/ai-docs/)
