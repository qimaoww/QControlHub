# Module Architecture

QControlHub keeps feature boundaries explicit across the web console and Go
services. A module owns one cohesive responsibility and exposes a small API;
composition belongs at the application boundary.

## Go packages

- `internal/core` contains shared domain models and validation. It does not
  depend on transport, persistence, or operating-system adapters.
- `internal/store` contains PostgreSQL persistence. Keep schema declarations in
  `schema.go` and split persistence workflows by domain (for example
  `enrollment.go`, `agent_config.go`, and `traffic.go`).
- `internal/api` contains HTTP and WebSocket adapters. Authentication,
  resource handlers, and transport helpers should live in focused files while
  sharing the `Server` type.
- `internal/agent` contains node-side execution and platform adapters. Linux
  implementations use `_linux.go`; portable protocol and validation code stays
  in platform-neutral files.

New functionality should be added to the owning package or a new focused
package. Avoid adding unrelated handlers, SQL workflows, or platform code to a
large entrypoint file. Keep dependencies directed toward domain code and
adapters; avoid importing an application entrypoint from a feature module.

## Frontend

The web console follows the more detailed [frontend module
contract](frontend-modules.md). `frontend/app.js` owns shared state and route
composition, while feature rendering and reusable helpers live under
`frontend/modules/`. `make module-policy-test` checks the boundary on every
pull request.

When moving a responsibility, preserve the public API at the composition
boundary, add focused smoke coverage, and run the package tests that exercise
the moved code. This keeps refactors reviewable and allows future work to grow
through new modules instead of expanding monolithic entrypoints.
