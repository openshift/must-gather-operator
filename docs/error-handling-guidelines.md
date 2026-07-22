# Error Handling Guidelines

## Error Categories

This operator distinguishes two fundamentally different error classes. Getting the category wrong causes either infinite requeue loops or silent terminal failures.

### Validation Errors (Terminal)

Validation errors mark the CR as permanently failed with no requeue. Use `setValidationFailureStatus` which sets `Status.Status = "Failed"`, `Status.Completed = true`, and returns `(reconcile.Result{}, nil)`.

Validation types are defined in `controllers/mustgather/constant.go`:
- `ValidationServiceAccount` -- missing SA, operator's own SA used
- `ValidationSFTPCredentials` -- secret missing `username` or `password` fields
- `ProtocolSFTP` -- SFTP connection/auth failure
- `ValidationImageStream` -- invalid imagestream ref or tag

The condition reason is `"ValidationFailed"` (not `"LastReconcileCycleFailed"`), making validation failures distinguishable in status conditions.

### Runtime Errors (Retriable)

Runtime errors return via `ManageError(ctx, instance, err)` which returns `(reconcile.Result{}, err)`. Controller-runtime applies exponential backoff requeue (starting at 5ms, up to 1000s or about 16.7 minutes). `ManageError` also records a `Warning` event and sets a `ReconcileError` condition with reason `"LastReconcileCycleFailed"`.

### Transient API Errors (Immediate Requeue)

For ServiceAccount and Secret lookups, transient API server errors use `reconcile.Result{Requeue: true}, err` directly (bypassing `ManageError`) to ensure fast requeue. Reserve this pattern for cases where the API server itself is temporarily unavailable, not for user-caused errors.

## Decision Tree for New Error Handling

```
Is the error caused by invalid user input that won't self-heal?
  YES -> setValidationFailureStatus (terminal, no requeue)
  NO  -> Is it a transient API server error during resource lookup?
    YES -> return reconcile.Result{Requeue: true}, err
    NO  -> return r.ManageError(ctx, instance, err)
```

## Transient Error Classification

`IsTransientError` in `validation.go` classifies errors for SFTP retry logic. An error is transient if:
- `errors.Is(err, context.DeadlineExceeded)` or `errors.Is(err, context.Canceled)`
- `errors.As(err, &net.Error)` and `ne.Timeout()` returns true

Connection refused is NOT transient. Auth failures are NOT transient. Only timeouts and cancellations trigger retry.

## SFTP Error Classification

`classifySFTPError` converts raw network/SSH errors into user-facing messages by string-matching against lowercased error text. When adding new SFTP error categories:
1. Add a `strings.Contains` check in `classifySFTPError`
2. Return an actionable message (tell the user what to fix)
3. Wrap the result: `fmt.Errorf("%s: %w", classifySFTPError(err), err)`

This preserves the original error for programmatic checks while providing human-readable output.

## Retry Logic

### SFTP Validation Retries

`validateSFTPWithRetry` retries up to `MaxSFTPValidationRetries` (3) times with NO backoff delay. Non-transient errors bail immediately. After exhausting retries, wrap with attempt count: `fmt.Errorf("validation timed out after %d attempts: %w", MaxSFTPValidationRetries, lastErr)`.

### Kubernetes Job Retries

The Job template sets `backoffLimit = 3` for pod-level retries. The upload shell script uses `set -o errexit` with no internal retry logic; retries are delegated to Kubernetes.

## Error Wrapping Conventions

### Import Aliasing

The controller imports two `errors` packages. Always follow this convention:
```go
goerror "errors"                          // standard library
"k8s.io/apimachinery/pkg/api/errors"      // imported as errors (default)
```

Use `goerror.Is`, `goerror.As`, `goerror.New` for standard error operations. Use `errors.IsNotFound`, `errors.IsConflict`, etc. for API machinery checks.

### Wrapping Patterns

Always wrap with `%w` to preserve the error chain:
```go
fmt.Errorf("failed to get imagestream %s in namespace %s: %w", name, ns, err)
```

For sentinel errors, wrap the sentinel with `%w` and append the cause with `%v`:
```go
fmt.Errorf("%w: %v", errImageValidation, err)
```

This allows callers to use `goerror.Is(err, errImageValidation)` while still including the root cause.

### Sentinel Errors

Only one sentinel error exists: `errImageValidation`. It gates a routing decision in the reconcile loop -- image validation errors go to `setValidationFailureStatus` while other `getJobFromInstance` errors go to `ManageError`. Add new sentinels only when you need to route errors to different handling paths.

## Finalizer Cleanup

The finalizer uses a **fail-fast, abort-on-error** strategy. If any cleanup step fails, the finalizer stays on the object and the controller requeues to retry the entire sequence. Steps execute sequentially:

1. Get Job (NotFound is OK -- return nil early)
2. Verify ownership (skip cleanup if not owned by this instance)
3. List pods
4. Delete pods
5. Delete job
6. Cleanup TrustedCA ConfigMap

Never swallow errors in finalizer cleanup. A failed cleanup that removes the finalizer will leak resources. The only safe "ignore" is NotFound, which means the resource is already gone.

For ConfigMap cleanup during deletion, NotFound is also safe to ignore -- see `cleanupTrustedCAConfigMap` which returns nil when the ConfigMap does not exist.

## Status Updates

### Two Condition-Setting Paths

1. **`ManageError`** (from operator-utils): uses `apis.AddOrReplaceCondition` with reason `"LastReconcileCycleFailed"`. Called for runtime errors.
2. **`setValidationFailureStatus`** (local): uses `apimeta.SetStatusCondition` with reason `"ValidationFailed"`. Called for validation errors.

When adding new error paths, use the correct function. Do not mix them -- the reason field is used by consumers to distinguish error types.

### Status Update Failures

If the status update itself fails inside `setValidationFailureStatus`, it falls through to `ManageError` to ensure the error is not silently dropped:
```go
if statusErr := r.GetClient().Status().Update(ctx, instance); statusErr != nil {
    return r.ManageError(ctx, instance, statusErr)
}
```

## Reconcile Return Patterns

| Scenario | Return | Effect |
|---|---|---|
| CR not found (deleted) | `Result{}, nil` | Stop reconciling |
| Validation failure | `Result{}, nil` | Terminal, no requeue |
| Deletion complete | `Result{}, nil` | Stop reconciling |
| Job created | `ManageSuccess` | Sets success condition, no requeue |
| Runtime error | `ManageError` | Warning event + requeue with backoff |
| Transient API error | `Result{Requeue: true}, err` | Immediate requeue |
| Finalizer cleanup error | `Result{}, err` | Requeue with backoff |

Never return `ManageSuccess` after an error. Never return `ManageError` for a validation failure (it would cause infinite retries of something that will never succeed).

## Logging Conventions

- `reqLogger.Error(err, "message", keyvals...)` -- all error paths, always include the error object
- `reqLogger.Info("message", keyvals...)` -- normal operational flow
- `reqLogger.V(4).Info("message")` -- debug/verbose output for successful operations
- Warnings go through Kubernetes events (`r.GetRecorder().Event`), not logger

Do not use V(1), V(2), or V(3) -- this codebase uses only V(4) for debug verbosity.

## Shell Script Error Handling

The upload script (`build/bin/upload`) uses `set -o errexit` to exit on any command failure. Required parameters (`caseid`, `username`, `password`) are validated with explicit empty checks that `exit 1`. The script has no retry logic -- pod-level retries via `backoffLimit` handle transient upload failures.

## Testing Error Paths

### Injecting Failures

Use the `interceptClient` wrapper in tests to override specific CRUD operations:
```go
cl := &interceptClient{
    Client: realClient,
    onGet: func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
        if key.Name == "target" { return errors.NewNotFound(...) }
        return realClient.Get(ctx, key, obj)
    },
}
```

Use `failingStatusWriter` to simulate status update failures.

### Mock Function Injection

SFTP validation tests override package-level vars to avoid real network calls:
```go
originalDialFunc := sftpDialFunc
defer func() { sftpDialFunc = originalDialFunc }()
sftpDialFunc = func(ctx context.Context, u, p, h string) error { return someError }
```

Always restore the original function in a defer.

### Asserting Validation Failures

After reconciliation, verify terminal failures by checking all three status fields:
```go
if out.Status.Status != "Failed" { t.Fatalf(...) }
if !out.Status.Completed { t.Fatalf(...) }
if !strings.Contains(out.Status.Reason, expectedReason) { t.Fatalf(...) }
```

Also verify the condition's `Reason` field is `"ValidationFailed"` (not `"LastReconcileCycleFailed"`).
