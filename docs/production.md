# 生产部署

本文采用“单机 Docker Compose API 控制面 + 独立 SPA + 宿主机 Nginx + 多台 systemd Agent”的基线。只有 `qcontrol-web` 发布到回环地址，Nginx 负责公网 TLS；控制面 API 只在 Compose 内部网络可达，控制面到 PostgreSQL 使用项目内部后端网络，数据库持久化到命名卷。

## 1. 准备控制面主机

建议使用受支持的 Linux 发行版，并安装 Docker Engine、Docker Compose v2、Nginx、OpenSSL 与证书管理工具。防火墙只对管理来源和 Agent 网络开放 TCP 443；不要开放 8080 或 5432。

初始化随机密钥：

```bash
make init-env
chmod 600 .env
```

把 `.env` 中的 PostgreSQL 密码和管理员令牌保存到密码管理器。确认以下生产设置：

```dotenv
QCH_BEHIND_TLS_PROXY=true
QCH_ALLOW_INSECURE_HTTP=false
QCH_ALLOW_INSECURE_DATABASE=true
QCH_CONTROL_PROXY_SUBNET=172.30.254.0/24
QCH_CONTROL_PROXY_GATEWAY=172.30.254.1
QCH_TRUSTED_PROXY_CIDRS=172.30.254.1/32
QCH_BIND_ADDRESS=127.0.0.1
QCH_PORT=8080
POSTGRES_PORT=5432
QCH_CORS_ORIGINS=https://qcontrolhub.example.com
# 可选：配置正文与修订的 AES-256-GCM 落盘加密密钥（任意非空字符串）。
# 开启后旧明文行仍可透明读取，新写入自动加密；密钥丢失将无法解密，务必备份。
QCH_CONFIG_ENCRYPTION_KEY=replace-with-a-long-random-secret
```

`QCH_CORS_ORIGINS` 仅在浏览器从另一个 origin 调用 JSON API 时需要；使用同域 Web 控制台可以留空。`QCH_TRUSTED_PROXY_CIDRS` 必须只包含实际 Nginx 对端，基础 Compose 因此为控制面发布网络固定了一个可覆盖的网段与 gateway；若网段冲突，三个相关值必须一起修改。若手工设置 PostgreSQL 密码，必须对 URL 保留字符进行百分号编码；`make init-env` 生成的十六进制密码可直接用于 Compose URL。

## 2. 启动 PostgreSQL 与控制面

```bash
make up
docker compose ps
curl --fail http://127.0.0.1:8080/healthz
curl --fail http://127.0.0.1:8080/readyz
```

控制面启动时会自动建立或升级当前 schema。Compose 的 PostgreSQL 连接在单机内部网络使用 SCRAM 密码认证；`QCH_ALLOW_INSECURE_DATABASE=true` 只为这个隔离 bridge 上的 `sslmode=disable` 连接提供显式豁免，不适用于跨主机数据库。

首次部署后执行一致性备份，并设置定期备份。例如逻辑备份可以在受保护目录中运行：

```bash
set -a
. ./.env
set +a
umask 077
docker compose exec -T postgres pg_dump \
  -U "$POSTGRES_USER" \
  -d "$POSTGRES_DB" \
  --format=custom > qcontrolhub.dump
```

备份含完整配置正文，必须加密并限制访问。恢复演练应在隔离环境完成。

## 3. 配置 Nginx 与 TLS

1. 获取 `qcontrolhub.example.com` 的有效证书。
2. 复制 [Nginx 示例](../deploy/nginx/qcontrolhub.conf) 到 `/etc/nginx/conf.d/qcontrolhub.conf`。
3. 替换其中的域名与证书路径。
4. 如果并非所有子域都强制 HTTPS，评估并调整 HSTS 的 `includeSubDomains`。
5. 校验并平滑重载：

```bash
sudo nginx -t
sudo systemctl reload nginx
curl --fail https://qcontrolhub.example.com/healthz
```

控制面根据 `QCH_BEHIND_TLS_PROXY=true` 把连接视为安全传输，并直接设置 Secure Cookie 与 HSTS，不依赖代理传入的协议头。只有 TLS 确实在本机受信反向代理终止时才能设置该值；不要为“解决登录问题”启用 `QCH_ALLOW_INSECURE_HTTP`。SPA 登录调用 `/api/v1/auth/login`，浏览器后续写请求自动携带会话 CSRF 头。

Nginx 示例按真实客户端 IP 对 `/api/v1/auth/login` 和 `/agent/v1/enroll` 做额外限速。控制面仅在直接 TCP 对端匹配 `QCH_TRUSTED_PROXY_CIDRS` 时解析 `X-Forwarded-For`，并从右向左剥离可信代理；不要把整个私网或 `0.0.0.0/0` 加入该列表。

Agent 使用 `/agent/v1/connect` 的长期 WSS 会话。Nginx 示例已转发 `Upgrade`/`Connection`，并把上游读取空闲超时提高到一小时；删除这些设置会导致 Agent 无法升级或在无任务时周期性断线。

## 4. 安装远程 Agent

在受控构建主机执行：

```bash
make build VERSION=0.1.0
```

将 `bin/qagent` 安全传输到 Agent 主机，然后以 root 安装：

```bash
sudo install -o root -g root -m 0755 qagent /usr/local/bin/qagent
sudo install -d -o root -g root -m 0700 /etc/qcontrolhub /var/lib/qcontrolhub
sudo install -d -o root -g root -m 0750 /etc/qagent/mihomo /etc/qagent/xray /etc/qagent/sing-box /etc/qagent/shadowsocks-rust /etc/qcontrolhub/tls
sudo install -d -o root -g root -m 0755 /usr/local/lib/qagent/cores
sudo install -o root -g root -m 0644 \
  deploy/systemd/qagent.service \
  /etc/systemd/system/qagent.service
sudo install -o root -g root -m 0600 \
  deploy/systemd/agent.env.example \
  /etc/qcontrolhub/agent.env

# 空白节点还没有内核 unit 和初始配置时执行；已有文件不会被覆盖。
sudo deploy/bootstrap-core-services.sh
```

TLS 入站默认引用 `/etc/qcontrolhub/tls/server.crt` 与 `/etc/qcontrolhub/tls/server.key`。QControlHub 不代替 ACME 或站点证书管理；使用 TLS、TUIC、Hysteria2、Trojan 或 AnyTLS 前，应把适用的证书链和私钥安装到这两个路径（私钥权限建议 `0600`），或在方案表单中改为站点的既有绝对路径。

如果文件是从另一台构建主机复制来的，请把示例文件的本地路径替换为实际路径。先登录控制台，在“远程节点”页为目标节点生成添加命令；原始凭证只显示一次，可重复安装，直到删除对应的添加记录。然后编辑 `/etc/qcontrolhub/agent.env`：

- `QCH_SERVER_URL` 应使用控制面的 `wss://` origin；Agent 会从同一 origin 派生首次注册所需的 HTTPS 地址。
- 公网可信证书无需额外配置；私有 CA 或自签名证书必须复制为 root 所有的普通 PEM 文件，并通过 `QCH_TLS_CA_FILE` 指定绝对路径。Agent 不提供跳过证书验证的模式。
- 安装或重装时临时把该节点的添加凭证填入 `QCH_ENROLLMENT_TOKEN`。
- 设置有辨识度的节点名与标签。
- `QCH_AGENT_ENGINES` 只列出本机真实安装的内核。
- 保持 `QCH_AGENT_DRY_RUN=true` 完成首轮验证。
- 核对每个内核的 binary、config、service 三项覆盖值。

默认值与覆盖变量：

| 内核 | 默认二进制 | 默认配置路径 | 默认服务 | 覆盖前缀 |
| --- | --- | --- | --- | --- |
| Mihomo | `/usr/local/lib/qagent/cores/mihomo` | `/etc/qagent/mihomo/config.yaml` | `qagent-mihomo.service` | `QCH_MIHOMO_*` |
| Xray | `/usr/local/lib/qagent/cores/xray` | `/etc/qagent/xray/config.json` | `qagent-xray.service` | `QCH_XRAY_*` |
| sing-box | `/usr/local/lib/qagent/cores/sing-box` | `/etc/qagent/sing-box/config.json` | `qagent-sing-box.service` | `QCH_SING_BOX_*` |
| Shadowsocks Rust | `/usr/local/lib/qagent/cores/ssserver` | `/etc/qagent/shadowsocks-rust/config.json` | `qagent-shadowsocks-rust.service` | `QCH_SS_RUST_*` |

私有 CA 示例：

```bash
sudo install -o root -g root -m 0644 control-plane-ca.pem /etc/qcontrolhub/control-plane-ca.pem
sudo sh -c 'printf "%s\n" "QCH_TLS_CA_FILE=/etc/qcontrolhub/control-plane-ca.pem" >> /etc/qcontrolhub/agent.env'
```

启动：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now qagent
sudo journalctl -u qagent -f
```

在控制台确认 Agent 在线，并确认 `/var/lib/qcontrolhub/agent-state.json` 的权限是 `0600`。随后：

1. 从 `/etc/qcontrolhub/agent.env` 删除 `QCH_ENROLLMENT_TOKEN` 行。
2. 通过控制台下发四种内核的 `validate` 与 `status` 测试。
3. 等待两个心跳周期，确认节点卡片显示 CPU、内存、根磁盘、实时上下行速率和累计流量；网速首个样本为 0，第二个样本开始按计数器差值计算。
4. 核实 dry-run 输出及每个固定目标路径；在节点卡片尝试稳定版、开发版或自定义版本任务时，dry-run 不会下载或替换文件。
5. 只有需要真实部署时才设置 `QCH_AGENT_DRY_RUN=false` 并重启 Agent。

```bash
sudo systemctl restart qagent
```

systemd 单元的 `ProtectSystem=strict` 只放行默认的四个配置目录以及 `/usr/local/lib/qagent/cores`，用于在同一文件系统内原子切换内核二进制；`/usr/local/bin` 不可写，`/usr/local/bin/qagent` 也保持只读。四个内核服务统一使用 `qagent-` 前缀，不会控制管理员自行安装的通用服务。自定义二进制或配置路径必须预先创建并精确加入 `ReadWritePaths=`，不要放宽为整个 `/etc` 或 `/usr`。

真实版本切换要求 `QCH_AGENT_DRY_RUN=false`，并且节点已经预置对应配置目录、可通过的初始配置和 systemd 单元。空白 Linux 节点可先运行 `deploy/bootstrap-core-services.sh` 完成这些前置条件；脚本仅创建缺失配置和新的 `qagent-*` unit，不迁移也不操作旧的通用服务或二进制。稳定版使用官方 latest，开发版只使用官方 prerelease，自定义版本必须是类似 `1.19.29` 或 `1.14.0-beta.3` 的完整版本号；不支持自定义下载地址。Agent 在下载后强制核对 GitHub Release API 给出的 SHA-256，运行候选二进制确认版本，随后原子替换并重启服务；失败时恢复上一二进制。

## 5. 运维操作

### 更新控制面

```bash
docker compose build --pull control-plane
docker compose up -d control-plane
docker compose ps
```

控制面重启会让 Web 用户重新登录，但 Agent 身份与任务保存在 PostgreSQL 中。

### 更新 Agent

先在少量节点保留 dry-run 进行验证，再原子替换二进制并重启：

```bash
sudo install -o root -g root -m 0755 qagent /usr/local/bin/qagent
sudo systemctl restart qagent
sudo systemctl status qagent --no-pager
```

### 撤销与重新注册

从控制台删除 Agent 会立即使其签名身份失效。收到永久身份拒绝后，Agent 会正常退出而不是把它当作瞬时网络故障持续重连；配套的 `Restart=on-failure` 不会再次拉起它。若需重新注册：

```bash
sudo systemctl stop qagent
sudo rm /var/lib/qcontrolhub/agent-state.json
```

然后重新执行该节点的添加命令。控制面会复用原节点 ID、替换旧签名密钥并关闭旧连接；若添加记录已删除，则需要重新创建。删除状态文件是不可逆身份操作，必须先确认控制台中旧身份已撤销。

### 外部 PostgreSQL

使用外部数据库时，不应把密码直接写入可读命令行。通过受保护环境或密钥注入设置完整 `QCH_DATABASE_URL`，并使用类似以下参数：

```text
postgresql://qcontrolhub:URL_ENCODED_PASSWORD@db.example.com:5432/qcontrolhub?sslmode=verify-full&sslrootcert=/run/secrets/db-ca.pem
```

同时移除或禁用 Compose 中的 `postgres` 服务，将 `QCH_ALLOW_INSECURE_DATABASE=false`，并确保 CA 文件对控制面容器只读可见。当前仓库的基础 Compose 面向单机内置 PostgreSQL，外部数据库需要站点级 override 文件。

## 6. 监控建议

- 外部探测 `/healthz` 作为进程存活信号、`/readyz` 作为 PostgreSQL 就绪信号，同时监控容器重启次数。
- 告警 Agent 超过 45 秒离线、任务持续失败或任务积压。
- 收集控制面与 Agent 日志，但不要采集 `.env`、Authorization 请求头或配置正文。
- 监控磁盘、WAL 与备份新鲜度；在升级前执行恢复测试。
- 所有控制面和 Agent 主机启用 NTP/chrony，Agent 签名时间窗为正负 90 秒。

更完整的上线检查见 [鉴权与安全基线](security.md#上线核对表)。
