# Security Guidelines

## Credential Handling

### SFTP Credentials (Case Management Secret)

The operator reads credentials from a Kubernetes Secret referenced by `caseManagementAccountSecretRef.name` in `spec.uploadTarget.sftp`. Required keys are `username` and `password`. Both must exist and be non-empty; the controller validates this before creating any Job.

- Credentials are injected into the upload container via `SecretKeyRef` env vars, never hardcoded or logged.
- The Secret must exist in the same namespace as the MustGather CR. The Job references it directly via `SecretKeyRef` (no cross-namespace copy occurs).
- The upload script passes the password through `SSHPASS` environment variable with `sshpass -e`, avoiding command-line exposure.
- When adding new credential fields, follow the existing pattern in `getUploadContainer()` in `template.go`: use `corev1.EnvVarSource.SecretKeyRef` referencing the secret, never embed raw values.

### Pre-flight SFTP Validation

Before creating a Job, the controller validates SFTP credentials via `validateSFTPWithRetry()`. This establishes a real SSH/SFTP connection to verify authentication works.

- Transient errors (timeouts, context cancellation) are retried up to `MaxSFTPValidationRetries` (3). Non-transient errors (auth failure) fail immediately.
- The `#nosec G106` annotation in `validation.go` is intentional: `InsecureIgnoreHostKey` is used to match the upload script's `StrictHostKeyChecking=no`. Do not add host key verification to one without the other.
- All network I/O functions (`sftpDialFunc`, `netDialFunc`, `sshNewClientConnFunc`, `sftpNewClientFunc`) are package-level vars that tests override. When adding new network-touching validation, follow this pattern to keep tests deterministic.

### Proxy Credential Handling

Proxy authentication in `proxyDialContext()` sends `Proxy-Authorization: Basic` headers using credentials from the proxy URL's userinfo. The credentials are base64-encoded but not encrypted. This matches the upload script's `--proxy-auth` approach for HTTP proxies.

## ServiceAccount Restrictions

### Operator SA Rejection

The controller rejects MustGather CRs that specify the operator's own service account in the operator's namespace. This prevents privilege escalation where a must-gather Job could inherit the operator's RBAC permissions.

```go
if instance.Namespace == r.OperatorNamespace && saName == r.OperatorServiceAccountName {
    // rejected with ValidationServiceAccount failure status
}
```

The operator SA name is discovered at startup via the `OPERATOR_SERVICE_ACCOUNT` env var (set from `spec.serviceAccountName` in the Deployment via downward API). When modifying SA validation logic, ensure this check runs on every reconcile, not just on Job creation, so pre-existing CRs are also caught.

### SA Existence Validation

Before creating a Job, the controller verifies the referenced ServiceAccount exists in the CR's namespace. A missing SA results in a `ValidationServiceAccount` failure status, not a requeue.

### must-gather-admin ClusterRole

The `must-gather-admin` ClusterRole in `deploy/05_must-gather-admin.ClusterRole.yaml` grants full cluster access (`*` on all resources). This is bound to the `must-gather-admin` ServiceAccount used by gather Jobs. Avoid binding this role to the operator's own SA or to user-facing accounts.

## RBAC Patterns

### Kubebuilder RBAC Markers

All RBAC permissions are declared via `//+kubebuilder:rbac` markers in `mustgather_controller.go`. When adding new resource access, add the marker there and run `make manifests` to regenerate. The markers map to the ClusterRole in `deploy/02_must-gather-operator.ClusterRole.yaml`.

Key permissions:
- Secrets: `get;list;watch;create;update;patch;delete` (kubebuilder marker includes update/patch, but the generated ClusterRole omits update - secrets are created and deleted, not modified in practice)
- ServiceAccounts: `get;list;watch` only (read access for pre-flight validation)
- Jobs: full CRUD (operator creates and deletes gather Jobs)
- ConfigMaps: `*` (all verbs, for trusted CA certificate propagation and leader election)

### Least Privilege Conventions

- The operator ClusterRole grants full access to ConfigMaps (including update) for trusted CA propagation and leader election functionality.
- Proxy config access is scoped to the `cluster` resource name only: `resourceNames: ["cluster"]`.

## CRD Validation Rules

### Immutability

The MustGather spec is immutable after creation via:
```
+kubebuilder:validation:XValidation:rule="!has(oldSelf.spec) || self.spec == oldSelf.spec"
```
This prevents post-creation tampering with credentials references, images, or commands.

### CEL Validation Rules on SFTPSpec

- `caseID` must not be empty: `rule="size(self.caseID) > 0"`
- `caseManagementAccountSecretRef.name` must not be empty: `rule="size(self.caseManagementAccountSecretRef.name) > 0"`

### Input Size Limits

- `gatherSpec.command` and `gatherSpec.args`: max 256 items, each max 256 chars. These limit injection surface for container commands.
- `persistentVolume.claim.name`: max 253 chars with DNS subdomain validation via CEL.

### Discriminated Union Validation

`UploadTargetSpec` uses `+union` / `+unionDiscriminator` with a CEL rule ensuring `sftp` config is present if and only if `type == "SFTP"`. Follow this pattern when adding new upload target types.

## Trusted CA Certificate Handling

### ConfigMap Propagation

When `--trusted-ca-configmap` is set, the controller copies the named ConfigMap from the operator namespace to the CR's namespace. The ConfigMap is mounted read-only at `/etc/pki/tls/certs` in both the gather and upload containers.

Key conventions:
- The source ConfigMap should have label `config.openshift.io/inject-trusted-cabundle: "true"` for OpenShift CA injection.
- Owner references track which MustGather CRs use the copied ConfigMap. Cleanup removes only the requesting instance's owner reference; the ConfigMap is deleted only when no owners remain.
- If the CR is in the same namespace as the operator, no copy is made (the source ConfigMap is used directly by the Job).

### Volume Mount Pattern

```go
trustedCAVolumeName  = "trusted-ca"
trustedCAMountPath   = "/etc/pki/tls/certs"
// Always mounted ReadOnly: true
```

When adding new certificate or CA-related volumes, mount them read-only and follow the same ConfigMap-copy-with-owner-reference pattern.

## Upload Script Security

The upload script (`build/bin/upload`) has these security-relevant behaviors:

- `StrictHostKeyChecking=no` with a dedicated `UserKnownHostsFile` at `/tmp/must-gather-operator/.ssh/known_hosts`. The SSH directory and known_hosts file are created by the upload container via `mkdir -p` and `touch` commands with restricted permissions (`chmod 700` for the directory, `chmod 600` for known_hosts) in `template.go`.
- Password is passed via `SSHPASS` env var with `sshpass -e`, never on the command line.
- Proxy credentials may appear in `ProxyCommand` arguments for HTTP proxies (via `--proxy-auth`). For HTTPS proxies, credentials are passed via exported env vars to `https-proxy-connect-util`.
- Internal users upload to a different SFTP path (`username/caseID_filename`). This path construction is not sanitized beyond the SFTP server's own controls.

## FIPS Mode

FIPS is enabled by default via build tag `fips_enabled` (set in the Makefile). The `fips.go` file imports `crypto/tls/fipsonly` which restricts TLS to FIPS-approved algorithms. Do not add crypto imports that bypass FIPS compliance (e.g., direct use of `crypto/rc4`, `crypto/des`).

## Validation Status Pattern

All security validation failures use `setValidationFailureStatus()` which:
1. Sets status to `Failed` with `Completed: true`
2. Records a `ReconcileError` condition with reason `ValidationFailed`
3. Emits a Warning event with `ProcessingError` reason
4. Does NOT requeue (terminal failure)

Use existing validation type constants from `constant.go` (`ValidationServiceAccount`, `ValidationSFTPCredentials`, `ValidationImageStream`, `ProtocolSFTP`). Add new constants there when introducing new validation categories.

## Cleanup and Finalizer Security

The operator uses finalizer `finalizer.mustgathers.operator.openshift.io` to ensure resource cleanup:
- Before deleting a Job during cleanup, the controller verifies ownership via `OwnerReferences` UID match. This prevents accidental deletion of unrelated Jobs with the same name.
- Jobs, Pods, and trusted CA ConfigMaps are cleaned up on MustGather deletion (unless `retainResourcesOnCompletion` is true).
- Finalizer removal only happens after successful cleanup, preventing orphaned privileged resources.

## Process Namespace Sharing

Jobs use `ShareProcessNamespace: true` so the upload container can detect gather completion via `pgrep`. This means all containers in the pod can see each other's processes. Do not add containers to the Job pod that handle sensitive data in process arguments.

## Testing Security Validations

- E2E test fixtures for invalid/empty credentials are in `test/e2e/testdata/case-management-secret-*.yaml`.
- Unit tests for SFTP validation use function-var overrides (not real network connections). Follow this pattern: save the original function, override, and defer restoration.
- The `interceptClient` pattern in controller tests allows injecting failures for specific CRUD operations to test error handling paths.
