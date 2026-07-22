# Integration Guidelines

Rules and conventions for integrating with external systems, managing cross-resource dependencies, and handling the operator's lifecycle. Derived from the actual codebase patterns.

## SFTP Upload

The operator validates SFTP credentials in Go before creating any Job, then delegates the actual upload to a shell script running inside the upload container.

### Pre-flight Validation (Go side, `validation.go`)

- **Always validate before Job creation.** Call `validateSFTPWithRetry()` after extracting `username`/`password` from the Secret. This catches bad credentials without spawning pods.
- **Retry only transient errors.** `IsTransientError()` checks for `context.DeadlineExceeded`, `context.Canceled`, and `net.Error` with `Timeout()=true`. Auth failures return immediately. Max retries: `MaxSFTPValidationRetries = 3`.
- **Classify errors for users.** `classifySFTPError()` pattern-matches on lowercase error messages to return actionable strings. When adding new error types, add a case there.
- **Use `InsecureIgnoreHostKey`.** Both Go validation and the shell script skip host key verification. This is intentional to match `StrictHostKeyChecking=no` in the upload script.
- **Testability convention.** All network functions (`sftpDialFunc`, `netDialFunc`, `sshNewClientConnFunc`, `verifySFTPSubsystemFunc`, `sftpNewClientFunc`) are package-level vars replaceable in tests. Follow this pattern for new external calls.

### Upload Script (`build/bin/upload`)

- Archives with `tar --ignore-failed-read` (tolerates missing files).
- Remote path differs by user type: internal users upload to `${username}/${caseid}_${filename}`, external to `${caseid}_${filename}`.
- IPv6 addresses are auto-bracketed (`[addr]`) for sftp/ssh compatibility.
- `SSHPASS` env var is set from the `password` env var for `sshpass -e sftp`.

## HTTP/HTTPS Proxy Support

Proxy support exists in two independent layers that must stay consistent.

### Layer 1: Go Pre-flight (`validation.go`)

- Uses `httpproxy.FromEnvironment()` to read `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`.
- Implements HTTP CONNECT tunneling via `proxyDialContext()` for SSH-over-proxy.
- Proxy auth uses `Proxy-Authorization: Basic` header extracted from the proxy URL.
- Default proxy ports: `3128` (http), `3129` (https).

### Layer 2: Shell Upload (`build/bin/upload`)

- Reads `http_proxy`/`https_proxy` (lowercase, injected by the template).
- HTTP proxy: uses `nc --proxy` via SSH `ProxyCommand`.
- HTTPS proxy: uses `socat`-based `https-proxy-connect-util` via `ProxyCommand`.
- Supports proxy auth (user:pass extracted from URL).

### Proxy Env Propagation (`template.go`)

- The operator reads its own `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` via `proxy.ReadProxyVarsFromEnv()` (from `operator-framework/operator-lib/proxy`).
- Injects them as lowercase `http_proxy`/`https_proxy`/`no_proxy` into the upload container.
- Only set if non-empty. Do not inject empty proxy vars.

## Secret Management

- **No cross-namespace secret copy.** The Secret must exist in the same namespace as the MustGather CR. The Job references it via `SecretKeyRef` env vars.
- **Required fields:** `username` and `password`, both must be non-empty byte slices.
- **Validation order:** Check Secret exists -> check fields present and non-empty -> validate SFTP credentials -> create Job.
- **Secret not-found is terminal;** transient Get errors requeue with `Requeue: true`.

## Job Lifecycle

### Creation (`template.go`)

- Two-container pod: `gather` (runs must-gather) + `upload` (waits, compresses, uploads via SFTP).
- `ShareProcessNamespace: true` lets the upload container detect gather completion via `pgrep -a gather` (5 consecutive misses over ~150s triggers upload).
- `BackoffLimit: 3`, `RestartPolicy: Never`.
- Created via `CreateResourceIfNotExists` with the MustGather CR as owner.
- Upload container is only added when `spec.uploadTarget` is set and type is `SFTP`.

### Monitoring (controller)

- `Active > 0`: still running, no action.
- `Succeeded > 0`: call `handleJobCompletion("Completed")`, then cleanup.
- `Failed > backoffLimit`: increment error metric, call `handleJobCompletion("Failed")`.

### Cleanup (`cleanupMustGatherResources`)

- **Verify ownership first.** Check `OwnerReferences` match the MustGather UID before deleting.
- Delete pods (by `controller-uid` label) before deleting the Job.
- Skipped when `RetainResourcesOnCompletion: true`.

### Scheduling

- Infra node affinity: `PreferredDuringSchedulingIgnoredDuringExecution` with weight=1 for `node-role.kubernetes.io/infra`, plus matching toleration.

## Trusted CA ConfigMap

### Cross-Namespace Copy Pattern (`ensureTrustedCAConfigMap`)

- Only active when `--trusted-ca-configmap` CLI flag is set (empty by default).
- Skips copy if MustGather CR is in the operator namespace.
- Copies from operator namespace to CR namespace, preserving labels and data.
- **Multi-owner support:** Multiple MustGather CRs in the same namespace share one ConfigMap copy. Each adds its own OwnerReference.
- If ConfigMap exists in target namespace, only append the OwnerReference (no data update).

### Cleanup (`cleanupTrustedCAConfigMap`)

- Remove this instance's OwnerReference.
- If no OwnerReferences remain, delete the ConfigMap entirely.
- If other owners remain, update the ConfigMap (remove only this reference).

### Volume Mounting

- Both gather and upload containers mount the trusted CA ConfigMap at `/etc/pki/tls/certs` (read-only).
- The source ConfigMap in the operator namespace must have label `config.openshift.io/inject-trusted-cabundle: "true"` to trigger OpenShift CA injection.

## ImageStream Integration

- If `spec.imageStreamRef` is nil, use `DEFAULT_MUST_GATHER_IMAGE` env var.
- If set, fetch the ImageStream from the **operator namespace** (not the CR namespace).
- Validate: tag exists, tag has items, first item has non-empty `DockerImageReference`.
- Errors are wrapped with `errImageValidation` sentinel, triggering `setValidationFailureStatus` with `ValidationImageStream` type.

## Prometheus Metrics

- Two counters in `pkg/localmetrics/localmetrics.go`:
  - `must_gather_operator_must_gather_total`: incremented on Job creation.
  - `must_gather_operator_must_gather_errors`: incremented when Job fails past backoff limit.
- Served via `openshift/operator-custom-metrics` on port 8080, path `/metrics`.
- Controller-runtime's built-in metrics server is **disabled** (`BindAddress: "0"`) to avoid port conflicts.
- Includes ServiceMonitor for Prometheus auto-discovery.

## Error Handling and Requeue Conventions

| Scenario | Return | Effect |
|---|---|---|
| Resource not found (normal) | `Result{}, nil` | No requeue |
| Validation failure (terminal) | `Result{}, nil` after `setValidationFailureStatus()` | No requeue, status set to Failed |
| Transient API error | `Result{Requeue: true}, err` | Immediate requeue |
| Operational error | `r.ManageError(ctx, instance, err)` | Sets `ReconcileError` condition, requeues with backoff |
| Success | `r.ManageSuccess(ctx, instance)` | Sets `ReconcileSuccess` condition |

### Validation Failure Status Pattern

When validation fails terminally, call `setValidationFailureStatus()` which:
1. Sets `Status.Status = "Failed"`, `Status.Completed = true`, descriptive `Status.Reason`.
2. Sets a `ReconcileError` condition with `Reason: "ValidationFailed"`.
3. Records a Warning event.
4. Returns `Result{}, nil` (no requeue -- the failure is permanent).

## Event Filtering (Predicates)

Three predicates control what triggers reconciliation (`predicates.go`):

1. **MustGather CRs:** `resourceGenerationOrFinalizerChangedPredicate()` -- only on spec changes (generation bump) or finalizer changes. Ignores status-only updates.
2. **Owned Jobs:** `isStateUpdated()` -- only on `job.Status` changes. Ignores create/delete/generic events.
3. **Owned ConfigMaps:** `isNameEquals(name)` -- only for the trusted CA ConfigMap name. Conditionally registered (only when `--trusted-ca-configmap` is set).

## PersistentVolume Storage

When `spec.storage.type = "PersistentVolume"`:
- Output volume uses PVC instead of EmptyDir.
- Each pod gets an isolated subdirectory via `SubPathExpr` combining user `subPath` and `$(POD_NAME)`.
- `POD_NAME` env var (from Downward API) is only injected when PVC storage is configured.
- Empty/whitespace/slash-only subPath values resolve to just `$(POD_NAME)` at PVC root.

## Environment Variables

Required at operator startup:
- `DEFAULT_MUST_GATHER_IMAGE`: gather container image.
- `OPERATOR_IMAGE`: upload container image (the operator's own image).
- `OPERATOR_SERVICE_ACCOUNT`: operator's SA name (from Downward API `spec.serviceAccountName`). Falls back to `config.OperatorName` in local mode only.
- `OPERATOR_NAMESPACE`: from Downward API `metadata.namespace`.
