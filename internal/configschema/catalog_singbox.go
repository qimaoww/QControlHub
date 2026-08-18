package configschema

func singBoxFields() []Field {
	base := "https://sing-box.sagernet.org/configuration/"
	return []Field{
		field("$schema", "JSON Schema", "字符串", "编辑器使用的官方 JSON Schema 地址。", base+"schema/"),
		field("log", "日志", "对象", "日志级别、输出和时间戳。", base+"log/"),
		field("dns", "DNS", "对象", "DNS 服务器、规则、Fake-IP 与缓存。", base+"dns/"),
		field("ntp", "NTP", "对象", "网络时间同步配置。", base+"ntp/"),
		field("certificate", "全局证书", "对象", "系统/浏览器证书存储及自定义证书。", base+"certificate/"),
		field("certificate_providers", "证书提供者", "对象数组", "ACME、Tailscale 等动态证书来源。", base+"shared/certificate-provider/"),
		field("http_clients", "HTTP 客户端", "对象数组", "可复用的 HTTP 客户端。", base+"shared/http-client/"),
		field("network_namespaces", "网络命名空间", "对象数组", "默认或 unshare 网络命名空间。", base+"network-namespace/"),
		field("endpoints", "网络端点", "对象数组", "WireGuard、Tailscale、OpenVPN 等端点。", base+"endpoint/"),
		field("inbounds", "入站连接", "对象数组", "全部官方入站协议均在右侧目录列出。", base+"inbound/"),
		field("outbounds", "出站连接", "对象数组", "全部官方出站协议均在右侧目录列出。", base+"outbound/"),
		field("route", "路由", "对象", "路由规则、规则集与自动探测。", base+"route/"),
		field("services", "服务", "对象数组", "API、DERP、Resolved 等内置服务。", base+"service/"),
		field("experimental", "实验功能", "对象", "Clash API、V2Ray API 与缓存等实验功能。", base+"experimental/"),
	}
}

const singBoxTopics = `
schema certificate dns dns/fakeip dns/rule dns/rule_action dns/server dns/server/dhcp dns/server/fakeip dns/server/hosts dns/server/http3 dns/server/https dns/server/legacy dns/server/local dns/server/mdns dns/server/openconnect dns/server/openvpn dns/server/quic dns/server/resolved dns/server/tailscale dns/server/tcp dns/server/tls dns/server/udp
endpoint endpoint/openconnect endpoint/openvpn-client endpoint/openvpn-server endpoint/tailscale endpoint/wireguard
experimental experimental/cache-file experimental/clash-api experimental/v2ray-api
inbound inbound/anytls inbound/cloudflared inbound/direct inbound/http inbound/hysteria inbound/hysteria2 inbound/mixed inbound/naive inbound/redirect inbound/shadowsocks inbound/shadowtls inbound/snell inbound/socks inbound/tproxy inbound/trojan inbound/tuic inbound/tun inbound/vless inbound/vmess
log network-namespace network-namespace/default network-namespace/unshare ntp
outbound outbound/anytls outbound/block outbound/bridge outbound/direct outbound/dns outbound/http outbound/hysteria outbound/hysteria2 outbound/naive outbound/selector outbound/shadowsocks outbound/shadowtls outbound/snell outbound/socks outbound/ssh outbound/tor outbound/trojan outbound/tuic outbound/urltest outbound/vless outbound/vmess outbound/wireguard
route route/geoip route/geosite route/rule route/rule_action route/sniff
rule-set rule-set/adguard rule-set/headless-rule rule-set/source-format
service service/api service/ccm service/derp service/hysteria-realm service/ocm service/resolved service/ssm-api service/usbip-client service/usbip-server
shared/dial shared/dns01_challenge shared/http-client shared/http2 shared/listen shared/multiplex shared/neighbor shared/pre-match shared/quic shared/tcp-brutal shared/tls shared/udp-nat shared/udp-over-tcp shared/v2ray-transport shared/wifi-state shared/certificate-provider shared/certificate-provider/acme shared/certificate-provider/cloudflare-origin-ca shared/certificate-provider/tailscale
`
