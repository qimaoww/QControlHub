package configschema

func xrayFields() []Field {
	base := "https://xtls.github.io/config/"
	return []Field{
		field("env", "环境变量", "对象", "Xray 环境变量配置。", base+"env.html"),
		field("log", "日志", "对象", "访问日志、错误日志与级别。", base+"log.html"),
		field("api", "API", "对象", "供内部入站调用的 gRPC API。", base+"api.html"),
		field("dns", "DNS", "对象", "内置 DNS 服务器与查询策略。", base+"dns.html"),
		field("routing", "路由", "对象", "域名/IP/端口等路由规则。", base+"routing.html"),
		field("policy", "本地策略", "对象", "连接超时、统计等本地策略。", base+"policy.html"),
		field("inbounds", "入站连接", "对象数组", "全部官方入站协议均在右侧目录列出。", base+"inbound.html"),
		field("outbounds", "出站连接", "对象数组", "全部官方出站协议均在右侧目录列出。", base+"outbound.html"),
		field("stats", "统计", "对象", "启用内置统计模块。", base+"stats.html"),
		field("fakedns", "FakeDNS", "对象数组", "FakeDNS 地址池配置。", base+"fakedns.html"),
		field("metrics", "Metrics", "对象", "指标与调试服务监听配置。", base+"metrics.html"),
		field("observatory", "连接观测", "对象", "后台出站连接质量探测。", base+"observatory.html"),
		field("burstObservatory", "并发连接观测", "对象", "并发式出站连接质量探测。", base+"observatory.html"),
		field("reverse", "反向代理", "对象", "Bridge/Portal 反向代理配置。", base+"reverse.html"),
		field("transport", "全局传输", "对象", "全局传输层参数。", base+"transport.html"),
		field("geodata", "GeoData", "对象", "Geo 数据加载与匹配设置。", base+"geodata.html"),
		field("version", "版本约束", "对象", "声明配置适用的 Xray-core 版本范围。", base),
	}
}

const xrayTopics = `
api.html dns.html env.html fakedns.html features features/browser_dialer.html features/env.html features/fallback.html features/multiple.html features/xtls.html geodata.html inbound.html
inbounds inbounds/dokodemo.html inbounds/http.html inbounds/hysteria.html inbounds/shadowsocks.html inbounds/socks.html inbounds/trojan.html inbounds/tun.html inbounds/tunnel.html inbounds/vless.html inbounds/vmess.html inbounds/wireguard.html
log.html metrics.html observatory.html outbound.html
outbounds outbounds/blackhole.html outbounds/dns.html outbounds/freedom.html outbounds/http.html outbounds/hysteria.html outbounds/loopback.html outbounds/shadowsocks.html outbounds/socks.html outbounds/trojan.html outbounds/vless.html outbounds/vmess.html outbounds/wireguard.html
policy.html reverse.html routing.html stats.html transport.html
transports transports/finalmask.html transports/grpc.html transports/h2.html transports/http.html transports/httpupgrade.html transports/hysteria.html transports/mkcp.html transports/quic.html transports/raw.html transports/reality.html transports/sockopt.html transports/splithttp.html transports/tcp.html transports/tls.html transports/websocket.html transports/xhttp.html
`
