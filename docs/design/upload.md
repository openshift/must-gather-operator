# Design: SFTP validation and upload

**Modules**: `controllers/mustgather/validation.go`, `build/bin/upload`, `build/bin/https-proxy-connect-util`

## Preconditions

- Upload runs only when the Job template added the upload container (`uploadTarget` or obfuscation).
- SFTP Secret keys `username` and `password` are non-empty. The controller tests connectivity before creating the Job.
- For gather+upload Jobs, gather has finished (upload polls with `pgrep`) and `/must-gather` contains the tree to compress. Obfuscate-source Jobs skip gather; upload reads the source PVC with `uploadCommandDirect`.
- Proxy env vars, if any, are the operator's `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY`, forwarded as lowercase `http_proxy` / `https_proxy` / `no_proxy` on the container.

## Invariants

- Credentials never appear on process command lines. Validation uses the SSH library; the script uses `sshpass -e` and `SSHPASS`.
- Validation error paths close the TCP connection or proxy response body and ignore `Close` errors so they do not mask the primary dial or CONNECT failure.
- Host key checking is disabled on both paths (`InsecureIgnoreHostKey` / `StrictHostKeyChecking=no`, `#nosec G106`). The configured target defaults to `sftp.access.redhat.com`.
- Validation retries at most `MaxSFTPValidationRetries` (3) and only on transient errors (deadline, cancel, net timeout). Auth failure, connection refused, and DNS errors fail immediately.
- `classifySFTPError` maps dial/handshake failures to fixed user-facing messages. Detailed errors stay in operator logs, not CR status.
- HTTP proxy: Go validation opens a CONNECT tunnel on raw TCP (default ports 3128/3129). The `https` proxy scheme only changes the default port; CONNECT itself is not TLS. The upload script uses `nc --proxy` for HTTP and `socat` plus `https-proxy-connect-util` for HTTPS.
- Internal Red Hat users prefix the remote path with `${username}/`; external users do not.
- Package-level function variables (`sftpDialFunc`, `netDialFunc`, …) are the test seams. Production code must go through them so unit tests can intercept I/O.

## Rationale

- Pre-flight SFTP checks were chosen instead of failing only after a long gather: bad credentials or an unreachable host should fail the CR before cluster-wide collection starts.
- Skipping host-key verification matches the managed endpoint's key rotation. Compensating controls: encrypted SSH, CEL-frozen host/secret *names*, and credential checks before Job create. Restore verification if Red Hat publishes a stable host key.
- Sanitized status errors avoid leaking proxy URLs, usernames, or dial details into `oc get mustgather -o yaml`.
- Dual proxy implementations (Go for validation, shell for upload) exist because gather+upload run in different images; both must honor the same env vars and basic auth.

## Trade-offs

- `StrictHostKeyChecking=no` is an accepted MITM residual on the SFTP control connection, documented rather than treated as an accident.
- No backoff sleep between validation retries: the 5s SSH dial timeout is the pacing. Persistent timeouts still cost ~15s before the CR is marked failed.
