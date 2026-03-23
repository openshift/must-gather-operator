# E2E Test Suggestions: must-gather-operator

## Summary

**Repository**: github.com/openshift/must-gather-operator
**Framework**: controller-runtime (Ginkgo/Gomega based tests)
**Base Branch**: origin/release-4.21
**Changes Analyzed**: EP-1923 - Time-based log filtering feature

## Detected Changes

### API Type Changes
- **File**: `api/v1alpha1/mustgather_types.go`
- **Changes**:
  - New `GatherSpec` type with two fields:
    - `Since *metav1.Duration` - Relative duration for log filtering
    - `SinceTime *metav1.Time` - Absolute RFC3339 timestamp for log filtering
  - Added `GatherSpec *GatherSpec` field to `MustGatherSpec`
  - Comprehensive godoc comments explaining behavior and environment variable mapping

### CRD Schema Changes
- **File**: `deploy/crds/operator.openshift.io_mustgathers.yaml`
- **Changes**:
  - Added `gatherSpec` object to MustGather CRD schema
  - `since` field with `format: duration` validation
  - `sinceTime` field with `format: date-time` validation
  - Detailed descriptions for user documentation

### Controller Implementation Changes
- **File**: `controllers/mustgather/template.go`
- **Changes**:
  - Modified `getGatherContainer()` function signature to accept `gatherSpec` parameter
  - Added logic to set `MUST_GATHER_SINCE` environment variable when `gatherSpec.Since != nil`
  - Added logic to set `MUST_GATHER_SINCE_TIME` environment variable when `gatherSpec.SinceTime != nil`
  - Proper nil-safety checks for optional fields

### Test Coverage Added
- **File**: `api/v1alpha1/tests/mustgathers.operator.openshift.io/mustgather.testsuite.yaml`
- **Changes**:
  - 13 API integration test cases (9 onCreate, 3 onUpdate)
  - Tests for duration formats, timestamps, validation, immutability

- **File**: `controllers/mustgather/template_test.go`
- **Changes**:
  - 5 new unit tests for `getGatherContainer()` with gatherSpec
  - Tests for nil safety, duration/timestamp formatting, environment variable setting

## Highly Recommended E2E Scenarios

### Priority 1: Core Functionality (MUST HAVE)

#### 1. MustGather with `since` Duration
**Why**: Directly tests the primary use case from EP-1923
**What to verify**:
- CR with `gatherSpec.since: "2h"` is accepted
- Job is created with `MUST_GATHER_SINCE=2h0m0s` environment variable
- Job completes successfully (assuming logs exist)

**Risk if not tested**: Core feature doesn't work

#### 2. MustGather with `sinceTime` Timestamp
**Why**: Tests the absolute timestamp filtering mode
**What to verify**:
- CR with `gatherSpec.sinceTime: "2026-03-23T10:00:00Z"` is accepted
- Job is created with `MUST_GATHER_SINCE_TIME=2026-03-23T10:00:00Z` environment variable
- RFC3339 format is preserved correctly

**Risk if not tested**: Timestamp-based filtering doesn't work

#### 3. Backward Compatibility (No gatherSpec)
**Why**: Ensures existing MustGather CRs continue to work
**What to verify**:
- CR without `gatherSpec` is accepted
- Job is created WITHOUT `MUST_GATHER_SINCE` or `MUST_GATHER_SINCE_TIME` env vars
- Default behavior (full log collection) is maintained

**Risk if not tested**: Breaking change to existing functionality

#### 4. Environment Variable Verification
**Why**: Controller implementation correctness
**What to verify**:
- When `gatherSpec` is present, env vars are set in the Job's gather container
- When `gatherSpec` is nil, no env vars are added
- Values are correctly formatted (duration as "2h0m0s", time as RFC3339)

**Risk if not tested**: Controller bug goes undetected, feature appears to work but doesn't

### Priority 2: Validation and Edge Cases (SHOULD HAVE)

#### 5. Invalid Duration Format Rejection
**Why**: Ensures API validation works
**What to verify**:
- CR with `since: "invalid-duration"` is rejected by API server
- Error message is clear

**Risk if not tested**: Invalid input accepted, leads to Job failures

#### 6. Various Duration Formats
**Why**: Kubernetes supports multiple duration formats
**What to verify**:
- "30m", "300s", "24h", "1h30m" are all accepted
- Normalized correctly in environment variables

**Risk if not tested**: Some valid formats might not work

#### 7. Both `since` and `sinceTime` Together
**Why**: EP documents that both can be specified (sinceTime takes precedence in gather script)
**What to verify**:
- CR with both fields is accepted
- Both env vars are set in the Job
- No validation error (API allows both, gather script handles precedence)

**Risk if not tested**: Unexpected validation error or missing env var

### Priority 3: Integration (SHOULD HAVE)

#### 8. gatherSpec with Upload Target
**Why**: Ensures time filtering works with existing upload feature
**What to verify**:
- CR with both `gatherSpec` and `uploadTarget` is accepted
- Job has both gather and upload containers
- Both sets of environment variables are present

**Risk if not tested**: Feature conflicts with existing functionality

#### 9. gatherSpec with Proxy Configuration
**Why**: Common configuration combination
**What to verify**:
- CR with both `gatherSpec` and `proxyConfig` works
- Both proxy env vars and time filter env vars are set

**Risk if not tested**: Configuration conflicts

#### 10. gatherSpec with Custom Timeout
**Why**: Timeout and time filtering are related concepts
**What to verify**:
- CR with both `gatherSpec` and `mustGatherTimeout` works
- Job timeout and log time filtering are independent

**Risk if not tested**: Misunderstanding of feature interaction

### Priority 4: Lifecycle and Cleanup (NICE TO HAVE)

#### 11. Complete Lifecycle
**Why**: Ensures end-to-end flow works
**What to verify**:
- MustGather transitions through statuses correctly
- Job runs to completion
- Status.Completed becomes true
- Cleanup occurs (if `retainResourcesOnCompletion` is false)

**Risk if not tested**: Status updates or cleanup issues

#### 12. Immutability Check
**Why**: MustGather spec is immutable per CRD validation
**What to verify**:
- Attempt to update `gatherSpec` after creation is rejected
- Error message indicates immutability

**Risk if not tested**: Spec can be modified unexpectedly

## Optional/Nice-to-Have Scenarios

### 13. Negative Test: Negative Duration
**Why**: Edge case validation
**What**: Try `since: "-2h"` - should be rejected

### 14. Extreme Values
**Why**: Boundary testing
**What**: Try very long duration like `since: "720h"` (30 days) - should work

### 15. Future Timestamp
**Why**: sinceTime validation
**What**: Use a timestamp far in the future - should work (no logs collected)

### 16. Past Timestamp Beyond Cluster Age
**Why**: Practical edge case
**What**: Use a timestamp before cluster creation - should collect all available logs

## Gaps and Limitations

### Cannot Test Directly in E2E:

1. **Actual Log Filtering Behavior**:
   - E2E can verify env vars are set, but can't verify the gather script actually filters logs
   - **Workaround**: Requires integration test with real must-gather image or manual verification

2. **Archive Size Reduction**:
   - Cannot programmatically verify archive is smaller with time filtering
   - **Workaround**: Manual inspection or separate performance test

3. **Gather Script Compatibility**:
   - Assumes must-gather image supports `MUST_GATHER_SINCE` and `MUST_GATHER_SINCE_TIME`
   - **Workaround**: Document required must-gather image version

4. **Upload with Filtered Logs**:
   - End-to-end upload test requires real SFTP credentials
   - **Workaround**: Mock SFTP or use test credentials

## Test Execution Recommendations

### For CI/CD Pipeline:
- **Include**: Priority 1 tests (scenarios 1-4)
- **Optionally include**: Priority 2 tests (scenarios 5-7)
- **Skip**: Integration tests requiring real SFTP (scenario 8 upload part)

### For Manual/Pre-Release Testing:
- **Run all**: Priority 1-3 scenarios
- **Verify manually**: Actual log filtering behavior, archive size reduction
- **Test on live cluster**: Upload functionality with real credentials

### For Regression Testing:
- **Focus on**: Backward compatibility (scenario 3)
- **Check**: No environment variables added when gatherSpec is nil (scenario 4)

## Integration into Existing Test Suite

The existing e2e test file is:
- `test/e2e/must_gather_operator_runner_test.go`
- Package: `osde2etests`

**Recommended approach**:
1. Copy relevant `Describe/Context/It` blocks from `e2e_test.go` into the existing test file
2. Ensure `k8sClient` and other test fixtures match the existing test setup
3. Run tests with existing test suite: `make test-e2e` or similar
4. Each test is independent and can be run individually or as a suite

## Summary of Test Value

| Scenario | Priority | Effort | Value | Recommended |
|----------|----------|--------|-------|-------------|
| since duration | P1 | Low | High | ✅ YES |
| sinceTime timestamp | P1 | Low | High | ✅ YES |
| Backward compatibility | P1 | Low | High | ✅ YES |
| Env var verification | P1 | Low | High | ✅ YES |
| Invalid duration | P2 | Low | Medium | ✅ YES |
| Duration formats | P2 | Low | Medium | ✅ YES |
| Both fields together | P2 | Low | Medium | ⚠️ Consider |
| With upload target | P3 | Medium | Medium | ⚠️ Consider |
| With proxy | P3 | Low | Low | ⏭️ Skip |
| With timeout | P3 | Low | Low | ⏭️ Skip |
| Complete lifecycle | P4 | Medium | Medium | ⚠️ Consider |
| Immutability | P4 | Low | Medium | ⚠️ Consider |

**Legend**:
- ✅ YES: Highly recommended, include in CI
- ⚠️ Consider: Useful but not critical, include if time permits
- ⏭️ Skip: Low value or covered by other tests

## Conclusion

The generated E2E tests provide comprehensive coverage of the time-based log filtering feature (EP-1923). **Minimum recommended test set** includes scenarios 1-4 (Priority 1), which covers the core functionality and backward compatibility. This ensures the feature works as designed and doesn't break existing behavior.
