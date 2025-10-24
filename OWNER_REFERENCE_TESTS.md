# Unit Test Cases for Owner Reference Management on Existing Secrets

## Overview
These test cases validate the code block (lines 230-245) in `mustgather_controller.go` that handles adding owner references to existing secrets when `retainResourcesOnCompletion` is true.

## Test Cases

### 1. **reconcile_existing_secret_adds_owner_reference_when_retain_resources_true**

**Purpose:** Verify that when a secret already exists in the operator namespace and `retainResourcesOnCompletion` is true, an owner reference is added to the secret.

**Setup:**
- MustGather CR in `user-ns` namespace with UID `mg-uid-456`
- `retainResourcesOnCompletion: true`
- User secret exists in `user-ns`
- Operator secret exists in operator namespace WITHOUT owner reference

**Expected Behavior:**
- Secret is not recreated (already exists)
- Owner reference is added to existing secret
- Secret has exactly 1 owner reference with correct UID

**Validation:**
```go
if len(operatorSecret.OwnerReferences) != 1 {
    t.Fatalf("expected one owner reference on existing secret, got %d", len(operatorSecret.OwnerReferences))
}
if operatorSecret.OwnerReferences[0].UID != "mg-uid-456" {
    t.Fatalf("expected owner reference UID to be mg-uid-456, got %s", operatorSecret.OwnerReferences[0].UID)
}
```

---

### 2. **reconcile_existing_secret_skips_owner_reference_when_already_exists**

**Purpose:** Verify that when a secret already has an owner reference, no duplicate owner reference is added.

**Setup:**
- MustGather CR in `user-ns` namespace with UID `mg-uid-789`
- `retainResourcesOnCompletion: true`
- User secret exists in `user-ns`
- Operator secret exists WITH owner reference already set

**Expected Behavior:**
- Secret is not updated (already has owner reference)
- No duplicate owner reference is added
- Update operation should NOT be called

**Validation:**
```go
// Interceptor ensures Update is never called
onUpdate: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
    if secret, ok := obj.(*corev1.Secret); ok && secret.Name == "existing-secret" {
        return errors.New("unexpected update to secret that already has owner reference")
    }
    return nil
}
```

---

### 3. **reconcile_existing_secret_skips_owner_reference_when_retain_resources_false**

**Purpose:** Verify that when `retainResourcesOnCompletion` is false, no owner reference is added to existing secrets.

**Setup:**
- MustGather CR in `user-ns` namespace with UID `mg-uid-101`
- `retainResourcesOnCompletion: false` (default)
- User secret exists in `user-ns`
- Operator secret exists WITHOUT owner reference

**Expected Behavior:**
- Secret remains without owner reference
- Update operation should NOT be called
- Secret will be manually deleted during cleanup

**Validation:**
```go
if len(operatorSecret.OwnerReferences) != 0 {
    t.Fatalf("expected no owner references when retainResourcesOnCompletion is false, got %d", len(operatorSecret.OwnerReferences))
}
```

---

### 4. **reconcile_existing_secret_owner_reference_update_fails**

**Purpose:** Verify error handling when the update operation to add owner reference fails.

**Setup:**
- MustGather CR in `user-ns` namespace with UID `mg-uid-202`
- `retainResourcesOnCompletion: true`
- User secret exists in `user-ns`
- Operator secret exists WITHOUT owner reference
- Update operation is forced to fail

**Expected Behavior:**
- Reconcile returns error
- Error is properly logged
- Reconciliation can be retried

**Validation:**
```go
// Interceptor simulates update failure
onUpdate: func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
    if secret, ok := obj.(*corev1.Secret); ok && secret.Name == "existing-secret" {
        return errors.New("failed to update secret with owner reference")
    }
    return nil
}
```

---

## Test Execution Results

All tests pass successfully:

```
=== RUN   TestReconcile/reconcile_existing_secret_adds_owner_reference_when_retain_resources_true
--- PASS: TestReconcile/reconcile_existing_secret_adds_owner_reference_when_retain_resources_true (0.00s)

=== RUN   TestReconcile/reconcile_existing_secret_skips_owner_reference_when_already_exists
--- PASS: TestReconcile/reconcile_existing_secret_skips_owner_reference_when_already_exists (0.00s)

=== RUN   TestReconcile/reconcile_existing_secret_skips_owner_reference_when_retain_resources_false
--- PASS: TestReconcile/reconcile_existing_secret_skips_owner_reference_when_retain_resources_false (0.00s)

=== RUN   TestReconcile/reconcile_existing_secret_owner_reference_update_fails
--- PASS: TestReconcile/reconcile_existing_secret_owner_reference_update_fails (0.00s)
```

## Coverage Matrix

| Scenario | retainResourcesOnCompletion | Owner Ref Exists | Expected Action | Test Case |
|----------|----------------------------|------------------|-----------------|-----------|
| Existing secret without owner ref | true | No | Add owner reference | Test 1 ✓ |
| Existing secret with owner ref | true | Yes | Skip (no duplicate) | Test 2 ✓ |
| Existing secret without owner ref | false | No | Skip (manual cleanup) | Test 3 ✓ |
| Update fails | true | No | Return error | Test 4 ✓ |

## Code Coverage

These tests specifically cover the code block at **lines 230-245** in `mustgather_controller.go`:

```go
// Secret already exists, update owner reference if needed
if instance.Spec.RetainResourcesOnCompletion && !hasOwnerReference(newSecret, instance) {
    newSecret.OwnerReferences = append(newSecret.OwnerReferences, metav1.OwnerReference{
        APIVersion: instance.APIVersion,
        Kind:       instance.Kind,
        Name:       instance.Name,
        UID:        instance.UID,
    })
    err = r.GetClient().Update(ctx, newSecret)
    if err != nil {
        log.Error(err, fmt.Sprintf("Error updating owner reference for secret %s", secretName))
        return reconcile.Result{}, err
    }
    reqLogger.Info(fmt.Sprintf("Added owner reference to existing copied secret %s", secretName))
}
```

## Integration with Overall Test Suite

Total test count: **39 tests**
- 5 cleanup tests
- 30 reconcile tests (including 4 new ones)
- 4 template tests

**All tests passing** ✓

## Key Testing Patterns Used

1. **Interceptors**: Used to simulate failures and verify operations aren't called when they shouldn't be
2. **Post-test checks**: Verify the state of resources after reconciliation
3. **Multiple namespaces**: Test cross-namespace secret copying scenarios
4. **UIDs**: Use specific UIDs to validate owner references
5. **Error injection**: Test error handling paths


