# QControlHub

QControlHub 是一个纯 Go 的 Linux 节点配置与远程运维控制平台。控制面同时提供 JSON API 和服务端渲染的 Web 控制台，将配置与任务保存在 PostgreSQL；远程 Agent 负责校验、原子部署并通过 systemd 管理节点服务。

> QControlHub 能下发敏感配置并控制系统服务，属于高权限基础设施。生产环境必须使用 HTTPS、随机长令牌、受限网络和可靠备份；不要把控制面端口直接暴露到公网。

## 主要能力

- 一个 Go 控制面同时承载后端 API 与 Go SSR 前端，无 Node.js 运行时。
- PostgreSQL 持久化节点、配置版本、执行任务、签名 nonce 与审计结果。
- Agent 通过既有 WSS 心跳上报 CPU、内存、根磁盘、默认路由接口地址、实时上下行速率及累计流量；节点页每 5 秒从带会话鉴权的同源接口刷新最新快照。
- 支持 `mihomo`、`xray`、`sing-box`、`ss-rust` 四类配置，单份配置上限 2 MiB。
- 远程节点以服务端入站方案为主：可随机生成并自定义端口、PSK、密码、UUID、WebSocket 路径、Reality X25519 密钥与 Short ID，再生成完整原生配置；部署后由节点运行区直接使用 Agent 上报的默认路由接口地址，生成带安全遮罩的分享 URI 和逐项接入参数，配置编辑器不再混入客户端导出表单。
- 支持 `validate`、`deploy`、`read-config`、`start`、`stop`、`restart`、`status` 与 `install` 八类远程任务；配置工作区可从 Agent 白名单路径读取节点实际文件，经目标内核校验后载入手动编辑器。已预置 systemd 单元和初始配置的内核可选择官方稳定版、开发版或严格版本号安全升级。
- dry-run 结果会以结构化 `simulated` 状态保存并在界面标为“模拟完成”；模拟部署不会更新“已部署配置”，避免把未写入节点的版本误报为真实生效。
- 节点结构化编辑明确区分增加、修改和删除：增加拒绝重名，修改/删除要求目标真实存在，所有操作都保留其他入站与未知字段，并强制进入节点内核校验或部署流程。
- Agent 部署前调用真实内核校验，采用受限目录句柄、`fsync` 和重命名完成原子替换；最多保留 3 份 `0600` 备份，重启失败时自动回滚并尝试恢复服务。
- 内核版本任务只访问 MetaCubeX/mihomo、XTLS/Xray-core、SagerNet/sing-box 与 shadowsocks/shadowsocks-rust 官方 GitHub Release；Agent 固定选择当前 Linux 架构资产，强制核对 GitHub 提供的 SHA-256 后原子替换，重启失败自动恢复上一二进制。
- 管理 API 使用 Bearer 管理令牌；Web 控制台登录后使用短期服务端会话及 CSRF 防护。支持 `QCH_OPERATOR_TOKENS` 与 `QCH_READONLY_TOKENS` 划分运维/只读角色，API 与界面按角色分级。
- Agent 首次注册使用管理员签发的短期、限次入网码；后续 WSS 握手使用 Ed25519 签名、时间窗和持久化 nonce 防重放，任务与心跳在已认证长连接中双向传输。
- 移除节点会立即关闭其现有 WSS、终止未完成任务并永久拒绝原签名身份重连；重新接入必须使用新入网码注册新身份。
- 任务失败、节点离线或恢复在线时，可通过设置页配置的 Webhook 地址接收 HMAC-SHA256 签名的结构化 JSON 事件（`QCH_WEBHOOK_SECRET`）。
- 节点页提供最近 24 小时流量趋势图（每分钟采样、保留 7 天），并为漂移配置提供行级差异视图。
- 设置页展示最近操作审计轨迹（登录、配置、任务、节点、注册码、设置）。
- 节点页支持多选批量重启/启停/查状态；配置档案页支持带 `{{node_name}}`、`{{lan_ip}}`、`{{random_port}}` 占位符的模板，一键按节点渲染生成配置。
- 设置 `QCH_CONFIG_ENCRYPTION_KEY` 后，配置正文与修订使用 AES-256-GCM 加密落盘，旧明文行透明迁移。

```mermaid
flowchart LR
    Admin["管理员浏览器 / API"] -->|"HTTPS + 管理员鉴权"| CP["Go 控制面 + SSR Web"]
    CP -->|"pgx"| PG[("PostgreSQL")]
    Agent["远程 Go Agent"] -->|"出站 WSS + Ed25519 握手"| CP
    Agent -->|"固定配置路径"| Engines["Mihomo / Xray / sing-box / Shadowsocks Rust"]
    Agent -->|"受限 systemctl 动作"| Systemd["systemd"]
```

## 一键部署

QControlHub 当前仅支持 Linux 部署。

仓库提供两种从零到可访问的部署方式：全套内置 PostgreSQL 或连接已有 PostgreSQL。交互式一键脚本见 `deploy/quick-start.sh`：

```bash
./deploy/quick-start.sh
```

脚本也支持非交互部署和外部 PostgreSQL：

```bash
./deploy/quick-start.sh -m bundled
./deploy/quick-start.sh -m external \
  -d 'postgresql://user:pass@db.example.com:5432/qcontrolhub?sslmode=verify-full'

# 如果 PostgreSQL 在同一台主机的其他 Compose 项目中，指定它的 Docker 网络，并使用数据库服务名：
./deploy/quick-start.sh -m external -n 1panel-network \
  -d 'postgresql://qcontrolhub:URL_ENCODED_PASSWORD@postgresql:5432/qcontrolhub?sslmode=disable'
```

`-n` 会为控制面追加一个 external Docker network；容器内的 `127.0.0.1` 指向控制面容器自身，不能用来访问宿主机上的 PostgreSQL。若数据库只绑定宿主机回环地址，应将控制面加入数据库所在的 Docker network，或调整数据库监听地址后再使用宿主机地址。

首次运行会生成随机密钥并写入权限为 `0600` 的 `.env`。重复运行默认复用已有密钥并只补齐缺失项；如需轮换应用密钥，使用 `-f`，脚本会先备份 `.env` 且不会更改内置 PostgreSQL 密码。使用 `-h` 可查看全部选项。

项目发布以下 GHCR 镜像；一键部署默认拉取控制面镜像，Agent 镜像可用于节点侧交付：

```text
ghcr.io/qimaoww/qcontrolhub/qcontrol-plane:latest
ghcr.io/qimaoww/qcontrolhub/qagent:latest
```

公开镜像可以直接拉取；将 `QCH_IMAGE_TAG=local` 写入 `.env` 时，脚本会改为使用仓库内的 Dockerfile 本地构建。

也可以直接用 Make 目标手动部署。

### Linux · Docker Compose（推荐）

适用于已安装 Docker Engine、Docker Compose v2 和 OpenSSL 的 Linux 主机。一键拉起 PostgreSQL + 控制面，仅监听回环地址。

```bash
git clone https://github.com/qimaoww/qcontrolhub.git
cd qcontrolhub
make init-env    # 生成 0600 的 .env（含 QCH_ADMIN_TOKEN、数据库密码、Webhook 密钥、加密密钥）
make dev-up      # 本机体验模式：回环 HTTP，关闭 Secure Cookie
```

访问 `http://127.0.0.1:8080`，管理员令牌位于 `.env` 的 `QCH_ADMIN_TOKEN`。停止：`make down`。

生产环境改用 `make up`（保持 Secure Cookie + `QCH_ALLOW_INSECURE_HTTP=false`），并在前面架设 [Nginx TLS](deploy/nginx/qcontrolhub.conf)。完整步骤见 [生产部署](docs/production.md)。

### Linux · Agent 一键安装（远程节点）

在控制面已启动、且已在 Web 控制台签发短期单次入网码后，在远程 Linux 节点执行：

```bash
curl -fsSL https://raw.githubusercontent.com/qimaoww/qcontrolhub/main/deploy/remote/install-agent.sh \
  | sudo bash -s -- http://<控制面地址>:8080 <入网码> <节点名>
```

脚本会：从控制面 `GET /api/v1/agent-binary` 下载 Agent → 运行 `deploy/bootstrap-core-services.sh` 安装四个内核 systemd 单元和最小配置 → 写入 `/etc/qcontrolhub/agent.env`（`0600`）→ 安装 `qagent.service` 并启动。默认 `QCH_AGENT_DRY_RUN=true`，首轮只做校验不写文件。

> 节点没有预装内核二进制时，先执行 `sudo bash deploy/remote/install-core-engines.sh` 从官方 Release 下载 mihomo / xray / sing-box / ssserver。

---

## 快速体验

要求：Docker Engine、Docker Compose v2，以及用于生成随机值的 OpenSSL。

```bash
make init-env
make dev-up
```

然后访问 `http://127.0.0.1:8080`，使用 `.env` 中生成的 `QCH_ADMIN_TOKEN` 登录。`make dev-up` 只为本机体验显式允许容器内的明文 HTTP，并关闭 Secure Cookie；Compose 默认仅发布到回环地址。

停止环境：

```bash
make down
```

生产环境不要使用 `make dev-up`。保持 `QCH_BEHIND_TLS_PROXY=true` 和 `QCH_ALLOW_INSECURE_HTTP=false`，让控制面只发布到宿主机回环端口，并使用 [Nginx TLS 示例](deploy/nginx/qcontrolhub.conf) 对外提供 HTTPS。基础 Compose 的 `QCH_ALLOW_INSECURE_DATABASE=true` 仅适用于同机 Compose 后端网络；外部 PostgreSQL 必须改为证书校验。完整步骤见 [生产部署](docs/production.md)。

## 接入 Agent

Agent 默认是安全的 dry-run 模式。先构建二进制：

```bash
make build
```

在 Web 控制台的“远程节点”页签发一个短期、单次入网码。然后在远程节点安装 `bin/qagent`、[systemd 单元](deploy/systemd/qagent.service) 和 [环境文件模板](deploy/systemd/agent.env.example)。首次启动时填入该入网码；成功注册且 `/var/lib/qcontrolhub/agent-state.json` 已生成后，应从环境文件删除它。

如果控制面使用私有 CA 或局域网自签名证书，先把 CA PEM 安装到远程主机，再设置 `QCH_TLS_CA_FILE=/etc/qcontrolhub/control-plane-ca.pem`；不要使用跳过 TLS 校验的开关。

在使用节点页的“首次安装 / 切换”前，可在空白 Linux 节点的仓库目录执行 `sudo deploy/bootstrap-core-services.sh`。它会创建非 root 的 `qcontrolhub-core` 运行用户，安装四个受限 systemd 单元，并且只在目标配置不存在时写入绑定回环地址的最小配置；已有配置和 unit 不会被覆盖。随后确认 Agent 的引擎路径、服务名与配置目标无误，再将 `QCH_AGENT_DRY_RUN` 改为 `false`。默认映射如下：

| 内核 | 二进制 | 配置路径 | systemd 服务 |
| --- | --- | --- | --- |
| Mihomo | `/usr/local/bin/mihomo` | `/etc/mihomo/config.yaml` | `mihomo.service` |
| Xray | `/usr/local/bin/xray` | `/usr/local/etc/xray/config.json` | `xray.service` |
| sing-box | `/usr/local/bin/sing-box` | `/etc/sing-box/config.json` | `sing-box.service` |
| Shadowsocks Rust | `/usr/local/bin/ssserver` | `/etc/shadowsocks-rust/config.json` | `shadowsocks-rust.service` |

全部映射均可通过 Agent 环境变量覆盖，详见 [生产部署](docs/production.md#安装远程-agent)。

## 配置样例

`examples/configs/` 提供四份可通过 QControlHub 结构校验、并绑定本机回环地址的最小配置：

- [Mihomo](examples/configs/mihomo-minimal.yaml)
- [Xray](examples/configs/xray-minimal.json)
- [sing-box](examples/configs/sing-box-minimal.json)
- [Shadowsocks Rust](examples/configs/shadowsocks-rust-minimal.json)

这些样例用于验证部署链路，不包含公网服务凭据。对外开放入站前，请按对应内核文档加入强随机认证、访问控制和防火墙规则。

## 常用命令

```bash
make build          # 构建控制面和 Agent
make test           # 执行 Go 测试
make check          # gofmt 检查、go vet、go test
make compose-config # 校验 Compose 渲染结果（需要已初始化 .env）
make up             # 以 Secure Cookie 模式启动 Compose
make logs           # 查看控制面与 PostgreSQL 日志
```

## 文档

- [开发指南](docs/development.md)
- [生产部署](docs/production.md)
- [鉴权与安全基线](docs/security.md)
- [服务端入站方案](docs/server-plans.md)
- [HTTP API](docs/api.md)

管理 API 与 Web 控制台共用 admin（`QCH_ADMIN_TOKEN`）、operator（`QCH_OPERATOR_TOKENS`）与 readonly（`QCH_READONLY_TOKENS`）三级令牌，按角色分级授予读取、操作与管理权限。Web 会话保存在控制面进程内存中；生产环境建议先以单控制面实例运行。配置正文会以明文存入 PostgreSQL，因此数据库、备份和管理员终端都应按机密系统保护。设置 `QCH_CONFIG_ENCRYPTION_KEY`（任意非空字符串）后，配置正文与修订会改用 AES-256-GCM 加密落盘（密钥经 SHA-256 派生）；旧明文行无需迁移即可透明读取，新写入自动加密——但密钥一旦丢失将无法解密数据，务必妥善备份。
