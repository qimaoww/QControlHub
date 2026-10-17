# Shared-instance received/sent undercount fix

Investigation baseline: `55e2d4ca4a85d3d23b026072cdb616144f81c462`. The final branch is rebased onto main `dae30aaa` with one bug-fix commit.

## Root cause and focused fix

The shared collector explicitly skipped native/marked exit accounting. Shared runtimes already had independently marked outbound sockets, but only client-listener counters were collected. Consequently target → proxy bytes were missing from received detail, and proxy → target bytes were missing from sent detail. Testing upload alone did not reveal the omitted target receive leg.

The collector now selects the protected configuration and managed service by **engine plus allocation**, verifies its independently marked exits, and reads the existing four-leg counters. Two users running the same core never share a source. Fixed native statistics API ports remain disabled for independent shared instances. Invalid attribution preserves baselines and stops only the affected allocation.

The agreed quota rule is **measured client leg + target leg, received + sent**, matching administrator port statistics. Application upload is client → proxy; download is proxy → client. Host NIC totals are outside this personal accounting path. Network counters include IP/transport headers, TLS/WebSocket framing, ACKs and retransmissions; Ethernet headers are excluded.

Shared quota is now incremented from the same accepted deltas as period/history statistics, in the existing transaction. Raw `ShareUsedBytes` cannot charge rejected, duplicate, late or failed samples. PostgreSQL timestamp precision is normalized before deduplication. Existing epoch baselines and per-policy acknowledgements prevent checkpoint replay or listener-to-dual migration from charging already acknowledged bytes again. Connection reply marks include policy/epoch identity to avoid old-port replies entering the next allocation.

There are no new pages, node-card summaries, audit tables, database migrations or historical repair commands. The existing traffic status renderer only stops incorrectly labeling a verified dual source as listener-only. Existing totals are retained; missing historical target traffic is not estimated. Calendar usage and allocation cumulative usage keep their existing lifecycles.

## Real VLESS TLS WS evidence

On the user-authorized affected node, the existing Caddy TLS WS deployment produced these before-fix observations:

| Operation | Application bytes | Listener RX | Listener TX |
|---|---:|---:|---:|
| Upload | 4,194,304 | 4,220,202 | 7,281 |
| Download | 4,194,304 | 14,148 | 4,223,490 |

The production observation can contain background traffic. Existing production binaries, configurations and balances were not changed.

The after-fix test used a disposable enrolled OpenRC Agent, two ordinary users, two independent sing-box 1.14.3 instances, three allocated ports and real native VLESS TLS WS clients. Accounts installed/deployed through authenticated panel APIs and normal Agent tasks. Transfer bodies had fixed sizes and SHA256 verification. Agent reports/tasks were not injected.

| User / port | Application up / down | Independent IP RX / TX | Agent RX / TX | Accepted RX+TX | Capture difference |
|---|---:|---:|---:|---:|---:|
| A / 25101 | 5,242,880 / 4,194,304 | 9,586,034 / 9,580,829 | 9,586,034 / 9,580,829 | 19,166,863 | 0 B |
| A / 25102 | 2,097,152 / 1,048,576 | 3,200,095 / 3,195,971 | 3,200,095 / 3,195,971 | 6,396,066 | 0 B |
| B / 25201 | 4,194,304 / 2,097,152 | 6,393,271 / 6,385,041 | 6,393,271 / 6,385,041 | 12,778,312 | 0 B |

The capture contained 9,493 packets and zero drops. Totals exceed twice the application body by 1.55%, 1.66% and 1.55% due to measured transport overhead. See [capture comparison](evidence/dual-pcap-comparison.json), [capture receipt](evidence/dual-capture.txt), [application transfers](evidence/dual-transfers-concurrent.json), and [actual API counter snapshots](evidence/deployed-node-measurements.json). The collector and deployed configuration path in this measurement remain the final implementation; the final quota SQL additionally uses the existing tables directly, without the earlier proposed audit extension.

A-only 4 MiB upload: RX 4,266,253 B, TX 4,250,959 B, allocation increment 8,517,212 B; A's other port and B stayed unchanged. Owner/system direct traffic left both users unchanged. Instance, Agent and panel restarts preserved settled totals; subsequent traffic continued normally. A quota exhaustion affected only A, while B's real transfer succeeded; top-up restored A's permitted start. UDP over VLESS TLS WS verified IPv4 and IPv6 targets. [Quota results](evidence/quota-results.json), [restart receipt](evidence/dual-after-restarts-receipt.json), [IPv4 UDP](evidence/dual-udp4.json), [IPv6 target UDP](evidence/dual-udp6.json).

## Validation boundaries

Focused regressions cover missing exits, same-core allocation isolation, duplicate/failed/late reports, concurrent submission, source migration, checkpoint overlap, cycle changes and quota acknowledgements. The disposable TCP/UDP IPv4/IPv6 namespace matrix passed. Final full Agent (237.35 s), store (145.58 s) and server configuration (0.33 s) suites passed on the rebased code. All-package compile, frontend-module, formatting and style checks passed. Latest CI results are linked in the PR.

Physical host reboot, a second live shared host, every protocol permutation and post-upgrade production Caddy traffic were not tested. The production Caddy before-fix observation and disposable native TLS WS after-fix test are distinct evidence. Existing Mihomo non-global-target socket-mark limits remain unchanged.

## Cleanup

Both environments are stopped after verification. `qch-shared-traffic-d074-probe-cleanup.timer` is persistent and scheduled for **2026-10-20 23:27:38 UTC+08:00** (**15:27:38 UTC**), 72 hours after the probe ended. It removes only the marked temporary probe directory/binary after scope/hash checks; existing production services, configurations, rules and user data are excluded. [Receipt](evidence/remote-cleanup.json).

`qch-shared-traffic-d074-cleanup.timer` removes only the seven stopped ID/label-verified test containers, their dedicated network and marked temporary directory. Its final execution is **2026-10-21 00:38:23 UTC+08:00**, exactly 72 hours after the recorded final test end. Exact resource IDs are in the [local registration receipt](evidence/local-cleanup.json). Reports, sanitized evidence, earlier screenshots and cleanup receipts are preserved outside the removed directory. No global prune or firewall flush is used.
