# Must Gather Operator

The Must Gather operator helps collecting must-gather information on a cluster and uploading it to a case.
To use the operator, a cluster administrator can create the following MustGather CR:

```yaml
apiVersion: operator.openshift.io/v1
kind: MustGather
metadata:
  name: example-mustgather-basic
spec:
  serviceAccountName: must-gather-admin
  uploadTarget:
    type: SFTP
    sftp:
      caseID: '02527285'
      caseManagementAccountSecretRef:
        name: case-management-creds
```

This request will collect the standard must-gather info and upload it to case `#02527285` using the credentials found in the `caseManagementCreds` secret.

## Collecting Audit logs
The field `audit` is **false** by default unless explicitly set to **true**.
This will generate the default collection of audit logs as per [the collection script: gather_audit_logs](https://github.com/openshift/must-gather/blob/master/collection-scripts/gather_audit_logs)
```yaml
apiVersion: operator.openshift.io/v1
kind: MustGather
metadata:
  name: example-mustgather-full
spec:
  serviceAccountName: must-gather-admin
  gatherSpec:
    audit: true
  uploadTarget:
    type: SFTP
    sftp:
      caseID: '02527285'
      caseManagementAccountSecretRef:
        name: case-management-creds
```

## Upgrading from Tech Preview

Starting with release 5.0, the must-gather-operator is Generally Available (GA). The OLM channel has changed from `tech-preview` to `stable` and the API version has been promoted from `operator.openshift.io/v1alpha1` to `operator.openshift.io/v1`.

If you previously installed the operator via the `tech-preview` channel, update your Subscription to use the `stable` channel:

```shell
oc patch subscription support-log-gather-operator -n must-gather-operator \
  --type merge -p '{"spec":{"channel":"stable"}}'
```

Existing `v1alpha1` MustGather CRs continue to work — the CRD serves both versions — but `v1alpha1` is deprecated and will be removed in a future release. New CRs should use `operator.openshift.io/v1`.

> **Note:** This operator may still be unsupported. For development, consider using the `stable` channel as described above. For support, consult the appropriate Red Hat documentation and the [OpenShift Operator Life Cycles](https://access.redhat.com/support/policy/updates/openshift_operators) policy.

## Resource Cleanup

When a MustGather Job succeeds or fails, the controller runs cleanup in the same reconciliation pass: it deletes owned Pods and Jobs, and removes the CR's ownerReference from any configured trusted CA ConfigMap (deleting the ConfigMap only if no other owners remain). This behavior applies when `retainResourcesOnCompletion` is unset or `false`.

On CR deletion, the finalizer performs this same cleanup only when retention is disabled. Setting `retainResourcesOnCompletion: true` skips explicit cleanup while the `MustGather` CR exists; deleting the CR can still trigger Kubernetes garbage collection of owned resources.

## Tech Stack

| Component | Version / Detail |
|---|---|
| Language | Go 1.25.7 |
| Framework | [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) v0.21.0 |
| Kubernetes client | client-go v0.33.3 |
| Testing | [Ginkgo](https://github.com/onsi/ginkgo) v2 / [Gomega](https://github.com/onsi/gomega) v1.36 |
| Metrics | [Prometheus client_golang](https://github.com/prometheus/client_golang) v1.22, [operator-custom-metrics](https://github.com/openshift/operator-custom-metrics) |
| SFTP | [pkg/sftp](https://github.com/pkg/sftp) v1.13 |
| Build system | Makefile with [openshift-eng boilerplate](boilerplate/) |
| FIPS | Enabled by default (BoringCrypto) |

## Project Structure

```text
must-gather-operator/
├── api/v1alpha1/          # MustGather CRD types and generated code
├── controllers/mustgather/ # Reconciler, Job template, predicates
├── build/bin/             # Upload shell script (compress + SFTP)
├── config/                # Operator constants, metadata, templates
├── deploy/                # Deployment manifests and CRD YAML
│   └── crds/              # CRD definition
├── pkg/
│   ├── k8sutil/           # Namespace detection utility
│   ├── localmetrics/      # Prometheus metrics definitions
│   └── mustgatherutil/    # Must-gather helper utilities
├── test/
│   ├── e2e/               # End-to-end tests (Ginkgo, -tags e2e)
│   └── library/           # Test helper library
├── bundle/                # OLM bundle manifests
├── hack/                  # Release and OLM registry tooling
├── examples/              # Example CRs and supporting resources
├── scripts/               # Build scripts
├── boilerplate/           # openshift-eng boilerplate convention system
├── main.go                # Operator entrypoint
└── Makefile               # Build targets (delegates to boilerplate)
```

## Building and Testing

This project uses the openshift-eng boilerplate Makefile system.

```shell
# Build, run tests, and lint (default target)
make

# Run unit tests only
make go-test

# Build the operator binary
make go-build

# Run end-to-end tests (requires a running cluster)
make test-e2e

# Generate deepcopy, OpenAPI, and CRD code
make generate

# Generate CRD and RBAC manifests
make manifests

# Run linting
make lint

# Build container image
make docker-build

# Push container image
make docker-push

# Build and push in one step
make build-push

# Update boilerplate
make boilerplate-update
```

## Deploying the Operator

This is a cluster-level operator that you can deploy in any namespace; `must-gather-operator` is recommended.

### Deploying directly with manifests

Here are the instructions to install the latest release creating the manifest directly in OCP.

```shell
git clone git@github.com:openshift/must-gather-operator.git; cd must-gather-operator
oc apply -f deploy/crds/operator.openshift.io_mustgathers.yaml
oc new-project must-gather-operator
oc -n must-gather-operator apply -f deploy
```

### Meeting the operator requirements

In order to run, the operator needs a secret to be created by the admin as follows (this assumes the operator is running in the `must-gather-operator` namespace).

```shell
oc create secret generic case-management-creds --from-literal=username=<username> --from-literal=password=<password>
```

## Local Development

Execute the following steps to develop the functionality locally. It is recommended that development be done using a cluster with `cluster-admin` permissions.

In the operator's `Deployment.yaml` [file](deploy/99_must-gather-operator.Deployment.yaml), add a variable to the deployment's `spec.template.spec.containers.env` list called `OPERATOR_IMAGE` and set the value to your local copy of the image:
```shell
          env:
            - name: OPERATOR_IMAGE
              value: "registry.example/repo/image:latest"
```
Then run:
```shell
go mod download
```

Using the [operator-sdk](https://github.com/operator-framework/operator-sdk), run the operator locally:

```shell
oc apply -f deploy/crds/operator.openshift.io_mustgathers.yaml
oc new-project must-gather-operator
export DEFAULT_MUST_GATHER_IMAGE='quay.io/openshift/origin-must-gather:latest'
OPERATOR_NAME=must-gather-operator operator-sdk run --verbose --local --namespace ''
```

## Further Documentation

- [AGENTS.md](AGENTS.md) -- Component overview, architecture, reconciliation flow, and AI agent guidance
- [harness-evals/harness-docs/](harness-evals/harness-docs/) -- Domain model, architectural decisions (ADRs), development and testing guides

### Development Guidelines

- [Development Guide](harness-evals/harness-docs/MGO_DEVELOPMENT.md) — Build, common tasks, env vars, common mistakes
- [Testing Guide](harness-evals/harness-docs/MGO_TESTING.md) — Unit tests (fake client + interceptClient), E2E (Ginkgo)
- [Architecture](harness-evals/harness-docs/architecture/components.md) — Repo layout, reconciliation flow, Job template
- [Domain Model](harness-evals/harness-docs/domain/mustgather.md) — MustGather CRD fields, validation, lifecycle
- [Decision Records](harness-evals/harness-docs/decisions/) — ADRs for immutable spec, two-container Job, extensible upload
