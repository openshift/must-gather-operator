# Platform Ecosystem References

Links to generic OpenShift/Kubernetes patterns in the Platform ecosystem hub. The must-gather-operator inherits these platform-wide patterns and practices.

## Operator Patterns

**Location**: [openshift/enhancements/ai-docs/platform/operator-patterns/](https://github.com/openshift/enhancements/tree/master/ai-docs/)

- **Controller Runtime**: Reconciliation loops, event handling, client patterns
- **Status Conditions**: Available, Progressing, Degraded condition semantics
- **Finalizers**: Resource cleanup patterns
- **RBAC**: Service account and permissions

**Component Usage**:
- Single controller-runtime reconciler (`MustGatherReconciler`)
- Uses `operator-utils` `ReconcilerBase` for condition management (`ManageError`, `ManageSuccess`)
- Finalizer for Job/Pod/ConfigMap cleanup on CR deletion
- See [architecture/components.md](../architecture/components.md) for component-specific patterns

## Testing Practices

**Location**: [openshift/enhancements/ai-docs/practices/testing/](https://github.com/openshift/enhancements/tree/master/ai-docs/)

- **Test Pyramid**: Unit > Integration > E2E
- **E2E Framework**: OpenShift E2E test patterns

**Component Usage**:
- Unit tests: controller-runtime `fake` client with `interceptClient` wrapper, template generation tests
- E2E tests: Ginkgo-based, 1h timeout, `-p 1` (serial)
- See [MGO_TESTING.md](../MGO_TESTING.md) for component-specific test suites

## Security Practices

**Location**: [openshift/enhancements/ai-docs/practices/security/](https://github.com/openshift/enhancements/tree/master/ai-docs/)

- **RBAC Guidelines**: Role and ClusterRole design

**Component Usage**:
- `must-gather-admin` ClusterRole grants `*` on all resources (required for diagnostic collection)
- SFTP credentials handled via Kubernetes Secrets (SecretKeyRef)
- SSH host key verification intentionally disabled (known trade-off)
- FIPS mode via `GOEXPERIMENT=boringcrypto` at build time

## Reliability Practices

**Location**: [openshift/enhancements/ai-docs/practices/reliability/](https://github.com/openshift/enhancements/tree/master/ai-docs/)

- **Observability**: Metrics, logging patterns

**Component Usage**:
- Two Prometheus counters: `must_gather_operator_must_gather_total`, `must_gather_operator_must_gather_errors`
- OSD custom metrics server on port 8080 (controller-runtime built-in metrics disabled)

## Kubernetes Fundamentals

**Location**: [openshift/enhancements/ai-docs/domain/kubernetes/](https://github.com/openshift/enhancements/tree/master/ai-docs/)

- **Jobs**: Batch job lifecycle, backoff limits
- **CRDs**: CustomResourceDefinition patterns, CEL validation

**Component Usage**:
- Jobs with backoffLimit=3, restartPolicy=Never, ShareProcessNamespace
- CRD with extensive CEL validation (immutable spec, union types, mutual exclusion)

## OpenShift Fundamentals

**Location**: [openshift/enhancements/ai-docs/domain/openshift/](https://github.com/openshift/enhancements/tree/master/ai-docs/)

- **ImageStreams**: Image management and tagging
- **Cluster Proxy**: Proxy configuration propagation

**Component Usage**:
- ImageStream-based image allowlisting for custom must-gather images
- Cluster proxy settings forwarded to upload container and SFTP validation

## Cross-Repository ADRs

**Location**: [openshift/enhancements/ai-docs/decisions/](https://github.com/openshift/enhancements/tree/master/ai-docs/)

Component-specific ADRs are in [ai-docs/decisions/](../decisions/).

---

**Note**: Platform documentation links point to the openshift/enhancements ecosystem hub (planned/in-progress). Component-specific patterns are in this repository's `ai-docs/` directory.

**Last Updated**: 2026-07-28
