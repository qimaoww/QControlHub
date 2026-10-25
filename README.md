# QControlHub

面向 Linux 节点的配置与远程运维平台，由 Go 控制面、Go Agent、Web 控制台和 PostgreSQL 组成。节点支持 systemd 和 Alpine OpenRC。

## 主要能力

- 管理 Mihomo、Xray、sing-box 和 Shadowsocks Rust 的安装、配置版本、校验、部署与服务状态。
- 通过出站 WSS 接入节点，集中查看运行指标、任务、审计、实时日志和 IPQuality 报告。
- 生成客户端接入参数、分享链接和原生配置，支持按账号同步到 Sub-Store。
- 提供多用户工作区、节点共享、独立内核实例、端口授权和流量配额。
- 管理证书申请、导入、授权、续期与实例同步。
- 支持端口流量统计、超额封禁、Webhook 和 Komari 集成。

## 安装

在 Linux 控制面主机执行，按菜单选择内置或外部 PostgreSQL：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/qimaoww/qcontrolhub/main/deploy/quick-start.sh)
```

默认安装到当前目录下的 `qcontrolhub`，可用 `QCH_INSTALL_DIR` 指定目录。安装器保存目录选择，后续运行会复用。管理员令牌只在创建或轮换时显示一次，请妥善保存。

生产环境需配置 HTTPS 反向代理；部署参数、仅 IPv6 主机的下载方式和备份要求见[部署与维护](docs/deployment.md)。

## 更新

重新运行安装命令并选择“更新”，或在安装目录执行：

```sh
bash deploy/quick-start.sh -o update
```

更新前备份数据库、`.env` 和 `.secrets/`。更新保留配置和数据；未变化的镜像跳过重建。数据库迁移后的恢复要求见[部署与维护](docs/deployment.md#更新与备份)。

## 节点接入

登录 Web 控制台，在节点管理中生成添加命令，并在目标 Linux 节点以 root 执行。安装器支持 systemd 和 Alpine OpenRC，接入后从面板安装所需内核。

Agent 会执行配置、服务和流量规则操作。共享、配置部署及日常操作见[使用指南](docs/usage.md)。

## 文档

| 文档 | 内容 |
| --- | --- |
| [部署与维护](docs/deployment.md) | 安装、HTTPS、环境配置、更新、备份与故障定位 |
| [使用指南](docs/usage.md) | 配置、节点共享、流量、证书与日志 |
| [协议与客户端](docs/protocols.md) | sing-box 预设、Xray XHTTP、TLS 与导出限制 |
| [只读 API](docs/api.md) | API Key、接口、分页与诊断示例 |
| [开发指南](docs/development.md) | 本地开发、生成文件和验证命令 |

## License

依据 [GNU General Public License version 3](LICENSE) 发布，SPDX：`GPL-3.0-only`。相关来源与归属见[协议与客户端](docs/protocols.md#来源与许可)。
