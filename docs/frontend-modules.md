# Frontend Modules

The Web console is organized as ES modules under `frontend/modules/`. The
entrypoint, [`frontend/app.js`](../frontend/app.js), owns shared application
state and routing; feature modules own one feature area and expose a named API.
Route modules are loaded on demand by the route loader so adding a feature does
not require copying its implementation into the entrypoint.

`agents.js` and `configs.js` retain their public installer and helper exports.
Their focused collaborators own public-address selection (`agent-addresses`),
batch eligibility (`agent-batch`), card drag geometry (`agent-card-drag`), Komari
display/cache state (`agent-komari`), enrollment dialogs (`agent-enrollment`),
the shared source editor (`code-editor`), server-plan form fields
(`server-plan-form`), snapshot/deploy preflight (`live-config-state`), and
configuration archives (`config-archive`). Helpers do not import the route
module that composes them. Stateful controllers receive explicit collaborators
and read the current account's `state.data` instead of retaining an old session.

New frontend functionality should follow these rules:

- Put feature logic and rendering in a focused file in `frontend/modules/`.
- Export the public functions explicitly; keep implementation details private.
- Depend on shared helpers or other modules, never on `app.js` (the entrypoint
  may depend on modules, but the dependency must remain one-way).
- Add focused smoke coverage beside the existing `frontend/*_smoke.mjs` tests.
- Register route installation through the existing route loader and keep
  module initialization idempotent where a route can be revisited.

`make module-policy-test` checks the directory and dependency boundary. It is
also part of `make check`, while `make frontend-check` runs behavior smoke
tests. This keeps the structural rule enforced in every pull request.

## Browser regression ownership

`agents_browser_smoke.mjs` serves the fixtures and drives Chromium.
`agents_browser_runtime.mjs` selects the scenario and reports its result; it
does not own mock API responses or feature assertions. Shared browser assertions,
the per-page Agent fixture factory, and focused interaction scenarios live under
`frontend/browser/`. The existing standalone configuration, dashboard, sharing,
and user runtimes keep their own fixtures. Fixture installation must finish before
importing `app.js`, and each scenario receives its page-local fixture explicitly.
Keep all desktop, mobile, permission, stale-response, and navigation cases in the
smoke runner's mode list when moving a scenario.
