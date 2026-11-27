# MG-368 NetworkPolicy E2E tests

The dedicated NetworkPolicy e2e file (`must_gather_networkpolicy_e2e_test.go`) and `registerNetworkPolicyTests()` hook were removed. Main-suite gather/SFTP cases still exercise jobs that the operator places behind NetworkPolicy, but there are no explicit MG-368 Ginkgo cases.
