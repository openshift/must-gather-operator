# Design: reconcile predicates

**Module**: `controllers/mustgather/predicates.go`

## Preconditions

- Every `Owns()` / `Watches()` registration attaches a predicate via `builder.WithPredicates(...)`.
- Predicate `UpdateFunc` implementations compare the field that should wake the reconciler, not the entire object.

## Invariants

- MustGather updates reconcile only when `.metadata.generation` or finalizers change (`resourceGenerationOrFinalizerChangedPredicate`). Status-only patches do not requeue.
- Owned Job events reconcile only on Update when `Job.Status` changes (`isStateUpdated`). Create, Delete, and Generic Job events are ignored; the primary CR watch covers create/delete intent.
- Owned trusted-CA ConfigMap events are filtered to the configured ConfigMap name (`isNameEquals`). Other ConfigMaps in the namespace do not reconcile MustGather.
- Predicates use `reflect.DeepEqual` on the relevant sub-object (Job status, finalizer slice).

## Rationale

- MustGather spec is immutable, so generation changes are rare (create, and metadata that bumps generation). Filtering status updates prevents a reconcile storm from condition writes.
- Job Create is suppressed because the reconciler created the Job and does not need an extra pass until status moves (Active / Succeeded / Failed).
- Name-equals on the CA ConfigMap avoids watching every ConfigMap in a busy namespace.

## Trade-offs

- Ignoring Job Delete means a Job removed out-of-band is noticed only on the next MustGather or ConfigMap event, or not at all until the CR is touched. Retention and finalizer paths are the supported delete story.
- DeepEqual on Job status can still fire on noisy status subfields; that is preferred over missing a Succeeded/Failed transition.
