# Contributing to Must Gather Operator

Thank you for your interest in contributing to the Must Gather Operator. This guide covers the contribution workflow for both human and AI contributors. For architecture details, code conventions, and common pitfalls, see [AGENTS.md](AGENTS.md). For domain-specific guidelines, see the files under [docs/](docs/).

## Prerequisites

- Go 1.25+ (matching `go.mod`)
- Access to an OpenShift cluster with `cluster-admin` permissions (for E2E testing)
- `oc` CLI installed and configured
- `operator-sdk` installed (for local development)
- Docker or Podman (for container builds)

## Setting Up the Development Environment

1. **Fork and clone** the repository:

   ```bash
   # Fork github.com/openshift/must-gather-operator on GitHub, then:
   git clone git@github.com:<your-username>/must-gather-operator.git
   cd must-gather-operator
   git remote add upstream git@github.com:openshift/must-gather-operator.git
   ```

2. **Install dependencies**:

   ```bash
   go mod download
   ```

3. **Verify the build**:

   ```bash
   make
   ```

   This runs the default target (build + test + lint). If this passes, your environment is ready.

4. **Run locally** (requires an OpenShift cluster):

   ```bash
   oc apply -f deploy/crds/operator.openshift.io_mustgathers.yaml
   oc new-project must-gather-operator
   export DEFAULT_MUST_GATHER_IMAGE='quay.io/openshift/origin-must-gather:latest'
   export OPERATOR_IMAGE='quay.io/openshift/origin-must-gather-operator:latest'
   OPERATOR_NAME=must-gather-operator operator-sdk run --verbose --local --namespace ''
   ```

## Contribution Workflow

### 1. Create a Branch

Branch from `master` with a descriptive name, ideally including the Jira ticket ID:

```bash
git fetch upstream
git checkout -b MG-NNN-short-description upstream/master
```

### 2. Make Your Changes

Follow the conventions documented in [AGENTS.md](AGENTS.md). Key points:

- If you modify `api/v1alpha1/mustgather_types.go`, you **must** run code generation (see below).
- Add or update tests for any behavior changes.
- Follow existing Go patterns in the codebase (table-driven tests, `interceptClient` for mocking, package-level function vars for external I/O).

### 3. Run Code Generation (When Required)

If you changed CRD types, kubebuilder markers, or RBAC markers:

```bash
make generate    # Regenerate deepcopy, CRDs, OpenAPI
make manifests   # Regenerate CRD YAML and RBAC
```

Commit the generated output alongside your source changes. PRs that change types without regenerating will fail CI.

### 4. Run Quality Checks Locally

Run the full suite before pushing:

```bash
make              # Build + unit tests + lint (the default target)
```

Individual targets are available:

| Command          | What it does                                     |
|------------------|--------------------------------------------------|
| `make go-test`   | Unit tests only (excludes E2E via build tags)    |
| `make go-build`  | Build the operator binary                        |
| `make lint`      | Run linters                                      |
| `make coverage`  | Generate coverage report                         |
| `make test-e2e`  | E2E tests (requires a live cluster + KUBECONFIG) |

### 5. Commit Your Changes

Commit messages **must** be prefixed with the Jira ticket ID:

```
MG-NNN: Short description of the change
```

Examples from the project history:

- `MG-274: Prevent user from using namespace's service Account`
- `MG-259: Add additional automated tests in operator E2E`
- `MG-277: improve unit test coverage percentage`

For bug fixes tracked with OCPBUGS, use the `OCPBUGS-NNNNN:` prefix instead.

Keep commits focused. It is fine to have multiple commits on a branch -- the PR will be squash-merged or merged as-is depending on reviewer preference.

### 6. Push and Open a Pull Request

```bash
git push origin MG-NNN-short-description
```

Open a PR against `openshift/must-gather-operator:master`. In the PR description:

- Reference the Jira ticket (e.g., `MG-NNN`).
- Summarize what changed and why.
- Note any testing performed (unit tests, E2E, manual cluster testing).

## Code Quality Expectations

### Tests

- **Unit tests are required** for controller logic, template generation, validation, and predicates. Use stdlib `testing` (not Ginkgo) for unit tests.
- **E2E tests** use Ginkgo/Gomega and require the `//go:build e2e` tag. Add E2E coverage for user-facing behavior changes.
- **Table-driven test pattern** is the standard -- follow existing examples in the test files.
- See [docs/testing-guidelines.md](docs/testing-guidelines.md) for detailed conventions, mocking patterns, and the test file map.

### Linting

The project uses linters configured through the boilerplate system. Run `make lint` and fix all findings before submitting.

### RBAC

RBAC permissions are declared via `//+kubebuilder:rbac` comments in the controller. When adding new API access, add the marker and run `make manifests`. Do not edit RBAC YAML files by hand.

### Boilerplate

Files under `boilerplate/` are managed externally. Do not edit them directly. Run `make boilerplate-update` to pull updates when needed.

## PR Review Process

### OWNERS

The [OWNERS](OWNERS) file controls who can review and approve PRs:

- **Reviewers** can review and leave `/lgtm`.
- **Approvers** can approve with `/approve`.
- **Maintainers** have full merge authority.

Team aliases are defined in [OWNERS_ALIASES](OWNERS_ALIASES).

### CI Checks

PRs are tested via OpenShift CI (`ci-operator`). The build image is configured in `.ci-operator.yaml`. PRs must pass:

- **Build**: `make go-build`
- **Unit tests**: `make go-test`
- **Lint**: `make lint`
- **Code generation**: Generated files must be up to date

CodeCov tracks coverage but does not currently gate PRs (configured in `.codecov.yml`).

CodeRabbit provides automated code review using the guidelines in `docs/*-guidelines.md` (configured in `.coderabbit.yaml`).

### Review Expectations

- At least one `/lgtm` from a reviewer and one `/approve` from an approver.
- All CI checks passing.
- Commit messages following the `MG-NNN:` format.
- Tests covering the change (unit tests at minimum, E2E when applicable).
- Generated code committed if types or markers changed.

## Detailed Reference

For deeper guidance beyond this contribution workflow, see:

| Document | Scope |
|----------|-------|
| [AGENTS.md](AGENTS.md) | Architecture, code conventions, common pitfalls, PR expectations |
| [docs/testing-guidelines.md](docs/testing-guidelines.md) | Test patterns, mocking, E2E conventions |
| [docs/security-guidelines.md](docs/security-guidelines.md) | Credential handling, RBAC, ServiceAccount validation |
| [docs/api-contracts-guidelines.md](docs/api-contracts-guidelines.md) | CRD validation, CEL rules, immutability |
| [docs/error-handling-guidelines.md](docs/error-handling-guidelines.md) | Error categories, retry logic, status conditions |
| [docs/performance-guidelines.md](docs/performance-guidelines.md) | Reconciliation concurrency, predicates, requeue strategy |
| [docs/integration-guidelines.md](docs/integration-guidelines.md) | SFTP upload flow, proxy support, Job lifecycle |

## Quick Checklist

Before opening your PR, verify:

- [ ] `make` passes (build + test + lint)
- [ ] `make generate && make manifests` output is committed (if types changed)
- [ ] Commit messages use the `MG-NNN:` prefix
- [ ] Unit tests cover the change
- [ ] E2E tests added for user-facing behavior changes
- [ ] No hand-edited RBAC YAML or boilerplate files
