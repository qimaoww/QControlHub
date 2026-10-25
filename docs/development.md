# 开发指南

## 环境

从仓库根目录运行命令。Go 版本以 [go.mod](../go.mod) 为准，Node.js 使用与现有 CI 相同的 24 版本。数据库集成测试需要独立 PostgreSQL 测试库及创建隔离数据库或 schema 的权限，通过 `QCH_TEST_DATABASE_URL` 指定；未设置时部分测试会跳过。

本地 Compose 开发环境：

```sh
make init-env
make compose-config
make dev-up
# 完成后停止服务，保留数据库卷：
make down
```

`dev-up` 从源码构建并使用本地 HTTP 设置。生产部署见[部署与维护](deployment.md)。

## 构建与验证

| 命令 | 内容与依赖 |
| --- | --- |
| `make build` | 构建 `bin/qcontrol-plane`、`bin/qagent` |
| `make test` | 顺序运行 Go 测试，数据库测试使用 `QCH_TEST_DATABASE_URL`；独立浏览器测试由 browser target 执行 |
| `make non-browser-checks` | 格式、前端模块、策略、生成文件、安装部署脚本及 `go vet`；其中节点重装检查需要 Linux root |
| `make frontend-module-check` | Node.js 前端模块 smoke |
| `make browser-checks` | Chromium 实际浏览器、配置首次安装、多用户与日志页面测试；需测试数据库 |
| `make check` | 上述非浏览器检查、浏览器检查及 Go 测试 |
| `make upgrade-sandbox-test` | Docker 只读沙箱中的 Agent 生命周期检查 |
| `make panel-auth-upgrade-test` | 历史版本到当前版本的账号升级测试，需 Git 历史、Go 和测试数据库 |

浏览器执行器支持 `QCH_CHROME_BIN` 指定 Chromium。完整目标与环境要求见 [Makefile](../Makefile)；测试应按修改影响选择。

部署菜单预览使用示例数据，不执行安装：

```sh
bash scripts/preview-quick-start.sh all
COLUMNS=40 NO_COLOR=1 bash scripts/preview-quick-start.sh update
```

可选参数为 `menu`、`mode`、`update`、`unchanged`、`result`、`all`。

## 源码与生成文件

- 后端入口位于 [cmd/](../cmd)，业务模块位于 [internal/](../internal)。
- 前端页面与共享模块位于 [frontend/](../frontend)，模块 smoke 与真实浏览器夹具分别在 `frontend/smoke/`、`frontend/browser/`。
- 部署脚本修改 [deploy/modules/](../deploy/modules) 中的源模块，再运行 `make generate-deploy-scripts`；用 `make deploy-modules-check` 检查生成结果。
- 样式源位于 [frontend/styles/](../frontend/styles)，修改后运行 `make generate-styles` 生成 `frontend/app.css`，用 `make styles-check` 核对。
- [install_assets.go](../install_assets.go) 显式嵌入部署脚本、服务模板与 [examples/configs/](../examples/configs)；二进制升级使用这些资源。各模块 `testdata/` 中的报告、数据库和协议文件供自动化测试读取。

## 前端交互约定

复用现有主题、控件、布局和反馈组件。业务提交、关闭和权限状态立即生效；轮询保持已挂载的 DOM、焦点、输入、光标和滚动位置。

动态效果由 `frontend/modules/` 中的 `motion.js`、`presence-motion.js`、`workspace-motion.js`、`list-motion.js` 及 `popup.js` 协调；`refresh.js` 负责保留刷新上下文，`card-move.js` 处理卡片排序。卡片布局只在成员或顺序改变时测量；日志、时钟和相同轮询结果保持安静。减少动态效果时保留标签、状态、焦点和操作语义。

调整交互后按需检查键盘、触屏、明暗主题及 `prefers-reduced-motion`。临时浏览器预览可运行 `QCH_BROWSER_SMOKE_SERVE_ONLY=1 node frontend/agents_browser_smoke.mjs`，使用输出的回环地址，完成后终止进程。

## 提交与 PR

PR 标题遵循 Conventional Commits；现有仓库策略要求标题和描述为 ASCII 英文，并包含 `Summary`、`Validation`、`Risk and rollback`。依据[现有 PR 模板](../.github/pull_request_template.md)填写实际验证与影响。

验收截图、录屏、排查日志和临时报告放在 PR 或外部附件中，仓库文档仅维护当前使用与开发说明。临时服务结束后停止进程并清理资源。
