# Frontend Modules

The Web console is organized as ES modules under `frontend/modules/`. The
entrypoint, [`frontend/app.js`](../frontend/app.js), owns shared application
state and routing; feature modules own one feature area and expose a named API.
Route modules are loaded on demand by the route loader so adding a feature does
not require copying its implementation into the entrypoint.

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
