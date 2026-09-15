# Security Policy

## Supported versions

Security updates follow currently supported OpenShift and operator releases.
See the [OpenShift Operator Life Cycles](https://access.redhat.com/support/policy/updates/openshift_operators) policy.

## Reporting a vulnerability

Do **not** open a public GitHub issue for security vulnerabilities.

Report them to [Red Hat Product Security](https://access.redhat.com/security/team/contact).
If GitHub [private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability) is enabled on this repository, you may use that as well.

Please include:

- Affected operator version or image
- Impact and a description of the issue
- Steps to reproduce (without exploiting production systems)

## Security scanning

This repository uses:

- [Dependabot](.github/dependabot.yml) for Go modules, Docker, and GitHub Actions (`vendor/` must be updated on gomod PRs)
- [CodeQL](.github/workflows/codeql.yml) for SAST
- [gosec and govulncheck](.github/workflows/security.yml)
- [gitleaks](.github/workflows/secret-scan.yml) (and a pre-commit hook) for secret detection
