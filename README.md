# Must Gather Operator

The Must Gather Operator is a Kubernetes operator for OpenShift that automates the collection of [must-gather](https://github.com/openshift/must-gather) diagnostic information on a cluster and uploads it to a Red Hat support case via SFTP. It is built using the [Operator SDK](https://github.com/operator-framework/operator-sdk) and the controller-runtime framework.

## Usage

To collect diagnostics, a cluster administrator creates a `MustGather` custom resource:

```yaml
apiVersion: operator.openshift.io/v1alpha1
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

This request will collect the standard must-gather info and upload it to case `#02527285` using the credentials found in the `case-management-creds` secret.

### Collecting Audit Logs

The field `audit` is **false** by default unless explicitly set to **true**. When enabled, it generates the default collection of audit logs as per [the collection script: gather_audit_logs](https://github.com/openshift/must-gather/blob/master/collection-scripts/gather_audit_logs).

```yaml
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: example-mustgather-full
spec:
  serviceAccountName: must-gather-admin
  uploadTarget:
    type: SFTP
    sftp:
      caseID: '02527285'
      caseManagementAccountSecretRef:
        name: case-management-creds
  audit: true
```

### Garbage Collection

MustGather instances are cleaned up by the operator about 6 hours after completion, regardless of whether they were successful. This prevents the accumulation of unwanted MustGather resources and their corresponding Job resources.

## Deploying the Operator

This is a cluster-level operator that you can deploy in any namespace; `must-gather-operator` is recommended.

### Deploying Directly with Manifests

```shell
git clone git@github.com:openshift/must-gather-operator.git; cd must-gather-operator
oc apply -f deploy/crds/operator.openshift.io_mustgathers_crd.yaml
oc new-project must-gather-operator
oc -n must-gather-operator apply -f deploy
```

### Meeting the Operator Requirements

The operator needs a secret to be created by the admin as follows (this assumes the operator is running in the `must-gather-operator` namespace):

```shell
oc create secret generic case-management-creds --from-literal=username=<username> --from-literal=password=<password>
```

The operator also requires two environment variables in its Deployment:

| Variable | Purpose |
|---|---|
| `DEFAULT_MUST_GATHER_IMAGE` | Image used for the gather container (e.g., `quay.io/openshift/origin-must-gather:latest`) |
| `OPERATOR_IMAGE` | Image used for the upload container (typically the operator's own image) |

## Local Development

Execute the following steps to develop the functionality locally. It is recommended that development be done using a cluster with `cluster-admin` permissions.

1. In the operator's `Deployment.yaml` [file](deploy/99_must-gather-operator.Deployment.yaml), add a variable to the deployment's `spec.template.spec.containers.env` list called `OPERATOR_IMAGE` and set the value to your local copy of the image:

   ```yaml
   env:
     - name: OPERATOR_IMAGE
       value: "registry.example/repo/image:latest"
   ```

2. Download dependencies:

   ```shell
   go mod download
   ```

3. Using the [operator-sdk](https://github.com/operator-framework/operator-sdk), apply the CRD and run the operator locally:

   ```shell
   oc apply -f deploy/crds/operator.openshift.io_mustgathers_crd.yaml
   oc new-project must-gather-operator
   export DEFAULT_MUST_GATHER_IMAGE='quay.io/openshift/origin-must-gather:latest'
   OPERATOR_NAME=must-gather-operator operator-sdk run --verbose --local --namespace ''
   ```

### Build Commands

This project uses a boilerplate-based Makefile system:

```bash
make                # Build, test, and lint (default target)
make go-test        # Run unit tests
make go-build       # Build the operator binary
make docker-build   # Build container image
make docker-push    # Push container image
make generate       # Regenerate deepcopy, CRDs, OpenAPI
make manifests      # Regenerate CRD YAML and RBAC
make lint           # Run linters
make coverage       # Coverage report
```

## Documentation

Detailed documentation for contributors and AI agents is organized as follows:

| Document | Description |
|---|---|
| [AGENTS.md](AGENTS.md) | Architecture overview, repository layout, code conventions, common pitfalls, and PR expectations |
| [docs/api-contracts-guidelines.md](docs/api-contracts-guidelines.md) | CRD validation rules, CEL constraints, immutability, and union discriminators |
| [docs/security-guidelines.md](docs/security-guidelines.md) | Credential handling, RBAC patterns, ServiceAccount validation, and proxy auth |
| [docs/performance-guidelines.md](docs/performance-guidelines.md) | Reconciliation concurrency, predicates, requeue strategy, and cleanup timing |
| [docs/error-handling-guidelines.md](docs/error-handling-guidelines.md) | Error categories, retry logic, status condition updates, and event recording |
| [docs/testing-guidelines.md](docs/testing-guidelines.md) | Unit tests, E2E tests, mocking patterns, and test file map |
| [docs/integration-guidelines.md](docs/integration-guidelines.md) | SFTP upload flow, proxy support, Job lifecycle, and two-container model |

Start with [AGENTS.md](AGENTS.md) for a comprehensive onboarding guide covering architecture, code conventions, and common pitfalls. The `docs/` guideline files provide domain-specific depth.
