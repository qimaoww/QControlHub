# IPQuality dependency preparation and diagnostics

## Cause

The old Agent required preinstalled `bash` and `curl`, then delegated all other
packages to upstream's `-y`. That flag only answers the installation prompt.
The current [official script](https://github.com/xykt/IPQuality/blob/main/ip.sh)
(`v2026-09-16`, checked on 2026-10-14) does not stop or verify every failed
package-manager command. It also discovers addresses and downloads references
before calling `install_dependencies`. Missing tools can therefore affect the
check before upstream even attempts installation.

The installed systemd Agent has `ProtectSystem=strict` and only
`CAP_CHOWN CAP_NET_ADMIN`. Its detector children cannot install host packages.
Existing OpenRC/root deployments can still fail on missing bootstrap tools,
repositories, locks or broken package state. Previously stderr was captured
but discarded when report validation failed, leaving only the misleading
`IPQuality must return one or two reports` error.

The reported Halo LAX task's original stderr was not available in this
workspace. The sandbox installation failure was reproduced locally; the code
and current upstream establish this failure mechanism, rather than proving
which individual repository/package command failed on that production node.

## Supported installation

Use the current Agent installer or upgrade the Agent binary. New installations
prepare dependencies before starting the restricted service. Every check also
verifies dependencies, so existing nodes repair missing tools after upgrading.
The installer first bootstraps curl and CA certificates when necessary to
download its Agent candidate. Remaining preparation uses the same fixed
`qagent prepare-ip-quality` utility as runtime repair.

| Host package manager | nc | dig | ip |
| --- | --- | --- | --- |
| Debian/Ubuntu `apt-get` | netcat-openbsd | dnsutils | iproute2 |
| Alpine `apk` | netcat-openbsd | bind-tools | iproute2 |
| `dnf` / `yum` | nmap-ncat | bind-utils | iproute |
| `zypper` | netcat-openbsd | bind-utils | iproute2 |

Other checks cover Bash >= 4, curl, jq, bc, GNU grep, gzip/gunzip, CA roots,
awk, sed, xargs, timeout and the base text/date utilities used by upstream.
Missing commands map to bash/curl/jq/bc/grep/gzip/ca-certificates, gawk, sed,
findutils or coreutils as appropriate. BusyBox commands are reused when their
required interface works. Already executable commands are retained. Packages
come only from configured system repositories; no repository is added, no
TLS/signature checks are disabled, and no upstream Bash-upgrade script runs.
Enterprise distributions must already have trusted repositories providing the
listed packages. Unsupported package managers fail with an explicit message.

Existing executable, owner, symlink-target and protected-directory checks still
apply. Unsafe existing files are rejected with repair advice rather than
overwritten. Non-root/read-only container deployments need an administrator to
provide dependencies or a root installation with writable package storage.

On systemd, the Agent launches its own validated executable in a unique,
temporary root service. Only the fixed dependency utility is accepted; task
input cannot supply packages, repositories or shell programs. The helper uses
`ProtectSystem=strict` with writable package locations in `/usr`, `/etc`,
`/var`, `/run` and legacy `/bin`, `/sbin`, `/lib*`. Package scripts require normal
root package-manager privileges. The detector and main Agent keep their
existing sandbox and capabilities. This also works with an older Agent unit
after a binary upgrade. OpenRC/root hosts run the same fixed preparation
directly. Installation holds a protected cross-process flock, rechecks after
acquiring it, and validates commands again after installation.

## Budgets, cancellation and retries

The complete task retains its 10-minute budget and 12-minute lease. Dependency
preparation has a four-minute budget; initial checks/helper launch allow at
most ten extra seconds within the task budget. Each executable probe has a
five-second deadline. APT/apk wait up to 30 seconds for native locks; detected
lock failures retry twice with cancellable delays. Package downloads keep the
host repository routing; APT/dnf/yum set bounded request retries/timeouts.

The helper has a 245-second systemd runtime limit and five-second stop timeout.
Cancellation explicitly stops its named service and all service children; the
runtime limit also covers a crashed/disconnected launcher. Ordinary subprocess
groups are killed on cancellation and after exit. Pending task cancellation
retains the existing API behavior; running operations stop on Agent shutdown
or deadline. The API does not add a new running-task cancellation feature.

Public script/resource requests have three attempts of at most 25 seconds,
with short cancellable backoffs for network/408/429/5xx failures. Permanent HTTP
errors and invalid content stop immediately. Detection uses the remaining
shared task budget. Retrying a failed task rechecks actual installed state.

## Downloads and report correctness

The official moving script is first downloaded over HTTPS into a private
directory. Redirect sources are restricted to the official detector and
`xykt/IPQuality` GitHub/Raw locations. HTTP status, received length, 1 MiB bound,
UTF-8, HTML rejection, expected script markers and Bash syntax are checked
before execution. Logs record its SHA-256 for diagnosis. Upstream publishes no
signed digest for this moving script; the recorded hash and syntax check are
not an upstream authenticity signature.

After preparation the script runs with `-n -y -p -f -o report.json`: skip
upstream's unchecked installation, retain privacy mode and printed reports.
Only its public reference GETs are routed through the Agent's official-source
downloader. Its fixed jsDelivr fallback maps back to the original GitHub Raw
source. All GitHub requests and redirects use the existing IPv6 channel policy
in [github-downloads.md](github-downloads.md), including the specified
`https://v6.gh-proxy.org/` channel on confirmed IPv6-only hosts. IPv4/dual-stack
remain direct. Reference JSON is validated. No enrollment credentials, user
proxy variables or curl configuration are inherited by detector children.

Repository access, GitHub resources and detection-service access remain
separate diagnostic stages. Curl errors from detection services are captured,
with default connect/request deadlines; upstream's address-family options and
per-service deadlines are preserved. A failed IPv4 probe on an IPv6-only node
does not invalidate a successful IPv6 report, and vice versa.

The existing regular-file/no-symlink and report-size checks remain in place.
Empty output, error pages, corrupt JSON, incomplete sections, duplicate families
and missing/mismatched printed reports fail validation. One valid IPv4 or IPv6
report, or two valid different-family reports, is accepted even if upstream's
final command exits nonzero. Missing database results remain unknown. Failed,
pending and retry attempts preserve the last successful report and its actual
timestamp through the existing store/archive APIs.

## Operator feedback

Agent logs and final task output show checks, installation, script download,
detection, parsing and completion/failure. Installation errors identify the
command, exit status, retryability, bounded diagnostics and repair advice:

- Repository/DNS/HTTPS download failure: check the system repositories' own
  address-family connectivity and retry.
- Package lock: wait for the other transaction; do not delete lock files.
- Permission/read-only failure: fix the root/service/mount environment.
- Missing packages/broken transactions: repair trusted repositories or package
  state before retrying. The Agent does not force-unlock or bypass signatures.
- No valid report: inspect stdout/stderr for detection-service and public
  reference errors, separately from dependency installation.

ANSI/control characters are removed from diagnostics; URL credentials,
paths/queries, authorization values and common secret assignments are redacted.
Tail capture retains final errors after verbose progress, and truncation keeps
the helper's classification as well as its final diagnostic lines.

Local diagnostics use exactly the same pipeline:

```sh
umask 077
sudo /usr/local/bin/qagent ip-quality > report.json
```

This command performs real package installation/checks and contacts third-party
services. Report JSON goes to stdout; stages/errors go to stderr. Protect the
report file because it contains the node's public addresses and provider data.

## Validation (2026-10-14)

`make ip-quality-clean-test` builds the real static Agent and runs two complete
checks in each fresh Debian 12 slim, Ubuntu 24.04 and Alpine 3.23 container.
No detector dependencies are installed by the harness. It asserts first-run
installation, valid reports and second-run dependency reuse. It is opt-in
because it requires real package repositories and public detection services;
normal CI uses deterministic fixtures.

| Environment | Observed result |
| --- | --- |
| Clean Debian 12 slim, IPv4 | Initial automatic package installation and valid IPv4 report; repeated check reused dependencies. |
| Clean Ubuntu 24.04, IPv4 | Initial automatic package installation and valid IPv4 report; repeated check reused dependencies. |
| Clean Alpine 3.23, IPv4 | Bash/curl/jq/bc/nc/dig/ip/GNU grep installed; valid IPv4 report and repeated dependency reuse. |
| Clean Debian 12, IPv6 only | No IPv4 address/default route; real packages installed over IPv6, official script fetched through the specified GitHub channel, valid IPv6 report. DNS used a local IPv6 DoH forwarder because the host resolver omitted AAAA records. |
| Clean Alpine 3.23, actual dual stack | Independent IPv4 and IPv6 egress, local IPv6 DoH DNS; dependencies installed and both address-family reports validated. GitHub downloads remained direct. |
| Debian 12, actual systemd 252 sandbox | Fixed utility installed packages outside the restricted Agent; report accepted after the detector's final exit status 1. A failed read-only helper attempt also recovered on retry after the helper policy was corrected. |
| Fresh Debian 12, full shipped Agent unit restrictions | Detector utility substituted for normal WSS startup; first dependency installation and report generation completed with exit status 0. All shipped sandbox/capability directives were retained. |
| Actual systemd cancellation | Removed bc in the isolated node, held the installation flock, started the full restricted unit and canceled it while its helper waited. The named helper was stopped and no helper service remained running. |

Deterministic regressions cover complete/partial/missing dependencies, old Bash,
BusyBox grep, missing CAs, unsupported managers, non-root/unsafe paths,
repository failures, native locks, concurrent-preparation cancellation/recovery,
failed post-install validation, malformed/truncated/HTTP downloads, retries,
IPv4/dual-stack/IPv6/NAT64 resource routing, secret redaction, process cancellation,
single/dual-family reports and existing report-size/content limits. PostgreSQL
tests preserve dependency failure diagnostics and the successful report's exact
time while a newer attempt fails; existing API/archive/retry tests also run.

Live dnf/yum/zypper, older Alpine versions, non-amd64 nodes, real NAT64, a
production Halo LAX rerun, actual package-database interruption recovery and
OpenRC daemon lifecycle have not been exercised. Running-task API cancellation
remains unsupported. A shared GitHub proxy quota or unavailable detection
provider can still cause explicit network-stage failures.

## Rollback

Revert/redeploy the previous Agent and generated installer. No database/schema
changes are required. System packages already installed remain available;
rollback does not remove them. Existing successful reports and archive times
are unchanged.
