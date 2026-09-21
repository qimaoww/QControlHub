# Inbound client connection history

The **Client connection IP** page (`#client-connections`) records the source IPs
connecting to managed proxy inbounds. For example, a VLESS listener on server
port 443 shows the connecting client's IP and source port, the local listening
IP and port, the engine, and the inbound name and protocol. It does not collect
proxy destination IPs, visited sites, credentials, or application payloads.

The page queries the panel's PostgreSQL database. The context sidebar uses the
shared All nodes / node list navigation; selecting a node queries it immediately. Other filters are collapsed above the results.
Filter by server, engine,
inbound name, inbound protocol, TCP/UDP, exact IPv4/IPv6 source address, server
port, and time range. All filters also apply to the summary; pagination only
changes the detail rows. Time inputs use the browser's local time zone. The page
shows summary counts and connection details without a timeline chart. The API
still provides timeline buckets using UTC boundaries.

Queries default to public source IPs only, excluding private, loopback, link-local,
CGNAT and other special-use addresses from details, counts and timelines. Under
More filters, select Include non-public to query all stored observations
(`include_non_public=true` in the API). This filter applies to the source IP,
not the local listening address. Collection and panel retention include both.

## Collection and interpretation

Upgrade the panel and Agent to enable collection. Linux Agents read established
TCP sockets and match them to actual listening sockets and managed configuration
ports. UDP/QUIC peers come from the original direction of `conntrack -L -p udp`;
the destination must be a local interface address with a bound UDP socket.
The Agent installer/update script installs `conntrack` (Debian/Ubuntu) or
`conntrack-tools` (Alpine/RPM distributions) when missing. Package installation
failure warns without blocking the Agent update or TCP collection. Binary-only
online upgrades do not install OS packages: existing nodes must run the updated
installer or install the package separately. No conntrack daemon or firewall
changes are needed. UDP collection requires kernel connection tracking and the
Agent's existing `CAP_NET_ADMIN` capability; restricted VPS/container hosts may
need an operator to grant it.

Only actually bound managed UDP ports trigger a lookup, so protocols declared
as TCP/UDP but currently listening only on TCP do not produce a missing-conntrack
warning. IPv4 and IPv6 are queried explicitly and independently. Missing commands,
permission failures, timeouts and incomplete output have distinct report details.
The expandable collection status shows these reasons and remediation in Chinese,
alongside the last report time; already collected TCP/UDP observations remain
available if another part fails. Missing conntrack, unreadable configuration/socket
tables, and capped samples are reported as partial coverage in the page. Unsupported platforms
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

Schema version 64 adds `client_connections`, `client_connection_sources`, and
the `client_connection_locations` cache.
Retention bounds time, not database bytes: size depends on observed tuples and
node count. No port traffic counters or quotas are changed. Rolling back binaries
leaves the additive tables intact; preserve the database if history is needed.

## Country and province

The country/region column resolves the inbound source IP on the panel through
its existing GeoJS provider. Chinese (`CN`) results include a recognized province,
autonomous region or municipality in Chinese. Other countries display only the
country/region; unknown subdivisions are left blank. Non-public addresses are
labelled separately and are never sent to the provider.

Only IPs from an authorized detail page are looked up. Results are persisted in
panel PostgreSQL for 48 hours; failures retry after five minutes and retain any
previous result. Unreferenced cache entries are pruned with connection history.
Lookups are deduplicated within a page, use at most eight concurrent requests,
and share a three-second deadline. Provider failure leaves connection history
available, and IPs not reached within the deadline can resolve on a later query.
The browser first requests `locations=cached`, which returns history and cached
locations without waiting for the provider. Public locations are refreshed in a
second `locations=only` request with the same authorized filters and a cursor
anchored to the displayed page; this request reads only detail rows and skips summary, timeline and source queries.
The UI merges locations by record ID and ignores responses after navigation,
account changes or a newer query. Matching results are cached in account memory
for return visits and revalidated; failed refreshes clear them. Initial shell
settings/overview reads do not delay the connection page. Requests without the
`locations` parameter retain synchronous enrichment for API compatibility.
The browser and Agent do not contact the provider. GeoJS receives the public
source addresses being resolved; location is an IP database estimate.
