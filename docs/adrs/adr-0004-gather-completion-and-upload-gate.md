# ADR-0004: Gather completion and upload gate

**Status**: Proposed
**Date**: 2026-10-13
**Component**: Must-Gather Operator
**Related decision**: [ADR-0002](adr-0002-two-container-job.md)

## Context

The upload container must wait for a gather container that can run a default or custom command. The original `pgrep`-based check depends on process naming and can miss custom commands. It also permits upload after a failed gather. A hard-killed gather (OOMKilled, SIGKILL) cannot run a shell EXIT trap, so a completion marker alone would leave the upload container waiting forever.

## Decision

Keep `ShareProcessNamespace: true` from ADR-0002. Wrap default and custom gather commands to preserve their exit codes. The wrapper writes `echo $$ > /must-gather/.gather.pid` as its **first** statement (overwriting any stale PVC PID), then installs an EXIT trap that writes `/must-gather/.gather-complete` on any graceful exit, then clears stale success/completion markers. Only exit code 0 writes `/must-gather/.gather-success`. Default commands capture the exit code with `PIPESTATUS[0]` despite piping through `tee`; timeout exits 124/137 remain failures.

The upload shell polls for two signals (no wait deadline — gather and upload may run for hours):
1. The PID file exists, `/proc/$pid` was observed alive at least once (`seen_live`), and then `/proc/$pid` is gone (covers OOMKilled/SIGKILL that skip the EXIT trap; avoids a false break when the upload container has just joined the shared PID namespace)
2. The completion marker (graceful exit via the EXIT trap, after wrapper suffixes such as obfuscation `chown`)

After either signal, upload requires the success marker before running obfuscation or SFTP upload. Obfuscate-source Jobs have no gather container and run `uploadCommandDirect`.

## Rationale

- Exit-code propagation lets Job failure and the MustGather status reflect gather and timeout failures.
- Separate completion and success signals prevent a failed gather from being uploaded.
- PID + `/proc` check reuses the existing shared process namespace to detect hard kills without depending on process names or Kubernetes API calls.
- No new binary entrypoint, RBAC, or Downward API — only shell constructs in the template constants.

## Consequences

### Positive

- Custom gather commands no longer need a process name containing `gather`.
- Failed or timed-out gathers skip upload, and the controller can report the gather exit code.
- No additional RBAC needed; the CR's ServiceAccount does not need `get` Pods.

### Negative

- Retains `ShareProcessNamespace: true` — cross-container process visibility, same trade-off as ADR-0002.
- An upload failure still retries the whole Pod and reruns gather.
- If gather is killed before the first wrapper statement, `.gather.pid` is never written and upload can wait indefinitely. A hard kill that finishes before upload ever observes a live PID is similarly not detected by the `seen_live` guard (completion marker is also absent). A wait timeout is rejected so long-running gathers/uploads are not cut off; closing those residual races would require a different Job shape.

## Alternatives Considered

- **Retain `pgrep`**: depends on process naming and cannot distinguish gather success from failure.
- **Use only a completion marker**: hard kills cannot write it, leaving upload waiting forever.
- **Success marker alone for wait**: gather failure never writes success, so upload would hang without a separate "done" signal (completion marker or PID gone).
- **Go-based `wait-for-gather` helper polling Pod API status**: correct, but adds RBAC (`get` Pods), Downward API env vars, a new subcommand, and ~440 lines. `/proc` under a shared PID namespace solves the same problem with shell only.
- **Make gather an init container**: eliminates waiting but changes the Job lifecycle shape vs ADR-0002.
- **Use a fixed wait deadline** (including a short bootstrap timeout while the PID file is absent): gather and upload can run for hours, so any deadline on the wait path is either too short or risks cutting off legitimate work.

## References

- [Job template design](../design/job-template.md)
- [Controller design](../design/controller.md)
- `controllers/mustgather/constant.go`, `controllers/mustgather/template.go`
