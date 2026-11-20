# ADR-0005: Reconcile loop composed with reconciler.io/runtime

**Status**: Accepted
**Date**: 2026-11-19
**Component**: Must-Gather Operator

## Context

The MustGather reconcile loop was a single ~200-line imperative `Reconcile` method. Resource fetching, finalizer bookkeeping, status persistence, event recording and outcome conditions were hand-rolled (the latter two inherited from `redhat-cop/operator-utils`' `ReconcilerBase`). Every behavior change required editing the monolith and reasoning about its nested early returns, and the operator carried an extra dependency for a handful of helpers.

## Decision

Rewrite the reconciler as a composition of [reconciler.io/runtime](https://github.com/reconcilerio/runtime) (v0.23, compatible with controller-runtime v0.21) sub-reconcilers, and drop the `operator-utils` dependency:

```
ResourceReconciler[MustGather]   fetch CR, requeue on error, persist status changes
└─ Advice "ManageOutcome"        ReconcileSuccess/ReconcileError conditions + events
   └─ WithFinalizer              add/clear finalizer.mustgathers.operator.openshift.io
      └─ Sequence
         ├─ ValidateServiceAccount       reject the operator's own SA (terminal)
         ├─ SyncTrustedCAConfigMap       replicate the trusted CA bundle
         ├─ ValidateObfuscationConfigMap require the obfuscation ConfigMap
         └─ GatherJob                    create/observe the Job (Sync),
                                          clean up on completion and deletion (Finalize)
```

- Terminal validation failures set a Failed status and return `reconcilers.ErrHaltSubReconcilers`: the sequence stops, the framework treats the request as successful, no requeue.
- Transient errors propagate as errors; the `ResourceReconciler` still persists status mutations made before the failure, and controller-runtime requeues with backoff.
- The `ManageOutcome` advice reproduces the operator-utils `ManageError`/`ManageSuccess` condition and event contract (`ReconcileError`/`ReconcileSuccess` with `LastReconcileCycleFailed`/`LastReconcileCycleSucceded` reasons, `ProcessingError` warning events), uniformly for all transient errors.
- `handleJobCompletion` and `setValidationFailureStatus` keep their explicit status writes so status is persisted *before* cleanup: a failed write requeues with the Job intact instead of recreating it.

## Rationale

- **Maintainability**: each lifecycle concern is a small named function composed declaratively; the framework owns the boilerplate (fetch, NotFound handling, status diffing, finalizer patching, event recording).
- **Behavior preservation**: the e2e contract (status fields, conditions, events, finalizer cleanup, metrics, Job template) is unchanged; unit tests keep the same assertions.
- **Dependency reduction**: `redhat-cop/operator-utils` is removed from the module.

## Consequences

### Positive

- The reconciliation flow is visible as a tree; adding a step means appending a `SyncReconciler`.
- Status writes are diffed by the framework, removing redundant API writes on unchanged reconciles.
- The finalizer is maintained with optimistic-lock patches instead of full-object updates.

### Negative

- A framework upgrade (controller-runtime/reconciler.io) now constrains future changes.
- Transient errors record a `ReconcileError` condition on paths that previously requeued silently; distinct error messages produce one extra status write each.
- After an explicit status write in `handleJobCompletion`/`setValidationFailureStatus`, the framework may issue one additional no-op status update.

## References

- `controllers/mustgather/mustgather_controller.go` — `newResourceReconciler` and the sub-reconciler methods
- [docs/design/controller.md](../design/controller.md)
