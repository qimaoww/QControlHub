# Inbound client connection history

The **Client connection IP** page (`#client-connections`) records the source IPs
connecting to managed proxy inbounds. For example, a VLESS listener on server
port 443 shows the connecting client's IP and source port, the local listening
IP and port, the engine, and the inbound name and protocol when logged. It does not collect
proxy destination IPs, visited sites, credentials, or application payloads.

The page queries the panel's PostgreSQL database. The context sidebar uses the
shared All nodes / node list navigation; selecting a node queries it immediately. Other filters are collapsed inside the connection detail panel. The page uses
the log workspace header and a compact summary bar; refresh preserves the
selected node, filters and detail page.
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

The panel extracts source IPs from proxy core logs already stored in its own
PostgreSQL database. It does not request connection samples or read socket tables
from Agents. The Agent socket/conntrack sampler, heartbeat field, capability and
optional conntrack installation have been removed. Older Agents' connection
sample fields are ignored; the existing core-log upload channel remains unchanged.

Supported messages are Xray accepted access logs, sing-box inbound connection
and packet-connection logs, Mihomo TCP/UDP routing logs (including JSON messages),
and shadowsocks-rust established TCP tunnel / created UDP association logs.
Only known source positions are parsed. Outbound destinations, DNS logs, malformed
addresses and rejected Xray connections are not treated as client IPs. IPv4-mapped
IPv6 addresses are normalized. Private addresses remain available through the
existing Include non-public filter.

The panel indexes accepted logs in the same transaction as log storage. Batch
retries are idempotent. On upgrade, a background job reads retained panel logs in
pages of 1,000 and persists its cursor; restart resumes the scan. No Agent update
or new connection is needed to read existing logs. New uploads are indexed during
backfill, and request handling continues to use the indexed history. Existing
Agent-sampled rows are excluded from this view and expire through normal retention.

Only events actually present in the panel's retained logs can appear. Disabled
access logging, the configured minimum log level, upload loss or unsupported
message formats can leave gaps. SS Rust connection messages require debug logging.
An absence of recent log lines does not mean a node is offline. The collection
status shows whether the panel has read core logs and the latest log receive time;
it does not assert complete network coverage.

Missing protocol, inbound name, transport or local listener address/port stays
unknown (`""` / local port `0` in the API), displayed as unknown or a dash. Filters
for a specific field exclude records where that field is unknown. Destination
ports are never used as listener ports, and current configurations are not used
to guess metadata for historical logs. Transport reflects the log's connection
kind when available, not packet inspection or proof of the underlying tunnel.
These observations do not prove successful authentication or session start/stop.
NAT or a fronting relay changes the source IP visible to the core.

The panel coalesces repeated observations of a tuple into one row per minute,
with the first and last stored log timestamps. Log ingestion already normalizes
missing or excessively future timestamps and rejects events older than retention. The tuple includes agent,
engine, protocol, inbound, transport, source IP/port and local IP/port. Reuse of
the same tuple can merge separate sessions; counts are labelled observed tuples.
Each timeline bucket and the overall summary independently deduplicate tuples
and source IPs. Counts across buckets must not be added to infer total sessions.
A blank interval means no retained observations, not proven zero connections.

## Storage, access and retention

Records are retained for seven days and pruned by the existing panel retention
job. A single query spans at most seven days. Detail pages contain at most 200
rows and use an ID cursor; summaries cover the entire selected range. Collection
status uses the latest log receive time, without a heartbeat-expiry threshold.
Stored history remains queryable
when a node goes offline or the panel restarts.

The endpoint is `GET /api/v1/client-connections`, guarded by `core-logs.read` and
host administration access. Owners can read their nodes, administrators can read
visible nodes, and an engine-sharing grant does not expose another owner's
client IP telemetry. Owner-hidden nodes remain private from fleet administrators.
Revoked nodes are excluded from queries.

Schema version 67 adds log-source markers, unknown metadata support and a durable
backfill cursor to the connection history introduced in version 64.
Retention bounds time, not database bytes: size depends on observed tuples and
node count. No port traffic counters or quotas are changed. Back up PostgreSQL
before upgrading. Older binaries reject schema 67; rollback
requires restoring a matching pre-upgrade database backup and application version.

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
