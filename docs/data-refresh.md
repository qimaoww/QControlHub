# Data refresh model

QControlHub separates data acquisition from view application. Route changes
cancel the active route request immediately, and refresh channels reject
responses whose request sequence, route epoch, or route ownership is stale.
Same-route renders reconcile the existing DOM instead of replacing the
application tree.

## Refresh inventory

| Page or flow | Data sources | Refresh entry | View application |
| --- | --- | --- | --- |
| All routes | overview and settings | initial route, hash change, task refresh button | latest-route scheduler; same-route keyed reconciliation |
| Dashboard | agents, recent tasks, preloaded overview | route render | same-route reconciliation |
| Node settings | agents, enrollment tokens, preloaded overview, metric history | route render, manual metrics refresh, 2-second metrics timer, node mutations | metric fields patch in place; structural changes coalesce behind drag and FLIP completion |
| Kernel presets | agents, deployments, client access, per-node configs, preloaded overview | route and selected-node hash | one selected keyed workspace; no background timer |
| Node config | config workspace, generated plans, fields, revisions | route, node/engine/protocol/field selection, saved mutation | request sequence guard plus same-route reconciliation; generated parameters patch only their controls |
| Live config | agents, node config workspace, task result and snapshot | route, node/engine selection, manual read, post-task completion | request sequence guard plus same-route reconciliation |
| Client access | client profiles and agents | route, sidebar/filter/search selection, address mutation | refresh channel plus same-route reconciliation |
| Config archive | configs, templates, agents, revisions | route, selection, save/restore/delete/template mutation | request sequence guard plus same-route reconciliation |
| Tasks | tasks, agents, bounded settings cache | route, manual refresh, 0.6–5-second timer, cancel/retry | one effective request; keyed task-card reconciliation and in-place clock patches |
| Core logs | logs and agents | route, filters, 10-second timer | one timer and refresh channel; keyed log reconciliation |
| Traffic | agents and traffic policies | route, 5-second timer, create/edit/reset/delete | one timer and refresh channel; keyed policy reconciliation |
| Settings | panel settings and users | route and settings/user mutations | same-route reconciliation |

The task-result wait loop remains a bounded 600 ms status read used only after
an explicit live-config action. It does not render while waiting; the final
snapshot is applied through the guarded live-config route.

## Render and binding audit

- `shell()` remains the common page composition entry, but `app.innerHTML` is
  used only for login or a real route change. A same-route refresh parses an
  off-DOM template and reconciles it into the current application tree.
- The remaining module `innerHTML` assignments either build detached templates,
  update a select after an explicit user choice, populate the one-time command
  modal, or fill an initially empty metric-history placeholder. Periodic data
  paths do not replace an active page, list, dragged card, or form subtree.
- Node, preset, client-profile, task, log, traffic-policy, template, and user
  records use stable IDs or explicit refresh keys. Additions and removals move,
  insert, or remove only the affected keyed child.
- Repeated render bindings use property handlers or `bindEvent()`. The latter
  retains one native listener and swaps its current callback, including
  pointer, editor, form, filter, and dialog handlers.
- Hash parsing is owned by the route scheduler. Preset and node-detail hashes
  select an object-specific workspace key; filter links stay on their current
  route. Anchor scrolling is limited to explicit navigation and is consumed
  once rather than repeated by background polling.
- Object switches intentionally replace the keyed main workspace so a draft
  from one node, engine, inbound, field, or revision cannot leak into another.
  Ordinary refreshes of that same object preserve its local state.

## Interaction invariants

- A stable keyed record keeps its DOM node identity. Real additions and
  deletions insert or remove only the affected record.
- Reconciliation preserves window and internal scroll positions, focused
  controls and their selection, dirty form values, selected options,
  `details` and modal state, and existing event-listener identity.
- Rebinding updates the callback behind one stable listener instead of adding
  duplicate listeners.
- Node-card structural refreshes wait until pointer capture, the drag ghost,
  the drop target, and the FLIP transition or fallback cleanup have settled.
- A failed background refresh marks the page-local status without clearing or
  replacing the current data. Its single timer remains available for recovery.
- Mutation notices use a fixed overlay and never scroll or shift the workspace.
- Leaving node settings explicitly cancels pointer/FLIP state, removes its
  ghost, and discards queued callbacks from the departed page.

## Runtime performance contract

The executable refresh smoke uses controlled slow and out-of-order work. Its
representative keyed update adds one record, removes one record, replaces zero
stable nodes, and retains the focused input, selection, details/modal state,
workspace scroll, nested scroll, and window scroll. It also asserts:

- one active request result per refresh channel;
- one future timer per poller, including concurrent triggers and recovery;
- one fleet request for a three-node metrics patch;
- two parallel data requests per ordinary task refresh and per log or traffic
  refresh cycle (the task page refreshes its settings cache every 30 seconds);
- structural node refresh remains deferred from pointerdown through drop and
  FLIP cleanup.

| Representative path | Requests and timers per background cycle | DOM/interaction result |
| --- | --- | --- |
| Node settings, 3 agents | 1 fleet request; 1 future 2-second timer | metrics patch existing nodes; structural patches wait for drag/FLIP |
| Selected-node preset | 0 background requests; 0 timers | exactly 1 selected workspace and 0 unselected node workspaces |
| Tasks | 2 requests; 1 future configured timer; settings adds 1 request only after its 30-second cache expires | unchanged cards retain identity; changed fields patch inside their keyed card |
| Core logs | 2 requests; 1 future 10-second timer | keyed rows reconcile in the existing stream and retain its scroll position |
| Traffic | 2 requests; 1 future 5-second timer | keyed policy cards and dirty open edit forms retain identity |

The controlled representative reconciliation reports one real insertion, one
real removal, zero stable-node replacements, and zero deltas for workspace,
nested-container, and window scroll positions.
