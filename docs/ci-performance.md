# CI performance and reliability

## Measurement

The controlled comparison uses `main` at `ffdc09adbffb6b47bfcebd275b10b6125817a4c0`.
Before delivery the branch was rebased onto latest main
`8ced1108d784b08c015a404d6021e11630285fb3` (#270), retaining its new IPQuality
implementation/tests. That newer final-head CI is reported separately in the PR;
its duration is not treated as a same-code comparison. This PR changes no
application code, existing assertions, platform integrations or pinned core releases. PR #267 tracks the measured cold/warm comparison:
https://github.com/qimaoww/QControlHub/pull/267

Use attempt-specific job data and rerun **all** jobs for repetitions. The report
rejects incomplete runs and retries that retain jobs from an earlier attempt.
Download timing artifacts before rerunning; GitHub removes the previous attempt's
artifacts on a full rerun:


```sh
python3 .github/scripts/ci-report.py RUN_ID --attempt 1 --output report.json
gh run download RUN_ID --pattern 'ci-timings-*' --dir timing-artifacts
```

The report measures time from workflow scheduling to completion of every
necessary job, queue time from job creation to runner start, individual step
time and aggregate actual runner minutes for the CI workflow. It excludes image
publication, the separate short PR title/body workflow and GitHub's rounded billing units. Upload/download and cache save times are included
in job duration. Submission webhooks before Actions creates the run are not
observable from these APIs.

## Successful run comparison

All runs below passed every necessary CI job. The controlled branches use the
same application code/test assertions as main, on Ubuntu 24.04 runners (4 CPUs,
about 16GiB), Debian Go 1.25.0/glibc, Alpine Go 1.25.14/musl and PostgreSQL 17.
Those toolchain figures are the historical 1.25-era baseline; CI now builds with
Go 1.26.10 in every job.
All candidate data includes real database/runtime execution, cache upload and
strict required gates. These measurements precede the #270 main update. The scope difference is the user-requested optional
browser matrix, described below; this is not a claim of identical mandatory coverage.

| Configuration / cache state | Run / attempt | Necessary checks | Actual job minutes |
| --- | --- | ---: | ---: |
| Original, controlled cold | [38556917818/1](https://github.com/qimaoww/QControlHub/actions/runs/38556917818) | 32m19s | 66.15 |
| Original latest main, mixed | [38553871096/1](https://github.com/qimaoww/QControlHub/actions/runs/38553871096) | 33m01s | 69.22 |
| Original successful PR, mixed | [38549771383/1](https://github.com/qimaoww/QControlHub/actions/runs/38549771383) | 33m20s | 69.50 |
| Candidate, controlled cold A | [38562198885/1](https://github.com/qimaoww/QControlHub/actions/runs/38562198885) | 8m39s | 23.08 |
| Candidate, controlled cold B | [38563741568/1](https://github.com/qimaoww/QControlHub/actions/runs/38563741568) | 8m01s | 22.10 |
| Candidate PR, cold platform / warm other caches | [38562167419/1](https://github.com/qimaoww/QControlHub/actions/runs/38562167419/attempts/1) | 7m59s | 19.80 |
| Same candidate PR, full warm rerun | [38562167419/2](https://github.com/qimaoww/QControlHub/actions/runs/38562167419/attempts/2) | 6m23s | 17.05 |

Cold completion fell **73.2-75.2%**, with actual runner minutes down **65.1-66.6%**
against the controlled original cold run. The warm candidate completes **80.7%**
faster and uses **75.4%** fewer minutes than the successful mixed-cache main run.
The original architecture cannot produce a fully warm Alpine/sandbox run because
those object caches were ephemeral; historical warm Debian runs are labelled mixed.
The cold/warm difference also includes ordinary runner and PostgreSQL timing
variation; these samples do not establish a statistical reduction in flake rate.
Final-head validation and its second warm sample are linked in PR #267.

Raw attempt-specific reports are in [ci-performance/](ci-performance/).
Successful controlled cold runs used fresh Go/image scopes and reported cache
misses; the warm rerun restored all three exact Go keys and existing Bake layers.
Database tests, selected runtime tests and the full module suite still ran.
Failed/cancelled development runs are excluded from this table.

### Measured contributions and limits

| Change | Evidence | Interpretation |
| --- | --- | --- |
| One package metadata load | Same local checkout, 3 samples: median 0.669s -> 0.143s; 21 `go list` calls -> 1. CI metadata about 0.19s. | Preparation improves 79% locally; small absolute benefit. |
| Measured Go allocation | Cold shards Debian 397/279/53/75s; Alpine 406/70/50/68s. Warm 264/207/5/11s and 327/3/3/11s. | Database/agent work is isolated from small packages; database packages remain serial. Residual imbalance reflects the isolation constraint. |
| Browser execution | Original frontend browser package 850/935s, followed by another complete serial browser suite. Manual candidate matrix: 404/477s, all 81 modes on each platform passed. Local same assertions: 742.103s serial -> 377.142s balanced. | Duplicate browser coverage was removed; two processes roughly halve one full matrix. Moving browsers out of mandatory CI is an additional explicit scope change. |
| Persisted Go objects | Cold -> warm: release linking 20/21s -> 1/1s; Alpine small shards 50-70s -> 3-11s. Restore 5/6/3s on Debian/Alpine/sandbox. | Compiler work is reused; test execution is forced with count=1. |
| Sandbox/native reuse | Original sandbox job 95s; candidate cold 76s, warm 56s. Native Rust step 20s historically -> 4s cold / 3s warm. | Both compiler calls share the fingerprinted container toolchain cache; read-only/runtime isolation remains. |
| Shared image build, earlier start | Original cold image jobs total 219s, longest 115s after platform validation. Candidate shared preparation 84s cold / 29s warm, plus 16/15s of compatibility gates. | One solve reuses binary objects, starts immediately and removes image construction from the final critical chain. |

Cold cache-save steps were 24s Debian, 8s Alpine and 4s sandbox; warm post-cache
steps were 3/3/1s and did not upload an exact-key hit. Compressed Go entries were
about 112/244/188MiB. These costs are included above. Cold Bake build/export was
60s; warm import/solve/export was 11s. Cold image export spans overlapping
vertices, so those durations are not added as if sequential. Runner job queue
was typically 1-3s; dependency scheduling/provisioning gaps remain included in
completion time and can be examined through job start offsets.

Module downloading was 1-3s cold and 0-2s warm. Alpine dependencies were 3s cold
and 2s warm, with release compilation/linking measured separately. Debian's
system dependency step was 5s cold and 4s warm. Chromium formerly provided
systemd helpers and full ICU data transitively; mandatory CI now explicitly
installs systemd on Debian and icu-data-full on Alpine. Go shard elapsed minus
package-test elapsed includes compilation/loading/tool overhead, not a precise
compiler-only measurement. CPU utilization, peak process RSS and actual GitHub
billing were not measured. Publication with SBOM/provenance is configured and
gated, but has not run for this PR event.

## Original bottlenecks

Baseline PR run 38549771383 (first attempt, all jobs successful) required **2000s
(33m20s)** and **69.50 job minutes**. Its workflow completion timestamp is one
second later. See [raw job/step summary](ci-performance/baseline-pr.json).

| Baseline component | Debian / systemd | Alpine / OpenRC |
| --- | ---: | ---: |
| Job queue | 2s | 2s |
| Container/service initialization | 25s | 12s |
| Go setup including cache restoration | 17s | No persisted Go cache |
| Node setup | 1s | Included in dependency step |
| System test dependencies | 20s | Included in combined step |
| All checks | 1684s | 1882s including environment preparation/build |
| Release binaries | 21s | Included in combined step |
| Whole job | 1780s | 1901s |
| PostgreSQL API + store execution | 374.120s | 396.763s |
| Browser-bearing frontend Go package | 850.107s | 935.467s |

The old critical path was environment preparation -> the browser-bearing Go
shard -> a second complete browser suite -> platform bootstrap -> image builds
(94s / 51s / 20s on separate runners). Go shards 2 and 3 on Debian executed only
217s and about 0.9s of package tests, respectively. Function counts incorrectly
placed an 850s browser suite beside several small packages.

Old logs do not separately identify pure compilation, module downloading inside
a test command, individual scenes or BuildKit cache export. Those intervals must
not be presented as independently measured historical numbers. Baseline Debian
restored a 249MiB Go cache; Alpine and sandbox compilation were cold on every
run. This is a mixed-cache baseline, not a fully warm workflow.

After the user narrowed the scope on 2026-10-14, real browsers are optional: local
`make check` / `make frontend-check` retain all 81 modes; `.github/workflows/browser.yml`
provides manual Debian and Alpine validation. Routine PR checks and main publication
do not run or depend on that workflow. Frontend module smoke, contracts, policy,
style and diagnostic regressions remain mandatory. This is an explicit scope
change; its time saving is distinguished from scheduling/cache improvements.

After PR #267 was merged, the user also removed the two-version Mieru UDP
systemd workflow from automatic CI and publication dependencies. The timing
table above records the earlier scope, including those two jobs; it has not
been rewritten to claim additional measured savings. The opt-in
`TestMieruUDPWithSystemdSocketPolicy` test is retained for local Linux/systemd
execution with a verified official Mihomo binary, root privileges,
`QCH_LIVE_MIERU_SYSTEMD_TEST=1` and `QCH_LIVE_CORE_ROOT` pointing at its `bin`
directory's parent. Ordinary Mieru unit tests still run in the Go suite.

Separate dispatch-only measurement branches (`codex/ci-baseline-measure` and
`codex/ci-optimized-measure`) keep
the respective workflows and identical application/test code, add `workflow_dispatch`, and
disable setup-go restoration / select fresh image cache scopes for controlled
cold comparisons. The optimized experiment also selects fresh platform-cache
identities, retaining cache-save costs. They never publish because the event is
not a main push. Their runs and final PR cold/warm attempts are recorded in the comparison. Measurement branches/caches are removed after
exporting evidence. No extra benchmark workflow runs on routine PRs.

The optimized platform artifacts contain:

- `phases.tsv`: environment dependencies, modules and release compilation
  (the optional browser workflow records its own browser phase) elapsed time/status.
- `shard-plan.json`: one metadata call, selected/excluded packages and complete
  allocation. Runner regression fixtures write to their own temporary report
  directories so they cannot overwrite real evidence. Earlier samples' planner
  artifact was overwritten by fixtures; use their first 20/19-package log entry.
- `go-tests-*.ndjson`: complete per-test Go events, including failure output.
- `go-tests-*.json`: elapsed shard time, package execution time and outcome.
  Shard elapsed minus serial package execution includes compilation, loading
  and tooling overhead; it is not labelled pure compilation.
- `browser-*.json`: each scene's Chromium startup, interaction/assertion,
  cleanup, elapsed time and verdict. The same assertions and per-scene deadlines remain.

## Task structure and coverage

```mermaid
flowchart LR
  commit[Commit] --> deb[Debian: checks + four Go shards]
  commit --> alp[Alpine: checks + four Go shards + OpenRC agent selection]
  commit --> sand[Read-only upgrade + native Shadowsocks Rust]
  commit --> perms[All core service permission and forwarding checks]
  commit --> bake[Build Docker images: three targets with shared objects]
  deb --> gates[Existing Build image required checks]
  alp --> gates
  sand --> gates
  perms --> gates
  bake --> gates
  gates --> publish[Main push: publish with SBOM and provenance]
```

`make check` and routine CI share each ordinary Go package, every existing Node
and shell check, a full `go vet ./...`, and the complete serial module smoke suite.
The architecture package is included in the ordinary sweep. The only intentional
scope difference is real browsers: local `make check` includes the Go browser
wrapper once; routine CI skips that exact wrapper and does not install Chromium.
`make browser-checks` and the manual workflow execute the complete browser matrix.
`make frontend-check` includes both module smoke and browsers. `go test ./...`
still includes real browsers for callers who use Go directly.

The planner performs one `go list` with package directories/imports/test
imports. Runtime weights in `.github/ci-package-times.json` come from the
baseline package execution data; frontend now excludes the browser wrapper.
Unknown packages receive a conservative function-count estimate. Every selected
package is checked for exactly-once assignment. Database users, including
transitive helper imports and `internal/testdb`, share one `-p 1` shard.
The four-way partition remains; a serial database chain and a large agent
package cannot be evenly split without changing their isolation model.

Separate databases isolate data but do **not** isolate PostgreSQL's cluster-wide
xmin horizon. No database package or HOT-page test was made concurrent. An
unknown `--extra` task waits for the database shard. `--extra-independent` is
used only for Alpine's agent tests, which do not connect to PostgreSQL. For
future parallel database integration, use separate PostgreSQL server clusters.

Module smoke's awaited domain suites share temporary DOM globals and a fixture
object populated by earlier suites. Their order remains unchanged. Browser
workers instead run in separate Node processes, with distinct HTTP fixtures,
ports, Chromium profiles, storage and CDP connections. Every one of the original
81 modes is assigned once, retaining desktop, touch/mobile, permissions, motion,
performance and layout assertions. Chromium is still launched fresh per scene.
Local timing showed startup around 120-300ms: process reuse across scenes would
save little and complicate isolation.

Workers are capped at two and reduced to one when CPU/memory are constrained.
Reports print available CPUs and memory. Each worker cleans the entire Chromium
process group and its profile. Go's wrapper uses an 18m deadline inside the 20m
package timeout, allows graceful interruption, and limits pipe drain to 10s.
Scheduler cancellation escalates after 6s. Shell tasks run in process groups,
retain failures through pipelines/after-checks, and clean descendants on signals.
Named Docker containers are stopped and removed even if cancellation interrupts
image pulling or container creation.

## Caches and environment

Cache format is `qch-v4`. Module and object caches have independent identities
for Debian/glibc, Alpine/musl and the upgrade sandbox. Alpine and sandbox keys
fingerprint the actual image's Go version, architecture, OS and libc. The Go
images are pinned to the toolchain patch that `go.mod` requires
(`golang:1.26.10-*`) because the `go` line is a minimum: an image older than the
pinned patch cannot build, and the sandbox and native-runtime paths cannot fetch
a toolchain (`GOTOOLCHAIN=local`, `--network none`). Identities still change when
a pinned tag's base layers move. `nginx:1.31-alpine` keeps floating and re-keys
the web image when upstream updates it. Debian setup-go still selects Go 1.26.10
from go.mod.
Dependency hashes and Go source hashes (excluding .ci-cache itself) create new
cache entries; restore prefixes
can supply older objects/modules, which Go revalidates and recompiles as needed.
Changing Go/libc/OS identities cannot restore another platform's objects.

Alpine mounts restored module/object directories into its temporary container.
The sandbox mounts a separate persistent object directory while keeping its
rootfs and source/module mounts read-only and network disabled. Only compiler
objects gain a writable mount. Because the sandbox drops DAC_OVERRIDE, cache
permissions are prepared explicitly. Its existing CHOWN capability restores
cache ownership to the caller before the unprivileged native compiler runs.

All ordinary Go, selected agent, sandbox, native runtime and browser invocations
use `-count=1` or execute an explicit test binary. Database/runtime observations
are never accepted from Go's test-result cache. Missing caches still download
modules and compile normally. Pinned upstream binary versions and SHA256 checks
remain intact. These downloads took about 0-4s in the baseline: adding another
archive cache/restore/save layer was not justified by that cost. Debian apt and
Alpine apk still execute and get actual runtime dependencies.

Bake solves the three targets together, sharing module and compiler layers.
Production Go compilation copies only relevant source and embedded assets;
Go tests and browser fixtures are excluded from the image context. An object
layer precedes VERSION-dependent links, so a new commit's version can relink
without recompiling dependencies. Compiler objects live in exported layers;
unexported BuildKit cache mounts would not persist through GHA cache export.
Each target retains its own BuildKit scope. Publication imports the same scopes
and uses the same SHA, image targets/content, `sbom: true` and `provenance: mode=max`.
Publication itself is gated and has not been exercised by the PR event.

## Gate reliability and rollback

Ruleset 20995958 requires Title and description, Debian / systemd, Alpine /
OpenRC, and the three existing Build image check names. These names are retained.
The image checks now explicitly require all five producer jobs, using `always()`
and exact `success` comparisons. A failed, cancelled, skipped or missing producer
fails the gate. This also makes the previously non-required sandbox and
permission checks necessary for merge. Main publication has explicit dependencies
on all producers and gates for the same workflow commit.

Regression tests exercise failed shards/checks, serial after-check ordering,
new packages/helper imports, one metadata call, cancellation and all non-success
gate states. Real local cancellation left no worker/browser/profile or Docker
container behind. Actual replaced PR runs were cancelled; their required image
checks failed rather than reporting success. The local dependency update experiment ran all 24 authn test events on cold,
warm and changed dependencies (7.751/1.897/1.938s). A real new helper-importing
package was assigned once to the database shard and executed its new test.
See [component/local evidence](ci-performance/local-and-component-experiments.json).
Cold and warm mandatory CI attempts, optional browser experiments, dependency
changes and resource limits are detailed in the PR.

The optional workflow passed on both platforms in run 38560344524 (all 81 modes
per platform, 8m31s wall / 16.62 job minutes). A new workflow file is registered
only after reaching main; the pre-merge test dispatched its exact job definitions
through the existing ci.yml filename on a temporary branch. After merge:

```sh
gh workflow run browser.yml --ref main -f workers=2
# Local full coverage, including module smoke and real browsers:
make check
```

Rollback controls:

- Set `QCH_BROWSER_WORKERS=1` locally or choose worker count `1` in the manual workflow for serial
  browser execution; no coverage or assertion needs to change.
- Bump `qch-v4` identities / Bake scopes or disable cache restore/save to force
  a rebuild. Never remove `-count=1` to improve measured runtime.
- Remove `--extra-independent` if an extra task starts using PostgreSQL.
- To require browsers again, restore the platform Chromium dependencies and add
  `--after-checks "make browser-checks"` to both platform schedulers. Module smoke
  already remains mandatory.
- Revert this PR for the original scheduler, dependency graph and Dockerfile.
  No application schema/data migration is involved.
