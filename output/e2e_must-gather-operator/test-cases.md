# E2E Test Cases: must-gather-operator

## Operator Information
- **Repository**: github.com/openshift/must-gather-operator
- **Framework**: controller-runtime
- **API Group**: operator.openshift.io
- **API Version**: v1alpha1
- **Managed CRDs**: MustGather
- **Operator Namespace**: openshift-must-gather-operator
- **Changes Analyzed**: git diff origin/release-4.21...HEAD

## Prerequisites
- OpenShift cluster with admin access
- `oc` CLI installed and authenticated (`oc login`)
- Must-gather image available (default: `quay.io/openshift/must-gather:latest`)
- For upload tests: Red Hat support case credentials

## Installation
The Must-Gather Operator can be installed via OLM or manually.

### Manual Installation
```bash
# Create namespace
oc apply -f examples/other_resources/00_openshift-must-gather-operator.Namespace.yaml

# Create CRD
oc apply -f deploy/crds/operator.openshift.io_mustgathers.yaml

# Create RBAC resources
oc apply -f deploy/02_must-gather-operator.ClusterRole.yaml
oc apply -f examples/other_resources/01_must-gather-operator.ServiceAccount.yaml
oc apply -f deploy/03_must-gather-operator.ClusterRoleBinding.yaml
oc apply -f deploy/05_must-gather-admin.ClusterRole.yaml
oc apply -f examples/other_resources/04_must-gather-admin.ServiceAccount.yaml
oc apply -f deploy/06_must-gather-admin.ClusterRoleBinding.yaml

# Deploy operator
oc apply -f deploy/99_must-gather-operator.Deployment.yaml

# Wait for operator to be ready
oc wait --for=condition=Available deployment/must-gather-operator -n openshift-must-gather-operator --timeout=300s
```

## CR Deployment
Sample MustGather CRs are available in `examples/` directory:
- `mustgather_basic.yaml` - Basic must-gather without upload
- `mustgather_full.yaml` - Full configuration with SFTP upload
- `mustgather_proxy.yaml` - With proxy configuration
- `mustgather_timeout.yaml` - With custom timeout
- `mustgather_retain_resources.yaml` - With resource retention

## Test Cases

### API Type Changes: Time-Based Log Filtering (EP-1923)

#### Test 1: Create MustGather with `gatherSpec.since` Duration
- **Purpose**: Verify that the new `since` field accepts duration strings and triggers time-based log filtering
- **Steps**:
  1. Create a MustGather CR with `spec.gatherSpec.since: "2h"`
  2. Wait for the MustGather Job to be created
  3. Inspect the gather container's environment variables
- **Expected**:
  - CR is accepted by API server
  - Job is created successfully
  - Gather container has `MUST_GATHER_SINCE=2h0m0s` environment variable
  - Job completes successfully (assuming logs exist in the last 2 hours)
- **Verification**:
  ```bash
  oc get mustgather test-since -o yaml | grep -A5 gatherSpec
  oc get job test-since -n openshift-must-gather-operator -o yaml | grep -A10 "name: gather" | grep -A5 env
  ```

#### Test 2: Create MustGather with `gatherSpec.sinceTime` Timestamp
- **Purpose**: Verify that the new `sinceTime` field accepts RFC3339 timestamps
- **Steps**:
  1. Create a MustGather CR with `spec.gatherSpec.sinceTime: "2026-03-23T10:00:00Z"`
  2. Wait for the MustGather Job to be created
  3. Inspect the gather container's environment variables
- **Expected**:
  - CR is accepted by API server
  - Job is created successfully
  - Gather container has `MUST_GATHER_SINCE_TIME=2026-03-23T10:00:00Z` environment variable
  - Job completes successfully
- **Verification**:
  ```bash
  oc get mustgather test-sincetime -o yaml | grep -A5 gatherSpec
  oc get job test-sincetime -n openshift-must-gather-operator -o yaml | grep -A10 "name: gather" | grep -A5 env
  ```

#### Test 3: Create MustGather with Both `since` and `sinceTime`
- **Purpose**: Verify that both fields can be specified simultaneously (gather script handles precedence)
- **Steps**:
  1. Create a MustGather CR with both `spec.gatherSpec.since: "1h"` and `spec.gatherSpec.sinceTime: "2026-03-23T10:00:00Z"`
  2. Wait for the MustGather Job to be created
  3. Inspect the gather container's environment variables
- **Expected**:
  - CR is accepted by API server
  - Job is created successfully
  - Gather container has both `MUST_GATHER_SINCE` and `MUST_GATHER_SINCE_TIME` environment variables
- **Verification**:
  ```bash
  oc get job test-both -n openshift-must-gather-operator -o yaml | grep "MUST_GATHER_SINCE"
  ```

#### Test 4: Create MustGather Without `gatherSpec` (Backward Compatibility)
- **Purpose**: Verify that existing MustGather CRs without gatherSpec still work
- **Steps**:
  1. Create a MustGather CR without the `gatherSpec` field (use `examples/mustgather_basic.yaml`)
  2. Wait for the MustGather Job to be created
  3. Verify job completes successfully
- **Expected**:
  - CR is accepted by API server
  - Job is created successfully
  - No `MUST_GATHER_SINCE` or `MUST_GATHER_SINCE_TIME` environment variables
  - Full log collection occurs (default behavior)
  - Status.Completed becomes true
- **Verification**:
  ```bash
  oc wait --for=jsonpath='{.status.completed}'=true mustgather/example-mustgather --timeout=600s
  ```

#### Test 5: Validate Invalid Duration Format
- **Purpose**: Verify that invalid duration strings are rejected by the API server
- **Steps**:
  1. Attempt to create a MustGather CR with `spec.gatherSpec.since: "invalid-duration"`
  2. Observe the API server response
- **Expected**:
  - API server rejects the CR with a validation error
  - Error message indicates invalid duration format
- **Verification**:
  ```bash
  # This should fail
  oc apply -f - <<EOF
  apiVersion: operator.openshift.io/v1alpha1
  kind: MustGather
  metadata:
    name: test-invalid-duration
  spec:
    serviceAccountName: must-gather-admin
    gatherSpec:
      since: invalid-duration
  EOF
  ```

#### Test 6: Validate Various Duration Formats
- **Purpose**: Verify that standard Kubernetes duration formats are accepted (hours, minutes, seconds)
- **Steps**:
  1. Create MustGather CRs with various duration formats: "30m", "300s", "24h", "1h30m"
  2. Verify each is accepted and properly formatted in the Job env vars
- **Expected**:
  - All standard duration formats are accepted
  - Durations are normalized in environment variables (e.g., "30m" becomes "30m0s")

#### Test 7: MustGather with `gatherSpec` and Upload Target
- **Purpose**: Verify that time filtering works in combination with existing features (upload)
- **Steps**:
  1. Create a MustGather CR with `gatherSpec.since: "2h"` and `uploadTarget` configured
  2. Verify the gather and upload containers are both created
  3. Check that environment variables are correctly set
- **Expected**:
  - Job has both gather and upload containers
  - Gather container has `MUST_GATHER_SINCE` environment variable
  - Upload container has upload-related environment variables
  - Integration works smoothly

### Controller Changes: Environment Variable Passing

#### Test 8: Verify Environment Variables are Passed to Gather Container
- **Purpose**: Verify controller implementation correctly passes gatherSpec to Job template
- **Steps**:
  1. Create a MustGather CR with `gatherSpec.since: "1h"`
  2. Retrieve the created Job
  3. Check the gather container's `env` section
- **Expected**:
  - Job exists with name matching MustGather CR name
  - Gather container (name: "gather") has environment variable `MUST_GATHER_SINCE` with value "1h0m0s"
- **Verification**:
  ```bash
  oc get job <name> -n openshift-must-gather-operator -o jsonpath='{.spec.template.spec.containers[?(@.name=="gather")].env[?(@.name=="MUST_GATHER_SINCE")].value}'
  ```

#### Test 9: Verify No Environment Variables When gatherSpec is Nil
- **Purpose**: Verify controller does not add env vars when gatherSpec is not specified
- **Steps**:
  1. Create a MustGather CR without `gatherSpec`
  2. Retrieve the created Job
  3. Check the gather container's `env` section
- **Expected**:
  - Job exists
  - Gather container does not have `MUST_GATHER_SINCE` or `MUST_GATHER_SINCE_TIME` environment variables
  - Other environment variables (if any) are present
- **Verification**:
  ```bash
  oc get job <name> -n openshift-must-gather-operator -o jsonpath='{.spec.template.spec.containers[?(@.name=="gather")].env}' | grep -c "MUST_GATHER_SINCE" || echo "Not found (expected)"
  ```

### CRD Changes: Schema Validation

#### Test 10: Verify CRD Schema Includes New Fields
- **Purpose**: Verify the CRD was updated with gatherSpec fields and validation
- **Steps**:
  1. Retrieve the MustGather CRD
  2. Check the schema for `spec.gatherSpec.since` and `spec.gatherSpec.sinceTime`
- **Expected**:
  - CRD contains `gatherSpec` in the schema
  - `since` field has `format: duration` validation
  - `sinceTime` field has `format: date-time` validation
- **Verification**:
  ```bash
  oc get crd mustgathers.operator.openshift.io -o yaml | grep -A20 gatherSpec
  ```

### Status and Lifecycle

#### Test 11: MustGather Lifecycle with Time Filtering
- **Purpose**: Verify complete lifecycle from creation to completion with time filtering
- **Steps**:
  1. Create a MustGather CR with `gatherSpec.since: "2h"`
  2. Monitor the CR status as it progresses
  3. Wait for completion
  4. Verify cleanup (if `retainResourcesOnCompletion` is false)
- **Expected**:
  - CR transitions through statuses correctly
  - Job is created and runs
  - Status.Completed becomes true upon successful completion
  - If retention is false, Job and Pods are cleaned up
- **Verification**:
  ```bash
  oc get mustgather <name> -w
  oc wait --for=jsonpath='{.status.completed}'=true mustgather/<name> --timeout=600s
  ```

#### Test 12: Immutability of gatherSpec
- **Purpose**: Verify that gatherSpec cannot be modified after creation (spec is immutable)
- **Steps**:
  1. Create a MustGather CR with `gatherSpec.since: "1h"`
  2. Attempt to update the CR to change `gatherSpec.since` to "2h"
- **Expected**:
  - Update is rejected by the API server with validation error
  - Error message indicates "spec values are immutable once set"
- **Verification**:
  ```bash
  # First create
  oc apply -f mustgather-immutable-test.yaml
  # Then try to modify (should fail)
  oc patch mustgather test-immutable --type=merge -p '{"spec":{"gatherSpec":{"since":"2h"}}}'
  ```

## Verification

### Operator Health
```bash
# Verify operator is running
oc get deployment must-gather-operator -n openshift-must-gather-operator
oc get pods -n openshift-must-gather-operator

# Check operator logs
oc logs deployment/must-gather-operator -n openshift-must-gather-operator
```

### MustGather Resources
```bash
# List all MustGather CRs
oc get mustgather -A

# Check specific MustGather status
oc get mustgather <name> -o yaml

# List Jobs created by operator
oc get jobs -n openshift-must-gather-operator

# Check Job pods
oc get pods -n openshift-must-gather-operator
```

### Job Environment Variables (Key Test Point)
```bash
# Check gather container environment
oc get job <mustgather-name> -n openshift-must-gather-operator \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="gather")].env}' | jq '.'

# Specifically check for time filter env vars
oc get job <mustgather-name> -n openshift-must-gather-operator \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="gather")].env[?(@.name=="MUST_GATHER_SINCE")].value}'

oc get job <mustgather-name> -n openshift-must-gather-operator \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="gather")].env[?(@.name=="MUST_GATHER_SINCE_TIME")].value}'
```

### Logs and Debugging
```bash
# Check gather pod logs
oc logs -f job/<mustgather-name> -c gather -n openshift-must-gather-operator

# Check if time filtering is applied (look for log messages indicating filtering)
oc logs job/<mustgather-name> -c gather -n openshift-must-gather-operator | grep -i "since\|time"
```

## Cleanup

```bash
# Delete test MustGather CRs
oc delete mustgather test-since test-sincetime test-both example-mustgather

# If using manual installation, cleanup operator
oc delete deployment must-gather-operator -n openshift-must-gather-operator
oc delete clusterrolebinding must-gather-operator must-gather-admin
oc delete clusterrole must-gather-operator must-gather-admin
oc delete sa must-gather-operator must-gather-admin -n openshift-must-gather-operator
oc delete crd mustgathers.operator.openshift.io
oc delete namespace openshift-must-gather-operator
```

## Expected Outcomes Summary

### For Time-Based Filtering Feature (EP-1923):

1. **API Extension**: New `gatherSpec` field with `since` and `sinceTime` is available in MustGather CRD
2. **Validation**: Invalid durations are rejected, valid formats accepted
3. **Controller Behavior**: Environment variables `MUST_GATHER_SINCE` and `MUST_GATHER_SINCE_TIME` are set when gatherSpec fields are present
4. **Backward Compatibility**: Existing MustGather CRs without gatherSpec continue to work with full log collection
5. **Immutability**: gatherSpec cannot be modified after CR creation
6. **Integration**: gatherSpec works alongside existing features (upload, proxy, timeout, storage)

### Success Criteria:

- ✅ All test cases pass without errors
- ✅ Environment variables are correctly set in Job when gatherSpec is specified
- ✅ No environment variables added when gatherSpec is nil
- ✅ Operator logs show no errors or warnings
- ✅ MustGather Jobs complete successfully
- ✅ CRD schema validation works as expected
