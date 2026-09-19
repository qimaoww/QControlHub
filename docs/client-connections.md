# Inbound client connection history

The **Client connection IP** page (`#client-connections`) records the source IPs
connecting to managed proxy inbounds. For example, a VLESS listener on server
port 443 shows the connecting client's IP and source port, the local listening
IP and port, the engine, and the inbound name and protocol. It does not collect
proxy destination IPs, visited sites, credentials, or application payloads.

The page queries the panel's PostgreSQL database. Filter by server, engine,
inbound name, inbound protocol, TCP/UDP, exact IPv4/IPv6 source address, server
port, and time range. All filters also apply to the summary and timeline;
pagination only changes the detail rows. Time inputs use the browser's local
time zone. Timeline buckets use UTC boundaries and display in local time.

## Collection and interpretation

Upgrade the panel and Agent to enable collection. Linux Agents read established
TCP sockets and match them to actual listening sockets and managed configuration
ports. UDP/QUIC peers come from the original direction of `conntrack -L -p udp`;
the destination must be a local interface address with a bound UDP socket. Install the distribution's
`conntrack` package and allow the Agent to read connection tracking for UDP
coverage. Missing conntrack, unreadable configuration/socket tables, and capped
samples are reported as partial coverage in the page. Unsupported platforms
report unavailable; older Agents show no report. Overlapping configured ports
that cannot be uniquely assigned to an engine/inbound are omitted.

Collection occurs on heartbeats, at most once every 15 seconds. A report contains
at most 512 distinct tuples. It travels over the authenticated Agent WebSocket;
the panel supplies the agent identity and receive timestamp. The Agent does not
write client IP history to disk. There is no offline backlog or historical
backfill. Unmanaged/external core configurations are outside collection scope.

These are network observations, not successful proxy authentication events or
precise session start/stop times. TCP connections shorter than the sampling
interval can be missed. UDP entries can remain until the kernel's conntrack
expiry. NAT or a fronting relay changes the source IP visible at the server.
The recorded protocol is the configured listener protocol, not payload inspection.

The panel coalesces repeated observations of a tuple into one row per minute,
with the first and last panel receive timestamps. The tuple includes agent,
engine, protocol, inbound, transport, source IP/port and local IP/port. Reuse of
the same tuple can merge separate sessions; counts are labelled observed tuples.
Each timeline bucket and the overall summary independently deduplicate tuples
and source IPs. Counts across buckets must not be added to infer total sessions.
A blank interval means no retained observations, not proven zero connections.

## Storage, access and retention

Records are retained for seven days and pruned by the existing panel retention
job. A single query spans at most seven days. Detail pages contain at most 200
rows and use an ID cursor; summaries cover the entire selected range. Collection
status includes the last report time, coverage detail and truncation flag; a
report older than 90 seconds is shown as stale. Stored history remains queryable
when a node goes offline or the panel restarts.

The endpoint is `GET /api/v1/client-connections`, guarded by `core-logs.read` and
host administration access. Owners can read their nodes, administrators can read
visible nodes, and an engine-sharing grant does not expose another owner's
client IP telemetry. Owner-hidden nodes remain private from fleet administrators.
Revoked nodes are excluded from queries.

Schema version 62 adds `client_connections` and `client_connection_sources`.
Retention bounds time, not database bytes: size depends on observed tuples and
node count. No port traffic counters or quotas are changed. Rolling back binaries
leaves the additive tables intact; preserve the database if history is needed.
