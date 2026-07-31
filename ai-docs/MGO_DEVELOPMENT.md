# Must-Gather Operator — Development Guide

> **Generic Development Practices**: See [Platform Development Practices](https://github.com/openshift/enhancements/tree/master/ai-docs/) for Go standards, controller-runtime patterns, and CI/CD workflows.

This guide covers **must-gather-operator-specific** development practices.

## Quick Start

### Prerequisites

- Go 1.25.7 (see `go.mod`)
- Access to OpenShift cluster with `KUBECONFIG` set
- Container build tool (Podman or Docker)
- `DEFAULT_MUST_GATHER_IMAGE` and `OPERATOR_IMAGE` env vars set

### Build & Run

```bash
# Full build (lint + test + compile, FIPS-enabled)
make

# Build binary only (outputs to build/_output/bin/must-gather-operator)
make go-build

# Run unit tests (uses setup-envtest)
make go-test

# Run E2E tests (requires cluster)
make test-e2e

# Lint
make lint

# Generate CRD, deepcopy, OpenAPI
make generate

# Generate CRD YAML manifests
make manifests

# Verify generated files are up to date
make generate-check

# Build container image
make docker-build

# Build + push (app-sre pipeline)
make build-push
```

### Local Development

```bash
go mod download
oc apply -f deploy/crds/operator.openshift.io_mustgathers.yaml
oc new-project must-gather-operator
export DEFAULT_MUST_GATHER_IMAGE='quay.io/openshift/origin-must-gather:latest'
export OPERATOR_IMAGE='<your-operator-image>'
OPERATOR_NAME=must-gather-operator operator-sdk run --verbose --local --namespace ''
```

**Note**: FIPS build (`FIPS_ENABLED=true`) requires BoringCrypto and generally only works inside the provided Dockerfile. For local development, you may need to build without FIPS.

## Common Tasks

### Add a New Field to MustGather CR

1. Add field + kubebuilder markers to `api/v1alpha1/mustgather_types.go`
2. Add CEL validation rules if needed (XValidation markers)
3. Run `make generate` (deepcopy + OpenAPI)
4. Run `make manifests` (CRD YAML)
5. Update `controllers/mustgather/template.go` if the field affects Job generation
6. Add unit tests in `controllers/mustgather/template_test.go`
7. Add controller tests in `controllers/mustgather/mustgather_controller_test.go`
8. Add E2E test case in `test/e2e/must_gather_operator_test.go`
9. Add example CR in `examples/`

### Add a New Upload Target Type

1. Add new `UploadType` enum value in `api/v1alpha1/mustgather_types.go`
2. Add new `*NewTypeSpec` struct and union member field on `UploadTargetSpec`
3. Update CEL validation on `UploadTargetSpec` to handle the new discriminator value
4. Run `make generate` and `make manifests`
5. Update `controllers/mustgather/template.go`:
   - Modify `getJobFromInstance()` to conditionally add the upload container for the new type
   - Create a new upload container builder or modify `getUploadContainer()`
6. Add/update the upload script in `build/bin/` if needed
7. Update RBAC if the new target requires additional permissions

### Modify Job Template

Key files: `controllers/mustgather/template.go`

- `initializeJobTemplate()` — Job-level config (volumes, affinity, tolerations, SA)
- `getGatherContainer()` — Gather container (image, command, mounts, env)
- `getUploadContainer()` — Upload container (image, polling command, mounts, env)

Remember:
- Both containers share volumes: `must-gather-output` and `must-gather-upload`
- Gather writes to `/must-gather`, upload reads from it
- Upload container is **only added** when `UploadTarget` is configured
- `ShareProcessNamespace: true` is required for upload's `pgrep`-based polling

### Modify Upload Script

File: `build/bin/upload`

The upload script runs inside the upload container (uses `OPERATOR_IMAGE`). Changes require rebuilding the operator image. Test with actual SFTP connections — there is no mock SFTP in CI.

### Update Boilerplate

```bash
make boilerplate-update
```

This pulls from the `openshift/golang-osd-operator` convention. Includes Makefile targets, CI config, OLM tooling.

## Environment Variables Reference

### Required for Operator

| Variable | Source | Purpose |
|---|---|---|
| `DEFAULT_MUST_GATHER_IMAGE` | Deployment env | Gather container image |
| `OPERATOR_IMAGE` | Deployment env | Upload container image |
| `OPERATOR_SERVICE_ACCOUNT` | Downward API | Self-rejection guard |

### Optional

| Variable | Purpose |
|---|---|
| `OSDK_FORCE_RUN_MODE=local` | Skip leader election for local dev |
| `OPERATOR_NAMESPACE` | Override namespace detection |
| `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` | Passed to upload container |

## Common Mistakes

1. **Forgetting `make generate` after type changes** — deepcopy and OpenAPI won't match, causing runtime panics or CRD drift
2. **Editing generated files directly** — `zz_generated.deepcopy.go`, `zz_generated.openapi.go`, and CRD YAML are overwritten by `make generate`/`make manifests`
3. **Assuming secrets are replicated** — they are NOT. Secrets are referenced directly via SecretKeyRef from the CR's namespace. Only trusted CA ConfigMaps are copied to the CR namespace
4. **Using the operator's SA in the CR** — the controller rejects this when the CR is in the operator namespace (guard at `mustgather_controller.go:158-168`)
5. **Building FIPS locally** — `FIPS_ENABLED=true` requires BoringCrypto toolchain, generally only available in the CI Dockerfile

## SME Review Recommended

- Detailed guide for adding a complete new upload target type (end-to-end wiring)
- CI/CD pipeline specifics beyond what boilerplate provides
- OLM bundle update checklist for version bumps

## See Also

- [Testing Guide](./MGO_TESTING.md)
- [Architecture](./architecture/components.md)
- [Platform Development Practices](https://github.com/openshift/enhancements/tree/master/ai-docs/)
