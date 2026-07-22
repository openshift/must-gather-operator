# API Contracts Guidelines

Rules and conventions for the MustGather CRD (`operator.openshift.io/v1alpha1`).

## Spec Immutability

The entire `spec` is immutable after creation via a top-level CEL rule on the `MustGather` type:

```
+kubebuilder:validation:XValidation:rule="!has(oldSelf.spec) || self.spec == oldSelf.spec",message="spec values are immutable once set"
```

Do NOT add per-field immutability markers. All spec fields are covered by this single rule.
When adding a new spec field, it is automatically immutable -- no additional annotation is needed.

## Union Types and Discriminators

This repo uses the OpenShift union pattern with `+union`, `+unionDiscriminator`, and `+unionMember` markers.

`UploadTargetSpec` is the canonical example:
- The `type` field is the discriminator (`+unionDiscriminator`), marked `+kubebuilder:validation:Required` and constrained with `+kubebuilder:validation:Enum`.
- Each member (e.g., `sftp`) is tagged `+unionMember` and `+optional`.
- A CEL rule enforces that the member matching the discriminator is present, and others are absent:
  ```
  rule="has(self.type) && self.type == 'SFTP' ? has(self.sftp) : !has(self.sftp)"
  ```

When adding a new upload type (or any union variant):
1. Add the new constant to the `UploadType` (or equivalent) type and the `Enum` list on the discriminator.
2. Add the new member struct field with `+unionMember` and `+optional`.
3. Extend the CEL rule to include the new variant's presence/absence logic.

`Storage` follows the same pattern with `StorageType` enum (`PersistentVolume` only today).

## CEL Validation Rules (XValidation)

Cross-field rules live as `+kubebuilder:validation:XValidation` on the parent struct, not on individual fields.

Existing rules and where they live:
- **MustGather (top-level)**: spec immutability (see above).
- **MustGatherSpec**: two rules preventing `audit` when a custom image or custom command is set.
- **GatherSpec**: mutual exclusivity of `since` and `sinceTime`.
- **SFTPSpec**: non-empty `caseID` and non-empty `caseManagementAccountSecretRef.name`.
- **UploadTargetSpec**: union discriminator enforcement (see above).
- **PersistentVolumeClaimReference.Name**: DNS-1123 subdomain validation via `format.dns1123Subdomain()`.

When adding a new cross-field constraint, place the `XValidation` marker on the **lowest struct** that contains all referenced fields. Keep the `message` user-friendly and specific.

## Field Validation Patterns

### Required vs Optional
- Use `+kubebuilder:validation:Required` for fields that must be provided (e.g., `caseID`, union discriminator `type`).
- Use `+kubebuilder:validation:Optional` for fields with sensible defaults or that are truly optional.
- Use `+optional` (the shorthand) when the field has no kubebuilder-specific validation beyond optionality.

### Defaults
Defaults are declared via `+kubebuilder:default`:
- `serviceAccountName` defaults to `"default"`.
- `internalUser` defaults to `false`.
- `retainResourcesOnCompletion` defaults to `false`.
- `host` defaults to `"sftp.access.redhat.com"`.

Pointer fields (`*bool`, `*metav1.Duration`) use nil to mean "not set" -- the controller treats nil as the default behavior (e.g., nil `retainResourcesOnCompletion` means cleanup).

### Format Constraints
- Duration fields use `+kubebuilder:validation:Format=duration` (e.g., `mustGatherTimeout`, `since`).
- Timestamp fields use `+kubebuilder:validation:Format=date-time` (e.g., `sinceTime`).
- DNS names use CEL `format.dns1123Subdomain()` rather than a kubebuilder Format marker.

### Size Limits
String arrays (`command`, `args`) are constrained with:
- `+kubebuilder:validation:MaxItems=256` on the slice.
- `+kubebuilder:validation:Items:MaxLength=256` on each element.

String fields referencing Kubernetes names use `+kubebuilder:validation:MaxLength=253` (DNS subdomain limit).

When adding new string or array fields, always add MaxLength/MaxItems bounds to prevent abuse.

## Status Conventions

### Status Fields
`MustGatherStatus` has:
- `status` (string): `"Completed"` or `"Failed"`.
- `completed` (bool): `true` once the job finishes (success or failure).
- `reason` (string): human-readable explanation.
- `lastUpdate` (metav1.Time): timestamp of last status change.
- `conditions` ([]metav1.Condition): standard Kubernetes conditions with `patchStrategy:"merge"` and `patchMergeKey:"type"`.

### Condition Types
The repo uses a single condition type: `ReconcileError` (set by `setValidationFailureStatus`).
- `Status`: `metav1.ConditionTrue` when an error is active.
- `Reason`: `"ValidationFailed"` for pre-flight validation errors.
- `Message`: includes the validation type prefix (e.g., "SFTP validation failed: ...").
- `ObservedGeneration`: always set to `instance.GetGeneration()`.

Use `apimeta.SetStatusCondition` for upserts. The controller implements `GetConditions`/`SetConditions` on `MustGather` to support operator-utils.

### Validation Types (for status messages)
Defined as constants in `controllers/mustgather/constant.go`:
- `ValidationServiceAccount` = `"Service Account"`
- `ValidationSFTPCredentials` = `"SFTP credentials"`
- `ValidationImageStream` = `"ImageStream"`
- `ProtocolSFTP` = `"SFTP"` (used for connection validation failures)

When adding a new validation check, add a corresponding constant and call `setValidationFailureStatus`.

## Controller Ownership

### OwnerReferences
- Jobs created by the controller are owned by the MustGather CR via `CreateResourceIfNotExists` (from operator-utils), which sets OwnerReferences automatically.
- TrustedCA ConfigMaps use **manual OwnerReferences** (not controller-runtime's `controllerutil.SetOwnerReference`) to support multiple MustGather owners on the same ConfigMap in a namespace.
- Cleanup verifies ownership by checking `ref.UID == instance.UID` before deleting a Job -- never delete a Job without confirming it is owned by the current MustGather instance.

### Job Naming
Jobs are named identically to the MustGather CR (`instance.Name`) and created in the same namespace. This 1:1 naming means only one Job per MustGather CR.

## Finalizer Pattern

Finalizer name: `finalizer.mustgathers.operator.openshift.io`

Lifecycle:
1. Added on first reconcile if absent.
2. On deletion (DeletionTimestamp set), the finalizer triggers cleanup:
   - Delete Job (only if owned by this instance) and its Pods.
   - Delete copied TrustedCA ConfigMap (remove OwnerReference or delete if last owner).
   - Cleanup is skipped if `retainResourcesOnCompletion` is `true`.
3. Finalizer is removed after successful cleanup (or skip), allowing Kubernetes to delete the CR.

Never remove the finalizer before cleanup completes. On cleanup failure, return an error to retry on next reconcile.

## Reconciliation Predicates

The controller filters events to avoid unnecessary reconciles:
- **MustGather**: only reconcile on `generation` change or `finalizer` change (via `resourceGenerationOrFinalizerChangedPredicate`).
- **Owned Jobs**: only reconcile on `status` changes (via `isStateUpdated`).
- **Owned ConfigMaps**: only reconcile for the specific TrustedCA ConfigMap name (via `isNameEquals`).

Status-only updates do NOT increment `generation`, so status writes do not trigger re-reconcile.

## ImageStreamRef Pattern

`ImageStreamRef` is an optional pointer to `ImageStreamTagRef{Name, Tag}`. When nil, the controller uses the default must-gather image from `DEFAULT_MUST_GATHER_IMAGE` env var. When set:
- The controller looks up the ImageStream in the **operator namespace** (not the CR namespace).
- It resolves the tag to a pullable `DockerImageReference` from `imageStream.Status.Tags`.
- Validation failure (ImageStream not found, tag not found, tag not pullable) results in `ValidationImageStream` failure status -- no Job is created.

## Adding a New Spec Field Checklist

1. Add the field to `MustGatherSpec` (or a nested struct) in `api/v1alpha1/mustgather_types.go`.
2. Add kubebuilder markers: `+kubebuilder:validation:Optional` or `Required`, `+kubebuilder:default` if applicable, size/format constraints.
3. If the field interacts with other fields, add a CEL `XValidation` rule on the containing struct.
4. Run `make generate && make manifests`.
5. Update `getJobTemplate()` / `getGatherContainer()` / `getUploadContainer()` in `controllers/mustgather/template.go` if the field affects Job creation.
6. Add unit tests in `template_test.go` and `mustgather_controller_test.go`.
7. The field is automatically immutable (covered by the top-level spec immutability rule).

## Testing API Changes

- Unit tests use `fake.NewClientBuilder().WithScheme(s).WithObjects(...).WithStatusSubresource(&MustGather{}).Build()` -- always include `WithStatusSubresource` when testing status updates.
- The `interceptClient` pattern in `mustgather_controller_test.go` allows injecting failures for specific CRUD operations -- use this for error path testing.
- E2E tests live in `test/e2e/` with `//go:build e2e` tags and use real cluster clients via `controller-runtime/pkg/client/config`.
- CRD validation (CEL rules) can only be fully tested via integration tests against a real API server, since the fake client does not enforce admission webhooks or CEL.
