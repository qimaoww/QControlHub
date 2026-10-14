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
| Offline, inconclusive detection, missing DNS or overall probe deadline | Original URL; short detection cache |
| Explicit HTTPS egress proxy applies to the original host | Preserve configured proxy routing |

TLS probes target Cloudflare and Google using literal IPv4/IPv6 addresses and
validated server names. They bypass environment HTTP proxies. An IPv6 address,
default route or failed GitHub request alone cannot select acceleration. DNS64
is observed using `ipv4only.arpa`; a working IPv6 GitHub TLS connection is also
required before retaining direct NAT64 downloads.

Go bounds detection to four seconds, shares concurrent probes, and caches a
decision for five minutes. Unknown results expire after 15 seconds; canceled
probes do not populate the cache. Shell probes run concurrently with a maximum
of three seconds each, followed by bounded DNS/channel and NAT64 checks (at most
ten seconds total). Shell caches in the running process with the same TTLs.
Shell can identify direct GitHub IPv6 without the optional `getent` utility;
the DNS64 label additionally requires bounded `getent` support.

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
Metadata is bounded to 2 MiB. API calls have a 20-second attempt deadline;
archive attempts have a 90-second deadline, including redirects. Transient
failures retry at most three times, with cancellation and temporary-file
cleanup. Digest failures are permanent. The standalone installer verifies all
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
| IPv6-only standalone installer | Detected the network and selected the prefix; its pinned-tag metadata request returned upstream HTTP 403. It stopped before installing any core. Full standalone live installation remains unverified. |

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

## Rollback

Revert the change, rebuild/redeploy the previous Agent/control-plane image and
restore the previous generated deploy scripts. Download failures leave current
cores untouched. Agent service-start failures retain the existing automatic
binary rollback. Reverting channel selection does not uninstall installed cores
or change enrollment credentials, configurations or databases.
