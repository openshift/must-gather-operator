# ADR-0001: Immutable MustGather Spec

**Status**: Accepted
**Date**: 2025-08-14
**Component**: Must-Gather Operator

## Context

MustGather CRs represent a one-shot diagnostic collection job. Once the operator creates a Kubernetes Job from the CR spec, changing the spec would create a mismatch between the running Job and the declared intent. While Kubernetes 1.33 permits updates to selected scheduling fields on suspended Jobs, the gather execution inputs (image, command, arguments, timeout, upload target) cannot be changed safely in place because the Job is already running with those parameters.

## Decision

Enforce spec immutability via CEL validation rule on the MustGather type:

```
+kubebuilder:validation:XValidation:rule="!has(oldSelf.spec) || self.spec == oldSelf.spec"
```

Users must delete and recreate the CR to change parameters.

## Rationale

- Gather execution inputs cannot be changed safely on a running Job — allowing spec changes would be misleading
- One-shot semantics: each CR represents one gather operation, not a desired-state loop
- CEL enforcement at the API level prevents confusion before the controller even sees the update
- Simpler controller logic: no need to diff old/new spec or handle Job replacement

## Consequences

### Positive
- No ambiguity about what parameters a running gather used
- Controller never needs to handle spec drift

### Negative
- Users must delete + recreate for any parameter correction (even typos)
- Cannot add "retry with different timeout" workflow on existing CR

## References

- `api/v1alpha1/mustgather_types.go:231` — CEL rule
- [Must-Gather Operator Enhancement](https://github.com/openshift/enhancements/blob/master/enhancements/support-log-gather/must-gather-operator.md)
