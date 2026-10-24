# 内核日志来源健康验证

2026-10-24，基线 `7459d9a8`（main），分支 `codex/core-log-collection-recovery`。

## 环境

- Debian 13，Go 1.26.10，Chromium 152.0.7977.82，PostgreSQL 17.11（Docker，本机 127.0.0.1:55432）。
- 控制面与 Agent 均由本分支源码编译；Agent 管理真实 systemd 单元、真实 host journal 与真实 sing-box 1.12.0 进程。
- 只使用本地测试节点（`e2e-before` / `e2e-after`）与合成日志，不包含生产地址、节点身份、会话或真实日志内容。

## 复现场景一：已安装内核没有托管配置

同一条件（sing-box 二进制已安装、`/etc/qagent/sing-box/config.json` 不存在、无历史日志、筛选中 sing-box）：

| | 基线 `7459d9a8` | 本分支 |
| --- | --- | --- |
| Agent 心跳 | `core_log_status=failed`，`core_log_error=config-unavailable` | `core_log_status=not-configured`，`core_log_error` 为空，`core_log_source=file` |
| `GET /core-log-sources` | `failed` + `config-unavailable` | `not-configured` + `source=file`，无 error |
| 页面 | “日志采集失败” / “来源读取失败（config-unavailable），请检查 Agent 诊断日志。” | “未配置” / “e2e-after · sing-box · 主实例 尚无托管配置，部署后自动采集。” |

截图：`before/desktop-light.png`、`after/desktop-light.png`（桌面，浅色），`after/desktop-dark.png`（深色），`before/mobile-light.png`、`after/mobile-light.png`、`after/mobile-dark.png`（390×844）。

## 复现场景二：状态“正常”但没有日志

基线把“journalctl 进程启动成功”当作 `active`，因此未安装内核也会显示 active，真正的来源故障也会被当成正常：

| 检查 | 基线 | 本分支 |
| --- | --- | --- |
| 未安装的 ss-rust | `active`（没有任何服务与日志） | `waiting` + `service-inactive`；目录按 `installed=false` 报 `not-installed` |
| 服务未运行时的 journal 读取错误 | `failed` + `collector-failed` | `waiting` + `service-inactive` |
| host journal 投递 | 状态与日志来源无法区分 | 投递后 `active` + `core_log_source=journal-host` + `core_log_last_at` |
| 命名空间来源失败 | 覆盖同引擎的 host 来源状态 | 按传输隔离，健康来源不被覆盖 |

配置写入并启动 `qagent-sing-box.service` 后，Agent 重启复核：

```text
sing-box  active  -  journal-host  2026-10-24T08:56:24.859933376Z
ss-rust   waiting service-inactive journal   -
```

`GET /core-logs?agent_id=…&engine=sing-box` 返回 3 条真实 sing-box 访问日志；删除托管配置并重启 Agent 后回到 `not-configured`，错误字段为空，历史 3 条日志保留。

录屏：`record/core-log-recovery.mp4`（未配置 → 写入配置、启动服务、Agent 重启 → 采集正常并出现日志），`record/recovered-state.png` 为录屏终态。

## 自动化测试

```text
$ go build ./...
BUILD_OK

$ go test ./internal/agent -run 'CoreLog|Journal|Import|Instance|SSRust' -count=1
ok  github.com/qimaoww/qcontrolhub/internal/agent  55.085s

$ go test ./internal/store -count=1
ok  github.com/qimaoww/qcontrolhub/internal/store  229.859s

$ go test ./internal/api -count=1
ok  github.com/qimaoww/qcontrolhub/internal/api  310.479s

$ QCH_CORE_LOG_BROWSER=1 QCH_TEST_DATABASE_URL=… go test ./internal/api -run TestCoreLogsBrowserSessions -count=1 -v
--- PASS: TestCoreLogsBrowserSessions/desktop (13.32s)
--- PASS: TestCoreLogsBrowserSessions/mobile (13.22s)

$ node frontend/core_logs_smoke.mjs
Core log staged loading/cache smoke passed

$ go test ./frontend -run 'TestCoreLog' -count=1
ok  github.com/qimaoww/qcontrolhub/frontend
```

## 未验证项

- 三个线上节点（Breadcloud LAX、Catixs HK、Breadcloud ATT）只做了只读排查；节点上的 journal 内容与 systemd 单元无法通过只读 API 取得，本轮未在真机复验。
- “服务在运行、单元确实在产生日志、但采集器一行都收不到”的静默来源没有独立探测；本分支改为用 `last_log_at` 证明投递、按传输隔离状态并在 host journal 投递后提升来源，避免把“进程启动”当作“采集正常”。
- 共享实例 live 生命周期（`QCH_LOG_LIVE=1`，需要隔离节点镜像）与 OpenRC 真机路径未运行；OpenRC 文件来源由单元测试覆盖。
