# 部署与维护

## 安装与 HTTPS

控制面主机需要 Linux、Docker 和 Docker Compose。使用 [README 安装命令](../README.md#安装)，或在源码仓库运行 `bash deploy/quick-start.sh`。安装器支持：

- `bundled`：内置 PostgreSQL。
- `external`：连接已有 PostgreSQL，可选择已有 Docker 网络。
- `QCH_INSTALL_DIR`：指定持久安装目录；默认使用已保存的目录或当前目录下的 `qcontrolhub`。

完整选项通过 `bash deploy/quick-start.sh -h` 查看。远程安装只下载运行脚本和 Compose 文件，不克隆源码。

面板默认绑定 `127.0.0.1:8080`，数据库默认绑定回环地址。配置 HTTPS 反向代理后再开放管理入口，可参考 [Nginx 配置](../deploy/nginx/qcontrolhub.conf)，替换域名和证书路径。代理需转发 WebSocket、Host 和 HTTPS 来源信息。仅将实际代理地址列入 `QCH_TRUSTED_PROXY_CIDRS`；`QCH_BEHIND_TLS_PROXY=true` 应与实际部署一致。

## 配置与密钥

源码部署的配置项见 [.env.example](../.env.example) 和 [docker-compose.yml](../docker-compose.yml)。源码环境可用 `make init-env` 生成 `.env`、`.secrets/` 和 `docker-compose.secrets.yml`，修改配置后运行 `make compose-config`、`make up`。

| 设置 | 用途 |
| --- | --- |
| `QCH_ADMIN_TOKEN_SHA256` | 环境管理员令牌摘要；原始令牌至少 32 字节，仅在创建或轮换时显示 |
| `QCH_CONFIG_ENCRYPTION_KEY_SECRET_SOURCE` | 宿主机上的当前加密密钥文件，默认 `.secrets/config-encryption-key` |
| `QCH_CONFIG_ENCRYPTION_PREVIOUS_KEYS_SECRET_SOURCE` | 旧密钥文件，默认 `.secrets/config-encryption-previous-keys` |
| `QCH_DATABASE_STARTUP_TIMEOUT` | 数据库迁移及启动预算，默认 `5m`，允许 `1s` 至 `1h` |
| `QCH_BIND_ADDRESS`、`QCH_PORT` | 面板监听地址与端口 |
| `QCH_IMAGE_TAG` | 内置 Compose 的镜像标签；源码构建使用 `local` |
| `QCH_CORS_ORIGINS` | 允许访问 JSON API 的精确浏览器来源；同源界面无需额外配置 |

安装器将密钥保存在宿主机私有 `.secrets/` 中，通过只读文件挂载提供给控制面。密钥丢失会导致加密配置、证书等数据无法解密；轮换后需保留仍用于旧数据的密钥。`QCH_ADMIN_TOKEN` 和直接设置的加密密钥环境变量保留兼容支持。

外部数据库应启用 TLS 并验证服务端证书；`QCH_ALLOW_INSECURE_DATABASE=true` 适用于内置隔离网络。修改 Compose 网络地址时，同步调整相关网段、网关、固定服务地址及可信代理配置。

## 更新与备份

更新前保存数据库备份、安装目录的 `.env`、`.secrets/`、Compose 覆盖文件，以及自定义代理配置。节点侧还需保留服务配置、证书与状态目录。

在安装目录运行：

```sh
bash deploy/quick-start.sh -o update
```

安装器比较目标镜像 ID，无变化时跳过重建；`local` 源码模式仍重新构建。外部模式更新保留原 `.env`，先等待控制面迁移就绪，再启动前端；失败时尝试恢复原 Compose 与镜像。应用镜像回退不能降低数据库 schema，恢复旧版控制面需要匹配的迁移前数据库备份。

数据库迁移默认允许 5 分钟，安装器每阶段默认等待 360 秒。调高 `QCH_DATABASE_STARTUP_TIMEOUT` 时，同时提高安装器的 `-t SECONDS`。通过容器日志和 `/readyz` 检查迁移进度；健康检查失败时保留数据库卷并排查连接、迁移锁和启动预算。

卸载命令 `bash deploy/quick-start.sh -o uninstall` 保留配置、密钥和数据库卷。

## Agent 与内核运行

优先使用控制台生成的节点安装命令。手工配置可参考 [Agent 环境示例](../deploy/systemd/agent.env.example)。Agent 状态与身份默认保存在 `/var/lib/qcontrolhub/agent-state.json`；重装应保留原状态以复用身份。

Agent 以 root 运行，内核服务使用非 root 的 `qcontrolhub-core` 用户。systemd 提供文件系统沙箱，OpenRC 使用服务用户与能力限制。内核仅需 `CAP_NET_BIND_SERVICE` 和独立出口使用的 `CAP_NET_ADMIN`。

| 资源 | 默认路径 |
| --- | --- |
| 内核二进制 | `/usr/local/lib/qagent/cores/` |
| 内核配置 | `/etc/qagent/{mihomo,xray,sing-box,shadowsocks-rust}/` |
| 共享实例配置 | 对应内核目录的 `shares/<share-id>/` |
| 内核状态 | `/var/lib/qcontrolhub-<core>` 及共享实例状态目录 |
| 托管 TLS 配置与清单 | `/etc/qagent/tls-frontends/` |
| Caddy 证书及 ACME 状态 | `/var/lib/qcontrolhub-tls/caddy/` |
| 托管 Caddy 日志 | `/var/lib/qcontrolhub-tls/caddy.log` |

接入不会预建四个内核服务；面板安装内核时按需创建。手工准备可运行 `sudo sh deploy/bootstrap-core-services.sh mihomo`，支持 `xray`、`sing-box`、`shadowsocks-rust`，省略参数准备全部四个。

安装器可识别标准 Xray、sing-box 服务；Agent 还可识别 systemd 下的 `install-ss-rust`。识别到的原服务需在配置页显式迁移；无法识别时保留原服务。更新 Agent 会同步项目管理的服务模板和权限，运行中的旧进程可能短暂重启；自定义服务覆盖项需要人工核对。

## 常见维护问题

- **重启后退出登录**：会话保存在内存中，重新使用原账号密码登录。环境管理员令牌入口与数据库中的 `admin` 账号独立。401 检查凭据及停用状态，429 等待限流，500 检查数据库与迁移日志；登录成功后会话立即丢失时检查 HTTPS、Cookie 和代理来源。
- **证书不可读**：建议放入 `/etc/qcontrolhub/tls/`，父目录 `root:qcontrolhub-core`、0750，证书与私钥 0640。用 `namei -l PATH` 检查目录遍历权限；systemd 的 `ProtectHome`、`PrivateTmp` 会隔离家目录和临时目录。更换文件后重新部署。
- **服务权限异常**：更新并重启 Agent，检查日志中的 `permission migration`。systemd 用 `systemctl cat SERVICE` 查看覆盖项；OpenRC 检查 `/etc/conf.d/`。保留配置、证书和状态后再修正具体文件权限。
- **IPQuality 依赖失败**：更新 Agent 后重新检测，系统从已有可信软件源准备依赖，支持 apt-get、apk、dnf/yum、zypper。包管理锁应等待原事务完成；只读或非 root 环境需先准备依赖。可运行 `sudo /usr/local/bin/qagent ip-quality`，JSON 输出到 stdout，阶段日志到 stderr；该命令会安装缺失依赖并访问第三方服务。

## 仅 IPv6 主机下载

首次下载脚本无法依赖尚未运行的网络检测，可使用：

```bash
bash <(curl -q --fail --silent --show-error --proto '=https' \
  --connect-timeout 10 --max-time 90 \
  https://v6.gh-proxy.org/https://raw.githubusercontent.com/qimaoww/qcontrolhub/main/deploy/quick-start.sh)
```

脚本和 Agent 运行后自动选择公开 GitHub 资源的下载通道；IPv4、双栈和可用 NAT64 直连，确认仅 IPv6 时使用上述固定代理。检测不确定时先尝试直连，DNS/连接失败后验证代理通道。GHCR 镜像拉取、系统软件源及内核自行下载的规则资源使用各自网络配置。
