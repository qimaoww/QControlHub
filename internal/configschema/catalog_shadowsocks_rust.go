package configschema

func shadowsocksRustFields() []Field {
	const general = "https://github.com/shadowsocks/shadowsocks-rust/blob/master/crates/shadowsocks-service/src/config.rs"
	return []Field{
		field("server", "监听地址", "字符串", "服务端绑定的 IP 地址或主机名。", general),
		field("server_port", "监听端口", "整数", "服务端监听的 TCP/UDP 端口。", general),
		field("password", "密码", "字符串", "Shadowsocks 服务端密码。", general),
		field("method", "加密方法", "字符串", "标准 AEAD 或 AEAD-2022 加密方法。", general),
		field("mode", "转发模式", "字符串", "可选 tcp_only、udp_only 或 tcp_and_udp。", general),
		field("timeout", "TCP 超时", "整数", "TCP 中继空闲超时时间，单位为秒。", general),
		field("udp_timeout", "UDP 超时", "整数", "UDP 关联空闲超时时间，单位为秒。", general),
		field("fast_open", "TCP Fast Open", "布尔", "启用 TCP Fast Open。", general),
		field("no_delay", "TCP_NODELAY", "布尔", "启用 TCP_NODELAY。", general),
		field("keep_alive", "TCP Keepalive", "整数", "TCP keepalive 超时时间，单位为秒。", general),
		field("servers", "多服务端配置", "对象数组", "Shadowsocks Rust 扩展格式，可同时运行多个服务端。", general),
		field("dns", "DNS", "字符串或对象", "全局 DNS；servers[].dns 可为每个端口单独指定。", general),
		field("outbound_bind_addr", "出站绑定地址", "字符串", "出站使用的本机 IP；servers[].outbound_bind_addr 可逐端口覆盖。", general),
		field("ipv6_first", "IPv6 优先", "布尔", "全局优先使用 IPv6 地址，默认 false。", general),
		field("security", "安全策略", "对象", "重放攻击检测等安全策略。", general),
		field("acl", "访问控制", "字符串", "ACL 文件路径。", general),
	}
}

const shadowsocksRustTopics = `
configs.html quick-guide.html sip002.html sip003.html
`
