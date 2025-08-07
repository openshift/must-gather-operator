# Local Development of must-gather-operator

The operator can be built and run locally provided a `KUBECONFIG` is set in your environment pointing to an OpenShift cluster. It is recommended that development be done using a cluster with `cluster-admin` permissions.

## Build and Run

Execute the following pre-requisite steps to setup the must-gather-operator namespace with required manifests.

```sh
oc apply -f deploy/crds/managed.openshift.io_mustgathers.yaml 

oc new-project must-gather-operator

oc apply -f deploy/

# avoid running pods in the cluster for the operator
oc scale --replicas=0 -n must-gather-operator deploy/must-gather-operator
```

Build and run the operator:
```sh
go build .

export OPERATOR_NAMESPACE=must-gather-operator
export OPERATOR_NAME=must-gather-operator
export WATCH_NAMESPACE=must-gather-operator
export DEFAULT_MUST_GATHER_IMAGE='quay.io/openshift/origin-must-gather:latest'
export NAMESPACE=must-gather-operator

# the image for the operator is still required,
# it is used by the "upload" container in the Job's pod.
export OPERATOR_IMAGE="quay.io/swghosh/must-gather-operator:latest" 

podman build -t "${OPERATOR_IMAGE}" 

export OSDK_FORCE_RUN_MODE="local" # required to disable leader election
./must-gather-operator
```

## Example must-gather 

```sh
oc create -f - << EOF
apiVersion: v1
kind: Namespace
metadata:
  name: sandbox
  labels:
    name: sandbox
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: sandbox-admin
  namespace: sandbox
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: sandbox-admin-cluster-admin-binding
subjects:
- kind: ServiceAccount
  name: sandbox-admin
  namespace: sandbox
roleRef:
  kind: ClusterRole
  name: cluster-admin
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: v1
kind: Secret
metadata:
  name: sftp-access-rh-creds
  namespace: sandbox
type: Opaque
stringData:
  username: some-username
  password: a-password
---
apiVersion: managed.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: example
  namespace: sandbox
spec:
  caseID: '02527285'
  caseManagementAccountSecretRef:
    name: sftp-access-rh-creds
  serviceAccountRef:
    name: sandbox-admin
EOF
```