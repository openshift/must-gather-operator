# Enhancement Proposals & Design Documents

Catalog of design documentation for the must-gather-operator. Enhancement proposals are the source of truth — this file is an index only.

## openshift/enhancements Proposals

All proposals live under `enhancements/support-log-gather/` unless noted.

| Proposal | Status | Tracking | Summary |
|----------|--------|----------|---------|
| [Must-Gather Operator (Core)](https://github.com/openshift/enhancements/blob/master/enhancements/support-log-gather/must-gather-operator.md) | implementable | MG-5, OCPSTRAT-2259 | Core operator design: MustGather CR, two-container Job (gather+upload), SFTP upload, proxy support, trusted CA ConfigMap replication, event-driven cleanup |
| [Extensible Upload Targets](https://github.com/openshift/enhancements/blob/master/enhancements/support-log-gather/operator-upload-targets.md) | implementable | MG-53 | Refactors upload config into discriminated union (`spec.uploadTarget`) with `UploadType` enum. Breaking change from community API |
| [PVC Destination](https://github.com/openshift/enhancements/blob/master/enhancements/support-log-gather/must-gather-operator-pvc-destination.md) | (not set) | MG-68 | Adds `spec.storage` for PVC-backed gather output instead of ephemeral emptyDir. Addresses pod eviction data loss and storage limits |
| [Time-Based Log Filtering](https://github.com/openshift/enhancements/blob/master/enhancements/support-log-gather/must-gather-operator-time-filter.md) | implementable | MG-165 | Adds `gatherSpec.since` and `gatherSpec.sinceTime` for log time filtering. Passed via env vars to upstream must-gather toolchain |
| [Custom Must-Gather Images](https://github.com/openshift/enhancements/blob/master/enhancements/support-log-gather/must-gather-custom-images.md) | (not set) | MG-155 | ImageStream-based allowlist for custom gather images. Enables `spec.imageStreamRef` with `gatherSpec.command`/`args` |
| [Bundle Obfuscation](https://github.com/openshift/enhancements/blob/master/enhancements/support-log-gather/must-gather-bundle-obfuscation.md) | (not set) | MG-293 | Integrates `must-gather-clean` for automatic data obfuscation before upload. Three modes: gather+obfuscate+upload, obfuscate-only, obfuscate+upload |

### Related: CLI Foundation

| Proposal | Status | Summary |
|----------|--------|---------|
| [oc adm must-gather](https://github.com/openshift/enhancements/blob/master/enhancements/oc/must-gather.md) | implemented | Foundational CLI command defining must-gather image contract (`/usr/bin/gather` entrypoint, `/must-gather` output dir). The operator builds on this |

## Local Design Documents

Repository design documentation is organized as:

- `harness-docs/domain/` — MustGather CRD: fields, validation, lifecycle
- `harness-docs/architecture/` — Repo layout, reconciliation flow, Job template, upload
- `harness-docs/decisions/` — Architecture Decision Records (ADR-0001 through ADR-0003)
- `CLAUDE.md` — AI-oriented architecture reference
- `README.md` — Operator purpose, CR format, deployment instructions

## Feature Status Mapping

| Feature | Enhancement | Code Status |
|---------|-------------|-------------|
| Core gather + SFTP upload | MG-5 | Implemented |
| Extensible upload targets (union API) | MG-53 | Implemented |
| PVC storage | MG-68 | Implemented |
| Time-based filtering (since/sinceTime) | MG-165 | Implemented |
| Custom images via ImageStream | MG-155 | Implemented |
| Bundle obfuscation | MG-293 | In development |
