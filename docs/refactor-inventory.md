# Modularization completion inventory

This inventory covers PR #203 after synchronizing `main` at `b84342e`.
It records ownership, not a maximum line count. Public APIs, ordered
initialization, asynchronous guards, and test coverage must survive each move.

## Completion work

| Area | Required ownership boundary | Status |
| --- | --- | --- |
| Frontend shell, Agents, configurations | Composition, views, controllers, shared lifecycle records | Completed in `70376fb` |
| Dashboard and settings | Loading, markup, page bindings | Complete |
| Access control and system TCP | Loading, validation, editor/view, submission | Complete |
| Client access and SubStore | Models, loading, results/views, interactions | Complete |
| Tasks and core logs | Loading/polling, presentation, filters/actions | Complete |
| Traffic | Models, page view, drag lifecycle, forms, synchronization dialog | Complete |
| Users | Account page, allocation lifecycle, invitations, quota view | Complete |
| Frontend smoke tests | Ordered domain suites with explicit fixture ownership | Complete |
| Browser and frontend Go contracts | Focused scenario phases and contract files; all 38 modes retained | Complete |
| Styles | 65 ordered authoritative source slices and checked single-file generation | Complete |
| API resources and WebSocket | Settings, updates, audit, logs, metrics, templates; presence/registry | Complete |
| Config documents and client profiles | JSON/YAML adapters; client contracts and protocol helpers | Complete |
| Store users | Accounts, sessions, atomic purge transaction | Complete |
| Agent public IP and mainland access | Lifecycle separated from network/source/cache adapters | Complete |
| Large backend regression suites | Tests colocated with the source owner; shared fixtures retained | Complete |

Frontend owners and their shared lifetimes are listed in
[frontend-modules.md](frontend-modules.md). The ten remaining route entrypoints
are now 18-159 lines; `module_smoke.mjs` is a 53-line ordered runner, and the
Agent action scenario is a 15-line phase runner. These measurements describe
the result, not a size limit for future features.

The backend regression moves cover core discovery/migration/logs, executor and
managed configuration, updater download/archive/install/release, client
transport/features/task transitions, API WebSocket/task integration, store
task lifecycle/performance/shared ports, and engine-specific server generation.
Shared fixtures stay beside those owners; no `TestMain` was moved.

## Deliberately retained boundaries

- `internal/store/schema.go` is atomic: schema version and ordered SQL stay
  together. A migration or purge transaction is not divided between functions.
- The three deployment distribution scripts remain standalone generated
  artifacts. Their authoritative slices live in `deploy/modules/`.
- `frontend/app.css` remains the single distributed stylesheet. Source
  boundaries must not reorder or change the cascade.
- A cohesive algorithm, adapter, fixture factory, or end-to-end regression is
  not divided merely to reach an arbitrary file length.
- Route entrypoints preserve installer/helper exports for compatibility.
  Production consumers use focused owners directly.
- Factories are inert. Shared mutable state has one owner; callbacks preserve
  request, account, navigation, draft, and in-flight mutation guards.

The remaining larger files were reviewed rather than omitted:

| Retained source or suite | Reason |
| --- | --- |
| `internal/store/schema.go`, `migration_test.go` | Atomic ordered schema and complete historical migration fixtures |
| `internal/api/wss_integration_test.go` | One end-to-end connection/task/result protocol lifecycle |
| `internal/agent/client_lifecycle.go`, `client_reconnect_test.go` | One session/reconnection lifecycle and its race matrix |
| `internal/serverconfig/accounting_tagged.go` | One tagged-outbound accounting transformation |
| `internal/agent/core_logs_import_linux.go`, `core_migration_openrc.go` | Cohesive log/service handoff workflows |
| `frontend/modules/refresh.js`, `frontend/refresh_smoke.mjs` | Shared stable-view/refresh primitives and their contract; no feature-specific workflow |
| `frontend/browser/agent-fixture.mjs` | One page-local fixture with explicit installation before the application |
| `deploy/tests/inherit-existing-core.sh` | Standalone inherited-core mapping regression harness |
| Focused traffic, access, enrollment, and native-core regression files | Complete domain-specific scenario matrices; native-core tests retain their opt-in prerequisites |

## Completion evidence

Source comparison against the pre-continuation baseline (`70376fb`):

- All 73 public exports across the twelve route facades are unchanged.
- Of 702 functions/callbacks in the ten route implementations, 673 retain their
  bodies after normalizing only private lifecycle references. Expanding the
  extracted collaborators reproduces the original ordered statements of all
  19 changed non-root functions. The ten installer roots were reviewed for
  inert construction and single-owner state.
- All 472 executable top-level smoke statements, including 775 assertion
  calls, retain their order. All 443 statements in the Agent action scenario
  retain their order across its five phases. Static side-effect smoke imports
  retain their original order.
- All 1,151 Go test/helper declaration keys, including 908 `Test*` declarations,
  are preserved with their platform constraints. Only ten frontend source
  contracts select explicit owner files instead of the former monolith; their
  assertions are unchanged. The new source-owner helper is the only added Go
  test declaration.
- The audit caught and corrected an accidental `core_logs_windows_test.go`
  filename: `core_logs_window_test.go` keeps the log-window regression enabled
  on Linux as well as other platforms.
- All 2,117 production Go declaration keys match synchronized `main`.
  Moved package initializers are constant regular-expression compilations;
  ordered schema declarations and initialization hooks are unchanged.
- The 396,759-byte distributed CSS and all three deployment distribution
  scripts are byte-identical to their pre-continuation versions.

Validation performed on the completed sources:

- `make non-browser-checks`, including module/PR/schema policies, generated
  assets, installer/redeploy/quick-start regressions, documentation, formatting,
  and Go vet.
- `node frontend/module_smoke.mjs` and uncached frontend Go tests, including
  all 38 Chromium modes.
- Uncached `go test -buildvcs=false -p 1 ./... -count=1` with an isolated
  PostgreSQL 17 container, plus a fresh complete store run after the filename
  correction.
- Alpine non-browser checks, all-package test compilation, the OpenRC/service
  manager/Agent-upgrade/prerequisite/System-BBR suite, configschema and
  serverconfig tests, and both command builds. The Alpine container has no
  Compose executable; the real Compose regression passed in the host checks.
- Production web image: all 127 modules and 129 matching gzip assets are
  present, all 273 import/re-export URLs share the index asset version, and
  browser fixtures, smoke tests, and stylesheet sources are excluded.

Permanent policies now reject unreachable production/test modules, imports
through route facades, cyclic production dependencies, unregistered CSS
sources, incomplete CSS delimiters, and stale generated artifacts. Specialized
live-network, native-core, and privileged namespace tests keep their existing
opt-in requirements; this refactor does not remove or weaken them.
