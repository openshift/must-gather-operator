# Performance Guidelines

Guidelines for performance-sensitive code in the must-gather-operator. These rules reflect the actual patterns and conventions used in this repository.

## Controller Reconciliation

### Concurrency

The controller uses default controller-runtime settings: **1 concurrent reconcile**. Do not increase `MaxConcurrentReconciles` without verifying that the reconcile logic is safe for concurrent execution -- it currently performs create-or-update sequences on shared resources (secrets, ConfigMaps) without optimistic locking beyond OwnerReference checks.

### Event Filtering (Predicates)

Three predicates prevent unnecessary reconciles. Preserve them when modifying `SetupWithManager`:

- **MustGather CR**: `resourceGenerationOrFinalizerChangedPredicate` -- only reconciles on spec changes (generation bump) or finalizer mutations. Status-only updates are ignored. This is critical: without it, every status write would re-trigger reconciliation.
- **Owned Jobs**: `isStateUpdated` -- only reconciles when `job.Status` changes. Create, Delete, and Generic events are blocked entirely.
- **Owned ConfigMaps**: `isNameEquals(trustedCAConfigMapName)` -- scoped to only the trusted CA ConfigMap.

**Rule**: Never use `predicate.GenerationChangedPredicate{}` alone for the CR -- it misses finalizer-only updates. The custom predicate checks both generation and finalizers.

### Requeue Strategy

The controller uses three distinct requeue patterns:

| Pattern | When | Effect |
|---|---|---|
| `reconcile.Result{Requeue: true}` | Transient errors fetching ServiceAccount or Secret | Immediate requeue, no backoff |
| `r.ManageError(ctx, instance, err)` | General errors | Sets `ReconcileError` condition, returns error for controller-runtime's exponential backoff |
| `reconcile.Result{}` | Terminal states (validation failures, not-found, success) | No requeue |

**Rule**: Use `Requeue: true` only for errors that are expected to resolve quickly (API server transient failures). Use `ManageError` for errors that need backoff. Never requeue on validation failures -- they are terminal until the user updates the CR.

## Timeout Configuration

### Hardcoded Timeouts

| Timeout | Value | Location | Purpose |
|---|---|---|---|
| SSH dial timeout | 5s | `validation.go:24` | TCP connect + SSH handshake for SFTP validation |
| Upload wait polling (active) | 120s sleep | `template.go:51` (uploadCommand) | Sleep while gather process is detected via `pgrep` |
| Upload wait polling (confirm) | 30s sleep, up to 5 iterations | `template.go:51` (uploadCommand) | Confirm gather has exited (up to 150s total) |
| SFTP validation retries | 3 attempts | `constant.go:18` | Max retries for transient SFTP errors (no delay between retries) |
| Job backoff limit | 3 retries | `template.go:39` | Kubernetes Job pod restart limit |
| E2E test timeout | 1h | `Makefile:11` | Overall E2E suite timeout |
| E2E polling | 5s interval, 2m timeout | `must_gather_operator_test.go` | Gomega Eventually assertions |
| Namespace operations | 1s interval, 30s timeout | `test/library/utils.go` | Namespace create/delete polling |

### User-Configurable Timeout

The `mustGatherTimeout` CR field is passed as `timeout <seconds>` in the gather container command. When unset (nil), it defaults to `time.Duration(0)`, which evaluates to `timeout 0` in the shell command. According to GNU timeout behavior, a timeout of 0 typically means no time limit.

**Rule**: When modifying the upload polling loop, preserve the confirmation window (count > 4 means 5 iterations: 0,1,2,3,4). A single `pgrep` miss can be a false negative during process startup/teardown.

## Resource Cleanup

### Cleanup Triggers

Cleanup is **event-driven**, not timer-based. It runs on:
1. Job success (`job.Status.Succeeded > 0`)
2. Job failure (`job.Status.Failed > backoffLimit`)
3. CR deletion (via finalizer)

The `RetainResourcesOnCompletion` field skips cleanup in all three cases.

### Cleanup Order

The `cleanupMustGatherResources` function follows a strict order to avoid orphaned resources:
1. Get Job by name from instance namespace
2. Verify Job is owned by this MustGather (OwnerReference UID check)
3. List and delete pods matching `controller-uid` label
4. Delete Job
5. Delete trusted CA ConfigMap (if configured)

Note: Case management secrets are NOT copied or deleted by the operator. They are referenced directly from the CR's namespace via `SecretKeyRef` in the Job's environment variables.

**Rule**: Always verify OwnerReference before deleting a Job. The name-based lookup alone is insufficient -- a Job with the same name could belong to a different MustGather instance after recreation.

### Finalizer

The finalizer `finalizer.mustgathers.operator.openshift.io` ensures cleanup runs before CR deletion. The finalizer removal happens **after** cleanup succeeds. If cleanup fails, the finalizer stays, blocking deletion until the next reconcile succeeds.

## Pod Scheduling

### Node Affinity

Jobs use **PreferredDuringSchedulingIgnoredDuringExecution** (not Required) for infra nodes with weight 1, plus a toleration for the `node-role.kubernetes.io/infra:NoSchedule` taint. This means:
- Jobs prefer infra nodes but fall back to any available node
- The weight of 1 is minimal -- the scheduler treats it as a soft hint

**Rule**: Do not change to `RequiredDuringScheduling` -- must-gather must work on clusters without dedicated infra nodes.

### Resource Requests and Limits

Neither the gather nor upload container sets resource requests or limits. They rely on namespace-level LimitRange defaults. This is intentional -- must-gather workloads have unpredictable resource needs depending on cluster size and gather scope.

## Metrics

### Prometheus Counters

Two counters on a custom OSD metrics server (port 8080, path `/metrics`):

| Metric | Incremented When |
|---|---|---|
| `must_gather_operator_must_gather_total` | A new Job is created |
| `must_gather_operator_must_gather_errors` | Job failure detected (`job.Status.Failed > backoffLimit`) |

Controller-runtime's built-in metrics server is **disabled** (`BindAddress: "0"` in main.go). Only the OSD metrics server is active.

**Rule**: Increment `MetricMustGatherTotal` only on Job creation, not on requeues or status updates. Increment `MetricMustGatherErrors` only when failure exceeds the backoff limit, not on individual pod failures.

## SFTP Validation

### Retry Logic

`validateSFTPWithRetry` retries up to 3 times with **no delay** between attempts. Only transient errors trigger retry:
- `context.DeadlineExceeded`, `context.Canceled`
- `net.Error` where `Timeout()` is true

Non-transient errors (auth failures, DNS errors, connection refused) fail immediately.

**Rule**: Do not add sleep between SFTP retries. The 5-second dial timeout already provides sufficient spacing for network-level transients. Adding delays would slow down the reconcile loop for non-transient failures that get misclassified.

### Proxy Support

SFTP validation supports HTTP CONNECT tunneling through proxies. Default proxy ports: HTTP=3128, HTTPS=3129. Proxy configuration is read from environment on each call (no caching).

### Mockable Dial Functions

Four package-level variables enable test injection without real network I/O:
- `sftpDialFunc`, `netDialFunc`, `sshNewClientConnFunc`, `verifySFTPSubsystemFunc`

**Rule**: When adding new network operations, follow this pattern -- expose the function as a package-level variable so tests can override it.

## Build and Runtime

### FIPS Mode

FIPS is enabled by default (`FIPS_ENABLED=true` in Makefile). The `fips.go` file imports `crypto/tls/fipsonly`, restricting TLS to FIPS-approved cipher suites. This requires `CGO_ENABLED=1`.

**Rule**: Do not use non-FIPS TLS configurations. Any new TLS client or server code must work within FIPS constraints.

### Leader Election

Two layers of leader election exist:
1. `operator-lib/leader.Become()` -- ConfigMap-based lock (`must-gather-operator-lock`)
2. controller-runtime `LeaderElection` flag (disabled by default)

Both are bypassed when `OSDK_FORCE_RUN_MODE=local`.

### Health Probes

Liveness (`/healthz`) and readiness (`/readyz`) probes are simple pings on port 8081. They do not verify controller health or API server connectivity.

## Volume Configuration

- `must-gather-output`: EmptyDir by default; PVC when `storage.type=PersistentVolume`
- `must-gather-upload`: Always EmptyDir (staging area for compressed archive)
- `trusted-ca`: ConfigMap volume (only when `--trusted-ca-configmap` is set)

When PVC storage is used, each pod gets an isolated subdirectory via `SubPathExpr` using `$(POD_NAME)` to prevent data overwrites across retries.

**Rule**: `RestartPolicy` is `Never` -- failed pods are not restarted in-place. The Job controller creates new pods up to `backoffLimit`. This is required because the gather process is not idempotent when writing to shared volumes.
