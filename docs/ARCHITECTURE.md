# Architecture

Architectural decisions and institutional knowledge for the Must Gather Operator. This document explains the "why" behind key design choices.

## High-Level Architecture

The Must Gather Operator is a Kubernetes operator that automates diagnostic data collection on OpenShift clusters. It:

1. Watches `MustGather` custom resources cluster-wide
2. Validates ServiceAccounts and SFTP credentials
3. Creates Jobs that run must-gather containers
4. Optionally uploads collected data to Red Hat case management via SFTP
5. Cleans up resources after completion

### Operator Model: Single-Namespace with Cluster-Wide Watch

**Decision**: The operator runs in a single namespace (`must-gather-operator`) but watches MustGather CRs across all namespaces.

**Rationale**: 
- Jobs are created in the CR's namespace, not the operator's namespace
- This allows users to leverage namespace-scoped RBAC for must-gather Jobs
- The operator needs cluster-scoped RBAC only for watching CRs and creating Jobs
- Avoids requiring elevated permissions in every namespace

**Trade-off**: More complex RBAC setup (cluster-scoped ClusterRole) vs. simpler namespace-scoped deployment.

## Key Architectural Decisions

### Two-Container Job Model

**Decision**: Each MustGather Job uses two containers sharing a process namespace:
1. **gather** - Runs the must-gather image, writes to `/must-gather`
2. **upload** - Runs the operator image, waits for gather completion, compresses and uploads via SFTP

**Rationale**:
- **Separation of concerns**: Gather container runs user-specified images (potentially untrusted), upload container runs operator-controlled code
- **Permission isolation**: Gather runs with cluster-admin via ServiceAccount, upload runs with minimal permissions
- **Process detection**: `ShareProcessNamespace: true` allows upload to detect gather completion via `pgrep -a gather`
- **No gather image modification**: Must-gather images don't need built-in upload logic

**Why not a sidecar?**: Sidecars would require gather to signal completion. Process namespace sharing avoids modifying gather images.

**Why not sequential containers?**: Kubernetes doesn't support sequential container execution within a single pod. Init containers can't share volumes with main containers effectively for this use case.

### Pre-flight SFTP Validation

**Decision**: Validate SFTP credentials before creating the Job.

**Rationale**:
- **Fail fast**: Catch bad credentials without spawning pods
- **User experience**: Immediate feedback via CR status rather than waiting for Job failure
- **Resource efficiency**: Avoid creating Jobs that will fail
- **Retry logic**: Only transient errors (timeouts) trigger retry; auth failures are terminal

**Trade-off**: Adds network I/O to the reconcile loop, but the 5-second dial timeout keeps it fast.

### No Cross-Namespace Secret Copy

**Decision**: Case management secrets are referenced directly from the CR's namespace via `SecretKeyRef`. They are NOT copied to the operator namespace.

**Rationale**:
- **Security**: Avoids duplicating sensitive credentials across namespaces
- **RBAC alignment**: Users control secret access via namespace RBAC
- **Simpler cleanup**: No secrets to delete in the operator namespace

**Historical context**: Earlier designs copied secrets to the operator namespace. This was removed to reduce attack surface and simplify RBAC.

**Contrast with TrustedCA ConfigMap**: The TrustedCA ConfigMap IS copied because it's not sensitive and supports multi-owner patterns (multiple MustGather CRs can share one ConfigMap copy).

### Spec Immutability

**Decision**: The entire MustGather spec is immutable after creation (enforced by CEL rule).

**Rationale**:
- **Operational clarity**: Users can't accidentally change a running must-gather
- **Simpler controller logic**: No need to handle spec updates or Job recreation
- **Audit trail**: Each MustGather CR is a snapshot of what was run

**Trade-off**: Users must delete and recreate the CR to change parameters. This is acceptable because must-gather runs are typically one-shot operations.

### Finalizer-Based Cleanup

**Decision**: Use finalizer `finalizer.mustgathers.operator.openshift.io` to ensure cleanup runs before CR deletion.

**Rationale**:
- **Prevent orphaned resources**: Jobs and Pods are cleaned up even if the CR is force-deleted
- **Ownership verification**: Cleanup verifies OwnerReference UIDs before deleting Jobs
- **User control**: `retainResourcesOnCompletion` allows skipping cleanup for debugging

**Implementation detail**: Cleanup is fail-fast - if any step fails, the finalizer stays and the controller retries.

### Event-Driven Cleanup (Not Timer-Based)

**Decision**: Cleanup triggers on Job completion events, not periodic garbage collection.

**Rationale**:
- **Efficient**: No polling or timer overhead
- **Immediate**: Cleanup happens as soon as the Job finishes
- **Predictable**: Users know exactly when cleanup occurs (on success/failure)

**Note**: The "~6 hours after completion" cleanup mentioned in README.md refers to Kubernetes' TTL controller, not the operator's logic.

### Preferred (Not Required) Infra Node Affinity

**Decision**: Jobs use `PreferredDuringSchedulingIgnoredDuringExecution` for infra nodes, not `Required`.

**Rationale**:
- **Cluster compatibility**: Works on clusters without dedicated infra nodes
- **Failover**: Jobs can run on any node if infra nodes are unavailable or full
- **User override**: Users can provide their own NodeSelector if needed

**Trade-off**: May impact application workloads if infra nodes aren't available, but must-gather must work everywhere.

## Data Flow

### CR Creation to Upload

1. **User creates MustGather CR** in any namespace
2. **Controller receives reconcile event** (filtered by generation/finalizer predicate)
3. **Pre-flight validation**:
   - ServiceAccount exists in CR namespace
   - ServiceAccount is not the operator's own SA (privilege escalation check)
   - Secret exists with `username` and `password` keys
   - SFTP connection succeeds (with retry for transient errors)
4. **Job creation**:
   - Gather container configured with must-gather image, ServiceAccount, volumes
   - Upload container added if `spec.uploadTarget` is set
   - Job created in CR namespace with OwnerReference to the CR
5. **Job execution**:
   - Gather container runs must-gather, writes to shared `/must-gather` volume
   - Upload container polls via `pgrep` until gather exits
   - Upload compresses output and uploads via SFTP
6. **Completion**:
   - Controller detects Job success/failure via status watch
   - Status updated on MustGather CR
   - Cleanup triggered (unless `retainResourcesOnCompletion: true`)
7. **Cleanup**:
   - Pods deleted by `controller-uid` label
   - Job deleted (after OwnerReference UID verification)
   - TrustedCA ConfigMap cleanup (remove OwnerReference or delete)
   - Finalizer removed, allowing CR deletion

## Component Interactions

### Controller ↔ Kubernetes API

- **Watches**: MustGather CRs (all namespaces), Jobs (owned), ConfigMaps (owned, specific name)
- **Predicates**: Filter events to avoid unnecessary reconciles
- **Leader election**: ConfigMap-based lock in operator namespace
- **RBAC**: Cluster-scoped ClusterRole with scoped permissions

### Job ↔ External Systems

- **SFTP server**: `sftp.access.redhat.com` (default, overridable)
- **Proxy**: Inherits HTTP/HTTPS proxy config from operator environment
- **ImageStream resolution**: Operator namespace only (not CR namespace)

### Upload Container ↔ Gather Container

- **Shared volumes**: `/must-gather` (output), `/must-gather-upload` (staging)
- **Process namespace sharing**: Upload detects gather completion via `pgrep`
- **No direct communication**: No sockets, files, or signals exchanged

## Design Patterns

### Package-Level Function Variables for Testability

**Pattern**: Network-calling functions are declared as `var` at package level:
```go
var sftpDialFunc = func(ctx context.Context, username, password, host string) error { ... }
```

**Why**: Tests override these to avoid real network calls. Interfaces would add boilerplate without benefit for single-implementation functions.

### `interceptClient` for Controller Tests

**Pattern**: Unit tests wrap `fake.Client` with per-operation failure hooks.

**Why**: Allows fine-grained error injection (e.g., "fail only Job deletion") without building custom mocks for each test case.

### Sentinel Errors for Routing

**Pattern**: `errImageValidation` routes ImageStream errors to `setValidationFailureStatus` instead of `ManageError`.

**Why**: Error type determines handling path (terminal vs. retriable). Sentinels are clearer than string matching.

## Constraints and Trade-offs

### FIPS Mode by Default

**Constraint**: FIPS mode is enabled via build tag (`fips_enabled=true` in Makefile).

**Impact**: Restricts TLS cipher suites, requires CGO. Cannot be disabled without changing the build.

**Why**: OpenShift compliance requirement.

### No Resource Requests/Limits

**Decision**: Jobs do not set resource requests or limits.

**Rationale**: Must-gather workloads vary widely by cluster size. Hardcoded limits would either waste resources or cause OOMKills.

**Trade-off**: Relies on namespace LimitRange defaults. May compete with application workloads.

### Boilerplate Build System

**Constraint**: The Makefile includes `boilerplate/generated-includes.mk`.

**Impact**: Standard targets (`go-build`, `go-test`, etc.) are defined externally. Direct Makefile edits may conflict with boilerplate updates.

**Why**: OpenShift-eng convention for consistent build/CI across repos.

## Historical Context

### API Group Migration

The operator originally used a different API group. The current group is `operator.openshift.io/v1alpha1`. Manifests were migrated; old CRDs are no longer supported.

### Secret Copy Removal

Earlier versions copied case management secrets from the CR namespace to the operator namespace. This was removed to improve security and simplify RBAC.

### Garbage Collection Evolution

The operator used to implement timer-based cleanup (~6 hours). This was replaced with event-driven cleanup via the finalizer pattern, with Kubernetes' TTL controller handling age-based deletion.

## Future Considerations

### Multi-Upload Target Support

The `UploadTargetSpec` union pattern supports adding new upload types (e.g., S3, HTTP). The discriminator pattern is already in place; new types require adding a union member and implementing the upload logic.

### PersistentVolume Storage

PVC storage (via `spec.storage.type`) allows must-gather output to persist beyond pod lifetime. This supports use cases where the output is consumed by other tools or stored for compliance.

### Trusted CA ConfigMap Multi-Tenancy

The TrustedCA ConfigMap copy supports multiple owners in the same namespace. This allows several MustGather CRs to share one ConfigMap without conflicts.

## References

- **Detailed conventions**: See [AGENTS.md](../AGENTS.md)
- **Domain-specific rules**: See [docs/*-guidelines.md](.)
- **Contribution workflow**: See [CONTRIBUTING.md](../CONTRIBUTING.md)
