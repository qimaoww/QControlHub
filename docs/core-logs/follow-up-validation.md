# Core log conditional refresh and CI follow-up

Measured on 2026-10-11 against **main `df1ad925`**, the merge of [#258](https://github.com/qimaoww/QControlHub/pull/258). This report records only the new changes; the preview concurrency, covering indexes, pagination and regional DOM reconciliation from #258 are already present in both sides.

## CI diagnosis

[Main CI run 38119159082](https://github.com/qimaoww/QControlHub/actions/runs/38119159082) failed on `df1ad925aa270a146b2ad342143546d8ff74c1ea` in Alpine / OpenRC. Its Go frontend shard failed `TestAgentBrowserRuntimeSmoke` at `browser/motion.mjs`: "reverse navigation must not flash to a fully opaque frame". Debian, the upgrade sandbox and the separate Alpine browser invocation passed. The complete failed task log was read. The [last pre-merge run](https://github.com/qimaoww/QControlHub/actions/runs/38118366847), branch `codex/core-logs-performance` at `9e5d2457`, passed all six test/build jobs.

The existing assertion sampled opacity on opposite sides of asynchronous navigation and a later polling tick. Delaying the second observation by 200 ms reproduced the same failure with unchanged main animation code: normal resume playback was mistaken for a discontinuity. The fixed test waits for a visible departure, captures the reversal with a MutationObserver before the next rendering opportunity, and uses an explicit slow-request gate. The original 0.2 tolerance, DOM identity, restored opacity and restoration-before-request-completion assertions remain. A deliberate 200 ms delayed observer still passes; a separate mutation forcing the resume opacity to 1 correctly fails (`0.431451 -> 1`). Four desktop/mobile, normal/reduced motion modes pass on host Chromium and Alpine 3.24.1 / Chromium 152 / Node 24. No CI check, workflow or threshold was removed.

The follow-up PR runs also exposed two existing fixture preconditions. [Run 38121771629](https://github.com/qimaoww/QControlHub/actions/runs/38121771629), at `1b6ecd4f`, reversed before the simulated slow request had started on Debian; the test now waits for the request to enter its gate. [Run 38122775847](https://github.com/qimaoww/QControlHub/actions/runs/38122775847), at `21041ca4`, passed Debian but failed the Alpine popup test. Its injected menu lived inside the traffic page's server-owned workspace, which a two-second background poll legitimately reconciles. Triggering the actual poll at menu opening reproduced the original failure: the host disconnected, the menu closed and focus was lost. The fixture now lives outside that workspace, like the existing disclosure fixtures. A new regression observes a real poll removing a workspace marker, then asserts that the isolated menu remains connected, open and focused. Original reveal, rebind, Escape, outside-click and focus-exit assertions remain. Four popup modes pass on host and Alpine Chromium. Disabling the disclosure transition in a temporary negative control still fails the original reveal assertion.

[Run 38124636031](https://github.com/qimaoww/QControlHub/actions/runs/38124636031), at product commit `0b5025d1`, passed Debian and the upgrade sandbox, but the standalone Alpine browser suite hit a completion-marker timeout. Every Alpine Go shard passed, including the same complete browser suite through `TestAgentBrowserRuntimeSmoke` (583.547 s). The old error omitted the active mode, page state and browser stderr, so these logs do not establish a specific page failure, crash or OOM. The workflow did run two complete 71-mode Chromium suites concurrently with the remaining compilation/checks. The standalone suite now runs after every parallel task finishes, while both suites and their original per-mode deadlines remain. The runner's repeatable `--after-checks` stage executes even after a preceding failure and retains failures from every stage. Seven stub regressions verify stage ordering, CLI order, failure propagation/continuation and dry-run behavior. Debian/Alpine job limits (35/40 minutes) and the Go aggregate limit (20 minutes) remain unchanged. Latest remote outcomes are recorded in the PR; previously skipped image builds are not counted as passes.

Browser failures now include the active mode/URL, bounded Chromium stderr, process/CDP state, recent runtime exceptions and console errors, and a live page/animation snapshot collected before cleanup. Diagnostic subscriptions run asynchronously and cannot move the original observation deadline. Snapshot collection is attempted once after failure, with a separate 1500 ms bound; a stopped renderer cannot hang diagnostics or turn a failed mode into a pass. Ten fake-CDP regressions use virtual time to verify the original 45/60-second verdict boundaries, stalled subscriptions/snapshots, connection/startup failure, crash/detach, retained page assertions and bounded output. Expected error-handling cases still pass rather than failing on collected console exceptions.

## Current read path and changes

- Agent file tailing checks an idle source about once per second. Uploads use a 500 ms tick, at most 32 entries per acknowledged batch, and a bounded queue of 2048 entries. With negligible RTT the tick permits about 64 entries/s; sustained excess can drop the oldest queued entries. These are existing collection/update-latency limits, distinct from API TTFB or browser stalls; this PR does not change the agent protocol.
- Store writes already use one batched INSERT in a transaction. Real 32-entry uploads took 2.935 ms locally and 128.708 ms with the existing test proxy adding 20 ms per protocol write (six writes). These are diagnostics, not claimed improvements.
- A cold scope already starts preview (200/engine), complete window, and node metadata concurrently. Full-first cancels preview; a quiet complete preview cancels the speculative full request. Exact/related snapshots reuse session memory (three scopes, 16 MiB estimate, 30 s TTL). Search covers the full 8000-entry window while only 200 rows are mounted. No new cursor or incremental merge is introduced.
- The new API emits a weak SHA-256 ETag of the **actual sanitized JSON representation**, after normal authorization and the current bounded query. Matching `If-None-Match` returns a bodyless 304. Insertions, deletions, expiry, scope/permission changes and corrections under the same ID are evaluated from current data. `Cache-Control: no-store` remains; no server/shared response cache is added. CORS explicitly permits the validator and exposes ETag.
- Frontend validators are weakly associated with an owned array and its exact request URL. Evicted, partial, related-limit or other-node arrays cannot validate another representation. A 304 preserves the active array without JSON parsing, entry comparison, projection, counting, row markup or DOM reconciliation. Older servers without ETag continue to return ordinary arrays. Account changes and stale request sequences remain guarded.
- Metadata still refreshes state, but only membership, names, presence and collection availability repaint the logs. Metrics-only changes skip that work. The displayed storage policy and read permissions also participate in invalidation: asynchronously loaded minimum level/retention changes repaint once, while equivalent, unrelated or hidden settings do not. Failed refreshes retain valid rows and show feedback; recovery clears it. The ten-second poll, exponential failure backoff to sixty seconds, hidden-tab pause and cancellation on navigation remain.
- Stored messages are already sanitized, but the ANSI regexp replacement copied even clean strings on every read. An ESC-byte check now bypasses just that redundant replacement. Invalid UTF-8, NUL, whitespace and legacy/colorized cleanup retain their existing behavior.

## Real browser comparison

Chrome 153 on Linux, 1280 x 720, identical synthetic multi-node data and loopback HTTP server: 120 ms response delay, 4 MiB/s streamed transfer, about 1.5 KiB message text per long entry. Both versions use the **same updated fixture and script**. All times below are individual observations on a shared host, not percentiles. Completion waits include browser body consumption and changed-row paint, not just server response completion. The browser is real; this pressure server and node metadata are simulated, not the production Go API.

| Operation, long logs, gzip disabled | Main | This PR |
| --- | ---: | ---: |
| First visible logs, cold page | 853 ms | 859 ms |
| Initial 4000-entry window complete | 1955 ms | 2003 ms |
| Expand to 8000 entries | 3625 ms | 3622 ms |
| Cold node complete | 3680 ms | 3666 ms |
| Search full window | 47 ms | 48 ms |
| Page 2 | 100 ms | 99 ms |
| Changed-window refresh through paint | 3694 ms | 3721 ms |
| Three unchanged polls through browser consumption | 11071 ms | 747 ms |
| Unchanged polls: requests / status | 3 / 200 | 3 / 304 |
| Unchanged polls: response body bytes | 41965362 | 0 |
| Unchanged polls: JSON reads | 3 | 0 |
| Unchanged polls: long tasks >50 ms | 3 (max 72 ms) | 0 |

The unchanged-poll timing includes three **100 ms test poll intervals**; production remains 10 seconds. Body bytes exclude HTTP headers. The measured gain is 93.3% for the three slow-transfer polls. Fresh and changed data still need the full bounded window; these control cases show no meaningful cold-load improvement. Across the long-log navigation/switch sequence both sides made eight completed log requests and sent 67,138,829 bytes; baseline had four long tasks (max 77 ms), candidate four (max 74 ms). This PR does not eliminate full-payload parsing long tasks when data changes.

With gzip enabled, the three completed unchanged polls transferred 265,224 bytes compressed / 41,965,362 bytes decoded on main, versus zero body bytes and zero JSON reads with 304. Duration was 1312 -> 793 ms; long tasks were three (max 59 ms) -> zero. The accelerated candidate sample recorded four attempted requests: three completed 304 responses and one additional in-flight attempt with no response/body at sampling. Pausing the timer does not abort an active GET; this is not labeled a cancellation or omitted from request totals. Both versions already made zero unchanged-stream DOM mutations; this PR additionally removes the projection/view calls (instrumented view time 1.2 ms -> 0 over the three gzip polls).

Short logs separately verify a **real warm cache hit**: both versions display 200 rows synchronously on node return (14.9 -> 23.3 ms in this single observation). Three completed unchanged polls took 1724 -> 763 ms and sent 3,565,362 -> 0 body bytes. The large long-log snapshot exceeds the existing 16 MiB cache budget and displays zero synchronous rows on repeat selection; that repeat is explicitly not described as a cache hit. Full short-log, gzip, phase, attempted/completed request, long-task and race measurements are in [follow-up-measurements.json](follow-up-measurements.json). Only the final six-sample recheck after the policy fix is included in these comparisons; exact final source hashes accompany the raw observations.

The pressure script also checks three rapid alternating node selections, no cross-node rows, retained page 2/read anchor after new IDs arrive, and 503 failure/recovery without discarding rows. The changed long-log anchor moved only 0.469 px on both versions. Browser regressions additionally cover same-ID content corrections, search focus/caret and IME, 8000 entries, bounded DOM, complete-window search, cold/hot snapshots, backoff and hidden/route lifecycle. The anchor test now waits for actual finite route entrances to finish: faster 304 polls exposed its old fixed 150 ms sleep measuring a still-moving 480 ms parent animation. Its 2 px correctness threshold remains.

## Real backend comparison

PostgreSQL 18.6 in an isolated local container, Go production Store/API/auth/gzip middleware via `httptest`, synthetic three-node data with 8000 entries per node. This is real database/API execution with in-memory HTTP transport, separate from the browser pressure fixture; no production deployment or real agent traffic was used. The exact benchmark file was copied to the `df1ad925` worktree without changing baseline product code. Values are medians of three benchmark runs with 30 operations each.

| 8000 entries | Main | New 200 | New 304 |
| --- | ---: | ---: | ---: |
| 160-byte messages: API time | 29.391 ms | 25.800 ms | 23.262 ms |
| 4096-byte messages: API time | 251.202 ms | 237.130 ms | 217.405 ms |
| 4096-byte messages: allocated bytes/op | 185769832 | 120105689 | 132612361 |
| 4096-byte messages: allocation count/op | 88238 | 64172 | 64147 |
| 4096-byte messages: gzip body bytes | 450929 | 450927 | 0 |

The long-message 200 path reduced time by 5.6% and allocated bytes by 35.3%; 304 reduced time by 13.5%. The two-byte compressed-body difference is fixture timestamp encoding, not omitted rows. Content hashing costs CPU and holds an encoded buffer; the clean-message fast path offsets this in these samples. Crucially, 304 still performs the database read, sanitation and JSON encoding. It saves gzip, transport and browser work, not SQL queries.

A separate real EXPLAIN fixture with four nodes, 160,000 rows and seven populated daily partitions confirmed the existing node and fleet covering indexes: at 8000 rows SQL was 8.547 ms node / 8.227 ms aggregate, all scans index-only with zero heap fetches after VACUUM. Final quicksort used about 1130 KiB in memory, with no spill. Read/scan/clean took 20.77 / 17.59 ms and JSON encoding 28.65 / 20.79 ms. No schema/index migration is justified by this evidence. Original measurements and reproduction output are in [follow-up-backend.txt](follow-up-backend.txt).

## Reproduce and validation

```sh
# Use a disposable PostgreSQL instance. Every suite creates isolated schemas.
export QCH_TEST_DATABASE_URL='<isolated PostgreSQL URL>'
QCH_CHROME_BIN=/usr/bin/chromium make check
QCH_BROWSER_SMOKE_MODES=logs,logs-mobile,logs-restore node frontend/agents_browser_smoke.mjs
go test ./internal/api -run 'TestCoreLog' -count=1
go test ./internal/api -run '^$' -bench 'BenchmarkCoreLogRefresh/message-.*/(modified|unchanged)$' -benchtime=30x -count=3
QCH_CORE_LOG_MEASURE=1 go test ./internal/store -run TestCoreLogWindowMeasurements -count=1 -v
```

For the strict API baseline, copy `internal/api/core_logs_bench_test.go` to a disposable checkout of `df1ad925` and run `-bench 'BenchmarkCoreLogRefresh/message-.*/unconditional$'`. Do not run database measurements alongside migration/integration suites, which can affect vacuum visibility.

For browser pressure tests, run the updated server twice, pointing one at the baseline frontend. Open each loopback URL using Playwright CLI with Chromium sandbox disabled when running as root, then execute `playwright-cli run-code --filename=scripts/core-logs-refresh-performance.js`:

```sh
QCH_PERF_ROOT=/path/to/df1ad925/frontend QCH_PERF_TIMING=1 QCH_PERF_PORT=44384 node scripts/core-logs-performance-server.mjs
QCH_PERF_TIMING=1 QCH_PERF_PORT=44385 node scripts/core-logs-performance-server.mjs
# Repeat with QCH_PERF_GZIP=1 or QCH_PERF_LONG_REPEAT=0 on both servers.
```

Full `make check` passed for both `1b6ecd4f` and the final product commit `0b5025d1` with isolated PostgreSQL and real Chromium, including both complete browser invocations and all Go packages. Subsequent CI fixes preserve the original pending-request and popup behavior assertions and add the preconditions described above. Final review also caught a late-settings invalidation case; its regressions verify one policy repaint during an unchanged poll, retained snapshot/filter/page state, an unchanged next poll and permission notices. Final local and remote CI results after the runner changes are recorded in the PR. Generated stylesheet integrity is checked; no CSS source changed, so regeneration is unnecessary. Necessary new regressions cover conditional transport, actual API authorization and invalidation, and real browser state retention. Existing module boundaries, schema policy, sanitation and per-engine limits remain checked.

## Remaining limits and rollback

- There is no production post-change TTFB/agent latency measurement, physical cold-disk test, real mobile Safari or OS IME test. Local browser cold/warm refers to page/session/window caches, not disk cache. The 8000-entry 4096-byte backend fixture uses compressible diagnostic text.
- Existing covering B-tree indexes include `message`. A sufficiently long **incompressible** value can exceed PostgreSQL's index-tuple size (a 4096-byte random message reproduced a 4176-byte index row against a 2704-byte limit). This exists on main and can reject an upload. It needs a separate index/storage migration design; this PR neither silently truncates messages nor claims arbitrary 4096-byte payload support. The current log size limit and schema remain unchanged. Reproduce without touching application tables:

  ```sql
  CREATE TEMP TABLE qch_log_index_probe (
    id bigint, agent_id text, engine text, level text,
    message text, logged_at timestamptz, received_at timestamptz
  );
  CREATE INDEX ON qch_log_index_probe (agent_id,engine,id DESC)
    INCLUDE (level,message,logged_at,received_at);
  INSERT INTO qch_log_index_probe
  SELECT 1,'agt_0000000000000001','mihomo','info',
    string_agg(md5(random()::text),''),now(),now()
  FROM generate_series(1,128);
  ```

  The corresponding indexes already exist in `df1ad925:internal/store/schema.go`; the existing upload maximum is 4096 bytes in `internal/core/core_logs.go`.
- The agent's tick/ACK/queue limits above remain potential update-latency/loss sources under sustained output. Pending batches survive reconnect with the same batch ID; the database commits its deduplication marker and entries atomically before the ACK. Conditional GET does not change this recovery path.
- Full snapshots still transfer when any displayed content changes; quiet previews initially tagged at limit=200 need one full-scope read before later polls can validate the larger URL. Oversized windows are active/searchable but are not retained in the LRU.
- Roll back these runtime/frontend changes together or separately; old clients ignore ETag and new clients accept older ordinary arrays. No database migration, persisted cache or data rewrite needs rollback.
