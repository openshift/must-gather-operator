## Locally running e2e test suite
When updating your operator it's beneficial to add e2e tests for new functionality AND ensure existing functionality is not breaking using e2e tests. 
To do this, following steps are recommended:

1. Deploy your new version of operator in a test cluster
2. Get kubeadmin credentials from your cluster using:

```bash
ocm get /api/clusters_mgmt/v1/clusters/(cluster-id)/credentials | jq -r .kubeconfig > /path/to/kubeconfig
```

3. Run test suite using:

```bash
KUBECONFIG=/path/to/kubeconfig make test-e2e
```

Or to disable JUnit reports:

```bash
DISABLE_JUNIT_REPORT=true KUBECONFIG=/path/to/kubeconfig make test-e2e
```
