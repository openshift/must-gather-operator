This file provides guidance to AI agents when working with code in this repository.

## Overview

The Must Gather Operator is a Kubernetes operator that automates the collection of must-gather diagnostic information on OpenShift clusters and uploads it to Red Hat case management. It is built using the Operator SDK and controller-runtime framework.

## Build Commands

This project uses a boilerplate-based Makefile system. Common commands:

```bash
# Build, test, and lint (default target)
make

# Run tests
make go-test

# Build the operator binary
make go-build

# Build container image
make docker-build

# Push container image
make docker-push

# Build and push
make build-push

# Generate code (CRDs, deepcopy, OpenAPI)
make generate

# Generate manifests
make manifests

# Run linting
make lint

# Run coverage
make coverage
```

### Local Development

To run the operator locally:

1. Install dependencies: `go mod download`
2. Apply the CRD: `oc apply -f deploy/crds/operator.openshift.io_mustgathers_crd.yaml`
3. Create the namespace: `oc new-project must-gather-operator`
4. Set the environment variable: `export DEFAULT_MUST_GATHER_IMAGE='quay.io/openshift/origin-must-gather:latest'`
5. Run with operator-sdk: `OPERATOR_NAME=must-gather-operator operator-sdk run --verbose --local --namespace ''`

Note: The `OPERATOR_IMAGE` environment variable must be set in the deployment or locally for the operator to function. This image is used for the upload container.

### Testing

```bash
# Run unit tests
make go-test

# Apply a test MustGather CR
oc apply -f ./test/must-gather.yaml
```

## Architecture

### Core Components

**API Types** (`api/v1alpha1/mustgather_types.go`):
- `MustGather` CR defines the specification for must-gather collection jobs
- Key fields: `caseID`, `caseManagementAccountSecretRef`, `serviceAccountRef`, `audit`, `proxyConfig`, `mustGatherTimeout`, `internalUser`
- Status tracking with conditions and completion state

**Controller** (`controllers/mustgather/mustgather_controller.go`):
- Main reconciliation loop that manages MustGather lifecycle
- Creates Kubernetes Jobs with two containers: gather and upload
- Handles finalizers for proper cleanup of secrets, jobs, and pods
- Automatic garbage collection ~6 hours after completion
- Uses predicates to filter events (only reconciles on generation or finalizer changes)

**Job Template** (`controllers/mustgather/template.go`):
- Generates Job specs with two containers:
  1. **Gather container**: Runs must-gather collection (with or without audit logs)
  2. **Upload container**: Waits for gather to complete, then compresses and uploads to Red Hat SFTP
- Configures shared volumes, proxy settings, timeouts, and node affinity for infra nodes
- Uses `ShareProcessNamespace` to allow upload container to detect when gather completes

**Upload Script** (`build/bin/upload`):
- Shell script that compresses must-gather output and uploads via SFTP
- Supports proxy configurations (with authentication)
- Creates SSH known_hosts file for SFTP connections
- Handles both internal and external Red Hat users (different upload paths)

### Reconciliation Flow

1. Fetch MustGather instance
2. Initialize defaults (ServiceAccountRef, ProxyConfig from cluster)
3. Handle deletion via finalizer:
   - Delete secret from operator namespace
   - Delete job and associated pods
   - Remove finalizer
4. Create Job if it doesn't exist:
   - Copy case management credentials secret to operator namespace
   - Create Job with gather and upload containers
   - Increment Prometheus metrics
5. Monitor Job status:
   - Requeue for deletion when Job succeeds or fails
   - Update MustGather status based on Job completion

### Key Design Decisions

- **Operator runs in a single namespace** (default: `must-gather-operator`) but watches MustGather CRs cluster-wide
- **Secret replication**: Copies user-provided case management secrets from CR namespace to operator namespace for job access
- **Two-container approach**: Separate containers for gathering and uploading allows gather to run with cluster permissions while upload runs with limited permissions
- **Process namespace sharing**: Enables upload container to detect gather completion via `pgrep`
- **Infra node affinity**: Jobs prefer infra nodes (with tolerations) to avoid impacting application workloads
- **Proxy support**: Inherits cluster proxy config by default, overridable per MustGather CR
- **FIPS mode**: Enabled by default (`FIPS_ENABLED=true` in Makefile)

### Important Files

- `main.go`: Operator entrypoint, sets up controller manager and metrics
- `config/config.go`: Constants for operator name, namespace, and OLM configuration
- `pkg/localmetrics/localmetrics.go`: Prometheus metrics (total must-gathers, errors)
- `pkg/k8sutil/k8sutil.go`: Utility for detecting operator namespace
- `controllers/mustgather/predicates.go`: Event filters for reconciliation

### Environment Variables

Required for operation:
- `DEFAULT_MUST_GATHER_IMAGE`: Image for gather container (e.g., `quay.io/openshift/origin-must-gather:latest`)
- `OPERATOR_IMAGE`: Image for upload container (typically the operator's own image)

Optional:
- `OSDK_FORCE_RUN_MODE=local`: Bypasses leader election for local development
- Proxy variables: `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` (can be overridden per CR)

### API Group Migration

The operator previously used a different API group. The current API group is `operator.openshift.io/v1alpha1`. When working with manifests, ensure you're using the correct group.

### Boilerplate System

This project uses the openshift-eng boilerplate convention system. The actual Makefile includes generated makefiles from `boilerplate/generated-includes.mk`. To update boilerplate, run `make boilerplate-update`.

## Common Development Patterns

### Adding New Fields to MustGather CR

1. Update `api/v1alpha1/mustgather_types.go` with new field and kubebuilder markers
2. Run `make generate` to update generated code
3. Run `make manifests` to update CRD YAML
4. Update controller logic in `controllers/mustgather/mustgather_controller.go`
5. Update job template in `controllers/mustgather/template.go` if needed
6. Add tests in `controllers/mustgather/template_test.go` and `controllers/mustgather/mustgather_controller_test.go`

### Modifying Job Template

When changing the Job specification in `template.go`:
- Remember both containers share volumes (`must-gather-output` and `must-gather-upload`)
- Gather container writes to `/must-gather`, upload container reads from it
- Update `getJobTemplate()`, `getGatherContainer()`, or `getUploadContainer()` as needed
- Test with actual must-gather runs on a cluster

### Working with Finalizers

The operator uses `finalizer.mustgathers.operator.openshift.io` to ensure cleanup. When modifying finalizer logic:
- Ensure proper deletion of secrets in operator namespace
- Clean up job and pods before removing finalizer
- Handle errors gracefully (don't block deletion on transient errors)

### Metrics

The operator exposes Prometheus metrics via the openshift/operator-custom-metrics library:
- `MetricMustGatherTotal`: Incremented when a MustGather job is created
- `MetricMustGatherErrors`: Incremented when a MustGather job fails

Metrics are served on port 8080 at path `/metrics` with ServiceMonitor support.
