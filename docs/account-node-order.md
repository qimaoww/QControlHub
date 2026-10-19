# Account node ordering

## Data and API

Schema v79 adds `account_node_orders(account_id text PRIMARY KEY, node_ids text[],
revision bigint)`. Database accounts use their immutable `usr_` identity, restricted
environment tokens use their existing hashed workspace identity, and the single
environment administrator uses `environment-admin`. The session response exposes
`preference_id` for the last case. These identities never depend on a browser,
login session, username, node ownership, or node-management permission. Account
purge also deletes its preference.

Authenticated `GET /api/v1/preferences/node-order` returns `{ids, revision}` with
`Cache-Control: no-store`. Revision zero means no saved preference. Existing IDs
are restricted by the same live visibility predicate as the fleet list, including
hidden nodes, accepted shares, revocation, and disabled accounts. Newly visible
nodes append by enrollment time and ID. Offline nodes remain in their position.
A read persists normalization of an existing preference and advances its revision.

`PUT` accepts `{ids, revision, initialize?}`. It requires authentication and CSRF,
without node-management permission. Unknown fields, missing/null arrays or
revisions, malformed or duplicate IDs, oversized bodies/lists, and invisible nodes
are rejected. A transaction locks the authenticated account and takes an account
advisory lock. A stale normal write returns 409. Partial lists replace only the
selected nodes' slots in the complete visible order. Hidden, filtered, or paged-out
nodes retain their relative order. First initialization is atomic: an existing row
wins over all later `initialize` requests, including another device's migration.

## Browser lifecycle and migration

The shared module keeps a confirmed order and a read/write queue for the current
account lifetime. Navigation loads server order before rendering. Fleet polling
refreshes order in parallel, preserving roster authorization/removal timing.
Account-order reads and save readbacks survive route changes; account changes
still invalidate their responses. Client exports refresh their order before
rendering; client IP manual refresh reloads
order before constructing its order-sensitive cursor query. Cached filter switches
remain immediate. Changed order resets incompatible client IP pagination cursors.

Legacy migration only reads the authenticated account's scoped local key when its
identity is stable and there is no server preference. Only IDs returned by the
complete server-visible fleet are submitted. Surviving legacy IDs lead the order;
new IDs append. Bare/global keys and ambiguous role/username-only keys are never
uploaded. Confirmed server caches are marked separately so a default read cannot
become a subsequent legacy import. Existing server order always wins. Browser
storage denial does not prevent database reads or writes.

`card-move.js` accepts both synchronous booleans (traffic cards) and promises (node
cards). It keeps the existing preview, pointer/touch handling, keyboard bindings,
landing animation, theme, and live region. A separate save gate prevents refresh
from replacing cards during pending requests. Gestures update the DOM immediately;
success is announced only after confirmation. Saves serialize, conflicts rebase the
subset, and old operations cannot announce success over later gestures. Failed
writes read back the server state, restoring confirmed order and focus. Lost write
responses can be confirmed by readback; if readback also fails, the UI explicitly
marks the attempted order unconfirmed and asks the user to refresh/retry. A later
successful poll reconciles the visible cards. Logout/account change discards the
in-memory order and invalidates queued requests and late responses.

## Existing consumers

All existing callers share the same account order: node settings cards, dashboard,
eight context sidebars (node/config/archive, client access, TCP, core logs, traffic,
IP quality and Sub-Store contexts), client access cards and engine/profile filters,
client IP sources and server query ordering, IP quality cards, TCP cards, Sub-Store
groups, outbound preset node choices, and traffic fallback node grouping. Traffic's
separate personal port-card order continues to take precedence. Existing business
filters, engine grouping, and pagination are retained. No CSS or font changes.

See [validation evidence](validation/account-node-order/README.md).
