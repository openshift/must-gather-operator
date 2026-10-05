# Design: MustGather reconciler

**Module**: `controllers/mustgather/mustgather_controller.go`
**Architecture**: [components.md](../../harness-evals/harness-docs/architecture/components.md)

## Preconditions

- The manager scheme includes `operator.openshift.io/v1` and `v1alpha1`, plus `config/v1` and `image/v1` for proxy and ImageStream resolution.
- `DEFAULT_MUST_GATHER_IMAGE` and `OPERATOR_IMAGE` are set before a Job that needs gather or upload is created.
- The ServiceAccount named in the CR exists in the CR namespace.
- When `uploadTarget` is SFTP, the referenced Secret has non-empty `username` and `password` keys and SFTP connectivity has been validated.
- `OPERATOR_SERVICE_ACCOUNT` is available so the controller can reject using its own SA on a CR in the operator namespace.

## Invariants

- There is exactly one reconciler for the full MustGather lifecycle. Job create, status, cleanup, and finalizer handling stay in this loop.
- Secrets are referenced in place via `SecretKeyRef` from the CR namespace. The operator never copies Secrets. Only a trusted CA ConfigMap is replicated into the CR namespace.
- The controller rejects its own ServiceAccount when the CR is in the operator namespace. Gather must not run with the operator's credentials.
- Cleanup is event-driven (Job success/failure in the same reconcile, and CR deletion via the finalizer). There is no GC timer.
- Cleanup is skipped while `retainResourcesOnCompletion` is true. On CR deletion, the finalizer still skips explicit cleanup when retention is enabled (owned objects may then fall to Kubernetes GC).
- Trusted CA ConfigMap copies use ownerReferences. The ConfigMap is deleted only when this CR was the last owner.
- `MetricMustGatherTotal` increments after a successful Job create. `MetricMustGatherErrors` increments when reconcile observes `Job.Status.Failed > backoffLimit`, before `handleJobCompletion`. If completion cleanup then fails, `ManageError` requeues and the error counter can increment again. Neither counter increments for terminal validation failures.
- Terminal validation failures (`setValidationFailureStatus`) set `Completed=true` and return without requeue. The spec cannot change, so retrying is pointless.

## Rationale

- In-place Secret refs avoid a cluster-wide copy of case-management credentials. The Job Pod reads the Secret from the CR namespace at schedule time.
- The operator-SA guard exists because the operator SA is not a gather identity; using it would over-privilege collection and confuse RBAC reviews.
- Cleanup in the completion reconcile (instead of a TTL controller) keeps one-shot semantics: when the Job is done, owned Pods and the Job go away unless the user asked to retain them.
- `ManageError` is not used for every transient Get failure: writing a condition and event on each retry churns the API server. Transient errors requeue; validation failures are terminal.

## Trade-offs

- Secret data can change after the controller validated it and before the Pod starts (`SecretKeyRef` resolves at schedule time). CEL freezes the Secret *name*, not the Secret *data*.
- On cleanup failure during deletion, the reconciler returns an error and keeps the finalizer so Kubernetes retries. Deletion stays blocked until cleanup succeeds. A prolonged API outage delays CR removal; this path does not drop the finalizer and leave owned Jobs or Pods unreferenced.
- `MetricMustGatherErrors` is not gated on first observation. A failed Job whose completion cleanup keeps failing will over-count.
