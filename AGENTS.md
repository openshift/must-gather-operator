# AGENTS.md

Onboarding guide for AI agents working on the must-gather-operator codebase. This document covers cross-cutting conventions and architectural context that spans multiple domains. For domain-specific depth, refer to the guideline files listed below.

## Domain Guideline Index

| File | Scope |
|---|---|
| [docs/security-guidelines.md](docs/security-guidelines.md) | Credential handling, RBAC, ServiceAccount validation, proxy auth |
| [docs/performance-guidelines.md](docs/performance-guidelines.md) | Reconciliation concurrency, predicates, requeue strategy, cleanup timing |
| [docs/error-handling-guidelines.md](docs/error-handling-guidelines.md) | Error categories, retry logic, status condition updates, event recording |
| [docs/api-contracts-guidelines.md](docs/api-contracts-guidelines.md) | CRD validation, CEL rules, immutability, union discriminators |
| [docs/testing-guidelines.md](docs/testing-guidelines.md) | Unit tests, E2E tests, mocking patterns, test file map |
| [docs/integration-guidelines.md](docs/integration-guidelines.md) | SFTP upload flow, proxy support, Job lifecycle, two-container model |

## Quick Reference

```bash
make                # Build, test, and lint (default target)
make go-test        # Unit tests only (excludes e2e via build tags)
make test-e2e       # E2E tests (requires live cluster + KUBECONFIG)
make generate       # Regenerate deepcopy, CRDs, OpenAPI
make manifests      # Regenerate CRD YAML and RBAC
make lint           # Run linters
make coverage       # Coverage report
```

## Repository Layout

```
api/v1alpha1/            # CRD types, deepcopy, OpenAPI generation
controllers/mustgather/  # Controller, Job template, predicates, SFTP validation
  mustgather_controller.go  # Main reconcile loop
  template.go               # Job/container spec generation
  predicates.go             # Event filters for controller-runtime
  validation.go             # SFTP credential and proxy validation
  constant.go               # Shared constants (validation types, env var names)
config/                  # Operator name, OLM constants
pkg/localmetrics/        # Prometheus counter definitions
pkg/k8sutil/             # Namespace detection utility
build/bin/upload         # Shell script: compress + SFTP upload (runs in container)
deploy/                  # Kubernetes manifests (Deployment, RBAC, CRD)
test/e2e/                # E2E tests (Ginkgo/Gomega, build tag: e2e)
test/library/            # E2E test helpers (dynamic resource loader, kube clients)
examples/                # Sample MustGather CR YAML files
boilerplate/             # OpenShift-eng convention system (do not edit directly)
```

## Architecture: What Is Not Obvious

### Single-namespace operator, cluster-wide watch

The operator runs in `must-gather-operator` namespace but watches MustGather CRs across all namespaces. Jobs are created in the CR's namespace, not the operator's namespace. This means the operator needs cluster-scoped RBAC for Jobs, Pods, Secrets, and ServiceAccounts.

### Two-container Job model

Each MustGather Job has up to two containers sharing a process namespace (`ShareProcessNamespace: true`):

1. **gather** -- runs the must-gather image, writes to `/must-gather`
2. **upload** -- runs the operator image, waits for gather to finish (via `pgrep`), compresses output, uploads via SFTP

The upload container is only added when `spec.uploadTarget` is set. Without it, the Job has only the gather container.

### Owner references flow

```
MustGather CR --[owns]--> Job --[owns]--> Pod
MustGather CR --[owns]--> ConfigMap (trusted CA, cross-namespace copy)
```

The controller sets owner references so that deleting the MustGather CR cascades cleanup. The finalizer (`finalizer.mustgathers.operator.openshift.io`) handles explicit cleanup of Jobs and Pods when `retainResourcesOnCompletion` is false.

### Environment variable contract

The operator reads these at startup (not per-reconcile):

| Variable | Required | Source |
|---|---|---|
| `DEFAULT_MUST_GATHER_IMAGE` | Yes | Deployment env |
| `OPERATOR_IMAGE` | Yes | Deployment env -- used as the upload container image |
| `OPERATOR_SERVICE_ACCOUNT` | Yes (cluster) | Downward API: `spec.serviceAccountName` |
| `OPERATOR_NAMESPACE` | No | Downward API or SA namespace file |
| `OSDK_FORCE_RUN_MODE` | No | Set to `local` for local dev |
| `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` | No | Inherited from cluster proxy config |

### Spec immutability

The MustGather spec is immutable after creation (enforced by CEL rule on the CRD). Users must delete and recreate the CR to change spec fields. The controller does not need to handle spec updates.

## Code Conventions

### Naming

- **Commit messages**: prefixed with Jira ticket ID: `MG-NNN: <description>` (e.g., `MG-274: Prevent user from using namespace's service Account`).
- **Constants**: grouped in `constant.go` for shared values; template-specific constants live at the top of `template.go`.
- **Validation types**: string constants like `ValidationServiceAccount`, `ValidationSFTPCredentials`, `ValidationImageStream` used in `setValidationFailureStatus()` for consistent error categorization.
- **Container names**: `gather` and `upload` -- referenced by both controller code and E2E tests. Keep them stable.

### Go patterns

- **Package-level function vars for testability**: network-calling functions (`sftpDialFunc`, `netDialFunc`, `sshNewClientConnFunc`, `sftpNewClientFunc`, `getProxyURLForAddr`) are declared as `var` at package level. Tests override them to avoid real network calls. Follow this pattern when adding new external I/O.
- **`interceptClient` for controller tests**: unit tests use a wrapper around `fake.Client` that allows injecting failures for specific CRUD operations (Get, List, Delete, Update, Create). Use this instead of building custom mocks.
- **Unit tests are in-package**: test files use `package mustgather` (not `package mustgather_test`) to access unexported symbols. Use stdlib `testing` only, not Ginkgo.
- **E2E tests use Ginkgo/Gomega**: build-tagged with `//go:build e2e`. They use `embed.FS` for test data YAML files under `test/e2e/testdata/`.
- **Generic helper**: `ToPtr[T any](t T) *T` in `template.go` -- use it for pointer literals in specs.
- **Error wrapping with sentinels**: `errImageValidation` is a sentinel error used with `errors.Is()` to distinguish image validation failures from other errors in the reconcile loop.

### RBAC markers

RBAC permissions are declared via `//+kubebuilder:rbac` comments in the controller file. When adding new API access, add the marker comment and run `make manifests` to regenerate `deploy/` RBAC files. Do not edit RBAC YAML by hand.

### Boilerplate system

The Makefile includes `boilerplate/generated-includes.mk`. Do not edit files under `boilerplate/` directly. Run `make boilerplate-update` to pull updates. The boilerplate provides standard targets (`go-build`, `go-test`, `docker-build`, `lint`, etc.).

### FIPS mode

FIPS is enabled by default (`FIPS_ENABLED=true` in Makefile). The `fips.go` file imports `crypto/tls/fipsonly` under the `fips_enabled` build tag. Do not remove or modify this without understanding the compliance implications.

## Common Pitfalls

### 1. Forgetting to run code generation

After modifying `api/v1alpha1/mustgather_types.go`, you must run both `make generate` (deepcopy) and `make manifests` (CRD YAML). The CRD at `deploy/crds/operator.openshift.io_mustgathers.yaml` and the bundle CRD must stay in sync with the Go types. PRs that change types without regenerating will break the build.

### 2. Predicate bypass

The controller's `SetupWithManager` uses specific predicates that filter out most events. If you add a new owned resource, you must add a predicate for it -- otherwise every event on that resource type triggers reconciliation. Without the generation-or-finalizer predicate on the MustGather CR, status writes would cause infinite reconcile loops.

### 3. Status writes re-triggering reconciliation

Status updates (`r.GetClient().Status().Update()`) bump the resource version but not the generation. The `resourceGenerationOrFinalizerChangedPredicate` correctly ignores these. If you switch to a different predicate or add a new watch, verify that status-only updates do not cause re-reconciliation.

### 4. Upload container without UploadTarget

The upload container is conditionally added in `getJobTemplate()`. Code that assumes the Job always has two containers will break for MustGather CRs without `spec.uploadTarget`. Always check container count or search by name.

### 5. Cross-namespace resource assumptions

The trusted CA ConfigMap is copied from the operator namespace to the CR namespace. The case management Secret is referenced directly in the CR's namespace (not copied). These are different patterns -- do not confuse them.

### 6. E2E build tag

All E2E test files must have `//go:build e2e` at the top. Without it, `make go-test` will try to compile them and fail because they import cluster-only dependencies.

### 7. Cleanup ordering

The finalizer cleanup in `cleanupMustGatherResources` follows a strict order: delete Pods first, then Job, then ConfigMap. Changing this order can leave orphaned resources. The cleanup also verifies owner references before deleting a Job to avoid deleting unrelated Jobs with the same name.

## PR Expectations

- Commit messages follow `MG-NNN: <description>` format with Jira ticket reference.
- Run `make` (which runs build + test + lint) before pushing.
- If types changed: `make generate && make manifests` output must be committed.
- Unit tests for controller logic use table-driven patterns with `interceptClient`.
- E2E tests that need network access to Red Hat SFTP are tagged `[Skipped:Disconnected]`.
- The `OWNERS` file controls PR approval; reviewers and approvers are listed there.
- CI runs via OpenShift CI (`ci-operator`); the build image is configured in `.ci-operator.yaml`.

## Local Development Checklist

1. `go mod download`
2. Apply CRD: `oc apply -f deploy/crds/operator.openshift.io_mustgathers.yaml`
3. Create namespace: `oc new-project must-gather-operator`
4. Set required env vars:
   ```bash
   export DEFAULT_MUST_GATHER_IMAGE='quay.io/openshift/origin-must-gather:latest'
   export OPERATOR_IMAGE='quay.io/openshift/origin-must-gather-operator:latest'
   ```
5. Run locally: `OPERATOR_NAME=must-gather-operator operator-sdk run --verbose --local --namespace ''`

When running locally, `OSDK_FORCE_RUN_MODE=local` is set automatically, which bypasses leader election and uses `"default"` as the operator namespace.
