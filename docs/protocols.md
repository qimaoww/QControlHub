# 协议与客户端

## sing-box 预设

在配置页选择 sing-box 后增加入站。预设涵盖 VLESS Reality / HTTP2 Reality，VMess TCP / WS / HTTP / QUIC，VMess、VLESS、Trojan 的 WS / H2 / HTTPUpgrade TLS，以及原生 Trojan、TUIC、Hysteria2、AnyTLS、Shadowsocks 和认证 SOCKS5。

| 配置 | 使用要求 |
| --- | --- |
| VLESS TCP Reality | 使用 Vision；HTTP2 Reality 不使用 Flow |
| HTTP / H2 | 原生字段为 `transport.type=http`；无 TLS 为 HTTP/1.1，TLS / Reality 为 H2 |
| WS / HTTPUpgrade | ALPN 包含 `http/1.1`，Host、路径与客户端一致 |
| H2 | ALPN 包含 `h2`；托管反代回源使用 h2c |
| QUIC、TUIC、Hysteria2 | 内核具备 `with_quic`，开放实际 UDP 监听端口 |
| Reality | 内核具备 `with_utls`，使用可达的公网握手目标与正确 SNI |
| AnyTLS | 内核 1.12 或更新版本，客户端实现 AnyTLS |
| HTTPUpgrade | 内核 1.7 或更新版本 |

Shadowsocks 支持 `aes-128-gcm`、`aes-256-gcm`、`chacha20-ietf-poly1305`、`xchacha20-ietf-poly1305` 及三种 AEAD-2022 方法。AEAD-2022 的 AES-128 使用 16 字节随机 PSK，AES-256 和 ChaCha20 使用 32 字节随机 PSK，均以 Base64 表示。客户端需支持所选方法。

连接地址、SNI、HTTP Host 和路径分别配置。表单不能完整表达的多用户或自定义传输配置应通过源码编辑。版本、编译标签、证书和端口检查失败时，在任务详情查看具体原因。

## Mihomo Mieru

TCP 隧道也可承载 UDP，客户端配置保留 `transport: TCP` 与 `udp: true`。仅 UDP 不通时，先更新 Agent 并检查托管服务权限；Mihomo 的接口发现需要 NETLINK 地址族。

## TLS 方式

| 方式 | 使用与维护 |
| --- | --- |
| 托管 Caddy | WS、HTTPUpgrade、HTTP/H2 的默认方式；填写域名和公网端口，自动准备证书与反代，内部回环端口自动生成 |
| 托管 Nginx | WS / HTTPUpgrade；使用项目私有 CA，客户端需信任导出的公共 CA |
| 已有证书 · 原生 TLS | 内核直接监听 TLS；填写节点上可读的 PEM 证书、私钥及证书覆盖的 SNI |
| 外部反代 | 内核监听回环地址，用户管理公网代理、证书与续期，填写实际公网端口、SNI、Host 和路径 |

托管 Caddy 默认公网端口为 443，HTTP-01 还需要 TCP 80；内部与公网端口必须不同，共享用户需同时获授权。Reality、QUIC、TUIC、Hysteria2、AnyTLS 使用各自原生方式。

外部 Caddy 的 WS 反代可参考：

```caddyfile
proxy.example.com {
    reverse_proxy /your-path 127.0.0.1:20001
}
```

H2 使用 `reverse_proxy /your-path h2c://127.0.0.1:20001`，客户端 ALPN 为 h2。公共 CA 证书使用系统信任根；自签或私有 CA 需将公共证书填入客户端信任字段，导出原生 JSON。证书、权限与 ACME 设置见[使用指南](usage.md#证书与托管-tls)和[部署与维护](deployment.md#agent-与内核运行)。

## Xray XHTTP

| 方案 | 节点要求 |
| --- | --- |
| 独立 Reality | Xray 与公网 Reality target，不需本机证书或反代 |
| 独立 TLS / CDN | Xray 原生 TLS、可读的源站证书与私钥 |
| TLS/CDN 上传 → Reality 下载 | 公共 Xray 入口、内部 XHTTP 端口、本地 Nginx TLS 端口和组合证书 |
| Reality 上传 → TLS/CDN 下载 | 同上 |
| 五合一 | 同上，公共入口额外提供 Vision 用户 |

基础 XHTTP 要求服务端和客户端 Xray 至少 24.12.15；使用 `xmux.hMaxReusableSecs` 等高级字段需 26.3.27 或更新版本。组合需要 Agent 的 `xhttp-presets-v1` 能力。

独立 Reality / TLS 可启用下载端点，为上下行分别配置地址、端口、SNI、Host 与 ALPN。两个端点需进入同一 XHTTP 后端并使用相同路径；IPv6 地址字段填写裸地址。`stream-one` 不能与下载分离或混合组合同时使用，改用 auto、stream-up 或 packet-up。`xmux.maxConnections` 与 `maxConcurrency` 至少一项为 0。

组合的公网、内部 XHTTP 和本地 TLS 三个端口需互不相同；共享用户需获得全部端口授权，仅公网入口对外开放。组合证书须覆盖 TLS SNI 和全部允许的 Reality SNI。服务端 Host 非空时，两端必须匹配；多个 CDN Host 需要将服务端 Host 留空。

CDN 客户端信任边缘证书，CDN 到源站另行验证源站证书。对 XHTTP 路径关闭缓存并允许 POST、长请求与流式响应。原生源站只监听 TCP；推荐 h2，单独 h3 仅适用于实际支持 HTTP/3 的 CDN 入口。

## 客户端导出

| 格式 | 范围与限制 |
| --- | --- |
| sing-box JSON | sing-box 预设的完整参数及自定义信任证书；XHTTP 不支持 |
| Xray JSON | XHTTP 独立、分离、混合与五合一完整参数；本地 SOCKS 默认 `127.0.0.1:10808` |
| Mihomo YAML | 仅导出可等价表达的协议、传输与信任设置；HTTP/1.1、HTTPUpgrade、Trojan H2 等组合可能不支持 |
| 分享 URI | 基础协议；复杂 XHTTP extra、HTTP/H2、自定义信任等使用原生配置 |

界面会说明不支持的具体字段。XHTTP 的完整 `downloadSettings` / extra 优先使用 Xray JSON；Mihomo 的 HTTP/1.1 与 stream-one / stream-up 组合不提供导出，可改为 h2 / packet-up。手工修改配置后，需重新保存部署并核对客户端参数。

## 来源与许可

- sing-box 预设参考 [233boy/sing-box 固定版本](https://github.com/233boy/sing-box/tree/2d78583b5aecccb0da0148a816bc1495a284509f)的配置行为，独立实现，未复制或分发上游安装脚本；上游许可为 [GPL-3.0](https://github.com/233boy/sing-box/blob/2d78583b5aecccb0da0148a816bc1495a284509f/LICENSE)。字段依据 [sing-box 配置文档](https://sing-box.sagernet.org/configuration/)。
- XHTTP 方案参考 [XTLS 作者说明](https://github.com/XTLS/Xray-core/discussions/4118)及其[简化独立配置](https://github.com/XTLS/Xray-core/discussions/4118#discussioncomment-11593313)。
- 托管反代使用 Caddy（Apache-2.0）与 Nginx（BSD 类许可），安装时从官方发布或系统软件源取得。参考 [Caddy 自动 HTTPS](https://caddyserver.com/docs/automatic-https)及 [Nginx WebSocket 代理](https://nginx.org/en/docs/http/websocket.html)。
- IPQuality 使用 [xykt/IPQuality](https://github.com/xykt/IPQuality) 提供的检测脚本，检测结果包含第三方数据库及服务信息。
