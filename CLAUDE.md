@AGENTS.md

This file provides Claude Code-specific behavioral instructions for working with code in this repository.

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

## Claude Code Behavioral Preferences

- When modifying API types in `api/v1alpha1/mustgather_types.go`, always run `make generate` and `make manifests` before committing.
- Never edit files under `boilerplate/` directly. Use `make boilerplate-update` to pull updates.
- For controller tests, use the existing `interceptClient` pattern instead of building custom mocks.
- Always tag E2E test files with `//go:build e2e` at the top.
- Run `make` (build + test + lint) before committing changes.
