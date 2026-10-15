# GitHub downloads on IPv6 hosts

## Cause and policy

GitHub API, Raw and release URLs have independent DNS and redirect behavior.
Previously the Agent queried `api.github.com` and downloaded release files
directly. An IPv6-only node could fail before selecting an asset, even if a
manually prefixed release URL worked. The standalone installer and streamed
quick-start also used separate direct curl requests.

`internal/githubfetch` now selects a channel inside the downloading process.
`deploy/modules/shared/github-download.sh` implements the same policy for shell
entry points and is included by the deploy script generator. Changes belong in
the source modules; regenerate with `make generate-deploy-scripts`.

| Observation | Channel |
| --- | --- |
| Working IPv4, including dual stack | Original HTTPS URL |
| Working IPv6, two failed independent IPv4 probes, IPv6 route and proxy AAAA DNS | `https://v6.gh-proxy.org/` + original URL |
| IPv6 can already reach GitHub API, including working DNS64/NAT64 | Original HTTPS URL |
| Inconclusive detection / Unknown | Try original URL once; on DNS/connect failure validate the actual resource through the fixed accelerator |
| Both routes fail DNS/connect | Stop with both causes and IPv6 DNS repair advice; invalidate network/channel caches |
| Explicit HTTPS egress proxy applies to the original host | Preserve configured proxy routing |

TLS probes target Cloudflare and Google using literal IPv4/IPv6 addresses and
validated server names. They bypass environment HTTP proxies. An IPv6 address,
default route or failed GitHub request alone cannot select acceleration. DNS64
is observed using `ipv4only.arpa`; a working IPv6 GitHub TLS connection is also
required before retaining direct NAT64 downloads.

Go bounds detection to three seconds, shares concurrent probes, and caches a
decision for five minutes. Each TLS probe has a 2.5-second budget; channel AAAA
has 1.5 seconds, route selection 0.5 seconds and auxiliary DNS64 0.75 seconds.
Logs distinguish success, failure, an individual probe's timeout and observations
interrupted by the overall deadline/cancellation. Only completed probes or their
own timeouts can rule out a route; interrupted probes leave it undetermined. Completed
observations survive the overall deadline; DNS64 is optional and never blocks
an established usable route. Unknown still means insufficient evidence.
Unknown expires after 15 seconds. Cancellation does not cache a decision;
request failures invalidate it, including protection against stale in-flight
probes repopulating the cache. A successful accelerated resource request is
remembered separately for 30 seconds, covering subsequent metadata, files and
redirects without reclassifying Unknown as IPv6-only. Channel failures, including
interrupted response bodies, clear that preference so a bounded caller retry
can recover a freshly confirmed direct route.

Shell probes run concurrently with a maximum of three seconds each, followed
by independently bounded channel/API/DNS64 checks (at most ten seconds total).
They retain IPv6 evidence when optional DNS64 is unavailable. Shell caches the
same network/channel TTLs and clears them on terminal network failure.
Configured proxy exclusions follow each downloader's native matching rules;
Shell follows curl, including leading-dot exclusions matching the apex host.

## Entry points

| Entry | Download location and coverage |
| --- | --- |
| Agent core updater | Agent host/container: API latest, paginated prereleases, exact tags, asset files and redirects |
| Mihomo channels | Stable, exact version, official development and explicitly selected `vernesong/mihomo` development mirror |
| Xray, sing-box, Shadowsocks Rust | Their existing stable, exact version and development selectors; existing architecture/libc selection |
| `deploy/remote/install-core-engines.sh` | Executing host: pinned tag API metadata, all four archives, size and SHA-256 verification |
| `deploy/quick-start.sh` | Executing host: streamed bootstrap's Raw script and Compose downloads |
| CN IP feeds | `internal/cnip` downloader's actual host, retaining source and public-address validation |
| Mainland route/domain feeds | Actual Agent or control-plane process, retaining parsing and last-known-good caches |
| Flag SVG files | Control-plane process, retaining response limits and SVG validation |
| IPQuality script and official public reference files | Actual Agent/helper process, official source checks, bounded HTTP/content validation and the same IPv6 policy; see [dependency preparation](ip-quality-dependencies.md) |
| Control-plane Agent binary and installation assets | Existing control-plane origin and enrollment authentication; never sent to GitHub acceleration |

GHCR image pulls/checks remain OCI registry operations. URLs embedded in engine
configurations for Mihomo rule providers and sing-box remote rule sets are
consumed by those upstream executables, outside the application's HTTP/Shell
downloaders; their persisted configurations are not rewritten by this change.

The very first fetch in a remote `bash <(curl URL)` command happens before any
downloaded script can run. To obtain the initial script on a node that cannot
reach GitHub directly, use the reachable public channel once:

```sh
bash <(curl -q --fail --silent --show-error --proto '=https' \
  --connect-timeout 10 --max-time 90 \
  'https://v6.gh-proxy.org/https://raw.githubusercontent.com/qimaoww/qcontrolhub/main/deploy/quick-start.sh')
```

Once the script or Agent is running, channel selection is automatic. Existing
IPv4/dual-stack bootstrap commands continue to work. This change does not claim
that detection can run before the initial script has been obtained.

## Validation and failure behavior

Original GitHub source checks run before channel selection. Only the fixed
HTTPS channel and validated original GitHub targets are accepted. URL conversion
preserves escaping and queries and accepts a single existing prefix without
adding another. Nested prefixes, credentials in URL authority, non-HTTPS
targets and unrelated redirect hosts are rejected.

The Go transport unwraps channel redirects before the caller's existing
redirect policy runs, then applies the selected channel to the next request.
The shell downloader inspects each redirect before issuing another request.
Both cap redirects at five. The proxy receives only public request headers;
authorization, enrollment headers, cookies, Referer and arbitrary custom
headers are excluded. Shell disables curlrc loading and does not accept
credential/header arguments.

Core archives retain the 128 MiB maximum, expected size and SHA-256 checks.
Metadata is bounded to 2 MiB. Release resolution (including pagination) and
metadata retries share a 20-second total budget. Archive download retries share
one 90-second total budget, including probes, channel selection and redirects.
Go caps each route's DNS/connect/TLS/header phase at five seconds, then stops
that timer while the body uses the remaining caller budget. Shell bounds
connection establishment to five seconds; detection, redirects, retries and
backoff share the requested total download budget.

HTTP transient responses retry at most three times within those budgets.
Exhausted direct/accelerated network routes stop immediately instead of being
replayed by the outer installer retry loop. API quota, missing resources, TLS
verification, digest failures and cancellation do not trigger a channel switch.
An explicit outbound proxy keeps its original routing/authentication and never
falls back around that configuration. An accelerator request bypasses environment
proxies when they were excluded for the original GitHub host. Cancellation and
temporary-file cleanup remain in force. Digest failures are permanent. The standalone installer verifies all
four downloads and staged executable versions before replacing any installed
binary, using same-filesystem renames. Agent installation retains its existing
backup, service verification and rollback flow.

Logs report the observed network, acceleration choice and request/response
failure stage. API quota errors report the upstream reset time. The proxy's
shared upstream quota can still reject public metadata requests with HTTP 403;
wait for the reported reset and retry. Private tokens are never added to the
channel to address that condition.

## Live verification (2026-10-14)

The opt-in test is `TestLiveGitHubCoreDownloads` in
`internal/agent/core_github_live_test.go`:

```sh
QCH_TEST_LIVE_GITHUB=1 go test ./internal/agent \
  -run '^TestLiveGitHubCoreDownloads$' -v -timeout 16m
```

Set `QCH_TEST_GITHUB_CHANNEL=ipv6-proxy` or `direct` to assert every outgoing
GitHub request uses the expected channel. Run it in the target network
namespace/container, rather than inferring a node's connectivity from the panel.

| Environment | Actual result |
| --- | --- |
| IPv6-only Linux network namespace | No IPv4 addresses/default route; independent macvlan IPv6 interface and public IPv6 egress. Local DNS forwarded over IPv6 DoH because the host resolver omitted AAAA records. All four stable cores passed query, download, size/SHA-256 checks, extraction, real version execution, fresh install and existing-file replacement. Every application GitHub request used the specified prefix. |
| IPv4-only Docker bridge | All four stable cores passed the same flow with original API/release/redirect URLs. Host Debian 13 libraries were mounted read-only for the upstream Shadowsocks Rust glibc requirement; the initial Debian 12 userspace failed its version execution despite successful download/checksum. |
| Dual-stack Linux host | All four stable cores passed the same flow directly, including release asset redirects. |
| IPv6-only Raw download | Shell downloaded and checked the content of the real repository `go.mod`; API metadata and a real Xray checksum file also returned their expected content. |
| IPv6-only development | Official Mihomo and Xray passed real development installation and version verification. Mihomo mirror archive passed download/checksum in the first run; its later full installation query, sing-box development and Shadowsocks Rust development were blocked by shared upstream API quotas. |
| Standalone installer on dual stack and IPv6-only | Review found and fixed missing execute permission on gzip-extracted Mihomo before its staged version check. The regenerated script then completed pinned-tag API queries, all four archive downloads, size/SHA-256 verification, actual version execution and installation in isolated mount namespaces on both networks. The IPv6-only run had no IPv4 addresses/default route and selected the prefix automatically. An earlier run stopped before installation on upstream HTTP 403; shared API quotas remain an external limit. |

Stable versions observed were Mihomo `v1.19.32`, Xray `v26.3.27`, sing-box
`v1.14.3` and Shadowsocks Rust `v1.25.0`.

The live service manager is a test fixture: downloaded executables and file
replacement are real, while systemd restart/status are simulated. Replacing an
existing installation used the same release, not a cross-version production
upgrade. Real systemd/OpenRC daemon restarts, real NAT64/DNS64 infrastructure,
non-amd64 targets and all development-channel installs have not been validated
live. Deterministic regression tests cover the network states, channel URLs,
all engine/channel metadata selectors, redirects, credential isolation, retries,
cancellation and failure cleanup. The normal CI suite does not use the public
service.
Shell regressions also execute a real gzip fixture and verify that malformed
archives and failed staged version commands stop before installation.


## Follow-up to PR #265: DNS/probe failure (2026-10-14)

The affected HK node started Agent `b16f51cdeb4068a886e3a73c8c7cf2441c5047d0`
at 22:07:33 HKT after an upgrade task. That commit contains merged PR #265
(`b2e59cf1`). This was a current Agent, not an old binary or a missing API URL
mapping. The installation started at 22:10:36; the same process logged
`network=unknown accelerated=false` at 22:10:43 and 22:11:08, then failed at
22:11:18 with `lookup api.github.com: i/o timeout`.

Read-only checks in the Agent network/mount namespace found no configured
outbound proxy, working Cloudflare/Google IPv6 TLS, failed IPv4 TLS, and system
DNS ordered with two unreachable IPv4 servers ahead of reachable IPv6 servers.
Both the API and accelerator timed out through the system resolver. Explicit
queries to the IPv6 Google resolver succeeded; the API has A records but no
native AAAA, while the accelerator has AAAA. The old collector discarded partial
evidence on its deadline, then the transport/outer retries repeated direct-only
requests. The historical Agent only logged the final mode, so the precise
individual probe that hit its deadline cannot be proved retrospectively.

At the user's request, persistent openresolv configuration now prioritizes
Google and Cloudflare IPv6 DNS, excludes the two unavailable IPv4 resolvers,
and uses bounded resolver attempts. No Agent/core restart was needed. System
resolution and an actual accelerated Mihomo stable API query returned success
(HTTP 200). The user reran installation: the unchanged `b16f51cd` Agent logged
`network=ipv6-only accelerated=true` at 22:30:58, acknowledged installation at
22:31:06, and the subsequently deployed Mihomo systemd service was confirmed
active/running. This independently establishes the immediate DNS cause and
existing API-prefix coverage. The new PR is not deployed to that running Agent.

Follow-up regression and live checks:

- Real collector/transport fixtures retain partial evidence when DNS64 times out,
  leave insufficient observations Unknown, recover an API DNS timeout through
  the accelerator, preserve credentials/redirects/proxy routing, invalidate
  caches after a changed network, and honor cancellation.
- All supported engine/source selectors now also exercise Unknown -> API DNS
  failure -> accelerated metadata -> verified archive. Terminal failure tests
  verify that existing executables and staging directories remain unchanged
  before any service-manager call.
- A fresh macvlan namespace with no IPv4 addresses/routes, and DNS forwarded
  over IPv6 DoH, passed all four stable core installations/replacements with
  real binaries, size/digest checks and version execution. Mihomo official/mirror,
  Xray and sing-box development installations passed. Shadowsocks Rust development
  was explicitly skipped on upstream quota exhaustion, not counted as success.
- The precompiled live test also ran on the affected HK node after its DNS
  repair, in isolated installation directories. Real Mihomo stable API queries,
  verified archives, compatible-CPU selection, fresh install and replacement
  passed. The first replacement attempt hit the node's 213 MiB `/tmp` tmpfs;
  repeating with `TMPDIR=/var/tmp` passed. Agent and existing Mihomo services
  remained active throughout. The live suite now runs without a node-local Go
  toolchain because it does not use discovery compiler fixtures.
- Opt-in `TestLiveIPv6ProbeFailureRecovery` uses real HTTPS API/Raw requests
  while injecting only auxiliary probe failures: an unfinished DNS64 probe still
  selects acceleration, and Unknown followed by a real unreachable IPv4 GitHub
  connection recovers through the actual channel.

The namespace service manager is a fixture; production daemon restarts for the
new code, real NAT64, non-amd64 targets and cross-version upgrades remain
unverified. Production DNS repair and the successful existing-Agent Mihomo
installation are separate evidence from testing this PR's new binary.

## Rollback

Revert the change, rebuild/redeploy the previous Agent/control-plane image and
restore the previous generated deploy scripts. Download failures leave current
cores untouched. Agent service-start failures retain the existing automatic
binary rollback. Reverting channel selection does not uninstall installed cores
or change enrollment credentials, configurations or databases.
