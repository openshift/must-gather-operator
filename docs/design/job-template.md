# Design: gather/upload Job template

**Module**: `controllers/mustgather/template.go`
**ADR**: [adr-0002](../adrs/adr-0002-two-container-job.md)
**Proposed update**: [adr-0004](../adrs/adr-0004-gather-completion-and-upload-gate.md)

## Preconditions

- Gather image is `DEFAULT_MUST_GATHER_IMAGE` or an ImageStreamTag resolved from `spec.imageStreamRef`.
- Upload/obfuscate container image is `OPERATOR_IMAGE` whenever that container is added.
- Shared volumes exist before either container runs: `must-gather-output` (emptyDir or PVC) at `/must-gather`, and `must-gather-upload` (emptyDir) when upload or obfuscation is in play.
- The Pod ServiceAccount is the CR's `serviceAccountName` (cluster read for gather). SFTP credentials enter only as env `SecretKeyRef`.

## Invariants

- The gather container is present unless `obfuscate.source` names a PVC (`hasObfuscateSource`). That path obfuscates and/or uploads an existing tree; it does not run must-gather.
- The upload container is added only when SFTP upload is configured (`hasSFTPUpload`) or obfuscation is enabled (`isObfuscateEnabled`).
- Default gather commands use Bash `pipefail` and `PIPESTATUS[0]` so `tee` cannot hide a gather or timeout failure. Custom commands run through a Bash wrapper that preserves their arguments and exit code. The wrapper clears stale markers at the start of every Job attempt, writes `/must-gather/.gather-success` only for exit code 0, and preserves the gather exit code even when it runs the obfuscation ownership handoff (`chown`). Exit 124/137 remains non-zero.
- Gather hand-off to upload uses two independent signals under `ShareProcessNamespace: true`:
  1. The gather wrapper writes its PID as the **first** statement (`echo $$ > /must-gather/.gather.pid`), overwriting any stale PVC PID. The upload shell polls `/proc/$pid` and honors PID-gone only after it has observed that PID alive at least once (`seen_live=1`), covering hard kills (OOMKilled, SIGKILL) that never run the EXIT trap without false-breaking when the upload container has just joined the shared PID namespace.
  2. The gather script's EXIT trap writes a completion marker (`/must-gather/.gather-complete`) on any graceful exit (success, failure, or `timeout` 124/137), after suffixes such as obfuscation `chown` have finished.

  Either signal unblocks the upload wait loop. There is no wait deadline (gather and upload may run for hours). The upload shell then requires the success marker (`/must-gather/.gather-success`) before invoking `/usr/local/bin/upload`. Obfuscate-source Jobs have no gather container and use `uploadCommandDirect`.
- The Pod always sets `ShareProcessNamespace: true` so the upload container can see `/proc/$pid` of the gather wrapper.
- Job `restartPolicy` is `Never` and `backoffLimit` is `3`. The controller treats `Status.Failed > backoffLimit` as terminal. Changing one without the other breaks failure detection.
- Timeout is enforced in the gather command (`timeout …`) rather than `ActiveDeadlineSeconds`.
- Jobs prefer infra nodes (`node-role.kubernetes.io/infra`) with a matching NoSchedule toleration so collection does not land on application nodes by default.
- There are no resource requests or limits on the Job. Must-gather memory use scales with cluster size.

## Rationale

- Two containers in one Job (instead of an init container or two Jobs) keep a single lifecycle object while using different images: user-selectable must-gather vs operator image for upload.
- Upload starts with gather and polls, rather than using an init container, so both can mount the shared volume for the whole Pod lifetime.
- Skipping gather when `obfuscate.source` is set reuses a previously collected tree (or an operator-supplied PVC) without a second cluster-wide collection.
- Infra affinity is a preference (weight 1), not a hard requirement, so single-node and non-infra clusters still schedule.
- Shell-level timeout was chosen over `ActiveDeadlineSeconds` so the gather exit code and the completion marker can reach the upload gate. Timeout exits 124/137 fail the gather and skip upload.
- The completion marker alone is not sufficient: a hard-killed gather container (OOMKilled, SIGKILL) never runs its EXIT trap, so the marker is never written and a marker-only wait would block forever. The PID + `/proc` check provides a second signal using the existing shared process namespace without needing Kubernetes API calls or additional RBAC.
- Writing the PID as the first wrapper statement shrinks the window where a hard kill leaves upload with neither signal. Requiring `seen_live` before a PID-gone break avoids treating a not-yet-visible `/proc/$pid` at upload startup as gather death. A wait timeout is intentionally not used so long-running gathers and uploads are not cut off.

## Trade-offs

- `ShareProcessNamespace: true` exposes all processes between containers (same trade-off as ADR-0002). Isolation is by image and command, not by process namespace boundary.
- An upload failure creates a new Pod that reruns gather when gather is in the Job; there is no "upload-only retry" on that path. Obfuscate-source Jobs retry upload/obfuscate only.
- If the gather container is killed before the first shell statement runs, `.gather.pid` is never written and upload can wait indefinitely. Closing that residual race without a wait deadline would require a different Job shape (for example gather as an init container).
