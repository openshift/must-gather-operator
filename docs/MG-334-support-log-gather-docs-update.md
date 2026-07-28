# MG-334: Support Log Gather — docs change request for technical writers

**Jira:** [MG-334](https://redhat.atlassian.net/browse/MG-334)  
**Audience:** Technical writer (Shubha Narayanan)  
**Published docs (outdated):** [Gathering cluster data — Support Log Gather (OCP 4.21)](https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html/support/gathering-cluster-data#support-log-gather-overview_gathering-cluster-data) (also in 4.22)  
**Source assembly:** `support/gathering-cluster-data.adoc` in [openshift/openshift-docs](https://github.com/openshift/openshift-docs)

This file is a **change request**, not a full chapter rewrite. Each block maps to an openshift-docs module. Proposed text is AsciiDoc-oriented so it can be pasted into modules with light house-style edits.

**Legend**

| Marker | Meaning |
|--------|---------|
| `REMOVE` | Delete this text from the module |
| `REPLACE` | Replace the current excerpt with the proposed text |
| `ADD` | Insert the proposed text (new prerequisite, note, or verification step) |

**Before publish:** Confirm the GA OLM channel name and replace `<GA_CHANNEL>` (bundle today still uses `tech-preview`).

---

## Change summary

| Module | Change | Summary |
|--------|--------|---------|
| `modules/support-log-gather-overview.adoc` | REMOVE + REPLACE | Drop Technology Preview; clarify non-admin vs Job SA permissions |
| `modules/support-log-gather-install-console.adoc` | REMOVE | Drop Technology Preview |
| `modules/support-log-gather-install-cli.adoc` | REMOVE + REPLACE | Drop Technology Preview; Subscription channel → `<GA_CHANNEL>` |
| `modules/support-log-gather-configure-cli.adoc` | REMOVE + REPLACE + ADD | Drop TP; remove invalid `proxyConfig`; expand prerequisites; add user restrictions; improve verification |
| `modules/support-log-gather-reduce-size.adoc` | REPLACE | Do not use operator service account in example |
| `modules/support-log-gather-config-params.adoc` | REPLACE + ADD | Accurate SA / audit / image notes; document non-CR proxy and trusted CA |
| `modules/support-log-gather-uninstall-console.adoc` | — | No content change |
| `modules/support-log-gather-remove-resources-console.adoc` | — | No content change |

---

## 1. Module: `modules/support-log-gather-overview.adoc`

**Section title:** About Support Log Gather  
**Published anchor:** `#support-log-gather-overview_gathering-cluster-data`

### Change: REMOVE — Technology Preview

**Current:**

```asciidoc
:FeatureName: Support Log Gather
include::snippets/technology-preview.adoc[]
```

**Proposed:** Delete both lines (feature is GA for the target release).

### Change: REPLACE — key features bullet (permissions)

**Current:**

```asciidoc
* **No administrator privileges required**: Enables you to collect and upload logs without needing elevated permissions, making it easier for non-administrators to gather data securely.
```

**Proposed:**

```asciidoc
* **Flexible permissions model**: Users do not need `cluster-admin` to create a `MustGather` custom resource. The gather job runs as the service account that you specify in the CR, and that service account must have sufficient permissions to collect diagnostic data.
```

**Why:** Non-admins can create CRs, but the Job service account still needs gather-level RBAC. The current wording overstates “no elevated permissions.”

---

## 2. Module: `modules/support-log-gather-install-console.adoc`

**Section title:** Installing Support Log Gather by using the web console

### Change: REMOVE — Technology Preview

**Current:**

```asciidoc
:FeatureName: Support Log Gather
include::snippets/technology-preview.adoc[]
```

**Proposed:** Delete both lines.

---

## 3. Module: `modules/support-log-gather-install-cli.adoc`

**Section title:** Installing Support Log Gather by using the CLI

### Change: REMOVE — Technology Preview

**Current:**

```asciidoc
:FeatureName: Support Log Gather
include::snippets/technology-preview.adoc[]
```

**Proposed:** Delete both lines.

### Change: REPLACE — Subscription channel

**Current:**

```asciidoc
spec:
  channel: tech-preview
  name: support-log-gather-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
  installPlanApproval: Automatic
```

**Proposed:**

```asciidoc
spec:
  channel: <GA_CHANNEL>
  name: support-log-gather-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
  installPlanApproval: Automatic
```

### Change: REPLACE — subscription verification example output

**Current:** channel column shows `tech-preview`

**Proposed:** channel column shows `<GA_CHANNEL>` (same value as in the Subscription).

**Why:** After GA, users must not subscribe to the tech-preview channel.

---

## 4. Module: `modules/support-log-gather-configure-cli.adoc`

**Section title:** Configuring a Support Log Gather instance  
**Change intensity:** High — use the blocks below; optionally replace the whole procedure body with the “Full proposed draft” at the end of this section.

### Change: REMOVE — Technology Preview

Delete:

```asciidoc
:FeatureName: Support Log Gather
include::snippets/technology-preview.adoc[]
```

### Change: REPLACE — Prerequisites

**Current:**

```asciidoc
.Prerequisites

* You have installed the {oc-first} tool.

* You have installed {support-log-gather} in your cluster.

* You have a Red{nbsp}Hat Support case ID.

* You have created a Kubernetes secret containing your Red Hat Customer Portal credentials. The secret must contain a username field and a password field.

* If you are using a custom image, you have configured an `ImageStream` resource in the Operator namespace that references an approved custom image URL.

* You have created a service account. If you are using a custom image, you have created a service account with permissions to access the `ImageStream` resource.
```

**Proposed:**

```asciidoc
.Prerequisites

* You have installed the {oc-first} tool.

* You have installed {support-log-gather} in your cluster.

* You have created a service account in the same namespace where you will create the `MustGather` custom resource (CR). The gather job pods run as this service account, so the service account must have sufficient permissions to collect diagnostic data from the cluster.

* If you plan to upload the must-gather archive to a Red{nbsp}Hat Support case:

** You have a Red{nbsp}Hat Support case ID.

** You have created a Kubernetes secret in the same namespace as the `MustGather` CR. The secret must contain non-empty `username` and `password` data keys for Red{nbsp}Hat Customer Portal credentials.

* Optional: If you want to persist the archive on the cluster, you have created a persistent volume claim (PVC) in the same namespace as the `MustGather` CR.

* Optional: If you are using a custom must-gather image, you have created an `ImageStream` in the Operator namespace (for example, `must-gather-operator`) that references an approved custom image. The image tag must be present and pullable in the `ImageStream` status.
```

### Change: REPLACE — Example MustGather YAML (remove invalid `proxyConfig`)

**Current problem:** The example includes `proxyConfig`, which is **not** a field on `MustGather` / `MustGatherSpec`. Proxy settings come from the Operator pod environment (cluster proxy).

**Proposed example** (custom image + upload + PVC; no `proxyConfig`):

```asciidoc
.Example `support-log-gather.yaml`
[source,yaml]
----
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: example-mg
  namespace: must-gather-operator
spec:
  serviceAccountName: must-gather-admin
  gatherSpec:
    command:
    - "/usr/bin/custom-gather"
    args:
    - "--verbose"
    - "--subsystem=network"
  imageStreamRef:
    name: "network-debug-tools"
    tag: "v1.2"
  mustGatherTimeout: "1h30m"
  uploadTarget:
    type: SFTP
    sftp:
      caseID: "04230315"
      caseManagementAccountSecretRef:
        name: mustgather-creds
      host: "sftp.access.redhat.com"
  retainResourcesOnCompletion: true
  storage:
    type: PersistentVolume
    persistentVolume:
      claim:
        name: mustgather-pvc
      subPath: must-gather-bundles/case-04230315
----
```

Optional: also show a **minimal** default-image example for common support use:

```asciidoc
.Example minimal `MustGather` CR with default image and upload
[source,yaml]
----
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: example-mg-basic
  namespace: must-gather-operator
spec:
  serviceAccountName: must-gather-admin
  uploadTarget:
    type: SFTP
    sftp:
      caseID: "04230315"
      caseManagementAccountSecretRef:
        name: mustgather-creds
----
```

### Change: ADD — User restrictions (Important / Note callouts)

Insert after the example YAML (before “Create the MustGather object”), or immediately after Prerequisites:

```asciidoc
[IMPORTANT]
====
Consider the following restrictions when you configure a `MustGather` CR:

* Do not set `spec.serviceAccountName` to the Operator service account (typically `must-gather-operator`) when the `MustGather` CR is created in the Operator namespace. The Operator rejects that combination to prevent privilege escalation. Create and use a dedicated service account instead.

* The service account named in `spec.serviceAccountName` must already exist in the namespace of the `MustGather` CR. If the service account is missing, the CR fails validation and no gather job is created.

* The `MustGather` specification is immutable after you create the CR. To change configuration, create a new `MustGather` CR.

* You cannot enable `spec.gatherSpec.audit: true` together with `spec.imageStreamRef`.

* You cannot enable `spec.gatherSpec.audit: true` together with a custom `spec.gatherSpec.command` when using the default must-gather image. Audit collection is supported only with the default image and the default gather entrypoint.

* You can set either `spec.gatherSpec.since` or `spec.gatherSpec.sinceTime`, but not both.

* There is no `proxyConfig` field on the `MustGather` CR. If your cluster uses a proxy, the Operator inherits proxy environment variables and passes them to the upload container. Trusted CA certificates for proxy TLS, when configured on the Operator, are copied into the CR namespace automatically; they are not configured on the CR.
====

[NOTE]
====
* `spec.uploadTarget` is optional. If you omit it, the Operator does not upload the archive. Combined with omitting `spec.storage`, collected data is stored on an ephemeral volume and is deleted when the pod terminates.

* When `spec.uploadTarget` is set, the Operator validates the SFTP credentials and connectivity before it creates the gather job. Authentication or network failures are reported on the CR status.

* Custom images must be referenced through an `ImageStream` in the *Operator* namespace. The `ImageStream` tag must resolve to a pullable image reference. Each `MustGather` CR supports only one custom image; create a separate CR for each additional image.

* Persistent volume claims used with `spec.storage` must already exist in the CR namespace. The Operator does not create the PVC for you.
====
```

### Change: ADD — Verification for validation failures

Add after successful verification steps:

```asciidoc
. If the gather job is not created, check the `MustGather` status for validation failures:
+
[source,terminal]
----
$ oc get mustgather example-mg -o yaml
----
+
When validation fails, `status.status` is `Failed`, `status.completed` is `true`, and `status.reason` describes the failure (for example, service account, ImageStream, or SFTP credential validation).
```

### Full proposed draft (Configure module body)

Use this if you prefer a single contiguous replacement of the procedure content (after the module title / abstract):

```asciidoc
.Prerequisites

* You have installed the {oc-first} tool.
* You have installed {support-log-gather} in your cluster.
* You have created a service account in the same namespace where you will create the `MustGather` CR. The gather job runs as this service account and must have sufficient permissions to collect diagnostic data.
* If you plan to upload the archive to a Red{nbsp}Hat Support case, you have a case ID and a secret in the CR namespace with non-empty `username` and `password` keys.
* Optional: You have created a PVC in the CR namespace if you want to persist the archive.
* Optional: For a custom image, you have created a pullable `ImageStream` tag in the Operator namespace.

.Procedure

. Create a YAML file for the `MustGather` CR, such as `support-log-gather.yaml`.
+
.Example minimal `MustGather` CR
[source,yaml]
----
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: example-mg-basic
  namespace: must-gather-operator
spec:
  serviceAccountName: must-gather-admin
  uploadTarget:
    type: SFTP
    sftp:
      caseID: "04230315"
      caseManagementAccountSecretRef:
        name: mustgather-creds
----
+
For more information on the configuration parameters, see "Configuration parameters for MustGather custom resource".

[IMPORTANT]
====
* Do not use the Operator service account as `spec.serviceAccountName` in the Operator namespace.
* The named service account must exist in the CR namespace before the job is created.
* The `MustGather` spec is immutable after creation.
* `spec.gatherSpec.audit: true` cannot be combined with `spec.imageStreamRef` or with a custom `spec.gatherSpec.command` on the default image.
* Do not set a `proxyConfig` field on the CR; proxy and trusted CA handling are provided by the Operator, not by CR fields.
====

. Create the `MustGather` object:
+
[source,terminal]
----
$ oc create -f support-log-gather.yaml
----

.Verification

. Verify that the CR exists:
+
[source,terminal]
----
$ oc get mustgather
----

. Verify that the gather job pod is running in the CR namespace:
+
[source,terminal]
----
$ oc get pods
----

. To monitor upload progress (when `uploadTarget` is set):
+
[source,terminal]
----
$ oc logs -f pod/<mustgather-pod-name> -c upload
----

. If no job is created, inspect `status.reason` on the `MustGather` CR for validation errors.
```

**Why:** Aligns published configure docs with current API and controller validation (MG-274 SA guard, SFTP preflight, no `proxyConfig`, immutable spec, audit rules).

---

## 5. Module: `modules/support-log-gather-reduce-size.adoc`

**Section title:** Configurations for reducing the must-gather log size

### Change: REPLACE — Example service account

**Current (invalid when CR is in the Operator namespace):**

```yaml
serviceAccountName: must-gather-operator
```

**Proposed:**

```yaml
serviceAccountName: must-gather-admin
```

Full proposed example:

```asciidoc
.Example `MustGather` CR configured to skip rotated logs
[source,yaml]
----
apiVersion: operator.openshift.io/v1alpha1
kind: MustGather
metadata:
  name: full-mustgather
spec:
  serviceAccountName: must-gather-admin
  gatherSpec:
    command:
      - /bin/sh
      - -c
      - |
        REDUCE_LOGS=skip_rotated_logs gather
  uploadTarget:
    type: SFTP
    sftp:
      caseID: '02527285'
      caseManagementAccountSecretRef:
        name: sftp-access-rh-creds
      internalUser: true
----
```

Optional ADD under the example:

```asciidoc
[IMPORTANT]
====
Do not set `serviceAccountName` to the Operator service account when the `MustGather` CR is in the Operator namespace. Use a dedicated service account with permissions to run must-gather.
====
```

**Why:** Controller rejects the Operator SA in the Operator namespace (MG-274).

---

## 6. Module: `modules/support-log-gather-config-params.adoc`

**Section title:** Configuration parameters for MustGather custom resource

### Change: REPLACE — `spec.serviceAccountName` description

**Current note** only mentions that `default` has minimal permissions.

**Proposed cell / note:**

```asciidoc
|`spec.serviceAccountName`
a|Optional: Specifies the name of the service account used by the gather job pods. The default value is `default`. The service account must exist in the namespace of the `MustGather` CR.

[IMPORTANT]
====
* Because the `default` service account has minimal permissions, specify a dedicated service account with sufficient permissions to collect diagnostic data.
* Do not use the Operator service account (for example, `must-gather-operator`) when the CR is in the Operator namespace. That combination is rejected.
====
|`string`
```

### Change: REPLACE — `spec.gatherSpec.audit` (keep, slightly tighten)

```asciidoc
|`spec.gatherSpec.audit`
|Optional: Specifies whether to collect audit logs. The valid values are `true` and `false`. Set this field only with the default must-gather image and the default gather entrypoint. Do not set `audit: true` with `spec.imageStreamRef`, or with a custom `spec.gatherSpec.command` on the default image.
|`boolean`
```

### Change: REPLACE — `spec.mustGatherTimeout` note

```asciidoc
|`spec.mustGatherTimeout`
|Optional: Specifies the time limit for the default gather entrypoint. The valid units are `s`, `m`, or `h`. By default, no time limit is set. If you override `spec.gatherSpec.command`, this timeout wrapper might not apply.
|Duration string
```

### Change: REPLACE — `spec.storage.persistentVolume.claim.name`

```asciidoc
|`spec.storage.persistentVolume.claim.name`
|Specifies the name of an existing PVC in the same namespace as the `MustGather` CR. The Operator does not create the PVC.
|`string`
```

### Change: ADD — Notes after the parameters table

Keep the existing ephemeral-volume note, and add:

```asciidoc
[NOTE]
====
The following settings are *not* fields on the `MustGather` CR:

* *Cluster proxy*: The Operator inherits `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` from its environment (for example, from the cluster-wide proxy) and passes them to the upload container. Do not add a `proxyConfig` stanza to the CR.
* *Trusted CA certificates*: When the Operator is configured with a trusted CA ConfigMap, the Operator copies that ConfigMap into the CR namespace and mounts it for gather and upload containers. Users do not configure trusted CA on the CR.
====

[NOTE]
====
The `MustGather` specification is immutable after creation. Create a new CR to change parameters.
====

[NOTE]
====
If you do not specify `spec.uploadTarget` or `spec.storage`, the pod saves data to an ephemeral volume and the data is permanently deleted when the pod terminates.
====
```

(Merge with the existing ephemeral note so it appears only once.)

### Change: ADD — Optional row clarification for upload secret

Under `caseManagementAccountSecretRef` / secret name, ensure docs state:

- Secret must exist in the **CR namespace**.
- Data keys must be exactly `username` and `password` (non-empty).
- Operator performs an SFTP pre-flight check before creating the job.

---

## 7. Modules with no content change

| Module | Notes |
|--------|--------|
| `modules/support-log-gather-uninstall-console.adoc` | No Technology Preview include today; leave as-is |
| `modules/support-log-gather-remove-resources-console.adoc` | Leave as-is |

---

## Appendix A — Invalid patterns to avoid in examples

Do **not** publish examples that contain:

| Invalid pattern | Reason |
|-----------------|--------|
| `proxyConfig:` on `MustGather` | Not a CR field |
| `serviceAccountName: must-gather-operator` in Operator namespace | Rejected by controller |
| `gatherSpec.audit: true` + `imageStreamRef` | CRD validation rejects |
| `gatherSpec.audit: true` + custom `gatherSpec.command` (default image) | CRD validation rejects |
| Top-level `spec.audit` (without `gatherSpec`) | Outdated API shape; use `spec.gatherSpec.audit` |

---

## Appendix B — Deferred (do not document yet)

| Topic | Reason |
|-------|--------|
| must-gather-clean / obfuscation | Tracked under MG-297 / MG-157; not ready for user docs |
| ValidatingAdmissionPolicy requiring `use` on the ServiceAccount | Not shipped in current operator manifests |
| Automatic deletion of the `MustGather` CR after ~6 hours | Not implemented in the current controller; Job/pod cleanup on completion is controlled by `retainResourcesOnCompletion` |

---

## Appendix C — Writer checklist

- [ ] Confirm GA OLM channel; replace every `<GA_CHANNEL>` placeholder
- [ ] Remove all `include::snippets/technology-preview.adoc[]` from Support Log Gather modules
- [ ] Remove `proxyConfig` from configure example
- [ ] Fix reduce-size example SA
- [ ] Add Important/Note restriction callouts to configure + config-params
- [ ] Verify examples against target OCP version (4.21 / 4.22 / GA target)
- [ ] Open openshift-docs PR and link it on [MG-334](https://redhat.atlassian.net/browse/MG-334)

**Engineering contact for fact-check:** Must Gather / Support Log Gather operator owners; API source of truth: `api/v1alpha1/mustgather_types.go` in [openshift/must-gather-operator](https://github.com/openshift/must-gather-operator).
