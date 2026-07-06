# Testing Guidelines

## Running Tests

```bash
make go-test    # Unit tests (excludes e2e via build tags)
make test-e2e   # E2E tests (requires running cluster + KUBECONFIG)
make coverage   # Coverage with -coverpkg=./... (strips zz_generated.*)
```

## Unit Test Conventions

### Framework

Unit tests use stdlib `testing` only -- not Ginkgo/Gomega. Use `t.Fatalf`/`t.Errorf`.
Tests live in the `mustgather` package (not `_test` suffix) to access unexported symbols.

### Test File Map

| File | Covers |
|---|---|
| `mustgather_controller_test.go` | Reconcile loop, cleanup, finalizer, job creation, status |
| `template_test.go` | Job/container spec generation (gather + upload containers) |
| `validation_test.go` | SFTP connection, credential checks, error classification, proxy |
| `predicates_test.go` | Event filter predicates for controller-runtime |
| `trusted_ca_test.go` | Trusted CA ConfigMap lifecycle and owner references |
| `mustgather_image_test.go` | ImageStream resolution and default image fallback |

### Table-Driven Tests

All tests are table-driven. The standard shape for controller tests:

```go
tests := []struct {
    name           string
    setupObjects   func() []client.Object
    interceptors   func() interceptClient
    expectError    bool
    postTestChecks func(t *testing.T, cl client.Client)
}{...}
```

Test names use snake_case: `"cleanup_pod_delete_error_returns_error"`.

### Fake Client Setup

Every test builds its own scheme and fake client:

```go
s := runtime.NewScheme()
_ = corev1.AddToScheme(s)
_ = batchv1.AddToScheme(s)
_ = mustgatherv1alpha1.AddToScheme(s)

base := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).
    WithStatusSubresource(&mustgatherv1alpha1.MustGather{}).Build()
```

`WithStatusSubresource` is required when tests check `.Status` updates.

### Error Injection via interceptClient

The `interceptClient` wraps the fake client with per-operation failure hooks:

```go
type interceptClient struct {
    client.Client
    onGet, onList, onDelete, onUpdate, onCreate  // function hooks
    status   client.StatusWriter
}
```

Use type assertions in hooks to fail selectively:

```go
onDelete: func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
    if _, ok := obj.(*batchv1.Job); ok { return errors.New("forced error") }
    return nil
}
```

For failing status updates, set `status: &failingStatusWriter{}`.

### Reconciler Construction

```go
r := &MustGatherReconciler{
    ReconcilerBase:             util.NewReconcilerBase(cl, s, &rest.Config{}, &record.FakeRecorder{}, nil),
    DefaultMustGatherImage:     "test-must-gather-image",
    OperatorNamespace:          "must-gather-operator",
    OperatorServiceAccountName: mgconfig.OperatorName,
}
```

### Mocking External Dependencies

**SFTP validation** -- mock the package-level `sftpDialFunc` variable:

```go
originalDialFunc := sftpDialFunc
defer func() { sftpDialFunc = originalDialFunc }()
sftpDialFunc = func(ctx context.Context, username, password, host string) error {
    return nil
}
```

**Lower-level network mocks** -- `netDialFunc`, `sshNewClientConnFunc`,
`verifySFTPSubsystemFunc`, `sftpNewClientFunc`. Use `stubCheckSFTPConnectionDeps(t)`
in `validation_test.go` to save/restore all four at once.

**Environment variables** -- use `t.Setenv()` for `OPERATOR_IMAGE`,
`DEFAULT_MUST_GATHER_IMAGE`, proxy vars. Auto-restores after each test.

### Key Helpers

- `createMustGatherObject()` / `createMustGatherSecretObject()` / `createServiceAccountObject()` -- fixture factories in controller tests
- `testMustGather(name, ns, uid)` -- factory with UID control (trusted_ca, image tests)
- `newTrustedCAReconciler(t, objects, interceptor)` / `newImageTestReconciler(...)` -- domain-specific reconciler constructors
- `findGatherContainerInJob(t, job)` / `findUploadContainerInJob(t, job)` / `envValues(container)` -- Job inspection helpers
- `mustGatherOwnerRef(mg)` -- builds OwnerReference for ownership assertions
- `ToPtr[T any](t T) *T` -- generic pointer helper (defined in `template.go`)

### Post-Test Verification

Side-effect verification goes in `postTestChecks`:

```go
postTestChecks: func(t *testing.T, cl client.Client) {
    job := &batchv1.Job{}
    if err := cl.Get(context.TODO(), types.NamespacedName{...}, job); err == nil {
        t.Fatalf("expected job to be deleted")
    }
}
```

## E2E Test Conventions

### Build Tags

All E2E files require both tag formats:

```go
//go:build e2e
// +build e2e
```

### Framework and Structure

E2E tests use Ginkgo v2 + Gomega. Entrypoint: `test/e2e/must_gather_operator_runner_test.go`.
Tests use `Describe/Context/It` with `ginkgo.Ordered`. Unique names via
`fmt.Sprintf("name-%d", time.Now().UnixNano())`.

### Async Assertions

```go
Eventually(func() bool { ... }).
    WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(BeTrue())
```

Timeouts: 1-2 min for resource creation, 5 min for job completion, 30s for `Consistently`.

### Test Library (`test/library/`)

- `DynamicResourceLoader` -- creates/deletes resources from embedded YAML (`test/e2e/testdata/`)
- `CreateTestNS` / `DeleteTestingNS` -- namespace lifecycle with polling
- `TestingT` interface -- abstracts `*testing.T` and `GinkgoT()`

### Client Setup

Two clients: `adminClient` (full access) and `nonAdminClient` (impersonated user for RBAC testing).

### Disconnected Tests

Tests needing external SFTP are tagged `[Skipped:Disconnected]` in the name.

## Adding Tests for New Features

1. **New CR field**: Add table entries in `template_test.go` and `mustgather_controller_test.go`
2. **New validation**: Add to `validation_test.go`, then reconciler-level tests verifying status/conditions
3. **New external dependency**: Introduce a package-level function variable as mock seam (follow `sftpDialFunc`)
4. **New predicate**: Add to `predicates_test.go` covering all event types
5. **New E2E scenario**: Add `ginkgo.It` under the appropriate `Context`, use `Eventually` for reconciliation assertions
