# Module Architecture

QControlHub keeps feature boundaries explicit across the console, control
plane, node runtime, and deployment artifacts. A module owns one cohesive
responsibility and exposes a small API. Composition belongs at the application
boundary.

## Go ownership

| Area | Owns | Main composition point |
| --- | --- | --- |
| `cmd/control-plane` | Process configuration, lifecycle wiring, diagnostics, and maintenance loops | `main.go` plus focused command helpers |
| `cmd/agent` | Agent process configuration and executable wiring | `main.go` plus focused command helpers |
| `internal/core` | Domain contracts, permissions, validation, task protocol, and public model values | Focused contracts such as `agents.go`, `tasks.go`, `configs.go`, and `protocol.go` |
| `internal/store` | PostgreSQL access, transactions, migration execution, and per-domain persistence workflows | `store.go` opens the pool; `schema.go` holds the ordered schema contract; domain files own agent, configuration, task, traffic, enrollment, and SubStore workflows |
| `internal/api` | HTTP routes, authentication/session middleware, WebSocket transport, and resource handlers | `server.go` constructs `Server`; `routes.go` composes endpoints; focused handler files own agents, configs, tasks, enrollment, regions, Komari, downloads, resources, and SubStore sync |
| `internal/serverconfig` | Parsing, generating, validating, and transforming core configurations | Shared plans/models remain engine-neutral; engine transforms live in Mihomo, Xray, sing-box, and Shadowsocks Rust files, including access-control and accounting variants |
| `internal/agent` | Node-side task execution, service lifecycle, core installation, discovery, migration, and operating-system adapters | Executor and lifecycle composition call focused runtime and platform files |
| `internal/hostmetrics` | Host resource collection without control-plane or node-runtime dependencies | Focused CPU, memory, filesystem, and network collectors |
| Supporting packages | Authentication (`authn`), geo/IP data (`geoip`, `cnip`), network policy (`netpolicy`), notifications, Komari, and configuration catalog utilities | Small package APIs consumed by adapters |

Dependencies point from adapters toward domain code. `internal/core` must not
depend on adapters. `internal/store` does not depend on `api`, `agent`, or
`cmd`; `api` does not depend on `agent` or `cmd`; `hostmetrics` does not depend
on `agent`, `api`, or `store`; and command packages are never imported. The Go
part of `make module-policy-test` parses every production Go source file, so
these rules apply across build-tag variants as well as the current platform.

`schema.go` is intentionally a large, atomic source: migration ordering,
`currentSchemaVersion`, and `schemaSQL` form one contract. Schema changes must
increase the version when required and pass `make schema-policy-test`.

## Frontend ownership

`frontend/app.js` owns shared console state, navigation lifecycle, and lazy route
composition. Shell/session collaborators own transport, login, stable shell
rendering, appearance, feedback, and preloading. Feature rendering and reusable
helpers live under `frontend/modules/`; the detailed public boundary and
controller lifetimes are in [frontend-modules.md](frontend-modules.md).

The frontend policy test prevents modules from importing `app.js` or the
Agent/configuration composition facades and rejects dependency cycles. Add a
feature to its owning module or create a focused module with a named export;
keep composition at the application boundary. Controller construction is inert,
and asynchronous work must retain account and navigation isolation.

## Deployment artifacts

The remotely consumed scripts stay standalone at their established paths:
`deploy/quick-start.sh`, `deploy/existing-core-mapping.sh`, and
`deploy/remote/install-agent.sh`. Their editable source slices live under
`deploy/modules/`, grouped by bootstrap, options, state/configuration,
discovery, installation, lifecycle, and dispatch responsibilities. The
standard-library generator concatenates an explicit manifest into the
distribution scripts. Run its `--check` mode (included in the deployment
checks) after editing a source slice, or run `make generate-deploy-scripts` to
write the standalone outputs. This preserves `curl` use, embedded assets,
Docker builds, and existing installer paths.

## Working on a feature

Add behavior to the module that already owns its domain. Keep entrypoints thin,
avoid reverse dependencies, and put focused tests beside the implementation.
When mechanically moving declarations, preserve public names, comments, SQL,
and initialization order. Run the relevant package tests, then run
`make module-policy-test` when a boundary changes.
