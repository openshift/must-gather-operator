# MG-368 NetworkPolicy E2E tests

NetworkPolicy cases live in `must_gather_networkpolicy_e2e_test.go` and are registered inside the main Ordered suite (`MustGather resource`) via `registerNetworkPolicyTests()`. They reuse that suite’s BeforeAll namespace, admin client, and `must-gather-serviceaccount`.

Builders, probe/SFTP clients, and constants live in the same file. Probe/gather fixtures bind the ingress probe port in the main container process (`ProbePod` / `HoldScript`). `npListener()` only waits for that socket—CRI-O reaps listeners started via `oc exec`.

## Fixtures

Optional JSON via `MG368_FIXTURES` can supply dual SFTP endpoints, proxy listeners, custom gather image, OLM Subscription name, and `isolatedOperator` for disruptive cases.

Without that file:

- `MG368_PROBE_IMAGE` or the operator’s `DEFAULT_MUST_GATHER_IMAGE` is used as the probe/gather image (needs `python3` and `/bin/bash`).
- Operator namespace/deployment default to the main suite constants.
- Dual-port SFTP, custom-image, proxy, and disruptive cases skip until fixtures (and `MG368_DISRUPTIVE=true` where required) are provided.

Credentials stay in environment variables named by the fixture JSON; do not commit secrets.

## Execution

Same entrypoint as the rest of e2e:

```bash
KUBECONFIG=/path/to/kubeconfig make test-e2e
```

Focus NetworkPolicy only:

```bash
go test -timeout 1h -count 1 -v -p 1 -tags e2e ./test/e2e \
  -ginkgo.label-filter='MG-368 && !Disruptive'
```

Disruptive cases need `isolatedOperator: true` in fixtures and `MG368_DISRUPTIVE=true`.
