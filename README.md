# Must Gather Operator

The Must Gather operator helps collecting must-gather information on a cluster and uploading it to a case.
To use the operator, a cluster administrator can create the following MustGather CR:

```yaml
apiVersion: managed.openshift.io/v1alpha1
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

A more complex example:

```yaml
apiVersion: managed.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: example-mustgather-full
spec:
  caseID: '02527285'
  caseManagementAccountSecretRef:
    name: case-management-creds
  serviceAccountRef:
    name: must-gather-admin
  mustGatherImages:
  - quay.io/kubevirt/must-gather:latest
  - quay.io/ocs-dev/ocs-must-gather
```

In this example we are using a specific service account (which must have cluster-admin permissions as per must-gather requirements), and we are specifying a couple of additional must gather images to be run for the `kubevirt` and `ocs` subsystem. If not specified, serviceAccountRef.Name will default to `default`. Also the standard must gather image: `quay.io/openshift/origin-must-gather:latest` is always added by default.

## Proxy Support

The Must Gather operator supports using a proxy. The proxy setting can be specified in the MustGather object. If not specified, the cluster default proxy setting will be used. Here is an example:

```yaml
apiVersion: managed.openshift.io/v1alpha1
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
    http_proxy: http://myproxy
    https_proxy: https://my_http_proxy
    no_proxy: master-api
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
oc apply -f deploy/crds/managed.openshift.io_mustgathers_crd.yaml
oc new-project must-gather-operator
oc -n must-gather-operator apply -f deploy
```

### Meeting the operator requirements

In order to run, the operator needs a secret to be created by the admin as follows (this assumes the operator is running in the `must-gather-operator` namespace).

```shell
oc create secret generic case-management-creds --from-literal=username=<username> --from-literal=password=<password>
```
## Uploading to a (local) S3 bucket
Some environments are air-gapped. They cannot reach Red Hat sFTP servers directly. One option is to send the files to an S3 bucket that could be provisioned internally (or in a DMZ) then uploaded to Red Hat from there.

To use this option, you need to add some details to the `case-management-creds` Secret that define:
- The AWS credentials (aws_access_key_id, aws_secret_access_key, and region)
- Optionally, the endpoint_url. This is the internal URL for accessing the S3 bucket internally. If left blank, the public AWS S3 API will be contacted.
- The bucket name
A sample Secret could look like this:
```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: case-management-creds
  namespace: openshift-must-gather-operator
type: Opaque
data:
  password: bWlsbGVubml1bQ==
  username: YWJvaG1lZWQ=
  aws_access_key_id: S2Fpamh3WDF2SWxjZ25NOGREdUU=
  aws_secret_access_key: M0NlTEppeGpndFpMWHdZWjhHdGkydzB5ZVdPTHRjQXlKSFBhemF1Nw==
  aws_endpoint_url: aHR0cDovLzE5Mi4xNjguMi4xNzo5MDAw
  aws_region: dXMtZWFzdC0x
```
After you apply the secret using something like `oc apply -f case-management-creds.yaml`, you need to add the following details to the CR (Custom Resource):
- `target`: Set it to `s3`.
- `awsBucket`: The name of the S3 bucket where the files should go.

An example CR:
```yaml
apiVersion: managed.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: example-mustgather
spec:
  caseID: '02527285'
  caseManagementAccountSecretRef:
    name: case-management-creds
  serviceAccountRef:
    name: must-gather-admin
  target: s3
  awsBucket: mustgather01
```
### Local S3 bucket over TLS/SSL
If you are running a private S3 bucket (like on Minio, for example), you will probably connect to it over TLS with a private CA. In this case, awscli must trust this certificate for the operation to work. 

You can add a certificate and the operator will mount it to a file under `/ssl` and configure AWSCLI to trust it. Add the following to the Secret:
```yaml
ssl_cert: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUI4akNDQVplZ0F3SUJBZ0lRUlRGUXpmN202dmJPWTFOd3AxYzNSVEFLQmdncWhrak9QUVFEQWpCQU1Sd3cKR2dZRFZRUUtFeE5EWlhKMFoyVnVJRVJsZG1Wc2IzQnRaVzUwTVNBd0hnWURWUVFMREJkaGFHMWxaRUJ2Y0dWdQpjMmhwWm5RZ0tFRm9iV1ZrS1RBZUZ3MHlNekE0TURRd09UQTNORFJhRncweU5EQTRNRE13T1RBM05EUmFNRUF4CkhEQWFCZ05WQkFvVEUwTmxjblJuWlc0Z1JHVjJaV3h2Y0cxbGJuUXhJREFlQmdOVkJBc01GMkZvYldWa1FHOXcKWlc1emFHbG1kQ0FvUVdodFpXUXBNRmt3RXdZSEtvWkl6ajBDQVFZSUtvWkl6ajBEQVFjRFFnQUU5QkpybjZLOQpqcWdtWXp5WjNrMWVlOUZ4ejdTU2lKaGhEYWJxdyt5TlFMWEN0ZTgxaWhTMUxQUXpSSWo5UU5GWENLT2RJbGdZCkpkQUF5Q1Q0WUljOG1xTnpNSEV3RGdZRFZSMFBBUUgvQkFRREFnS2tNQk1HQTFVZEpRUU1NQW9HQ0NzR0FRVUYKQndNQk1BOEdBMVVkRXdFQi93UUZNQU1CQWY4d0hRWURWUjBPQkJZRUZQME9SZmlpY01qdzdBd09pQmhFQ1dvWgo0dCtyTUJvR0ExVWRFUVFUTUJHQ0NXeHZZMkZzYUc5emRJY0V3S2dDRVRBS0JnZ3Foa2pPUFFRREFnTkpBREJHCkFpRUEvcmp2b3hUQUxlSklNRFpVdllUem9TWGpGeDhLczlUUXUrV3NNVDQ2SXNFQ0lRRDM4T3p4WW9nT3ZZTzEKUTZja2NaT2RrL1l1Rk5CUWx6MHNTSjgvdlkrdHpnPT0KLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo=
```
This is a sample CA that AWSCLI will be configured to trust. You also need to enable Private TLS/SSL communcation in the CR itself:
```yaml
privateSsl: true
```
You may want to use HTTPS instead of HTTP when defining the endpoint_url if you are using private TLS/SSL.
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
oc apply -f deploy/crds/managed.openshift.io_mustgathers_crd.yaml
oc new-project must-gather-operator
export DEFAULT_MUST_GATHER_IMAGE='quay.io/openshift/origin-must-gather:latest'
export JOB_TEMPLATE_FILE_NAME=./build/templates/job.template.yaml
OPERATOR_NAME=must-gather-operator operator-sdk run --verbose --local --namespace ''
```
