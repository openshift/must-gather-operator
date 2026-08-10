# ADR-0002: Two-Container Job Architecture

**Status**: Accepted
**Date**: 2025-08-14
**Component**: Must-Gather Operator

## Context

The operator needs to (1) collect cluster diagnostics using a must-gather image and (2) compress and upload the results via SFTP. These operations require different images, different privilege levels, and sequential execution.

## Decision

Use a single Kubernetes Job with a gather container and a conditional upload container sharing a process namespace:

1. **Gather container** (always present): Runs the must-gather image (default or custom via ImageStream) with cluster read access
2. **Upload container** (added only when `uploadTarget` is configured): Runs the operator image, polls for gather completion via `pgrep`, then compresses and uploads

`ShareProcessNamespace: true` enables cross-container process visibility.

## Rationale

- **Separation of concerns**: Both containers share a single Pod-level ServiceAccount (the user's SA with cluster read access) and `ShareProcessNamespace: true`. Gather uses this SA for cluster diagnostics; upload uses only SFTP credentials (injected via `SecretKeyRef` env vars). The separation is at the image and command level, not at the privilege level — the upload container can see gather processes and has access to the SA token
- **Image flexibility**: Gather image is user-selectable; upload logic is baked into the operator image
- **No init container**: Both containers start simultaneously. Upload polls for gather completion via `pgrep` (5 consecutive checks, 30s apart). This avoids init container limitations with shared volumes
- **Single Job**: Simpler lifecycle tracking than chaining separate Jobs

## Consequences

### Positive
- Custom must-gather images work without bundling upload logic
- Shared volume (`/must-gather`) passes data without external storage
- When no upload target is configured, the Job runs only the gather container

### Negative
- `pgrep` polling is fragile — depends on process naming conventions in the gather image
- `ShareProcessNamespace` exposes all processes between containers (security trade-off)
- Upload container must handle the case where gather times out (exit 124/137 mapped to exit 0)
- With `restartPolicy: Never` and `backoffLimit: 3`, an upload container failure causes the Job to create a new Pod that reruns the gather container (there is no retry logic that skips gathering)

## References

- `controllers/mustgather/template.go` — `initializeJobTemplate`, `getGatherContainer`, `getUploadContainer`
- `build/bin/upload` — compression and SFTP upload script
- [Must-Gather Operator Enhancement](https://github.com/openshift/enhancements/blob/master/enhancements/support-log-gather/must-gather-operator.md)
