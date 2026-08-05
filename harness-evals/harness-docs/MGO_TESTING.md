# Must-Gather Operator — Testing Guide

> **Generic Testing Practices**: See [Platform Testing Practices](https://github.com/openshift/enhancements/tree/master/ai-docs/) for test pyramid philosophy and E2E framework patterns.

This guide covers **must-gather-operator-specific** test suites and patterns.

## Test Organization

```text
controllers/mustgather/
├── mustgather_controller_test.go   # Reconciler unit tests (fake client + interceptClient)
├── template_test.go                # Job template generation tests
├── predicates_test.go              # Event predicate tests
├── mustgather_image_test.go        # Image resolution tests
├── trusted_ca_test.go              # ConfigMap replication tests
├── validation_test.go              # SFTP validation tests

pkg/mustgatherutil/
└── util_test.go                    # Directory name generation tests

test/e2e/
├── must_gather_operator_test.go    # E2E test cases (Ginkgo)
├── must_gather_operator_runner_test.go  # E2E suite runner
└── testdata/                       # Test fixtures (PVC YAML, etc.)

test/library/
├── kube_client.go                  # Kubernetes client helpers
├── dynamic_resources.go            # Dynamic resource helpers
└── utils.go                        # Test utilities
```

## Unit Tests

### Running

```bash
# All unit tests (fake client)
make go-test

# Specific package
go test -v ./controllers/mustgather/...

# Specific test
go test -v ./controllers/mustgather/... -run TestReconcile

# With coverage
make coverage
```

### Controller Test Pattern

Tests use `controller-runtime/pkg/client/fake` with a custom `interceptClient` wrapper that allows injecting failures for specific CRUD operations:

```go
type interceptClient struct {
    client.Client
    onGet    func(ctx context.Context, key client.ObjectKey, obj client.Object) error
    onList   func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
    onDelete func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error
    onUpdate func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error
    onCreate func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error
}
```

Use this pattern to test error handling paths without a real cluster:
- `onGet` failures for secret-not-found, SA-not-found scenarios
- `onDelete` failures for cleanup error handling
- `onCreate` failures for Job creation errors

### Template Test Pattern

Table-driven tests in `template_test.go` verify Job spec generation across configurations:

```go
tests := []struct {
    name        string
    storage     *mustgatherv1alpha1.Storage
    caConfigMap string
}{
    {name: "Without PVC"},
    {name: "With PVC", storage: &mustgatherv1alpha1.Storage{
        Type: mustgatherv1alpha1.StorageTypePersistentVolume,
        PersistentVolume: mustgatherv1alpha1.PersistentVolumeConfig{
            Claim: mustgatherv1alpha1.PersistentVolumeClaimReference{
                Name: pvcClaimName,
            },
            SubPath: pvcSubPath,
        },
    }},
    {name: "With CA config map", caConfigMap: "trusted-ca-cert-001"},
}
```

Each test verifies: volume mounts, env vars, container commands, affinity rules, and SA binding for the given configuration.

### What to Test When Modifying

| Change | Test File | What to Verify |
|---|---|---|
| New CRD field | `template_test.go` | Field appears in Job env vars or container spec |
| New CRD field | `mustgather_controller_test.go` | Reconciler handles the field correctly |
| Predicate change | `predicates_test.go` | Events are filtered/passed correctly |
| Image resolution | `mustgather_image_test.go` | Default image, ImageStream resolution, error cases |
| CA ConfigMap | `trusted_ca_test.go` | Copy, ownerReference management, cleanup |
| SFTP validation | `validation_test.go` | Error classification, retry behavior, proxy handling |
| Directory naming | `util_test.go` | Format compliance, uniqueness |

## E2E Tests

### Prerequisites

- OpenShift cluster with operator deployed
- SFTP server accessible from the cluster (for upload tests)
- `KUBECONFIG` environment variable set
- SFTP credentials secret in the test namespace

### Running

```bash
# Full E2E suite (1h timeout, serial execution)
make test-e2e

# Specific test by Ginkgo description
go test -timeout 1h -count 1 -v -p 1 -tags e2e ./test/e2e -ginkgo.v -ginkgo.focus="test description"
```

**Important**: E2E tests use `-p 1` (serial execution) — they share cluster state and cannot run in parallel.

### E2E Test Structure

Tests use Ginkgo v2 with Gomega matchers. Each test case:

1. Creates test namespace and RBAC (ServiceAccount, ClusterRoleBinding)
2. Creates case management credentials Secret
3. Creates MustGather CR with specific options (`MustGatherCROptions`)
4. Waits for Job completion/failure
5. Verifies status conditions and outputs
6. Cleans up resources

### E2E Test Scenarios

| Scenario | What It Tests |
|---|---|
| Basic SFTP upload | Core gather + upload flow with internal/external user |
| PVC storage | Gather output persisted to PVC instead of emptyDir |
| Timeout | Gather respects `mustGatherTimeout` |
| Custom image (ImageStream) | Image resolution from ImageStream + custom command/args |
| ~~Time filtering (since/sinceTime)~~ | *No E2E coverage* — unit tests in `template_test.go` only |
| Retain resources | Job/Pods preserved after completion |
| Validation failure | SFTP credential validation errors surface correctly |

### Test Utilities (`test/library/`)

- `kube_client.go` — Kubernetes and controller-runtime client setup
- `dynamic_resources.go` — Dynamic resource creation/deletion helpers
- `utils.go` — Wait conditions, polling utilities

### SFTP Token Refresh

`test/e2e/refresh-sftp-token.sh` — refreshes SFTP authentication tokens for E2E test runs. Run before E2E if tokens have expired.

## CI Integration

The boilerplate convention provides CI configuration:
- **PR checks**: `make` (lint + unit tests + build)
- **E2E**: `make test-e2e` against a test cluster
- **Coverage**: `make coverage`
- **Generated code check**: `make generate-check` verifies no drift

## See Also

- [Development Guide](./MGO_DEVELOPMENT.md)
- [Architecture](./architecture/components.md)
- [Platform Testing Practices](https://github.com/openshift/enhancements/tree/master/ai-docs/)
