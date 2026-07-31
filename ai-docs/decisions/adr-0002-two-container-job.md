# ADR-0002: Two-Container Job Architecture

**Status**: Accepted
**Date**: 2025-08-14
**Component**: Must-Gather Operator

## Context

The operator needs to (1) collect cluster diagnostics using a must-gather image and (2) compress and upload the results via SFTP. These operations require different images, different privilege levels, and sequential execution.

## Decision

Use a single Kubernetes Job with two containers sharing a process namespace:

1. **Gather container**: Runs the must-gather image (default or custom via ImageStream) with cluster read access
2. **Upload container**: Runs the operator image, polls for gather completion via `pgrep`, then compresses and uploads

`ShareProcessNamespace: true` enables cross-container process visibility.

## Rationale

- **Separation of concerns**: Gather runs with the user's ServiceAccount (cluster read); upload runs with limited credentials (SFTP only)
- **Image flexibility**: Gather image is user-selectable; upload logic is baked into the operator image
- **No init container**: Both containers start simultaneously. Upload polls for gather completion via `pgrep` (5 consecutive checks, 30s apart). This avoids init container limitations with shared volumes
- **Single Job**: Simpler lifecycle tracking than chaining separate Jobs

## Consequences

### Positive
- Custom must-gather images work without bundling upload logic
- Upload container failure doesn't require re-running the gather
- Shared volume (`/must-gather`) passes data without external storage

### Negative
- `pgrep` polling is fragile — depends on process naming conventions in the gather image
- `ShareProcessNamespace` exposes all processes between containers (security trade-off)
- Upload container must handle the case where gather times out (exit 124/137 mapped to exit 0)

## References

- `controllers/mustgather/template.go` — `initializeJobTemplate`, `getGatherContainer`, `getUploadContainer`
- `build/bin/upload` — compression and SFTP upload script
- [Must-Gather Operator Enhancement](https://github.com/openshift/enhancements/blob/master/enhancements/support-log-gather/must-gather-operator.md)
