# Must Gather Operator

The Must Gather operator helps collecting must-gather information on a cluster and uploading it to a case.
To use the operator, a cluster administrator can create the following MustGather CR:

```yaml
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: example-mustgather-basic
spec:
  serviceAccountName: default
  uploadTarget:
    type: SFTP
    sftp:
      caseID: '02527285'
      caseManagementAccountSecretRef:
        name: case-management-creds
```

This request will collect the standard must-gather info and upload it to case `#02527285` using the credentials found in the `caseManagementCreds` secret.

## Using a custom SFTP port

By default the operator uploads to `sftp.access.redhat.com` on port 22. If outbound port 22 is blocked in your environment, Red Hat also accepts uploads on port 80 at the same address. Set the optional `port` field to override:

```yaml
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: example-mustgather-port80
spec:
  serviceAccountName: default
  uploadTarget:
    type: SFTP
    sftp:
      caseID: '02527285'
      caseManagementAccountSecretRef:
        name: case-management-creds
      port: 80
```

`port` accepts any value from 1–65535 and defaults to 22 when omitted.

## Collecting Audit logs
The field `audit` is **false** by default unless explicetely set to **true**.
This will generate the default collection of audit logs as per [the collection script: gather_audit_logs](https://github.com/openshift/must-gather/blob/master/collection-scripts/gather_audit_logs)
```yaml
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: example-mustgather-full
spec:
  serviceAccountName: default
  uploadTarget:
    type: SFTP
    sftp:
      caseID: '02527285'
      caseManagementAccountSecretRef:
        name: case-management-creds
  audit: true
```

## Garbage collection

MustGather instances are cleaned up by the Must Gather operator about 6 hours after completion, regardless of whether they were successful.
This is a way to prevent the accumulation of unwanted MustGather resources and their corresponding job resources.

## Deploying the Operator

This is a cluster-level operator that you can deploy in any namespace; `must-gather-operator` is recommended.

### Deploying directly with manifests

Here are the instructions to install the latest release creating the manifest directly in OCP.

```shell
git clone git@github.com:openshift/must-gather-operator.git; cd must-gather-operator
oc apply -f deploy/crds/operator.openshift.io_mustgathers_crd.yaml
oc new-project must-gather-operator
oc -n must-gather-operator apply -f deploy
```

> **Note:** The `deploy/` manifests include two sets of RBAC resources:
> - `04_must-gather-admin.ServiceAccount.yaml`, `05_must-gather-admin.ClusterRole.yaml`, and `06_must-gather-admin.ClusterRoleBinding.yaml` create the `must-gather-admin` ServiceAccount with cluster-admin equivalent access plus the `privileged` SCC. Use `serviceAccountName: must-gather-admin` in your MustGather CR for the gather job.
> - `07_must-gather-operator-default-sa.ClusterRoleBinding.privileged.yaml` grants the `default` ServiceAccount in `must-gather-operator` the same permissions. This is required because the `perf-node-gather-daemonset` (created dynamically by the must-gather image) runs under the `default` SA and needs `hostPID`, `hostPath` volumes, and cluster-level API access. Without this binding, daemonset pods will fail to schedule and node-level performance data will be missing from the bundle.

### Meeting the operator requirements

In order to run, the operator needs a secret to be created by the admin as follows (this assumes the operator is running in the `must-gather-operator` namespace).

```shell
oc create secret generic case-management-creds --from-literal=username=<username> --from-literal=password=<password>
```

#### Required RBAC permissions

The operator's service account (`must-gather-operator`) requires cluster-level **get/list/watch** on `serviceaccounts` to validate that the service account referenced in the MustGather CR exists before creating the gather job. When deploying via OLM this rule must be present in the CSV's `clusterPermissions` section. When deploying with manifests it is included in `deploy/02_must-gather-operator.ClusterRole.yaml`.

If you see the following error in operator logs:

```
serviceaccounts is forbidden: User "system:serviceaccount:must-gather-operator:must-gather-operator" cannot list resource "serviceaccounts" in API group ""
```

Apply the missing rule manually:

```shell
oc patch clusterrole <must-gather-operator-clusterrole-name> --type=json \
  -p='[{"op":"add","path":"/rules/-","value":{"apiGroups":[""],"resources":["serviceaccounts"],"verbs":["get","list","watch"]}}]'
```

#### Using the must-gather-admin service account

The `must-gather-admin` ServiceAccount (created by `deploy/04_must-gather-admin.ServiceAccount.yaml`) has the permissions required to run the full must-gather collection. Set `serviceAccountName: must-gather-admin` in your MustGather CR:

```yaml
spec:
  serviceAccountName: must-gather-admin
```

The `perf-node-gather-daemonset` created by the must-gather image always runs under the `default` ServiceAccount in the operator namespace. The `deploy/07_must-gather-operator-default-sa.ClusterRoleBinding.privileged.yaml` manifest grants the `default` SA the same permissions so that daemonset pods can use `hostPID` and `hostPath` volumes and access cluster APIs.

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
oc new-project must-gather-operator
export DEFAULT_MUST_GATHER_IMAGE='quay.io/openshift/origin-must-gather:latest'
OPERATOR_NAME=must-gather-operator operator-sdk run --verbose --local --namespace ''
```
