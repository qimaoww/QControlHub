# Managed core runtime permissions

## Scope and required policy

This audit starts from main `bd3ac8d3`. It includes Mihomo, Xray, sing-box and
Shadowsocks Rust, their normal services and independent `shr_...` instances,
systemd and Alpine OpenRC, installation resources, runtime generation,
existing-service recognition, upgrades, atomic writes, rollback and startup
synchronization. Configuration validation and a healthy supervisor are not
communication tests.

| Operation | Mihomo | Xray | sing-box | Shadowsocks Rust |
| --- | --- | --- | --- | --- |
| Execution identity | `qcontrolhub-core:qcontrolhub-core`, never root | Same | Same | Same |
| Binary and parent access | Protected root-owned executable 0755; application directories root:core 0750 | Same | Same | Same |
| Configuration reads | Root:core 0640, protected traversable 0750 parents | Same, including source bundles | Same, including source bundles | Same, including CLI ACL |
| Certificate/private-key reads | POSIX access plus scoped `SAFE_PATHS` | POSIX access | POSIX access | Native SS has no server TLS certificate |
| Writable state/cache | Own `/var/lib/qcontrolhub-mihomo[-share]` | Own `/var/lib/qcontrolhub-xray[-share]` | Own `/var/lib/qcontrolhub-sing-box[-share]` | Own `/var/lib/qcontrolhub-shadowsocks-rust[-share]` |
| Low TCP/UDP listen ports | `CAP_NET_BIND_SERVICE` | Same | Same | Same |
| Independent outbound `SO_MARK` | `CAP_NET_ADMIN` | Same | Same | Same |
| Address families under systemd | UNIX, INET, INET6, NETLINK | UNIX, INET, INET6 | UNIX, INET, INET6, NETLINK | UNIX, INET, INET6 |
| Interface binding | Interface discovery needs NETLINK; socket binding tested | Socket binding tested | Interface discovery needs NETLINK; socket binding tested | Socket binding tested |
| Actual process caps | Inheritable, effective, permitted, ambient and bounding = `0x1400` | Same | Same | Same |
| Privilege transitions | `NoNewPrivs=1`; non-root real/effective/saved/fs UID/GID; no root supplementary group | Same | Same | Same |

`CAP_NET_ADMIN` serves the project's existing independent egress/accounting
marks. It also permits network administration in the process network namespace;
it is broader than a single setsockopt operation. No NET_RAW, SYS_ADMIN,
DAC_OVERRIDE, CHOWN or SYS_PTRACE is granted to a core. Xray and SS Rust need no
NETLINK grant for the operations tested. Positive systemd family directives
extend the existing allowlist instead of resetting it.

Systemd retains ProtectSystem=strict, ProtectHome, PrivateTmp, PrivateDevices,
kernel/control-group/clock protections, namespace restrictions, restricted
architecture, memory execution restrictions and the dedicated state write path.
OpenRC retains the non-root identity, explicit capability ceiling, umask and
NoNewPrivileges; it does not implement systemd's mount, device or address-family
sandbox. Shared instances keep separate configuration, state, service, listener
and accounting identities. The existing common Unix account is not a hostile
tenant confidentiality boundary; in particular OpenRC has no per-instance
mount namespace. This change preserves that model and does not grant new file
read bypass capabilities.

System TUN devices and raw sockets are not needed by the supported managed
independent-egress plan. That plan rejects unsupported global TUN/listener
shapes; generated sing-box WireGuard uses userspace operation (`system: false`).
Do not add SYS_ADMIN, NET_RAW or `/dev/net/tun` access simply to make arbitrary
administrator configurations start. Such configurations require separate review.

## Findings and repair evidence

| Trigger / scope | Failure or inconsistency | Repair and basis |
| --- | --- | --- |
| Historical Mihomo normal/shared systemd filter; Mieru transport TCP carrying UDP | TCP succeeds, UDP times out; `netlinkrib: address family not supported by protocol`; controlled UDP target receives nothing | AF_NETLINK is already present in latest main's templates. Extend old-node synchronization to shared services and actual running processes, independently of accounting config changes. Netlink is used for local interface discovery, not as a raw socket privilege. |
| Xray normal unit with independent outbound marks | `failed to set SO_MARK` / `operation not permitted`; Xray can continue forwarding **without** its requested mark | Align the normal template with its existing shared/OpenRC/runtime policy by granting NET_ADMIN. Negative real-kernel case confirms unmarked packets; fixed case counts marked TCP and UDP packets. |
| Old sing-box normal/shared family filters despite correct ambient caps | Interface lookup/binding cannot use NETLINK; capability-only checks incorrectly say ready | Check NETLINK for sing-box and valid shared identities as well as Mihomo; recognize only exact bundled historical filters. |
| OpenRC caret-only IAB strings | CapEff/CapPrm/CapAmb are narrow but CapBnd retains the launcher ceiling | Add numeric `!N` IAB exclusions for every kernel capability except BIND_SERVICE/ADMIN, through `cap_last_cap`. Actual process CapBnd now equals 0x1400, including when the fixture Agent has SYS_PTRACE. Caret grants inheritable/ambient caps, not the bounding ceiling. |
| `/etc/qagent` root:root 0750 or old binary/application directory metadata | Non-root config/ACL read or binary execution fails with EACCES | Synchronize protected root-owned parents and files, and executable access. A CHOWN-only helper can repair the fixed parent through an older Agent read-only mount. Reject symlinks and untrusted/writable paths. |
| Agent 0077 umask when publishing Xray/sing-box source bundles | Requested 0640 files become 0600; old bundles stay inaccessible when reused | Apply final metadata explicitly; validate exact immutable content before repairing reused bundle metadata. Retain old revisions and rollback contents. |
| Mihomo certificate under its config directory while `-d` points to state | Listener logs `path is not subpath of home directory or SAFE_PATHS` and remains unavailable even though the core starts | Scoped allowlist includes the instance configuration directory and `/etc/qcontrolhub/tls`. Validation uses the same policy. It does not disable path checking. Real TLS handshake proves certificate and key reads. |
| External certificate unreadable, hidden under ProtectHome/PrivateTmp, or outside managed Mihomo allowlist | A startup/config syntax success can miss an unusable listener | Probe referenced cert/key paths as the formal non-root identity and report kernel, service, path and access operation. Do not change administrator certificate ownership automatically. Provision protected instance-local certificates or repair their parent traversal/read access. |
| Root-owned OpenRC state/cache leftovers | Non-root writes fail despite a correct service user | Repair protected state roots and root-owned regular contents without replacing data; refuse symlinks, multiple hard links, foreign owners and unsafe write modes with a manual-repair reason. Systemd retains StateDirectory ownership handling. |
| Template updated / daemon-reload performed while old process is alive | Static policy appears fixed but CapEff/CapAmb or socket filter is stale | Compare actual PID credentials/caps/NoNewPrivs and start time against loaded fragment, drop-ins and binary. Restart only active stale processes; preserve stopped/failed services. |
| Shared upgrade, old bind-only or log-fallback drop-in | Main bootstrap was invoked for a shared template; current recognized drop-ins were omitted from shell ownership checks | Refresh only the recognized shared template; accept exact historical/current capability and log variants and verify effective fragment, user/group and drop-ins. Do not alter boot enablement. |
| Administrator systemd overrides or OpenRC conf.d/runlevel overrides | Later definitions replace the intended identity, caps or command | Refuse automatic synchronization, identify the exact override, and request restoration/review of the bundled policy. A marker/name alone is not ownership proof. |

Mihomo intentionally omits marks on some non-global socket addresses. The
runtime fixture therefore uses owned benchmark/ULA loopback aliases and actual
nft TCP/UDP counters for mark proof; ordinary loopback traffic separately tests
interface binding and Mieru UDP over TCP. These behaviors must not be mistaken
for missing capabilities.

OpenRC's existing strict cross-UID `/proc/PID/exe` identity verification requires
the Agent host to retain process-inspection authority. A reduced Docker launcher
without SYS_PTRACE fails that inspection even when forwarding works. The
disposable migration fixture explicitly grants it to the fixture Agent; cores
still have only 0x1400. The production repair does not weaken executable identity
checks or grant ptrace to any core.

## Existing nodes, updates and rollback

The current Agent performs an idempotent permission sync on every startup,
before accounting migration. It enumerates installed default services and valid
shared instances even if their config content needs no rewrite. One customized
service reports its own failure without preventing repairs of other engines.
The normal deploy/start/restart/import paths use the same policy. Updates retain
the protected binary layout and repair execution access before managed validation.
Atomic config writes explicitly preserve root:core metadata.

To repair an installed node, deploy this Agent version through the existing
Agent upgrade/redeploy workflow and restart the Agent once if that workflow did
not restart it. Inspect the Agent log for `permission migration` or
`synchronize installed core runtime permissions`. Active stale core processes
restart once, causing a brief connection interruption; inactive/failed cores and
service enablement remain unchanged. A second startup does not restart repaired
PIDs. No running administrator service is overwritten.

If synchronization refuses an override, inspect `systemctl cat SERVICE` and
`systemctl show SERVICE -p FragmentPath -p DropInPaths`, or the reported
`/etc/conf.d/SERVICE[.RUNLEVEL]`. Review the exact file rather than deleting all
overrides. For certificate access, `namei -l PATH` shows missing directory search
permission; use a protected instance configuration directory with root:core
0750 parents and 0640 cert/key files. Do not make keys world-readable. For state
ownership refusals, inspect the reported path and keep its contents before
correcting ownership manually. Inspect actual `/proc/PID/status` to distinguish
lost privileges from an unavailable network peer.

Recognized OpenRC script replacement keeps atomic backups; Mihomo main-template
replacement keeps a protected `.permissions.bak.*` copy. For rollback, restore
the previous Agent plus the affected bundled service/template or saved exact
script, reload systemd if applicable, and restart **only services that were
running**. Keep configs, certs, source bundles and state data. Restoring old
permissions can restore the old Mieru/mark failures. Do not remove independently
maintained administrator overrides or broaden all filesystem access to recover.

## Reproducible actual-runtime validation

`deploy/tests/fetch-permission-cores.sh OUTPUT` downloads SHA256-verified official
Mihomo v1.19.32, Xray v26.3.27, sing-box v1.14.3 (glibc and musl) and Shadowsocks
Rust v1.25.0. The musl asset selection repair follows an actual Alpine failure:
the latest generic sing-box binary needs glibc and reports `not found` on musl;
this is a loader failure, not a file capability problem. Historical generic
static releases remain eligible when there is no separate musl asset.

The CI `All cores / actual service permissions` job runs the Python fixture with
every normal/shared systemd asset, then Alpine OpenRC and the production Go
installed-node migration. Fixtures copy only verified kernels and substitute
paths for unique disposable services; all production sandbox directives remain.
It checks non-root UIDs/GIDs, all five capability sets, NoNewPrivs, low TCP/UDP
ports, v4/v6 echo payloads and target receive counters, actual packet marks,
interface binding, TLS handshake/key reads, SS ACL enforcement, Xray log writes,
sing-box log/cache writes, atomic binary/config replacement and restart,
OpenRC copytruncate rotation and stopped-core negative controls. OpenRC explicitly
sets the unprivileged port threshold to 1024.

The real installed-node OpenRC test creates all eight exact historical services,
loses config/binary access and leaves root-owned cache files. Four services start
active and four remain stopped. It invokes `syncManagedCorePermissions`, verifies
new live process grants, preserved cache/config contents and running states, and
checks that repeating synchronization preserves the repaired PIDs.

The separate Mieru systemd Go regression runs the formal non-root account and
complete service sandbox against both pinned Mihomo v1.19.31/v1.19.32. It includes
the historical AF_NETLINK negative case, current normal/shared TCP cases, the
additive migration case, UDP transport control, two v4 and one v6 UDP target,
binary payloads, raw UDP DNS, idle expiry/reconnect and stopped-tunnel proof.

Validation targets: `make generate-deploy-scripts`, `make check`,
`make alpine-agent-test`, `make upgrade-sandbox-test`,
`make ss-rust-runtime-test`, the two actual service-manager fixtures and the
two pinned Mieru regressions. The PR records their actual results. Unverified
scope: arbitrary administrator configurations/plugins, public Internet IPv6
routing/firewalls, every upstream release/CPU architecture, and system TUN/raw
device features outside the managed plan. Controlled local v6 traffic proves
the service's socket permissions, not public IPv6 reachability.

## Policy references

- [systemd capability, address-family and filesystem execution settings](https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html)
- [Linux SO_MARK privilege requirement](https://man7.org/linux/man-pages/man7/socket.7.html)
- [libcap IAB bounding/inheritable/ambient syntax](https://man7.org/linux/man-pages/man3/cap_iab.3.html)
- [OpenRC supervise-daemon capability application](https://github.com/OpenRC/openrc/blob/master/src/supervise-daemon/supervise-daemon.c)
- [Mihomo v1.19.32 scoped safe paths](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/constant/path.go)
- [Mihomo v1.19.32 mark handling](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/dialer/mark_linux.go)
