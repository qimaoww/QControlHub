# Frontend Modules

The Web console is organized as ES modules under `frontend/modules/`. The
entrypoint, [`frontend/app.js`](../frontend/app.js), owns shared application
state and routing; feature modules own one feature area and expose a named API.
Route modules are loaded on demand by the route loader so adding a feature does
not require copying its implementation into the entrypoint.

## Shell and session ownership

`app.js` composes the shell and lazy route installers. It retains shared state,
navigation epochs, route cancellation, login cleanup, and the latest-render
scheduler; it does not own feature markup or HTTP transport.

| Module | Responsibility |
| --- | --- |
| `session-api` | JSON transport, CSRF headers, read-only route cancellation, account guards, render-scoped reads, and account-scoped panel caching |
| `login-page` | Login markup and submission; failures return to the application's login-cleanup callback |
| `shell-view`, `shell-context`, `shell-icons` | Stable shell reconciliation, workflow navigation, context sidebars, and icon data |
| `shell-appearance` | Theme persistence and the current account's font scale |
| `shell-feedback`, `shell-feedback-view` | Notifications and confirmation-dialog bindings |
| `routes`, `route-warmup` | Hash resolution, navigation-intent preloading, and cancellable idle warmup |

Factories are inert: construction must not request data, render, register
listeners, or invoke callbacks that refer to collaborators still being wired.
Navigation preloading is bound explicitly after route installation is composed.
Only reads inherit the route's abort signal; mutations keep their own optional
caller signal. An old account's response must not render login, migrate node
ordering, or update the new account.

## Agent and configuration ownership

`agents.js` and `configs.js` retain their public installer and helper exports,
but serve as composition boundaries. Other production modules import the
focused owner directly, not these compatibility facades.

| Area | Owners |
| --- | --- |
| Agent page loading and composition | `agents` |
| Agent rendering and workspace navigation | `agent-view`, `agent-workspace` |
| Polling, structural signatures, and interaction-aware refresh | `agent-refresh` |
| Core operations, settings, batch actions, and card interactions | `agent-core-actions`, `agent-settings`, `agent-batch-controller`, `agent-batch-view`, `agent-batch-feedback`, `agent-card-interactions` |
| Enrollment dialogs and page bindings | `agent-enrollment` |
| Live configuration loading, snapshot reads, and deployment recovery | `live-config-page`, `live-config-reader`, `config-deployment` |
| Live editor markup, navigation, and submission | `live-config-view`, `live-config-navigation`, `live-config-submit`; `config-workspace-toolbar` composes contextual actions and secondary tools |
| Preset loading/mounting, events, task feedback, and markup | `preset-editor`, `preset-bindings`, `preset-status`, `preset-view` |
| Configuration archives | `config-archive` |

Reusable helpers remain in `agent-addresses`, `agent-batch`, `agent-card-drag`,
`agent-service-state`, `agent-komari`, `code-editor`, `server-plan-form`, and
`live-config-state`. Stateful controllers receive explicit collaborators and
read the current `state.data`; captured account data and navigation epochs must
be checked before applying asynchronous results.

Preserve these shared lifetimes when changing a controller:

- Only a successful Agent render advances the structural comparison markers.
  Batch bindings retain the same mutable node map that polling revalidates.
- Snapshot reads and invalidation share one monotonic request counter.
  A stale task is drained before a replacement read; Agent bytes stay separate
  from saved-but-not-deployed editor content.
- Deployment recovery and status feedback share one account-scoped task
  monitor. An old failure cannot clear a newer pending deployment, while every
  successful deployment invalidates the live baseline.
- Preset bindings and status share the editor's private lifecycle record.
  Do not copy its request counter, host, save lock, or current context.

## Remaining route ownership

All fourteen route facades retain their public installer/helper exports. The
remaining twelve routes have the following focused owners:

| Route | Owners |
| --- | --- |
| Dashboard | `dashboard-model`, `dashboard-view`, `dashboard-bindings`; panel metrics keep their own lifecycle |
| IP quality | `ip-quality-controller`, `ip-quality-model`, `ip-quality-view`, `ip-quality-report-view`, `ip-quality-archive-view`, `ip-quality-bindings` |
| Settings | `settings-view`, `settings-bindings`; the route loads account-scoped settings |
| Access control | `access-control-controller`, `access-control-view`, `access-control-bindings`, `access-control-dialog` |
| System TCP/BBR | `system-bbr-model`, `system-bbr-view`, `system-bbr-editor`, `system-bbr-presets` |
| Client access | `client-access-model`, `client-access-view`, `client-access-results`, `client-access-bindings`, `client-access-profiles`, `client-clipboard` |
| SubStore | `substore-model`, `substore-view`, `substore-bindings`, `substore-selections`, `substore-targets` |
| Tasks | `task-model`, `task-view`, `task-timeline`, `task-bindings` |
| Client connection IP | `client-connection-model`, `client-connection-view`, `client-connection-bindings` |
| Core logs | `core-log-model`, `core-log-source-status`, `core-log-selection`, `core-log-view`, `core-log-bindings` |
| Traffic | `traffic-model`, `traffic-accounting-view`, `traffic-form-model`, `traffic-form-view`, `traffic-order`, `traffic-card-interactions`, `traffic-view`, `traffic-sync-dialog`, `traffic-forms`, `traffic-bindings` |
| Users and quota | `user-model`, `user-view`, `user-bindings`, `user-allocations`, `user-allocation-view`, `user-account-editor`, `user-quota`, `user-quota-view` |

The access-control controller is shared by the standalone route and embedded
configuration restrictions. Embedded consumers import that controller, not the
route. Client access and SubStore share `card-masonry`, but every factory call
owns a separate observer; rendering disconnects that route's previous observer
before binding the new cards.

Preserve these lifecycle boundaries:

- IP quality keeps its report snapshot, date/request serial, mutation guard and
  poller inside one controller. Only same-account, same-navigation, latest-date
  responses render. Failed reads never introduce demo data; a retained same-date
  snapshot is explicitly stale and cannot authorize writes. Reconciliation
  preserves expanded reports without retaining another date's records.
- User loading, allocation editing, draft capture, and quota views share one
  lifecycle record. Read and view serials remain monotonic; allocation saves
  keep their original account, draft, and connected-element guards.
- System TCP loading and editing share the same task map, drafts, pending
  submissions, errors, and account identity. Account changes reset them in the
  route before loading. `system-bbr-presets` owns the preset catalog and pure
  draft preparation. It validates every preset field against the server rules
  and reported parameter availability before merging into a fresh draft;
  unrelated selections remain intact. The editor retains confirmation and
  submission through the existing `configure-tcp` workflow. Before submission,
  it rechecks selected parameters against the confirmed baseline; missing or
  changed values retain the draft and require another confirmation.
- SubStore loading and selection/target actions share one account-scoped
  record, including the current target and pending selection-save promise.
- Traffic keeps one interaction gate and one deferred render at the route.
  The card controller owns drag cancellation, and the synchronization dialog
  owns its pending flag. Cancel, render, and rebind keep their original order.
- Task loading retains polling/cache coordination while `task-timeline` owns
  reconciliation, pagination, and scroll anchors. Core-log loading retains
  staged reads and account/navigation guards; selection persistence, source
  status, markup, and bindings have separate owners.

## Changes and checks

New frontend functionality should follow these rules:

- Put feature logic and rendering in a focused file in `frontend/modules/`.
- Export the public functions explicitly; keep implementation details private.
- Depend on shared helpers or other modules, never on `app.js` (the entrypoint
  may depend on modules, but the dependency must remain one-way).
- Add focused smoke coverage beside the existing `frontend/*_smoke.mjs` tests.
- Register route installation through the existing route loader and keep
  module initialization idempotent where a route can be revisited.

`make module-policy-test` checks named exports, existing import targets,
one-way composition dependencies for all fourteen route facades, an acyclic module
graph, reachability of every production module from the application, and
reachability of every extracted smoke/browser test module from its runner.
It is also part of `make check`, while `make frontend-check` runs behavior smoke tests.
`controller_modules_smoke.mjs`, `session_api_smoke.mjs`, and
`shell_modules_smoke.mjs` cover the extracted lifecycle and composition
contracts alongside the feature and browser regressions.
`route_modules_smoke.mjs` verifies inert construction and compatibility exports
for the remaining routes.

## Smoke and source-contract ownership

`module_smoke.mjs` is the ordered regression runner. It keeps the existing
side-effect smoke imports, then awaits the domain suites under `frontend/smoke/`.
Those suites expose inert `run` functions. Only the original shared read-only
fixtures cross suite boundaries; temporary DOM globals retain their original
setup and restore order. Deployment recovery phases explicitly receive one
fixture and restore it in `finally`. Public-address checks separate models,
DOM/CSS contracts, overview rendering, and live metric updates.
The IP quality controller smoke runs through an awaited import after these
suites, so its temporary DOM globals cannot overlap their asynchronous checks.

Go source-level frontend contracts live in focused `*_contract_test.go` files.
`feature_sources_test.go` names each feature's source owners explicitly: do not
traverse arbitrary dependencies, where an unrelated feature could accidentally
satisfy a missing assertion. Keep existing test names and assertions when
moving a contract.

## Stylesheet source and distribution

`frontend/styles/manifest.json` lists all authoritative CSS slices in cascade
order. Their numeric prefixes document the historical layering; shared theme
and responsive layers must not be regrouped by route in a way that changes
which rule wins. Source boundaries occur between complete rules or conditional
groups, never inside a selector, declaration, string, or comment.

Edit the owning source slice, register any new slice exactly once in the
manifest, then run `make generate-styles` and `make styles-check`. The generator
concatenates bytes without adding separators or reformatting. The check rejects
unlisted, missing, duplicate, non-regular, or incomplete sources and a stale
generated file. Both Debian and Alpine non-browser check groups run it.
`frontend/app.css` remains the single distributed stylesheet; the web image
ships that artifact, not the source manifest or test modules.

## Browser regression ownership

`agents_browser_smoke.mjs` serves the fixtures and drives Chromium.
`agents_browser_runtime.mjs` selects the scenario and reports its result; it
does not own mock API responses or feature assertions. Shared browser assertions,
the per-page Agent fixture factory, and focused interaction scenarios live under
`frontend/browser/`.

`config_inbounds_browser_runtime.mjs` and `users_browser_runtime.mjs` are
compatibility entrypoints. Configuration scenarios share `config-fixture.mjs`
and are separated into inbound, outbound, outbound-preset, and common-field
workflows. User scenarios separate allocation editing, allocation races, and
invitation consent, retaining their own per-scenario state and shared helpers.
The smaller standalone configuration, dashboard, and sharing runtimes retain
their cohesive fixtures.

Importing a scenario must not install its fixture. Fixture installation must
finish before importing `app.js`, and each Agent scenario receives its page-local
fixture explicitly. Preserve scenario order and all registered desktop, mobile,
permission, stale-response, and navigation modes when moving assertions.

`browser/agent-actions.mjs` runs overview, enrollment, batch, detail, and rename
phases in that order with the same page-local fixture. Each phase owns its
assertions, not a second fixture installation or application import.
`browser/ip-quality.mjs` owns desktop, real touch-mobile, and read-only report
scenarios, including third-party text escaping and the production CSP.
