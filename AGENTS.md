# QControlHub contribution boundaries

Keep every change inside the module that owns the behavior. New functionality
belongs in a focused domain file; entrypoints compose collaborators and should
not accumulate handler, SQL, platform, or rendering workflows.

- `internal/core` owns domain contracts, validation, and protocol values. It
  must not import persistence, HTTP, node-runtime, or command packages.
- `internal/store` owns PostgreSQL workflows. Keep ordered schema declarations,
  `currentSchemaVersion`, and `schemaSQL` together in `internal/store/schema.go`.
  Do not split a schema migration across files or change its order casually.
- `internal/api` owns HTTP and WebSocket transport. Routes compose focused
  handlers; handlers call domain and store APIs rather than command packages.
- `internal/serverconfig` owns proxy configuration parsing, generation, and
  engine-specific transforms. Keep shared contracts separate from Mihomo,
  Xray, sing-box, and Shadowsocks Rust implementations.
- `internal/agent` owns node-side execution and platform adapters. Platform
  behavior belongs in correctly tagged files.
- `cmd/` packages are executable composition roots and must not be imported by
  reusable packages.
- `frontend/app.js` and the shell modules compose the console. Feature UI and
  reusable helpers belong under `frontend/modules/`; follow
  `docs/frontend-modules.md` for its public module boundary.
- `deploy/modules/` contains authoritative deployment-script source slices.
  Regenerate the standalone deployment entrypoints through the checked-in
  generator; do not edit generated distribution scripts directly.

Put tests beside the module they verify. Run `make module-policy-test` after
changing dependency boundaries and the focused package tests after moving
behavior. Preserve public declarations and ordered initialization when a
refactor is mechanical.

Do not merge a pull request unless the user explicitly requests that merge.
