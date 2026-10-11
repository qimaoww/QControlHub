# Core log performance validation

Measured on 2026-10-11 against main `2bbd07b` and this branch. The browser is
Chrome 147 on Linux. These are individual observations on a shared host,
not percentile guarantees. Only synthetic logs appear in the recordings and
committed data. No production URL, node identity, session or log content is saved.

## Diagnosis

An authenticated, read-only production browser session measured:

- Aggregate 1000/engine: TTFB 12,274 ms, transfer 17 ms, 108,070 bytes compressed
  / 1,262,437 bytes decoded, 3153 loaded entries.
- Aggregate 2000/engine: TTFB 18,264 ms, transfer 25 ms, 211,693 bytes compressed
  / 2,466,329 bytes decoded.
- A selected node: preview TTFB 219 ms; full TTFB 700 ms, transfer 51 ms.
- UI actions produced 80-101 ms long tasks. Transport was a small fraction of
  the delay; the browser cannot separate database, pool, proxy and network wait
  inside TTFB. Production SQL timings were not accessible.

Local PostgreSQL 17 `EXPLAIN (ANALYZE, BUFFERS)` reproduced the missing fleet
access path: the existing `(agent_id, engine, id DESC)` covering index serves
node reads, while fleet reads fell back to primary-key heap scans. A quiet or
absent engine visits unrelated history across daily partitions. The new
`(engine, id DESC)` covering index allows each partition to seek that engine
and deliver a bounded window without payload heap reads. Query predicates,
permission checks, limits and JSON are unchanged. No incremental cursor is
introduced: compressed transfer was not the observed production bottleneck.

The existing preview was serial with the full read, and aggregate entry had no
preview. Both stages now start together; full-first results cancel the preview,
and a complete quiet preview cancels the speculative full read. Exact and
related fresh snapshots are reused; related snapshots are capped per engine.

The other bottleneck is forced layout during whole-shell refresh: the generic
view-state capture reads offsets for every descendant. On long rows, one
profiled search spent 601 ms in that capture, versus 47 ms filtering and 0.8 ms
counting. Log refresh now reconciles only changed regions; the inert stream
skips control/scroll capture. Source identity, counts, search text and row HTML
are reused. Off-screen rows defer layout. Selection feedback is confined to the
compact count, and new-row feedback is limited to three opacity animations.

## Comparisons

The existing large-log browser fixture, with 8000 searchable entries and 200 DOM
rows, recorded the following representative runs (milliseconds):

| Operation | Main | Branch |
| --- | ---: | ---: |
| Change to 2000/engine | 186 | 62 |
| Pagination | 154 | 24 |
| Full-window search | 125 | 19 |
| Cold node selection acknowledgement | 121 | 9 |
| Cached node selection acknowledgement | 141 | 19 |

The HTTP pressure fixture uses four engines, about 1.5 KiB of message text per
entry, 120 ms server delay and 4 MiB/s streaming, with compression disabled to
stress long responses. Both versions use the same server and data; the cold
1000/engine response is 6,991,454 bytes and the 2000/engine response is
13,988,454 bytes. Desktop viewport: 1280 x 720.

| Operation | Main | Branch |
| --- | ---: | ---: |
| First visible logs | 2393 ms | 1015 ms |
| Initial full window | 2697 ms | 2294 ms |
| 2000/engine full window | 4204 ms | 3724 ms |
| Search | 526 ms | 60 ms |
| Pagination | 589 ms | 127 ms |
| Cold node full window | 5096 ms | 3753 ms |
| Long tasks over 50 ms in this sample | 9 (max 465 ms) | 0 |

A cold aggregate read now sends two speculative stages rather than one; a hot
scope or a related fresh window sends one revalidation. Responses have the same
schema and size at a given limit. No-data polls still read the complete bounded
window. The larger long-log snapshot exceeds the existing 16 MiB cache budget,
so its repeat-switch metric is an acknowledgement, not a cache-hit claim. The
short-log run above verifies actual synchronous cache hits. Recorded resource
entries count completed requests; a final repeat-switch read may still be in
flight in the pressure sample.

A separate three-action CDP render profile (search, reset, page 2) separates
application work from rendering:

| Total across those actions | Main | Branch |
| --- | ---: | ---: |
| Filter/projection work | 48.2 ms | 15.1 ms |
| Count work | 2.6 ms | 0 ms (cached) |
| Row HTML generation | 21.9 ms | 1.2 ms |
| View-state capture | 1036.3 ms | 2.4 ms |
| Reconciliation including capture | 1057.5 ms | 12.0 ms |
| Layout events | 402 / 876.2 ms | 26 / 42.7 ms |
| Style updates | 427 / 97.4 ms | 40 / 7.6 ms |
| Paint | 51 / 11.5 ms | 50 / 11.2 ms |

Paint was already inexpensive. The removed repeated layout and unchanged-data
work account for most of the interaction improvement. Profiled durations can
include nested phases and should not be added together as exclusive CPU time.

The isolated database fixture seeds 160,000 entries across four nodes and seven
daily partitions. In the skewed case, two engines have no data and xray has only
1/40 of the rows. At aggregate 2000/engine, the warm read plus scan/sanitization
fell from 142.60 ms to 24.52 ms; JSON encoding was 13.32 vs 19.67 ms and the
4,551,502-byte decoded body was unchanged. Balanced 8000-entry reads were 56.40
vs 40.34 ms, with an unchanged 9,114,001-byte body. Timing varies; the regression
assertions check index key/coverage and zero heap fetches after VACUUM instead.

## Reproduce

Run against an isolated PostgreSQL instance and an installed Chrome/Chromium:

```sh
export QCH_TEST_DATABASE_URL='<isolated PostgreSQL URL>'
QCH_BROWSER_SMOKE_MODES=logs,logs-mobile,logs-restore node frontend/agents_browser_smoke.mjs
QCH_CORE_LOG_MEASURE=1 QCH_CORE_LOG_SKEW=1 go test ./internal/store -run TestCoreLogWindowMeasurements -count=1 -v
QCH_CORE_LOG_MEASURE=1 QCH_CORE_LOG_SKEW=1 QCH_CORE_LOG_BASELINE=1 go test ./internal/store -run TestCoreLogWindowMeasurements -count=1 -v
```

The baseline flag drops only the new index in the test's isolated schema.
Omit `QCH_CORE_LOG_SKEW` for balanced data. The database test logs the production
SQL's plan, scan/sanitize time, JSON time and payload size.

For HTTP measurements, serve a checkout of main and this branch in separate
terminals using the same script:

```sh
QCH_PERF_ROOT=/path/to/main/frontend QCH_PERF_PORT=44382 node scripts/core-logs-performance-server.mjs
QCH_PERF_PORT=44383 node scripts/core-logs-performance-server.mjs
```

Open each loopback URL with `playwright-cli`, then run:

```sh
playwright-cli run-code --filename=scripts/core-logs-performance.js
```

For phase and render measurements, start the servers with `QCH_PERF_TIMING=1`,
run the performance script, then
`playwright-cli run-code --filename=scripts/core-logs-render-profile.js`.
`QCH_PERF_GZIP=1` enables gzip for an additional production-like transport check.
No production API is proxied. Root-owned Chrome sessions require the local
Playwright config `browser.launchOptions.chromiumSandbox: false`.

Review recordings cover loading, 1000 to 2000/engine, complete-window search,
pagination, rapid node switches and an automatic-refresh reading session:
[desktop](desktop.mp4), [mobile](mobile.mp4).
To reproduce them, use
`node scripts/record-core-logs.mjs http://127.0.0.1:44383 desktop`
and replace `desktop` with `mobile`. The optional recorder uses Playwright CLI
and its matching ffmpeg download. Timing/trace details are in
[measurements.json](measurements.json).

## Validation and limits

- Log browser regressions pass on desktop and 390 x 844 touch emulation:
  1000/2000 per engine, 8000 total, long logs, multi-node cold/hot reads, full-window
  search, IME composition events, paging, focus/caret retention, unchanged-stream
  mutation count, metrics-only metadata, new-log reading anchors, failure
  backoff/recovery, hidden-page pause/resume and route-leave cancellation.
  Review also reproduces and fixes an ordinary input queued immediately before
  IME composition: composition start now cancels that pending filter frame.
- Unit regressions cover pending-read coalescing, preview/full races and
  cancellation, a fast failed full read with a successful late preview, content
  changes under the same ID, cache limits/TTL/isolation,
  account changes, revoked access and incomplete-window errors.
- PostgreSQL tests cover both covering indexes, per-engine limits/order,
  v71-to-v72 migration with retained rows, and permission integration.
- The default `make check` was run. It fails in the pre-existing
  `motion-selection` assertion `quota refresh retains focus`; the same failure
  reproduces on unmodified main `2bbd07b`. This change does not claim a green
  default full suite.
- `make non-browser-checks` passes. `go test -p 1 ./...` passes with
  `QCH_BROWSER_SMOKE_MODES=logs,logs-mobile,logs-restore` (including the isolated
  PostgreSQL suites). The remaining non-motion browser modes also pass, and
  `motion-popup-mobile` passes on recheck. The default motion-selection suite
  remains the baseline failure described above. Module runtime, module/smoke
  policy, frontend contracts, generated styles, formatting and schema-version
  checks pass.
- No updated backend was deployed to production. Post-change production TTFB,
  cold physical disk behavior, production index-build duration, real iOS/Safari
  and OS-level IME composition were not measured. Local network pressure is
  synthetic; production transport uses gzip. The 16 MiB session cache limit
  remains deliberate, and partial/oversized windows are not cached as complete.

## Compatibility and operational risk

Schema v72 adds an index without rewriting or deleting retained logs. Existing
frontends and API consumers keep the same contract. Both old and future daily
partitions acquire the fleet covering index. Retention, newest-first order,
per-engine limits, full-window local search, account scope and permission checks
are unchanged.

The new index duplicates log payload storage and adds one index update per
insert. The normal migration builds it transactionally, which can take time and
block log writes on a large retained dataset; production build duration and
space were not measured. Plan a maintenance window and available disk before
upgrading. Browsers without `content-visibility` simply render the bounded 200
rows normally. Roll back frontend/runtime changes while retaining schema v72;
the additive index can remain. A v71 binary rejects a v72 database, so a binary
rollback also needs a coordinated migration-version rollback, not just an old
image tag.
