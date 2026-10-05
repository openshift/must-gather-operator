# Design: gather/upload Job template

**Module**: `controllers/mustgather/template.go`
**ADR**: [adr-0002](../adrs/adr-0002-two-container-job.md)

## Preconditions

- Gather image is `DEFAULT_MUST_GATHER_IMAGE` or an ImageStreamTag resolved from `spec.imageStreamRef`.
- Upload/obfuscate container image is `OPERATOR_IMAGE` whenever that container is added.
- Shared volumes exist before either container runs: `must-gather-output` (emptyDir or PVC) at `/must-gather`, and `must-gather-upload` (emptyDir) when upload or obfuscation is in play.
- The Pod ServiceAccount is the CR's `serviceAccountName` (cluster read for gather). SFTP credentials enter only as env `SecretKeyRef`.

## Invariants

- The gather container is present unless `obfuscate.source` names a PVC (`hasObfuscateSource`). That path obfuscates and/or uploads an existing tree; it does not run must-gather.
- The upload container is added only when SFTP upload is configured (`hasSFTPUpload`) or obfuscation is enabled (`isObfuscateEnabled`).
- The Pod always sets `ShareProcessNamespace: true`. When gather and upload both run, upload `pgrep`s for gather completion. Gather writes `/must-gather`; upload reads it. Obfuscate-source Jobs have no gather process; upload uses `uploadCommandDirect` against the source PVC.
- Job `restartPolicy` is `Never` and `backoffLimit` is `3`. The controller treats `Status.Failed > backoffLimit` as terminal. Changing one without the other breaks failure detection.
- Timeout is enforced in the gather command (`timeout …`) rather than `ActiveDeadlineSeconds`.
- Jobs prefer infra nodes (`node-role.kubernetes.io/infra`) with a matching NoSchedule toleration so collection does not land on application nodes by default.
- There are no resource requests or limits on the Job. Must-gather memory use scales with cluster size.

## Rationale

- Two containers in one Job (instead of an init container or two Jobs) keep a single lifecycle object while using different images: user-selectable must-gather vs operator image for upload.
- Upload starts with gather and polls, rather than using an init container, so both can mount the shared volume for the whole Pod lifetime.
- Skipping gather when `obfuscate.source` is set reuses a previously collected tree (or an operator-supplied PVC) without a second cluster-wide collection.
- Infra affinity is a preference (weight 1), not a hard requirement, so single-node and non-infra clusters still schedule.
- Shell-level timeout was chosen over `ActiveDeadlineSeconds` so gather can map timeout exits (124/137) to a clean handoff for upload.

## Trade-offs

- `pgrep` depends on process names in the gather image. A custom image that does not look like origin-must-gather can stall upload.
- Shared process namespace lets upload see gather processes (and the SA token is Pod-scoped). Isolation is by image and command, not by privilege boundary.
- An upload failure creates a new Pod that reruns gather when gather is in the Job; there is no "upload-only retry" on that path. Obfuscate-source Jobs retry upload/obfuscate only.
