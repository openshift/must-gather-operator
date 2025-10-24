# Implementation Plan: OCPBUGS-63243 - Fix Hard-coded Namespace Dependencies

## Problem Statement
The must-gather-operator fails to start when deployed in a non-default namespace due to hard-coded namespace dependencies in the metrics configuration and cache setup.

## Root Cause Analysis
The operator has hard-coded namespace values in two locations:
1. `main.go:60` - `namespace = "must-gather-operator"` constant used for cache configuration
2. `config/config.go:8` - `OperatorNamespace string = "must-gather-operator"` used for metrics configuration

The deployment manifest already injects the correct namespace via the `OPERATOR_NAMESPACE` environment variable, but the code is not using it.

## Solution Implementation

### Step 1: Update main.go to use dynamic namespace detection
- Replace the hard-coded `namespace` constant with a function that reads from `OPERATOR_NAMESPACE` environment variable
- Provide a fallback to the current hard-coded value if the environment variable is not set
- Update the cache configuration to use the dynamic namespace

### Step 2: Update config/config.go to use dynamic namespace detection
- Replace the hard-coded `OperatorNamespace` constant with a function that reads from `OPERATOR_NAMESPACE` environment variable
- Provide a fallback to the current hard-coded value if the environment variable is not set

### Step 3: Add utility function for namespace detection
- Create a shared utility function that can be used by both main.go and config.go
- This function should:
  - Read from `OPERATOR_NAMESPACE` environment variable first
  - Fall back to the current hard-coded value "must-gather-operator" if not set
  - Log the namespace being used for debugging

### Step 4: Testing
- Ensure the operator still works when deployed in the default namespace
- Verify that it now works when deployed in custom namespaces
- Run existing unit tests to ensure no regression

## Acceptance Criteria
- [x] The operator successfully starts regardless of the namespace it's deployed in
- [x] The operator should not have hard-coded namespace dependencies
- [x] The metrics configuration works with the operator's deployment namespace
- [x] No startup failures due to missing hard-coded namespaces
- [x] Backward compatibility is maintained when deployed in the default namespace

## Implementation Details

### Files to be modified:
1. `main.go` - Update namespace detection logic
2. `config/config.go` - Update OperatorNamespace to be dynamic

### Environment Variables Used:
- `OPERATOR_NAMESPACE` - Already injected by the deployment manifest

### Testing Strategy:
- Deploy in default namespace (existing behavior should continue to work)
- Deploy in custom namespace (should now work without errors)
- Unit tests for namespace detection function