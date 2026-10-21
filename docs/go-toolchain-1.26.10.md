# Go toolchain 1.26.10

`go.mod` requires `go 1.26.10`. Since Go 1.21 the `go` line is also a minimum
toolchain version, patch level included, so every build path must run 1.26.10 or
newer. The Go base images are therefore pinned to that patch
(`golang:1.26.10-alpine`, `golang:1.26.10-bookworm`) instead of the floating
`1.26` tag: an image older than the pin cannot build, and the upgrade sandbox
(`GOTOOLCHAIN=local`) and native-runtime checks (`--network none`) cannot fetch a
toolchain at build time.

## What 1.26.10 changes for certificate verification

1.26.10 is a security-only release for `crypto/x509`. It adds a hardcoded
denylist of eight intermediate CA public keys (CVE-2026-111943, the compromised
eMudhra emSign intermediates). `Certificate.Verify` drops any candidate chain
that contains one of those keys.

Two properties matter when upgrading:

- There is no setting to disable the filter.
- It applies to every chain the verifier builds, including chains anchored in a
  custom root pool. A private deployment that deliberately trusts one of those
  keys fails as well.

## Symptom and response

Affected connections fail with:

```
x509: no valid chains built
```

That covers panel certificate readiness checks, IP quality batch verification and
any outbound TLS handshake that validates a chain through one of the eight
intermediates. The certificates were revoked for key compromise on 2026-10-18
and 2026-10-19, so the fix is to reissue them under a different intermediate. Do
not disable verification to keep an affected endpoint working.

Go 1.25 and earlier are out of support and will not receive this filter, so an
agent or control plane still built with an older toolchain keeps trusting those
chains.
