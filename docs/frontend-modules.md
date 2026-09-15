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
| `shell-feedback` | Notifications and confirmation-dialog bindings |
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
| Core operations, settings, batch actions, and card interactions | `agent-core-actions`, `agent-settings`, `agent-batch-controller`, `agent-card-interactions` |
| Enrollment dialogs and page bindings | `agent-enrollment` |
| Live configuration loading, snapshot reads, and deployment recovery | `live-config-page`, `live-config-reader`, `config-deployment` |
| Live editor markup, navigation, and submission | `live-config-view`, `live-config-navigation`, `live-config-submit` |
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
one-way composition dependencies, and an acyclic module graph. It is also part
of `make check`, while `make frontend-check` runs behavior smoke tests.
`controller_modules_smoke.mjs`, `session_api_smoke.mjs`, and
`shell_modules_smoke.mjs` cover the extracted lifecycle and composition
contracts alongside the feature and browser regressions.

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
fixture explicitly. Preserve scenario order and all 38 desktop, mobile,
permission, stale-response, and navigation modes when moving assertions.
