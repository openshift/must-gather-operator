# Design: MustGather reconciler

**Module**: `controllers/mustgather/mustgather_controller.go`
**Architecture**: [components.md](../../harness-evals/harness-docs/architecture/components.md)
**Decision record**: [ADR-0005: reconciler.io/runtime composition](../adrs/adr-0005-reconcilerio-runtime.md)

## Preconditions

- The manager scheme includes `operator.openshift.io/v1` and `v1alpha1`, plus `config/v1` and `image/v1` for proxy and ImageStream resolution.
- `DEFAULT_MUST_GATHER_IMAGE` and `OPERATOR_IMAGE` are set before a Job that needs gather or upload is created.
- The ServiceAccount named in the CR exists in the CR namespace.
- When `uploadTarget` is SFTP, the referenced Secret has non-empty `username` and `password` keys and SFTP connectivity has been validated.
- `OPERATOR_SERVICE_ACCOUNT` is available so the controller can reject using its own SA on a CR in the operator namespace.

## Invariants

- The reconcile loop is a composed [reconciler.io/runtime](https://reconciler.io/runtime) tree: a `ResourceReconciler` (fetch, requeue on error, persist status changes) wrapping an `Advice` (outcome conditions) wrapping a `WithFinalizer` wrapping a `Sequence` of named `SyncReconciler` steps (`ValidateServiceAccount`, `SyncTrustedCAConfigMap`, `ValidateObfuscationConfigMap`, `GatherJob`). There is exactly one reconciler for the full MustGather lifecycle: Job create, status, cleanup, and finalizer handling stay in this tree.
- The `ResourceReconciler` fetches the CR, treats NotFound as a no-op, and persists status changes by diffing the status before/after the sub-reconcilers. Status is not persisted for terminating CRs.
- `WithFinalizer` adds `finalizer.mustgathers.operator.openshift.io` (optimistic-lock patch) before the sequence runs and clears it after finalization succeeds; sub-reconciler `Sync` methods are skipped while the CR is terminating, and the `GatherJob` `Finalize` hook performs cleanup.
- Terminal validation failures set a Failed status and return `reconcilers.ErrHaltSubReconcilers`, which stops the sequence without requeueing. The spec cannot change, so retrying is pointless.
- Secrets are referenced in place via `SecretKeyRef` from the CR namespace. The operator never copies Secrets. Only a trusted CA ConfigMap is replicated into the CR namespace.
- The controller rejects its own ServiceAccount when the CR is in the operator namespace. Gather must not run with the operator's credentials.
- Cleanup is event-driven (Job success/failure in the same reconcile, and CR deletion via the finalizer). There is no GC timer.
- Cleanup is skipped while `retainResourcesOnCompletion` is true. On CR deletion, the finalizer still skips explicit cleanup when retention is enabled (owned objects may then fall to Kubernetes GC).
- Trusted CA ConfigMap copies use ownerReferences. The ConfigMap is deleted only when this CR was the last owner.
- `MetricMustGatherTotal` increments after a successful Job create. `MetricMustGatherErrors` increments when reconcile observes `Job.Status.Failed > backoffLimit`, before `handleJobCompletion`. If completion cleanup then fails, the returned error requeues and the error counter can increment again. Neither counter increments for terminal validation failures.
- On terminal Job failure, `buildJobFailureReason` lists Pods in the Job namespace with `job-name=<Job name>`, inspects the newest Pod, and prefers a non-zero gather container exit code over an upload container exit code. Exit 124 or 137 is reported as a gather timeout, except `OOMKilled`, which is reported as a gather failure. A failed upload is reported with its exit code. If the Pod lookup fails or has no usable terminated status, the status reason falls back to `MustGather Job pods failed`.
- Reconcile outcome conditions (`ReconcileSuccess`/`ReconcileError`, reasons `LastReconcileCycleSucceded`/`LastReconcileCycleFailed`) match the historical operator-utils values so existing dashboards keep working. Terminal validation failures (`ErrHaltSubReconcilers`) and finalization never touch them.

## Rationale

- The composed tree replaces a 200-line imperative `Reconcile` method: fetching, status persistence, finalizer bookkeeping and event recording come from the framework, and each lifecycle step is a small named function that can be read and tested in isolation.
- In-place Secret refs avoid a cluster-wide copy of case-management credentials. The Job Pod reads the Secret from the CR namespace at schedule time.
- The operator-SA guard exists because the operator SA is not a gather identity; using it would over-privilege collection and confuse RBAC reviews.
- Cleanup in the completion reconcile (instead of a TTL controller) keeps one-shot semantics: when the Job is done, owned Pods and the Job go away unless the user asked to retain them.
- `handleJobCompletion` and `setValidationFailureStatus` persist status explicitly before any cleanup so that a failed status write requeues with the Job still in place. The framework's own status diff then becomes a no-op write rather than a second state transition.

## Trade-offs

- Secret data can change after the controller validated it and before the Pod starts (`SecretKeyRef` resolves at schedule time). CEL freezes the Secret *name*, not the Secret *data*.
- On cleanup failure during deletion, the reconciler returns an error and keeps the finalizer so Kubernetes retries. Deletion stays blocked until cleanup succeeds. A prolonged API outage delays CR removal; this path does not drop the finalizer and leave owned Jobs or Pods unreferenced.
- `MetricMustGatherErrors` is not gated on first observation. A failed Job whose completion cleanup keeps failing will over-count.
- Failure reasons are best effort: Pod lookup happens before completion cleanup, but a Pod listing error, absent Pod, or missing terminated status leaves only the generic reason. Cleanup can then remove the Pod that supplied a detailed reason.
- Transient errors uniformly record a `ReconcileError` condition and a `ProcessingError` warning event via the `ManageOutcome` advice (previously only operator-utils `ManageError` call sites did). The framework skips the status write when the condition is unchanged, bounding API churn across retries, but each distinct error message still produces one status write.
- The finalizer is patched with `MergeFromWithOptimisticLock` instead of the previous full-object update, and is added in the same reconcile that creates the Job (previously a dedicated early return). Both reduce round-trips; the patch can conflict with concurrent metadata writers, which resolves on the automatic requeue.
- The controller is wired with the controller-runtime builder directly instead of the framework's `SetupWithManager`: reconciler.io/runtime v0.23 registers `For()` without options, and the primary watch must carry the generation/finalizer predicate (`SetupForOptions` only landed in v0.25, which requires controller-runtime >= v0.23). Revisit once the module upgrades controller-runtime.
