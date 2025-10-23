# Must Gather Operator

The Must Gather operator helps collecting must-gather information on a cluster and uploading it to a case.
To use the operator, a cluster administrator can create the following MustGather CR:

```yaml
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: example-mustgather-basic
spec:
  caseID: '02527285'
  caseManagementAccountSecretRef:
    name: case-management-creds
  serviceAccountRef:
    name: must-gather-admin
```

This request will collect the standard must-gather info and upload it to case `#02527285` using the credentials found in the `caseManagementCreds` secret.

## Collecting Audit logs
The field `audit` is **false** by default unless explicetely set to **true**.
This will generate the default collection of audit logs as per [the collection script: gather_audit_logs](https://github.com/openshift/must-gather/blob/master/collection-scripts/gather_audit_logs)
```yaml
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: example-mustgather-full
spec:
  caseID: '02527285'
  caseManagementAccountSecretRef:
    name: case-management-creds
  serviceAccountRef:
    name: must-gather-admin
  audit: true
```

## Proxy Support

The Must Gather operator supports using a proxy. The proxy setting can be specified in the MustGather object. If not specified, the cluster default proxy setting will be used. Here is an example:

```yaml
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: example-mustgather-proxy
spec:
  caseID: '02527285'
  caseManagementAccountSecretRef:
    name: case-management-creds
  serviceAccountRef:
    name: must-gather-admin
  proxyConfig:
    httpProxy: http://myproxy
    httpsProxy: https://my_http_proxy
    noProxy: master-api
```

## Garbage collection

MustGather instances are cleaned up by the Must Gather operator about 6 hours after completion, regardless of whether they were successful.
This is a way to prevent the accumulation of unwanted MustGather resources and their corresponding job resources.

## Deploying the Operator

This is a cluster-level operator that you can deploy in any namespace; `support-log-gather` is recommended.

### Deploying directly with manifests

Here are the instructions to install the latest release creating the manifest directly in OCP.

```shell
git clone git@github.com:openshift/must-gather-operator.git; cd must-gather-operator
oc apply -f deploy/crds/operator.openshift.io_mustgathers_crd.yaml
oc new-project support-log-gather
oc -n support-log-gather apply -f deploy
```

### Meeting the operator requirements

In order to run, the operator needs a secret to be created by the admin as follows (this assumes the operator is running in the `support-log-gather` namespace).

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
oc apply -f deploy/crds/operator.openshift.io_mustgathers_crd.yaml
oc new-project support-log-gather
export DEFAULT_MUST_GATHER_IMAGE='quay.io/openshift/origin-must-gather:latest'
OPERATOR_NAME=must-gather-operator operator-sdk run --verbose --local --namespace ''
```
