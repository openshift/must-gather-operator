# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

The Must Gather Operator is an OpenShift Kubernetes operator that automates the collection of must-gather information (debugging data) from clusters and uploads it to Red Hat case management systems. It creates a MustGather custom resource that triggers a Kubernetes Job to collect cluster diagnostics.

## Core Architecture

- **API**: Custom Resource Definitions in `api/v1alpha1/` define the MustGather CRD with support for case IDs, authentication, proxy configuration, audit logs, and timeouts
- **Controller**: `controllers/mustgather/` contains the reconciliation logic that creates Jobs based on MustGather CRs
- **Template System**: Job templates in `controllers/mustgather/template.go` generate the actual collection pods
- **Metrics**: Custom metrics collection via `pkg/localmetrics/` for operational insights
- **Configuration**: Operator constants defined in `config/config.go`

## Development Commands

### Build and Test
```bash
# Build the operator binary
make go-build

# Run unit tests
make go-test

# Run linting
make go-check

# Build e2e test binary
make e2e-binary-build

# Generate code (CRDs, deepcopy, etc.)
make generate

# Clean build artifacts
make clean
```

### Docker/Podman Operations
```bash
# Build container image
make docker-build

# Build and push image (requires REGISTRY_USER/REGISTRY_TOKEN)
make docker-push

# Build all configured images
make build-push
```

### Local Development
```bash
# Install dependencies
go mod download

# Run operator locally (requires DEFAULT_MUST_GATHER_IMAGE env var)
export DEFAULT_MUST_GATHER_IMAGE='quay.io/openshift/origin-must-gather:latest'
OPERATOR_NAME=must-gather-operator operator-sdk run --verbose --local --namespace ''
```

### Testing
```bash
# Apply test MustGather CR
oc apply -f ./test/must-gather.yaml

# Run e2e tests (requires cluster access)
DISABLE_JUNIT_REPORT=true KUBECONFIG=/path/to/kubeconfig ginkgo --tags=osde2e -v test/e2e
```

## Key Components

### MustGather CRD Specification
- `caseID`: Red Hat case number for upload destination
- `caseManagementAccountSecretRef`: Authentication credentials
- `serviceAccountRef`: Service account for job execution (defaults to "default")
- `audit`: Boolean flag to collect audit logs (default: false)
- `proxyConfig`: HTTP/HTTPS proxy settings
- `mustGatherTimeout`: Time limit for collection process
- `internalUser`: Flag for Red Hat internal users (default: true)

### Controller Reconciliation
The controller watches MustGather resources and:
1. Creates Kubernetes Jobs using predefined templates
2. Monitors job completion and updates status
3. Handles garbage collection (resources cleaned up ~6 hours after completion)
4. Manages proxy configuration and authentication

### Build System
Uses OpenShift boilerplate with standardized Makefile targets. Key variables in `boilerplate/openshift/golang-osd-operator/project.mk`:
- `OPERATOR_NAME`: must-gather-operator
- `OPERATOR_NAMESPACE`: must-gather-operator
- `IMAGE_REGISTRY`: quay.io
- `IMAGE_REPOSITORY`: app-sre

## Local Testing Setup

1. Deploy CRD: `oc apply -f deploy/crds/managed.openshift.io_mustgathers_crd.yaml`
2. Create namespace: `oc new-project must-gather-operator`
3. Create auth secret: `oc create secret generic case-management-creds --from-literal=username=<username> --from-literal=password=<password>`
4. Deploy manifests: `oc -n must-gather-operator apply -f deploy`

## File Structure Notes

- `main.go`: Entry point with controller-runtime manager setup
- `deploy/`: Kubernetes manifests for operator deployment
- `controllers/mustgather/`: Main controller logic and job templates
- `api/v1alpha1/`: CRD definitions and types
- `pkg/`: Utility packages (k8sutil, localmetrics)
- `test/`: E2E test suite and sample resources
- `boilerplate/`: OpenShift build system integration