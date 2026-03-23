# E2E Execution Steps: must-gather-operator

This document provides step-by-step instructions to execute E2E tests for the time-based log filtering feature (EP-1923) in the Must-Gather Operator.

## Prerequisites

```bash
# Verify oc CLI is installed
which oc
oc version

# Verify authenticated to OpenShift cluster
oc whoami
oc cluster-info

# Verify cluster access
oc get nodes
oc get clusterversion

# Verify admin permissions
oc auth can-i create namespace
oc auth can-i create customresourcedefinition
```

## Environment Variables

```bash
# Set operator namespace
export OPERATOR_NAMESPACE="openshift-must-gather-operator"

# Set test namespace (for test MustGather CRs)
export TEST_NAMESPACE="${OPERATOR_NAMESPACE}"

# Optional: Set custom must-gather image
export MUST_GATHER_IMAGE="quay.io/openshift/origin-must-gather:latest"
```

## Step 1: Install Operator

### Option A: Manual Installation (Recommended for E2E testing)

```bash
# Clone the repository (if not already)
# git clone https://github.com/openshift/must-gather-operator.git
# cd must-gather-operator

# Create namespace
oc apply -f examples/other_resources/00_openshift-must-gather-operator.Namespace.yaml

# Create CRD
oc apply -f deploy/crds/operator.openshift.io_mustgathers.yaml

# Verify CRD is created
oc get crd mustgathers.operator.openshift.io

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
oc wait --for=condition=Available deployment/must-gather-operator \
  -n ${OPERATOR_NAMESPACE} --timeout=300s

# Verify operator is running
oc get pods -n ${OPERATOR_NAMESPACE}
oc logs deployment/must-gather-operator -n ${OPERATOR_NAMESPACE} --tail=50
```

## Step 2: Verify CRD Schema (New Fields)

```bash
# Check that gatherSpec is in the CRD schema
oc get crd mustgathers.operator.openshift.io -o yaml | grep -A30 "gatherSpec:"

# Verify since field with duration format
oc get crd mustgathers.operator.openshift.io -o yaml | grep -A10 "since:"

# Verify sinceTime field with date-time format
oc get crd mustgathers.operator.openshift.io -o yaml | grep -A10 "sinceTime:"

# Expected output should show:
# - gatherSpec object with properties
# - since with format: duration
# - sinceTime with format: date-time
```

## Step 3: Test Case 1 - MustGather with `since` Duration

```bash
# Create MustGather CR with since field
cat <<EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: test-since
  namespace: ${TEST_NAMESPACE}
spec:
  serviceAccountName: must-gather-admin
  gatherSpec:
    since: 2h
EOF

# Wait a few seconds for Job creation
sleep 5

# Verify MustGather CR was created
oc get mustgather test-since -n ${TEST_NAMESPACE} -o yaml

# Check that Job was created
oc get job test-since -n ${TEST_NAMESPACE}

# CRITICAL: Verify MUST_GATHER_SINCE environment variable is set
oc get job test-since -n ${TEST_NAMESPACE} \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="gather")].env[?(@.name=="MUST_GATHER_SINCE")].value}'

# Expected output: 2h0m0s

# Check all environment variables in gather container
oc get job test-since -n ${TEST_NAMESPACE} \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="gather")].env}' | jq '.'

# Monitor Job progress
oc get pods -n ${TEST_NAMESPACE} -l job-name=test-since -w
# Press Ctrl+C after pod starts running

# Check gather pod logs (look for time filtering messages)
oc logs -f job/test-since -c gather -n ${TEST_NAMESPACE}
# Press Ctrl+C to exit logs

# Wait for completion (timeout 10 minutes)
oc wait --for=jsonpath='{.status.completed}'=true mustgather/test-since \
  -n ${TEST_NAMESPACE} --timeout=600s
```

## Step 4: Test Case 2 - MustGather with `sinceTime` Timestamp

```bash
# Create MustGather CR with sinceTime field
# Use a timestamp from today (adjust as needed)
SINCE_TIME=$(date -u -d '2 hours ago' '+%Y-%m-%dT%H:%M:%SZ')

cat <<EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: test-sincetime
  namespace: ${TEST_NAMESPACE}
spec:
  serviceAccountName: must-gather-admin
  gatherSpec:
    sinceTime: "${SINCE_TIME}"
EOF

# Wait for Job creation
sleep 5

# Verify MustGather CR
oc get mustgather test-sincetime -n ${TEST_NAMESPACE} -o yaml | grep -A5 gatherSpec

# CRITICAL: Verify MUST_GATHER_SINCE_TIME environment variable
oc get job test-sincetime -n ${TEST_NAMESPACE} \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="gather")].env[?(@.name=="MUST_GATHER_SINCE_TIME")].value}'

# Expected output: RFC3339 timestamp (e.g., 2026-03-23T10:00:00Z)

# Monitor and wait for completion
oc wait --for=jsonpath='{.status.completed}'=true mustgather/test-sincetime \
  -n ${TEST_NAMESPACE} --timeout=600s
```

## Step 5: Test Case 3 - MustGather with Both Fields

```bash
# Create MustGather CR with both since and sinceTime
cat <<EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: test-both
  namespace: ${TEST_NAMESPACE}
spec:
  serviceAccountName: must-gather-admin
  gatherSpec:
    since: 1h
    sinceTime: "2026-03-23T10:00:00Z"
EOF

# Wait for Job creation
sleep 5

# CRITICAL: Verify both environment variables are set
echo "Checking MUST_GATHER_SINCE:"
oc get job test-both -n ${TEST_NAMESPACE} \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="gather")].env[?(@.name=="MUST_GATHER_SINCE")].value}'
echo ""

echo "Checking MUST_GATHER_SINCE_TIME:"
oc get job test-both -n ${TEST_NAMESPACE} \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="gather")].env[?(@.name=="MUST_GATHER_SINCE_TIME")].value}'
echo ""

# Wait for completion
oc wait --for=jsonpath='{.status.completed}'=true mustgather/test-both \
  -n ${TEST_NAMESPACE} --timeout=600s
```

## Step 6: Test Case 4 - Backward Compatibility (No gatherSpec)

```bash
# Create MustGather CR without gatherSpec (using basic example)
cat <<EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: test-no-gatherspec
  namespace: ${TEST_NAMESPACE}
spec:
  serviceAccountName: must-gather-admin
EOF

# Wait for Job creation
sleep 5

# Verify Job is created
oc get job test-no-gatherspec -n ${TEST_NAMESPACE}

# CRITICAL: Verify NO MUST_GATHER_SINCE or MUST_GATHER_SINCE_TIME env vars
oc get job test-no-gatherspec -n ${TEST_NAMESPACE} \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="gather")].env}' | jq '.' | grep -i "must_gather_since" || echo "✓ No time filter env vars (expected)"

# Wait for completion
oc wait --for=jsonpath='{.status.completed}'=true mustgather/test-no-gatherspec \
  -n ${TEST_NAMESPACE} --timeout=600s
```

## Step 7: Test Case 5 - Invalid Duration (Should Fail)

```bash
# Attempt to create MustGather with invalid duration
cat <<EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: test-invalid-duration
  namespace: ${TEST_NAMESPACE}
spec:
  serviceAccountName: must-gather-admin
  gatherSpec:
    since: invalid-duration
EOF

# Expected: API server rejects with validation error
# If created successfully, this is a BUG

# Verify it was NOT created
oc get mustgather test-invalid-duration -n ${TEST_NAMESPACE} 2>&1 | grep "NotFound" && echo "✓ Correctly rejected"
```

## Step 8: Test Case 6 - Various Duration Formats

```bash
# Test different duration formats
for duration in "30m" "300s" "24h" "1h30m"; do
  name="test-duration-$(echo $duration | tr -d 'hms')"
  echo "Testing duration: $duration (name: $name)"

  cat <<EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: ${name}
  namespace: ${TEST_NAMESPACE}
spec:
  serviceAccountName: must-gather-admin
  gatherSpec:
    since: ${duration}
EOF

  sleep 3

  # Check env var value
  value=$(oc get job ${name} -n ${TEST_NAMESPACE} \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="gather")].env[?(@.name=="MUST_GATHER_SINCE")].value}' 2>/dev/null)

  echo "  → MUST_GATHER_SINCE=$value"
  echo ""
done
```

## Step 9: Test Case 7 - Immutability Check

```bash
# Create a MustGather
cat <<EOF | oc apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: test-immutable
  namespace: ${TEST_NAMESPACE}
spec:
  serviceAccountName: must-gather-admin
  gatherSpec:
    since: 1h
EOF

# Wait a moment
sleep 3

# Attempt to modify gatherSpec (should fail due to immutability)
oc patch mustgather test-immutable -n ${TEST_NAMESPACE} \
  --type=merge -p '{"spec":{"gatherSpec":{"since":"2h"}}}'

# Expected: Error message indicating spec is immutable
# If patch succeeds, this is a BUG
```

## Step 10: Verification

```bash
# List all test MustGather CRs
echo "=== All MustGather CRs ==="
oc get mustgather -n ${TEST_NAMESPACE}

# Check operator health
echo ""
echo "=== Operator Status ==="
oc get deployment must-gather-operator -n ${OPERATOR_NAMESPACE}
oc get pods -n ${OPERATOR_NAMESPACE}

# Check for any errors in operator logs
echo ""
echo "=== Recent Operator Logs ==="
oc logs deployment/must-gather-operator -n ${OPERATOR_NAMESPACE} --tail=20

# List all Jobs created
echo ""
echo "=== All Jobs ==="
oc get jobs -n ${TEST_NAMESPACE}

# Summary of completed vs failed
echo ""
echo "=== Completion Summary ==="
oc get mustgather -n ${TEST_NAMESPACE} \
  -o custom-columns=NAME:.metadata.name,COMPLETED:.status.completed,STATUS:.status.status
```

## Step 11: Cleanup

```bash
# Delete all test MustGather CRs
oc delete mustgather test-since test-sincetime test-both test-no-gatherspec \
  -n ${TEST_NAMESPACE} --ignore-not-found=true

# Delete duration format test CRs
oc delete mustgather test-duration-30 test-duration-300 test-duration-24 test-duration-130 \
  -n ${TEST_NAMESPACE} --ignore-not-found=true

# Delete immutability test CR
oc delete mustgather test-immutable -n ${TEST_NAMESPACE} --ignore-not-found=true

# Wait for cleanup
sleep 5

# Verify all Jobs are gone (if retainResourcesOnCompletion is false, which is default)
oc get jobs -n ${TEST_NAMESPACE}

# Optional: Uninstall operator
read -p "Do you want to uninstall the operator? (y/n) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
  echo "Uninstalling operator..."
  oc delete deployment must-gather-operator -n ${OPERATOR_NAMESPACE}
  oc delete clusterrolebinding must-gather-operator must-gather-admin
  oc delete clusterrole must-gather-operator must-gather-admin
  oc delete sa must-gather-operator must-gather-admin -n ${OPERATOR_NAMESPACE}
  oc delete crd mustgathers.operator.openshift.io
  oc delete namespace ${OPERATOR_NAMESPACE}
  echo "✓ Operator uninstalled"
fi
```

## Expected Results Summary

### Test Case 1 (since duration):
- ✅ CR created successfully
- ✅ Job created with `MUST_GATHER_SINCE=2h0m0s` env var
- ✅ Job completes successfully

### Test Case 2 (sinceTime timestamp):
- ✅ CR created successfully
- ✅ Job created with `MUST_GATHER_SINCE_TIME=<RFC3339>` env var
- ✅ Job completes successfully

### Test Case 3 (both fields):
- ✅ CR created successfully
- ✅ Job created with both `MUST_GATHER_SINCE` and `MUST_GATHER_SINCE_TIME` env vars
- ✅ Job completes successfully

### Test Case 4 (backward compatibility):
- ✅ CR created successfully without gatherSpec
- ✅ Job created WITHOUT time filter env vars
- ✅ Job completes successfully (full log collection)

### Test Case 5 (invalid duration):
- ✅ API server rejects CR with validation error
- ✅ CR is not created

### Test Case 6 (duration formats):
- ✅ All standard formats accepted ("30m", "300s", "24h", "1h30m")
- ✅ Values normalized in env vars ("30m" → "30m0s")

### Test Case 7 (immutability):
- ✅ Patch attempt rejected with immutability error
- ✅ Original gatherSpec remains unchanged

## Troubleshooting

### Operator not starting
```bash
oc logs deployment/must-gather-operator -n ${OPERATOR_NAMESPACE}
oc describe deployment must-gather-operator -n ${OPERATOR_NAMESPACE}
```

### Job not created
```bash
oc get mustgather <name> -n ${TEST_NAMESPACE} -o yaml
oc logs deployment/must-gather-operator -n ${OPERATOR_NAMESPACE} | grep <name>
```

### Job failing
```bash
oc logs job/<name> -c gather -n ${TEST_NAMESPACE}
oc describe job/<name> -n ${TEST_NAMESPACE}
```

### Environment variables not set
```bash
# This indicates a controller implementation bug
oc get job <name> -n ${TEST_NAMESPACE} -o yaml | grep -A50 "containers:" | grep -A30 "name: gather"
```

## Notes

- All tests should complete within 10 minutes each (600s timeout)
- Operator logs should not contain errors related to gatherSpec processing
- The gather script in the must-gather image must support `MUST_GATHER_SINCE` and `MUST_GATHER_SINCE_TIME` environment variables
- Time filtering reduces archive size but requires logs to exist within the specified time window
