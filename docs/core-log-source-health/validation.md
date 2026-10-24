# Core log source health validation

Measured on 2026-10-24 against main `7459d9a8` and this branch. Only local test nodes
(`e2e-before`, `e2e-after`) and synthetic log lines appear in the committed material.
No production address, node identity, session, token or real log content is saved.

## Environment

- Debian 13, Go 1.26.10, Chromium 152.0.7977.82, PostgreSQL 17.11 in Docker on 127.0.0.1:55432.
- The control plane and the Agent are built from this branch. The Agent manages a real
  systemd unit (`qagent-sing-box.service`), the real host journal and a real sing-box
  1.12.0 process, so the chain under test is service output -> journal -> Agent -> WSS ->
  control plane -> panel.

## Scenario one: installed kernel without a managed configuration

Same condition on both sides: sing-box binary installed, `/etc/qagent/sing-box/config.json`
absent, no retained log rows, sing-box selected in the filter.

| | main `7459d9a8` | this branch |
| --- | --- | --- |
| Agent heartbeat | `core_log_status=failed`, `core_log_error=config-unavailable` | `core_log_status=not-configured`, empty error, `core_log_source=file` |
| `GET /core-log-sources` | `failed` + `config-unavailable` | `not-configured` + `source=file`, no error |
| Panel | "日志采集失败" / "来源读取失败（config-unavailable），请检查 Agent 诊断日志。" | "未配置" / "e2e-after · sing-box · 主实例 尚无托管配置，部署后自动采集。" |

Screenshots: `before/desktop-light.png`, `before/desktop-dark.png`,
`after/desktop-light.png` and `after/desktop-dark.png` (1440x1000); `before/mobile-light.png`,
`before/mobile-dark.png`, `after/mobile-light.png` and `after/mobile-dark.png` (390x844). Both sides use the same viewport, theme, filter and a single visible node;
the screenshot hashes differ only because the baseline stack was captured separately.

## Scenario two: "healthy" state with no logs

main treated a started `journalctl` process as `active`, so an engine with no service at
all could report healthy and a broken source could hide behind a sibling:

| Check | main | this branch |
| --- | --- | --- |
| ss-rust not installed | `active` with no service and no rows | `waiting` + `service-inactive`; the directory reports `not-installed` from `installed=false` |
| journal read error while the unit is stopped | `failed` + `collector-failed` | `waiting` + `service-inactive` |
| line delivered on the host journal | transport not distinguishable | `active` + `core_log_source=journal-host` + `core_log_last_at` |
| namespaced journal failure | overwrote the same engine's host state | isolated per transport; a healthy source is not displaced |

After writing the managed configuration and starting the service, an Agent restart
reported:

```text
sing-box  active  -  journal-host  2026-10-24T08:56:24.859933376Z
ss-rust   waiting service-inactive journal   -
```

`GET /core-logs?agent_id=...&engine=sing-box` returned the three real sing-box connection
lines. Removing the managed configuration and restarting the Agent returned
`not-configured` with an empty error while the three retained rows stayed readable.

Recording: `record/core-log-recovery.mp4` (not configured -> configuration written,
service started and Agent restarted -> healthy with rows). `record/recovered-state.png`
is the final frame.

## Checks

```text
$ go build ./...
BUILD_OK

$ go test ./internal/agent -run 'CoreLog|Journal|Import|Instance|SSRust' -count=1
ok  github.com/qimaoww/qcontrolhub/internal/agent  55.085s

$ go test ./internal/store -count=1
ok  github.com/qimaoww/qcontrolhub/internal/store  229.859s

$ go test ./internal/api -count=1
ok  github.com/qimaoww/qcontrolhub/internal/api  310.479s

$ QCH_CORE_LOG_BROWSER=1 QCH_TEST_DATABASE_URL=... go test ./internal/api -run TestCoreLogsBrowserSessions -count=1 -v
--- PASS: TestCoreLogsBrowserSessions/desktop (13.32s)
--- PASS: TestCoreLogsBrowserSessions/mobile (13.22s)

$ node frontend/core_logs_smoke.mjs
Core log staged loading/cache smoke passed

$ go test ./frontend -run 'TestCoreLog' -count=1
ok  github.com/qimaoww/qcontrolhub/frontend
```

## Not verified

- The three production nodes (Breadcloud LAX, Catixs HK, Breadcloud ATT) were investigated
  read-only only. Their journal contents and systemd units are not readable through the
  read-only API, so no on-node re-check was performed.
- A silent source (service running, unit emitting, reader receiving nothing) has no
  independent probe yet. This branch proves delivery with `last_log_at`, isolates states
  per transport and promotes the host journal once it delivers, instead of treating
  "process started" as healthy.
- The shared-instance live lifecycle (`QCH_LOG_LIVE=1`, isolated node image) and a real
  OpenRC host were not run; OpenRC file sources are covered by unit tests.