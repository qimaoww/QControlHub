package configschema

func mihomoFields() []Field {
	general := "https://wiki.metacubex.one/config/general/"
	inbound := "https://wiki.metacubex.one/config/inbound/"
	return []Field{
		field("port", "HTTP 代理端口", "整数", "HTTP(S) 代理监听端口。", inbound+"port/"),
		field("socks-port", "SOCKS 代理端口", "整数", "SOCKS5 代理监听端口。", inbound+"port/"),
		field("redir-port", "Redirect 透明代理端口", "整数", "Linux/macOS Redirect 透明代理入口。", inbound+"port/"),
		field("tproxy-port", "TProxy 透明代理端口", "整数", "Linux TProxy TCP/UDP 入口。", inbound+"port/"),
		field("mixed-port", "混合代理端口", "整数", "同时接受 HTTP(S) 与 SOCKS 的入口。", inbound+"port/"),
		field("ss-config", "Shadowsocks 入口配置文件", "字符串", "外部 Shadowsocks 入口配置路径。", inbound),
		field("vmess-config", "VMess 入口配置文件", "字符串", "外部 VMess 入口配置路径。", inbound),
		field("inbound-tfo", "入站 TCP Fast Open", "布尔", "控制入站 TCP Fast Open。", inbound),
		field("inbound-mptcp", "入站 MPTCP", "布尔", "控制入站 Multipath TCP。", inbound),
		field("authentication", "入口认证", "字符串数组", "HTTP/SOCKS 入口用户名与密码。", inbound),
		field("skip-auth-prefixes", "跳过认证网段", "字符串数组", "指定无需入口认证的 IP 前缀。", inbound),
		field("lan-allowed-ips", "局域网允许网段", "字符串数组", "允许连接入口的 IP 网段。", general),
		field("lan-disallowed-ips", "局域网拒绝网段", "字符串数组", "拒绝连接入口的 IP 网段，优先于允许列表。", general),
		field("allow-lan", "允许局域网连接", "布尔", "控制是否接受局域网连接。", general),
		field("bind-address", "绑定地址", "字符串", "allow-lan 开启时使用的监听地址。", general),
		field("mode", "运行模式", "枚举", "规则、全局或直连模式。", general),
		field("unified-delay", "统一延迟计算", "布尔", "控制延迟测试的计算方式。", general),
		field("log-level", "日志级别", "枚举", "Mihomo 日志输出级别。", general),
		field("ipv6", "IPv6 总开关", "布尔", "控制 IPv6 连接及 AAAA 查询。", general),
		field("external-controller", "控制器监听地址", "字符串", "REST API 的 HTTP 监听地址。", general),
		field("external-controller-routing-mark", "控制器路由标记", "整数", "控制器监听套接字的 Linux routing-mark。", general),
		field("external-controller-unix", "控制器 Unix Socket", "字符串", "控制器 Unix Socket 地址。", general),
		field("external-controller-tls", "控制器 TLS 地址", "字符串", "REST API 的 HTTPS 监听地址。", general),
		field("external-controller-cors", "控制器 CORS", "对象", "REST API 跨域响应策略。", general),
		field("external-ui", "外部 UI 目录", "字符串", "控制器提供的静态 UI 目录。", general),
		field("external-ui-url", "外部 UI 下载地址", "字符串", "外部 UI 压缩包地址。", general),
		field("external-ui-name", "外部 UI 名称", "字符串", "外部 UI 子目录名称。", general),
		field("external-doh-server", "外部 DoH 路径", "字符串", "在控制器端口暴露的 DoH 路径。", general),
		field("secret", "控制器密钥", "字符串", "REST API Bearer 认证密钥。", general),
		field("interface-name", "出口网卡", "字符串", "全局指定出站接口。", general),
		field("routing-mark", "全局路由标记", "整数", "Linux 出站连接 routing-mark。", general),
		field("tunnels", "隧道", "对象数组", "Mihomo tunnel 转发规则。", "https://wiki.metacubex.one/config/tunnels/"),
		field("geo-auto-update", "Geo 数据自动更新", "布尔", "自动更新 Geo 数据文件。", general),
		field("geo-update-interval", "Geo 更新间隔", "整数", "Geo 数据更新间隔。", general),
		field("geodata-mode", "GeoData 模式", "布尔", "控制 Geo 数据加载模式。", general),
		field("geodata-loader", "GeoData 加载器", "字符串", "选择 Geo 数据加载实现。", general),
		field("geosite-matcher", "GeoSite 匹配器", "字符串", "选择 GeoSite 匹配实现。", general),
		field("tcp-concurrent", "TCP 并发拨号", "布尔", "同时尝试多个 IP 并采用最快连接。", general),
		field("find-process-mode", "进程匹配模式", "枚举", "控制规则的进程匹配行为。", general),
		field("global-client-fingerprint", "全局 TLS 指纹", "字符串", "代理未单独设置时使用的客户端指纹。", general),
		field("global-ua", "全局 User-Agent", "字符串", "外部请求的全局 User-Agent。", general),
		field("etag-support", "ETag 支持", "布尔", "控制资源更新请求的 ETag 行为。", general),
		field("keep-alive-idle", "Keepalive 空闲时间", "整数", "TCP keepalive 空闲时间。", general),
		field("keep-alive-interval", "Keepalive 间隔", "整数", "TCP keepalive 探测间隔。", general),
		field("disable-keep-alive", "禁用 Keepalive", "布尔", "全局关闭 TCP keepalive。", general),
		field("proxy-providers", "代理集合提供者", "映射", "可复用的代理集合来源。", "https://wiki.metacubex.one/config/proxy-providers/"),
		field("rule-providers", "规则集合提供者", "映射", "可复用的规则集合来源。", "https://wiki.metacubex.one/config/rule-providers/"),
		field("proxies", "代理节点", "对象数组", "全部官方代理类型均在右侧目录列出。", "https://wiki.metacubex.one/config/proxies/"),
		field("proxy-groups", "策略组", "对象数组", "选择、测速、负载均衡等策略组。", "https://wiki.metacubex.one/config/proxy-groups/"),
		field("rules", "路由规则", "字符串数组", "按顺序匹配的主规则列表。", "https://wiki.metacubex.one/config/rules/"),
		field("sub-rules", "子规则", "映射", "可由主规则跳转的子规则集合。", "https://wiki.metacubex.one/config/sub-rule/"),
		field("listeners", "自定义监听器", "对象数组", "全部官方监听器类型均在右侧目录列出。", "https://wiki.metacubex.one/config/inbound/listeners/"),
		field("hosts", "静态 Hosts", "映射", "域名到地址或别名的静态映射。", "https://wiki.metacubex.one/config/dns/hosts/"),
		field("dns", "DNS", "对象", "DNS、Fake-IP、上游与策略配置。", "https://wiki.metacubex.one/config/dns/"),
		field("ntp", "NTP", "对象", "时间同步配置。", "https://wiki.metacubex.one/config/ntp/"),
		field("tun", "TUN", "对象", "系统 TUN 入站与路由配置。", "https://wiki.metacubex.one/config/inbound/tun/"),
		field("tuic-server", "TUIC 服务端", "对象", "内置 TUIC 服务端入口。", inbound),
		field("iptables", "iptables", "对象", "Linux iptables 集成配置。", inbound),
		field("experimental", "实验功能", "对象", "Mihomo 实验功能开关。", "https://wiki.metacubex.one/config/experimental/"),
		field("profile", "持久化档案", "对象", "选中策略与 Fake-IP 持久化。", general),
		field("geox-url", "Geo 数据地址", "对象", "GeoIP、GeoSite、MMDB 等资源地址。", general),
		field("sniffer", "流量嗅探", "对象", "协议嗅探与目标覆盖配置。", "https://wiki.metacubex.one/config/sniff/"),
		field("tls", "TLS 服务端", "对象", "控制器等服务使用的证书与客户端认证配置。", general),
		field("clash-for-android", "Clash for Android 兼容项", "对象", "Android 兼容扩展配置。", general),
	}
}

const mihomoTopics = `
experimental general sub-rule tunnels dns dns/diagram dns/hosts dns/type inbound inbound/port inbound/tun inbound/listeners
inbound/listeners/anytls inbound/listeners/http inbound/listeners/hysteria2-realm inbound/listeners/hysteria2 inbound/listeners/mieru inbound/listeners/mixed inbound/listeners/redirect inbound/listeners/shadowquic inbound/listeners/snell inbound/listeners/socks inbound/listeners/ss inbound/listeners/sudoku inbound/listeners/tproxy inbound/listeners/trojan inbound/listeners/trusttunnel inbound/listeners/tuic-v4 inbound/listeners/tuic-v5 inbound/listeners/tun inbound/listeners/tunnel inbound/listeners/vless inbound/listeners/vmess
ntp proxies proxies/anytls proxies/built-in proxies/dialer-proxy proxies/direct proxies/dns proxies/http proxies/hysteria proxies/hysteria2 proxies/masque proxies/mieru proxies/openvpn proxies/rematch proxies/shadowquic proxies/snell proxies/socks proxies/ss proxies/ssh proxies/ssr proxies/sudoku proxies/tailscale proxies/tls proxies/transport proxies/trojan proxies/trusttunnel proxies/tuic proxies/vless proxies/vmess proxies/wg
proxy-groups proxy-groups/built-in proxy-groups/fallback proxy-groups/load-balance proxy-groups/relay proxy-groups/select proxy-groups/url-test
proxy-providers proxy-providers/content rule-providers rule-providers/content rules sniff
`
