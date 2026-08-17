# Must-Gather Operator — Architecture

## Repo Layout

```text
must-gather-operator/
├── main.go                          # Entrypoint: scheme, manager, metrics server, leader election
├── api/v1alpha1/                    # CRD types (MustGather, sub-types, deepcopy, openapi)
├── controllers/mustgather/
│   ├── mustgather_controller.go     # Single reconciler: Job lifecycle, cleanup, validation
│   ├── template.go                  # Job spec builder: gather + upload containers, volumes, env
│   ├── predicates.go                # Event filters: generation/finalizer changes, Job status changes
│   ├── validation.go                # SFTP pre-flight check: SSH dial, proxy tunneling, retry
│   ├── constant.go                  # Constants: env var names, default image var, validation types, retry count
│   ├── mustgather_controller_test.go
│   └── template_test.go
├── config/config.go                 # Operator name, OLM skip-range toggle
├── pkg/
│   ├── localmetrics/localmetrics.go # Two Prometheus counters (total, errors)
│   ├── k8sutil/k8sutil.go          # Operator namespace detection (Downward API or env var)
│   └── mustgatherutil/util.go       # Directory name generation (compact ISO 8601 timestamp + cluster ID + random)
├── build/bin/
│   ├── upload                       # Shell: compress + SFTP upload with proxy support
│   └── https-proxy-connect-util     # Shell: socat-based HTTPS CONNECT proxy tunneling
├── deploy/                          # Deployment, RBAC, CRD, sample PVC
├── bundle/manifests/tech-preview/   # OLM bundle (CSV, CRD)
├── examples/                        # 8 example MustGather CRs
├── test/e2e/                        # Ginkgo E2E tests
├── version/version.go               # Operator version (0.1.1), SDK version (1.21.0)
├── Makefile                         # Thin wrapper → boilerplate/generated-includes.mk
└── boilerplate/                     # openshift/golang-osd-operator convention (build, lint, CI)
```

## Framework

| Dependency | Version | Purpose |
|---|---|---|
| controller-runtime | v0.21.0 | Controller manager, reconciler, client, predicates |
| operator-sdk | 1.21.0 | Project scaffolding (version marker only) |
| operator-utils (redhat-cop) | v1.3.7 | `ReconcilerBase`: `ManageError`, `ManageSuccess`, `CreateResourceIfNotExists`, `DeleteResourcesIfExist` |
| operator-custom-metrics | v0.5.0 | OSD metrics server on port 8080 |
| operator-lib | v0.11.0 | Leader election (`leader.Become`), proxy env reading |

## Single Controller Design

There is exactly **one controller** (`MustGatherReconciler`) managing the full lifecycle. It uses controller-runtime (not library-go).

### Startup (`main.go:82-88 init`, `113-123 manager`, `175-186 controller registration`)

```text
Scheme: clientgoscheme + config/v1 + image/v1 + operator.openshift.io/v1alpha1
Manager: leader election via operator-lib + controller-runtime flag
Metrics: built-in metrics DISABLED (BindAddress: "0"), custom OSD metrics on :8080
```

### Watches (`mustgather_controller.go:371-381`)

| Resource | Predicate | Purpose |
|---|---|---|
| `MustGather` (primary) | `resourceGenerationOrFinalizerChangedPredicate` | Only reconcile on spec or finalizer changes, ignore status-only |
| `Job` (owned) | `isStateUpdated` | Only on Update when `Job.Status` changed (deep equal). Create/Delete/Generic suppressed |
| `ConfigMap` (owned, conditional) | `isNameEquals(trustedCAConfigMap)` | Filters to named ConfigMap only. Create/Delete/Generic admitted for the named ConfigMap |

**Predicate rules**: Always attach a predicate via `builder.WithPredicates(...)` when adding a new `Owns()` or `Watches()`. Use `reflect.DeepEqual` on the specific sub-field that matters, not the entire object.

### Reconciliation Flow

```text
Reconcile(req)
  │
  ├─ Fetch MustGather CR (NotFound → done)
  │
  ├─ DeletionTimestamp set?
  │   YES → cleanupMustGatherResources() (unless RetainResources)
  │        → remove finalizer → Update CR → done
  │
  ├─ Missing finalizer? → add it → Update CR → done (requeue via update event)
  │
  ├─ SA guard: reject operator's own SA in operator namespace → validation failure
  │
  ├─ Trusted CA ConfigMap → ensureTrustedCAConfigMap() (copy to CR namespace)
  │
  ├─ Lookup Job (same name as CR)
  │   │
  │   ├─ NotFound → CREATE PATH:
  │   │   ├─ Build Job template (getJobFromInstance)
  │   │   ├─ Validate ServiceAccount exists
  │   │   ├─ Validate SFTP secret (username/password fields, connectivity test)
  │   │   ├─ CreateResourceIfNotExists(Job)
  │   │   └─ MetricMustGatherTotal.Inc()
  │   │
  │   └─ Found → STATUS PATH:
  │       ├─ Active > 0     → log "still running", update status
  │       ├─ Succeeded > 0  → handleJobCompletion("Completed")
  │       └─ Failed > backoffLimit → MetricMustGatherErrors.Inc()
  │                                → handleJobCompletion("Failed")
  │
  └─ ManageSuccess() → set ReconcileSuccess condition
```

### Cleanup (Event-Driven, NOT Timer-Based)

`cleanupMustGatherResources()` runs on **job completion** (success or failure) and **CR deletion** (via finalizer). There is no scheduled garbage collection timer.

Cleanup steps:
1. Verify Job has ownerReference matching MustGather UID (skip if not owned)
2. List and delete pods with label `controller-uid=<Job.UID>`
3. Delete the Job
4. Remove trusted CA ConfigMap ownerReference (delete ConfigMap if last owner)

Skipped entirely when `retainResourcesOnCompletion: true`.

## Job Template (`template.go`)

### Job Container Architecture

```text
Job (backoffLimit: 3, restartPolicy: Never)
├── Container: "gather"
│   ├── Image: DEFAULT_MUST_GATHER_IMAGE or ImageStream-resolved
│   ├── Command: timeout-wrapped /usr/bin/gather or /usr/bin/gather_audit_logs
│   ├── Mounts: /must-gather (output), /etc/pki/tls/certs (CA, optional)
│   └── Env: MUST_GATHER_SINCE, MUST_GATHER_SINCE_TIME (optional)
│
├── Container: "upload" (ONLY when UploadTarget is configured)
│   ├── Image: OPERATOR_IMAGE
│   ├── Command: poll for gather completion (pgrep), then /usr/local/bin/upload
│   ├── Mounts: /must-gather (input), /must-gather-upload (staging), /etc/pki/tls/certs (optional)
│   └── Env: username, password (SecretKeyRef), caseid, host, internal_user,
│            must_gather_output, must_gather_upload, FILENAME_PREFIX,
│            http_proxy, https_proxy, no_proxy
│
├── ShareProcessNamespace: true (upload uses pgrep to detect gather completion)
├── Volumes: must-gather-output (emptyDir or PVC), must-gather-upload (emptyDir),
│            trusted-ca (ConfigMap, optional)
├── Affinity: prefer infra nodes (node-role.kubernetes.io/infra, weight 1)
└── Toleration: NoSchedule on infra nodes
```

**Upload polling**: 5 consecutive "no gather process" checks with 30s sleep between, plus 120s sleeps while gather is running.

### Upload Script (`build/bin/upload`)

1. Compress: `tar --ignore-failed-read -caf <output>.tar.gz <input>/`
2. Remote path: `${caseid}_${FILENAME_PREFIX}.tar.gz` (internal users: `${username}/` prefix)
3. Transfer: `sshpass -e sftp` with `StrictHostKeyChecking=no`
4. Proxy: HTTP → `nc --proxy`, HTTPS → `socat` TLS tunnel via `https-proxy-connect-util`

## Trusted CA ConfigMap Replication

**NOT secret replication** — secrets are accessed directly via `SecretKeyRef` from the CR's namespace. Only trusted CA ConfigMaps are replicated:

- Source: operator namespace ConfigMap (set via `--trusted-ca-configmap` flag)
- Target: CR namespace copy with ownerReference to MustGather CR
- Multiple CRs in same namespace share one ConfigMap via multiple ownerReferences
- On CR deletion: remove ownerReference; delete ConfigMap if last owner

## Metrics

| Metric | Type | Trigger |
|---|---|---|
| `must_gather_operator_must_gather_total` | Counter | Job created successfully |
| `must_gather_operator_must_gather_errors` | Counter | Job failed (exceeded backoffLimit) |

Served via OSD custom metrics on `:8080/metrics`. Controller-runtime built-in metrics are disabled.

## SFTP Validation (`validation.go`)

Pre-flight connectivity check before Job creation.

### Dependency Injection via Package-Level Vars

All external I/O functions are replaceable package-level vars for testability:

| Variable | Purpose |
|---|---|
| `sftpDialFunc` | Top-level SFTP connection test |
| `netDialFunc` | TCP dial (proxy-aware) |
| `sshNewClientConnFunc` | SSH handshake |
| `verifySFTPSubsystemFunc` | SFTP subsystem check |
| `sftpNewClientFunc` | SFTP client creation |
| `getProxyURLForAddr` | Proxy URL resolution |

### Connection Flow

`checkSFTPConnection` follows a layered protocol upgrade:
1. Normalize host address (add default port 22, bracket IPv6 via `net.JoinHostPort`/`net.SplitHostPort`)
2. TCP dial via `netDialFunc` (proxy-aware)
3. SSH handshake via `sshNewClientConnFunc` (upgrades TCP → SSH)
4. Verify SFTP subsystem via `verifySFTPSubsystemFunc` (upgrades SSH → SFTP)

Each layer wraps errors with `classifySFTPError` before returning. When a layer fails, all resources from prior layers must be closed.

### Error Classification

`IsTransientError` classifies for retry: `context.DeadlineExceeded`, `context.Canceled`, `net.Error.Timeout()` are transient. Auth failures, connection refused, DNS errors are not.

`classifySFTPError` translates raw errors into user-friendly messages (authentication, connection refused, host unreachable, DNS failure, timeout, connection reset, SFTP subsystem unavailable).

### Retry Logic

`validateSFTPWithRetry` retries up to `MaxSFTPValidationRetries` (3) times. Only transient errors trigger retry. No backoff delay (SSH dial timeout of 5s provides pacing). Non-transient errors return immediately.

### HTTP Proxy Support

**Go side** (`validation.go`): `httpproxy.FromEnvironment()` reads proxy env vars on every call (no caching). `proxyDialContext` establishes HTTP CONNECT tunnel with `Proxy-Authorization: Basic` header over raw TCP. Default ports: HTTP=3128, HTTPS=3129. **Limitation**: The `https` proxy URL scheme only changes the default port; the CONNECT request and credentials are sent over an unencrypted TCP connection regardless of scheme. The subsequent SSH handshake encrypts the SFTP session itself.

**Shell side** (`build/bin/upload`): HTTP proxy via `nc --proxy`, HTTPS proxy via `socat` TLS tunnel through `https-proxy-connect-util`.

Proxy env vars flow: operator process → `proxy.ReadProxyVarsFromEnv()` → upload container env vars (lowercase `http_proxy`, `https_proxy`, `no_proxy`). Both paths must handle proxy authentication consistently.

### Security

`InsecureIgnoreHostKey` used intentionally (matches upload script's `StrictHostKeyChecking=no`). This is a known, accepted trade-off documented in the codebase (`#nosec G106`). The operator connects exclusively to Red Hat's managed SFTP endpoint (`sftp.access.redhat.com`); host-key verification is disabled because the server's key may rotate without notice. Compensating controls: credentials are validated before Job creation, transfer occurs over SSH (encrypted in transit), and the connection target is immutable via CEL-enforced spec. Remediation plan: restore host-key verification if Red Hat publishes a stable host key or key-distribution mechanism. SSH directory at `/tmp/must-gather-operator/.ssh/` with restrictive permissions (700/600). Password passed via `SSHPASS` env var and `sshpass -e` — never on the command line.

## FIPS Mode

Build-time only (`Makefile:1` → `FIPS_ENABLED=true`):
- Build tag: `fips_enabled`
- `GOEXPERIMENT=boringcrypto` for BoringCrypto-backed crypto
- `ensure-fips` target runs `configure-fips.sh` — generally requires building inside the provided Dockerfile

## Environment Variables

### Operator Startup (Required)

| Variable | Source | Purpose |
|---|---|---|
| `DEFAULT_MUST_GATHER_IMAGE` | Deployment env | Gather container image |
| `OPERATOR_IMAGE` | Deployment env | Upload container image (operator's own image) |
| `OPERATOR_SERVICE_ACCOUNT` | Downward API (`spec.serviceAccountName`) | Self-rejection guard |

### Operator Startup (Optional)

| Variable | Purpose |
|---|---|
| `OSDK_FORCE_RUN_MODE=local` | Skip leader election |
| `OPERATOR_NAMESPACE` | Override namespace detection |
| `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` | Forwarded to upload container |

## Generated Code (DO NOT hand-edit)

| File | Generator | Make Target |
|---|---|---|
| `api/v1alpha1/zz_generated.deepcopy.go` | controller-gen `object` | `make generate` |
| `api/v1alpha1/zz_generated.openapi.go` | openapi-gen | `make generate` |
| `deploy/crds/operator.openshift.io_mustgathers.yaml` | controller-gen `crd` | `make manifests` |
| `boilerplate/generated-includes.mk` | boilerplate update | `make boilerplate-update` |

## Error Handling Patterns

### Import Conventions

Two `errors` packages with distinct roles:
```go
import (
    goerror "errors"                    // standard library: Is, As, New
    "k8s.io/apimachinery/pkg/api/errors" // imported as `errors`: IsNotFound, IsAlreadyExists
)
```

### Sentinel Errors

Declare with `goerror.New`, wrap with `%w` as first verb, check with `goerror.Is` (never string comparison):
```go
var errImageValidation = goerror.New("image validation failed")
return nil, fmt.Errorf("%w: %v", errImageValidation, err)
```

### Reconcile Return Conventions

| Pattern | When to use |
|---------|-------------|
| `return reconcile.Result{}, nil` | CR not found (deleted), deletion complete, or terminal validation failure via `setValidationFailureStatus` |
| `return reconcile.Result{}, err` | Propagate to controller-runtime for default exponential backoff requeue |
| `return reconcile.Result{Requeue: true}, err` | Transient API errors (non-NotFound Get failures) for immediate retry |
| `return r.ManageError(ctx, instance, err)` | Infrastructure errors — sets `ReconcileError` condition, emits Warning event |
| `return r.ManageSuccess(ctx, instance)` | Successful reconciliation — sets `ReconcileSuccess` condition |

### NotFound Handling by Resource

- **Primary CR**: `(Result{}, nil)` — deleted, nothing to reconcile
- **ServiceAccount**: Terminal validation failure via `setValidationFailureStatus`
- **Secret**: User-actionable error via `ManageError`
- **Job**: Expected during first reconciliation — proceed to create
- **ConfigMap during cleanup**: Log and return nil (idempotent)

### Validation Failures (Terminal)

`setValidationFailureStatus` sets `Status=Failed`, `Completed=true`, emits `ReconcileError` condition with `Reason: "ValidationFailed"`, and returns `(Result{}, nil)` (no requeue). Validation type constants live in `constant.go`: `ValidationServiceAccount`, `ValidationSFTPCredentials`, `ValidationImageStream`.

When `Status().Update()` fails after validation, fall through to `ManageError` for retry.

### Logging Conventions

Request-scoped logger: `reqLogger := log.WithValues("Request.Namespace", ..., "Request.Name", ...)`. Use structured key-value pairs. `V(4)` for debug, `Info` for state transitions, always log before returning an error.

### Metrics Increment Rules

- `MetricMustGatherTotal`: Increment once when Job is created (not on retries/requeues)
- `MetricMustGatherErrors`: Increment once when Job's `Status.Failed > backoffLimit` (not for validation failures or transient errors)
- Never increment counters inside retry loops

## Security Considerations

### Credential Handling

Credentials reach the upload container exclusively through `SecretKeyRef` env vars — never volume-mounted files. Secret must contain non-empty `username` and `password` keys, both validated non-empty before Job creation.

### RBAC Conventions

| Role | Scope | Design |
|---|---|---|
| `must-gather-operator` ClusterRole | Operator | Least-privilege for secrets: `get`, `delete`, `create`, `list`, `watch` (no `update`) |
| `must-gather-admin` ClusterRole | Gather Jobs | Full cluster-admin (`*`) — intentional, gather needs broad read access |
| Prometheus Role | `must-gather-operator` ns | Namespace-scoped, read-only on services/endpoints/pods |

New RBAC additions must be reflected in both the deploy manifest and kubebuilder RBAC markers on the controller's `Reconcile` method.

### Container Security

- **Non-root**: Dockerfile `USER 65534:65534` (nobody)
- **Process namespace sharing**: `ShareProcessNamespace: true` allows containers to see each other's processes (upload detects gather completion via `pgrep`)
- **Volumes**: `trusted-ca` ConfigMap volume mounted `ReadOnly: true` at `/etc/pki/tls/certs`. Never mount CA bundles as read-write

### Trusted CA Bundle

Operator copies CA ConfigMap from its namespace to CR namespace with ownerReferences to the MustGather CR. Preserves labels including `config.openshift.io/inject-trusted-cabundle: "true"`. Multi-owner tracking: only deletes ConfigMap if it was the last owner.

### CRD Immutability as Security

Spec immutability via CEL prevents changes to the Secret reference name (`caseManagementAccountSecretRef.Name`) and upload target after CR creation. However, CEL freezes only the reference — the referenced Secret's data may be updated between validation and Job execution or a Job retry. The controller validates credentials before creating the Job, but `SecretKeyRef` resolves at Pod scheduling time.

## Performance Considerations

### Requeue Strategy

Do not call `ManageError` for transient errors — it writes a status condition and event on every retry, creating API server churn. Use `Requeue: true` instead. Validation failures use `setValidationFailureStatus` with no requeue (CR is immutable, retrying is pointless).

### Job Configuration Rationale

- **No resource requests/limits**: Deliberate omission — must-gather collection is memory-intensive and varies by cluster size
- **No `ActiveDeadlineSeconds`**: Timeout enforcement at shell level via `timeout %v` in gather command
- **`BackoffLimit=3`**: Controller checks `Status.Failed > backoffLimit` for terminal failure detection. Update both template and controller if changed

### Controller Manager Settings

- **Metrics**: Built-in disabled (`BindAddress: "0"`); custom OSD metrics on `:8080`
- **Leader election**: `leader.Become()` from operator-lib (not controller-runtime built-in)
- **Concurrency**: Default `MaxConcurrentReconciles=1` — appropriate since CRs are infrequent and reconciliation does network I/O

### Context Propagation

Always propagate `ctx` to client calls and `validateSFTPWithRetry`. SSH dialer uses `sshDialTimeout = 5s` independently; context controls TCP dial cancellation.

## Anti-Patterns / Warnings

1. **DO NOT** assume secret replication — secrets are referenced in-place via SecretKeyRef, not copied
2. **DO NOT** hand-edit generated files — run `make generate` / `make manifests`
3. **DO NOT** use the operator's own ServiceAccount in the CR when the CR is in the operator namespace (guard at `mustgather_controller.go:158-168`)
4. **DO NOT** expect host key verification — both validation and upload intentionally skip it (`#nosec G106`)
5. **DO NOT** set `since` and `sinceTime` together — CEL validation rejects it

## SME Review Recommended

- Implementation recipe for adding new upload target types (beyond SFTP) — the union discriminator pattern exists but no second type has been added yet
- Rationale for disabling controller-runtime built-in metrics in favor of OSD custom metrics
- Whether `StrictHostKeyChecking=no` is an accepted long-term trade-off or planned for hardening
