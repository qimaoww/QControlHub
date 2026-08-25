# 开发指南

## 环境要求

- Go 1.25 或更高版本
- Node.js 22 或更高版本，以及 Google Chrome/Chromium（前端 smoke 测试）
- PostgreSQL 14 或更高版本（Compose 默认 PostgreSQL 17）
- Docker Engine 与 Docker Compose v2（推荐用于本地数据库）
- OpenSSL（仅用于 `make init-env` 生成随机密钥）

## Pull Request 约定

PR 标题和正文统一使用英文 ASCII 字符。标题使用 Conventional Commit 格式，
例如 `feat(agent): add capability` 或 `fix: handle startup failure`。正文应按仓库
模板填写 `Summary`、`Validation` 和 `Risk and rollback`；CI 会在 PR 创建、
更新标题或正文、推送新提交时校验这些内容。

## 全容器开发环境

首次初始化：

```bash
make init-env
make dev-up
docker compose ps
```

`make dev-up` 通过当前 shell 临时传入 `QCH_BEHIND_TLS_PROXY=false` 和 `QCH_ALLOW_INSECURE_HTTP=true`，使回环地址上的 HTTP 登录可以工作。它不会改写 `.env`。访问 `http://127.0.0.1:8080`，管理员令牌位于权限为 `0600` 的 `.env`。

数据库与控制面只发布到 `QCH_BIND_ADDRESS`，默认值是 `127.0.0.1`。不要在开发机上把它改为 `0.0.0.0`。

查看日志和停止环境：

```bash
make logs
make down
```

`make down` 会保留 PostgreSQL 命名卷。只有明确不再需要数据时才手动执行 `docker compose down -v`。

## 在宿主机运行 Go 控制面

先只启动 PostgreSQL：

```bash
docker compose up -d postgres
```

加载开发密钥并构造宿主机数据库 URL：

```bash
set -a
. ./.env
set +a
export QCH_DATABASE_URL="postgresql://${POSTGRES_USER}:${POSTGRES_PASSWORD}@127.0.0.1:${POSTGRES_PORT}/${POSTGRES_DB}?sslmode=disable"
export QCH_LISTEN=127.0.0.1:8080
export QCH_BEHIND_TLS_PROXY=false
export QCH_ALLOW_INSECURE_HTTP=false
export QCH_ALLOW_INSECURE_DATABASE=false
go run ./cmd/control-plane
```

控制面启动时会在 PostgreSQL 上获取 advisory lock 并自动应用幂等 schema。当前没有独立迁移命令。

## 运行本地 Agent

保持控制面运行，登录 Web 控制台，在“远程节点”页创建一个绑定 `dev-agent` 的添加节点命令。在第二个终端加载 `.env`，将其中的添加凭证临时传给 Agent，并使用单独的本地状态文件：

```bash
set -a
. ./.env
set +a
QCH_SERVER_URL=ws://127.0.0.1:8080 \
QCH_ALLOW_HTTP=true \
QCH_ENROLLMENT_TOKEN='刚生成或从受保护命令弹窗重新读取的添加节点凭证' \
QCH_AGENT_STATE=./data/dev-agent-state.json \
QCH_AGENT_NAME=dev-agent \
go run ./cmd/agent
```

`QCH_ALLOW_HTTP=true` 只应用于本机回环测试。Agent 会调用固定内核二进制、写入受保护配置并执行 systemctl；Agent 通过 `ws://127.0.0.1:8080/agent/v1/connect` 保持长连接。删除 `data/dev-agent-state.json` 会创建一套新 Ed25519 身份并再次消耗注册流程。

## 质量检查

```bash
make check
```

该命令检查 gofmt、执行 `go vet ./...` 和 `go test ./...`。

默认测试不依赖外部服务。要同时执行 PostgreSQL 集成测试，设置专用测试库连接：

```bash
QCH_TEST_DATABASE_URL="postgresql://qcontrolhub:password@127.0.0.1:5432/qcontrolhub_test?sslmode=disable" \
go test ./... -count=1
```

API 与存储测试会分别创建随机临时 schema，并在测试结束后删除，因此可并行运行且不会共享测试计数。仍应使用专用测试数据库账户，不要指向生产数据库。

构建发布二进制：

```bash
make build VERSION=0.1.0
```

输出位于 `bin/`，版本通过链接参数写入两个 `main.version` 变量。

Compose 文件可以单独渲染校验：

```bash
make compose-config
```

## API 调试

管理 API 必须使用 Bearer 令牌：

```bash
curl --fail-with-body \
  -H "Authorization: Bearer ${QCH_ADMIN_TOKEN}" \
  http://127.0.0.1:8080/api/v1/overview
```

不要在共享 shell 历史、CI 日志或问题报告中粘贴真实令牌。完整接口见 [HTTP API](api.md)。
