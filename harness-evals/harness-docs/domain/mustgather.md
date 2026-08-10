# MustGather

**API Group**: `operator.openshift.io/v1alpha1`
**Kind**: `MustGather`
**Scope**: Namespaced
**CRD name**: `mustgathers.operator.openshift.io`

## Purpose

Declares a must-gather diagnostic collection job. The operator creates a Kubernetes Job with a gather container and, when `uploadTarget` is configured, an upload container. It collects cluster diagnostics and optionally uploads the compressed archive to Red Hat SFTP for case management.

**Key Principle**: Spec is **immutable once set** (enforced via CEL: `!has(oldSelf.spec) || self.spec == oldSelf.spec`). To change parameters, delete and recreate the CR.

## Spec Structure

```go
// api/v1alpha1/mustgather_types.go
type MustGatherSpec struct {
    ServiceAccountName          string               `json:"serviceAccountName"`              // Required, MinLength=1
    ImageStreamRef              *ImageStreamTagRef    `json:"imageStreamRef,omitempty"`         // Custom gather image
    GatherSpec                  *GatherSpec           `json:"gatherSpec,omitempty"`             // Gather parameters
    MustGatherTimeout           *metav1.Duration      `json:"mustGatherTimeout,omitempty"`      // Format=duration
    UploadTarget                *UploadTargetSpec     `json:"uploadTarget,omitempty"`           // Where to upload
    RetainResourcesOnCompletion *bool                 `json:"retainResourcesOnCompletion,omitempty"` // Default: false
    Storage                     *Storage              `json:"storage,omitempty"`                // PVC storage option
}
```

| Field | Type | Required | Default | Notes |
|-------|------|----------|---------|-------|
| `serviceAccountName` | `string` | Yes | — | SA with cluster read access for gather container |
| `imageStreamRef` | `ImageStreamTagRef` | No | nil (uses `DEFAULT_MUST_GATHER_IMAGE` env var) | Custom must-gather image via ImageStreamTag |
| `gatherSpec` | `GatherSpec` | No | nil | Audit mode, custom commands, time filters |
| `mustGatherTimeout` | `metav1.Duration` | No | no limit | Timeout for gather container |
| `uploadTarget` | `UploadTargetSpec` | No | nil (no upload) | SFTP upload configuration |
| `retainResourcesOnCompletion` | `*bool` | No | `false` | If true, skip garbage collection of Job/Pods |
| `storage` | `Storage` | No | nil (ephemeral `emptyDir`) | PVC-backed storage for gather output |

## Field Conventions

- **Required fields**: Use `+kubebuilder:validation:Required`. For strings, pair with `+kubebuilder:validation:MinLength=1` to reject empty values. Non-pointer type, no `omitempty`.
- **Optional fields**: Use `+kubebuilder:validation:Optional` with pointer types and `omitempty` JSON tags.
- **Boolean flags**: Default-false booleans use `+kubebuilder:default:=false`. Use `*bool` for optional booleans needing three-state semantics.
- **New required spec fields**: Must provide `+kubebuilder:default` or use `+optional` to avoid breaking existing CRs (spec is immutable and `+kubebuilder:validation:Required`).

## Sub-Types

### GatherSpec

```go
type GatherSpec struct {
    Audit     bool             `json:"audit,omitempty"`     // Collect audit logs
    Command   []string         `json:"command,omitempty"`   // MaxItems=256, Items:MaxLength=256
    Args      []string         `json:"args,omitempty"`      // MaxItems=256, Items:MaxLength=256
    Since     *metav1.Duration `json:"since,omitempty"`     // Mutually exclusive with SinceTime
    SinceTime *metav1.Time     `json:"sinceTime,omitempty"` // Mutually exclusive with Since
}
```

### UploadTargetSpec (discriminated union)

```go
// UploadTargetSpec defines the desired state of UploadTargetSpec
// +kubebuilder:validation:XValidation:rule="has(self.type) && self.type == 'SFTP' ? has(self.sftp) : !has(self.sftp)",message="sftp upload target config is required when upload type is SFTP, and forbidden otherwise"
// +union
type UploadTargetSpec struct {
	// type defines the method used for uploading to a specific target.
	// +unionDiscriminator
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=SFTP
	// +required
	Type UploadType `json:"type"`

	// SFTP details for the upload.
	// +unionMember
	// +optional
	SFTP *SFTPSpec `json:"sftp,omitempty"`
}
```

Uses `// +union` on struct, `// +unionDiscriminator` on `Type`, `// +unionMember` on variants. The XValidation CEL rule enforces discriminator-to-member consistency. `+kubebuilder:validation:Required` and `+required` ensure the discriminator field is always present.

**Adding a new upload type**: Add `UploadType` const, `+unionMember` pointer field, extend `Enum` list, update CEL rule.

`Storage` uses `StorageType` with `Enum=PersistentVolume` but lacks full `+union` markers. New storage types should follow the `UploadTargetSpec` union pattern.

### SFTPSpec

| Field | Type | Required | Default |
|-------|------|----------|---------|
| `caseID` | `string` | Yes | — |
| `caseManagementAccountSecretRef` | `LocalObjectReference` | Yes | — |
| `internalUser` | `bool` | No | `false` |
| `host` | `string` | No | `"sftp.access.redhat.com"` |

### Storage

```go
type Storage struct {
    Type             StorageType            `json:"type"`             // Enum=PersistentVolume
    PersistentVolume PersistentVolumeConfig `json:"persistentVolume"` // PVC name + optional subPath
}
```

## Status

```go
type MustGatherStatus struct {
    Status     string             `json:"status,omitempty"`
    LastUpdate metav1.Time        `json:"lastUpdate,omitempty"`
    Reason     string             `json:"reason,omitempty"`
    Conditions []metav1.Condition `json:"conditions,omitempty"`
    Completed  bool               `json:"completed"`
}
```

Implements `ConditionsAware` interface via `GetConditions()`/`SetConditions()` (from `operator-utils`).

Status subresource enabled via `+kubebuilder:subresource:status`. `Completed` is non-pointer, always serialized (`json:"completed"` without `omitempty`). Conditions use `patchStrategy:"merge"` with `patchMergeKey:"type"`. Controller uses `apimeta.SetStatusCondition`. New condition types should follow CamelCase naming.

### Condition Types

| Type | Reason | When Set |
|------|--------|----------|
| `ReconcileSuccess` | `LastReconcileCycleSucceded` | Reconcile completes without error |
| `ReconcileError` | `LastReconcileCycleFailed` | Reconcile fails |
| `ReconcileError` | `ValidationFailed` | Validation fails: missing/invalid ServiceAccount, SFTP credential fields, SFTP connectivity, or ImageStream resolution |

## CEL Validation Rules

CEL rules are applied at four levels: top-level type (`MustGather`), spec struct (`MustGatherSpec`), nested structs (`GatherSpec`, `SFTPSpec`, `UploadTargetSpec`), and individual fields.

1. **Immutable spec** (type-level): `!has(oldSelf.spec) || self.spec == oldSelf.spec`
2. **Audit + custom image** (spec-level): Audit mode only works with default image (no `imageStreamRef`)
3. **Audit + custom commands** (spec-level): Audit mode cannot combine with custom `gatherSpec.command`
4. **Since exclusivity** (nested): Only one of `since` or `sinceTime` — pattern: `!(has(self.fieldA) && has(self.fieldB))`
5. **SFTP union** (nested): `sftp` field required when `type=SFTP`, forbidden otherwise
6. **Non-empty caseID/secretRef** (field): `size(self.caseID) > 0` complements `+kubebuilder:validation:Required`
7. **DNS name validation** (field): `!format.dns1123Subdomain().validate(self).hasValue()`

Cross-struct validation (e.g., audit + imageStreamRef) goes on the parent struct since CEL cannot reference sibling structs. Always provide a human-readable `message` with every `XValidation` rule.

**Field validation markers**: String arrays use `MaxItems=256` + `Items:MaxLength=256`. Duration fields use `Format=duration` with `*metav1.Duration`. Timestamps use `Format=date-time` with `*metav1.Time`. Name references use `MaxLength=253`. Secrets use `corev1.LocalObjectReference` (same-namespace only).

No admission webhooks — validation is CEL rules on the CRD plus controller-side validation (`controllers/mustgather/validation.go`).

## Lifecycle

1. **Creation**: Controller creates a Job with a gather container (always) and an upload container (only when `uploadTarget` is configured); credentials are accessed directly via SecretKeyRef from the CR namespace (no secret replication)
2. **Running**: Job executes gather → upload pipeline; controller monitors via Job status
3. **Completion**: Status updated, `completed=true`; cleanup runs immediately (Jobs, Pods, trusted CA ConfigMaps) unless `retainResourcesOnCompletion=true`
4. **Deletion**: Finalizer cleans up Job, Pods, and trusted CA ConfigMap ownerReferences

## Example: Basic SFTP Upload

```yaml
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: must-gather-basic
spec:
  serviceAccountName: must-gather-admin
  uploadTarget:
    type: SFTP
    sftp:
      caseID: "02527285"
      caseManagementAccountSecretRef:
        name: case-management-creds
      internalUser: true
```

## Example: With PVC Storage and Timeout

```yaml
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: must-gather-pvc
spec:
  serviceAccountName: must-gather-admin
  mustGatherTimeout: "30m"
  storage:
    type: PersistentVolume
    persistentVolume:
      claim:
        name: must-gather-pvc
      subPath: must-gather-data
  uploadTarget:
    type: SFTP
    sftp:
      caseID: "02527285"
      caseManagementAccountSecretRef:
        name: case-management-creds
```

## Naming Conventions

- JSON field names: camelCase (`serviceAccountName`, `imageStreamRef`, `caseID`, `sinceTime`)
- Acronyms: `caseID` (not `caseId`), `sftp` (lowercase field), `SFTP` (uppercase type/const)
- Enum types: PascalCase with `Type` suffix (`UploadType`, `StorageType`)
- Enum consts: `TypeNameValue` pattern (`UploadTypeSFTP`, `StorageTypePersistentVolume`)

## Controller-Level Validation Beyond CRD

The controller performs runtime validations that CEL cannot express:
- ServiceAccount existence check (API lookup)
- Secret existence and field validation (`username`/`password` keys non-empty)
- SFTP credential validation (live connection test with retry)
- ImageStream tag lookup and pullability check
- Rejection of the operator's own ServiceAccount

These set status to `Failed`, `Completed=true`, emit `ReconcileError` with `ValidationFailed` reason. Follow this pattern for new runtime validations.

## Finalizer Convention

Finalizer name: `finalizer.mustgathers.operator.openshift.io` (pattern: `finalizer.<plural>.<group>`). Cleanup verifies ownership via `OwnerReferences` before deletion. The MustGather primary resource only reconciles on generation or finalizer changes (Job status and named ConfigMap events also trigger reconciliation via separate watches), so spec-only updates after immutability rejection do not trigger reconciliation loops.

## RBAC Markers

RBAC markers go on the controller's `Reconcile` method in `mustgather_controller.go`, not on the types file. Pattern: `get;list;watch;create;update;patch;delete` on the primary resource, with `status` and `finalizers` subresources as separate markers.

## Related

- [Architecture](../architecture/components.md) — Controller reconciliation flow and Job template details
- [ADR-0001](../decisions/adr-0001-immutable-spec.md) — Why spec is immutable
