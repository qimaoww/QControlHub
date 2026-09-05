package configschema

func shadowsocksRustFields() []Field {
	const general = "https://github.com/shadowsocks/shadowsocks-rust/blob/v1.25.0/crates/shadowsocks-service/src/config.rs"
	fields := []Field{
		field("server", "监听地址", "字符串", "服务端绑定的 IP 地址或主机名。", general),
		field("server_port", "监听端口", "整数", "服务端监听的 TCP/UDP 端口。", general),
		field("password", "密码", "字符串", "Shadowsocks 服务端密码。", general),
		field("method", "加密方法", "字符串", "标准 AEAD 或 AEAD-2022 加密方法。", general),
		field("mode", "转发模式", "字符串", "可选 tcp_only、udp_only 或 tcp_and_udp。", general),
		field("timeout", "TCP 超时", "整数", "全局 TCP 超时，单位为秒。v1.25.0 实际使用顶层 timeout，不使用 servers[].timeout。", general),
		field("udp_timeout", "UDP 超时", "整数", "UDP 关联空闲超时时间，单位为秒。", general),
		field("fast_open", "TCP Fast Open", "布尔", "启用 TCP Fast Open。", general),
		field("no_delay", "TCP_NODELAY", "布尔", "启用 TCP_NODELAY。", general),
		field("keep_alive", "TCP Keepalive", "整数", "TCP keepalive 超时时间，单位为秒。", general),
		field("servers", "多服务端配置", "对象数组", "Shadowsocks Rust 扩展格式，可同时运行多个服务端。", general),
		field("dns", "DNS", "字符串或对象", "全局 DNS，影响所有端口；官方 SS Rust 不支持 servers[].dns。", general),
		field("outbound_bind_addr", "出站绑定地址", "字符串", "出站使用的本机 IP；servers[].outbound_bind_addr 可逐端口覆盖。", general),
		field("ipv6_first", "IPv6 优先", "布尔", "全局优先使用 IPv6 地址，默认 false。", general),
		field("security", "安全策略", "对象", "重放攻击检测等安全策略。", general),
		field("acl", "访问控制", "字符串", "端口 ACL 优先。删除端口 ACL 后使用启动 --acl；未指定启动 --acl 时才使用顶层 acl。新版 QAgent 模板指定固定的 qch-mainland-block.acl，顶层 acl 不会覆盖它。不新增或下载 CN 域名规则。", general),
		field("outbound_bind_interface", "出站网卡", "字符串", "出站使用的本机网卡；端口设置覆盖全局值。", general),
		field("outbound_fwmark", "出站标记", "整数", "Linux / Android 出站 SO_MARK；端口设置覆盖全局值。", general),
		field("outbound_udp_allow_fragmentation", "出站 UDP 分片", "布尔", "允许出站 UDP 分片；端口设置覆盖全局值。", general),
		field("inbound_udp_allow_fragmentation", "入站 UDP 分片", "布尔", "允许入站 UDP 分片；端口设置覆盖全局值。", general),
		field("outbound_proxy", "上游代理", "字符串或字符串数组", "端口非空代理链优先，否则继承全局链；HTTP(S) 跳点不转发 UDP，UDP 将直连。需要内核版本支持。", general),
		field("plugin", "SIP003 插件", "字符串", "仅当前端口使用的插件程序；需节点已安装且服务允许执行。", general),
		field("plugin_opts", "插件选项", "字符串", "仅当前端口的插件选项。", general),
		field("plugin_args", "插件参数", "字符串数组", "仅当前端口的插件启动参数。", general),
		field("plugin_mode", "插件转发模式", "字符串", "仅当前端口的插件模式：tcp_only、udp_only 或 tcp_and_udp。", general),
	}
	for i := range fields {
		fields[i].Scope = "global"
		switch fields[i].Key {
		case "server", "server_port", "password", "method", "plugin", "plugin_opts", "plugin_args", "plugin_mode":
			fields[i].Scope = "inbound"
		case "mode", "acl", "outbound_bind_addr", "outbound_bind_interface", "outbound_fwmark", "outbound_udp_allow_fragmentation", "inbound_udp_allow_fragmentation", "outbound_proxy":
			fields[i].Scope = "override"
		case "servers":
			fields[i].Scope = "structure"
		}
	}
	return fields
}

const shadowsocksRustTopics = `
configs.html quick-guide.html sip002.html sip003.html
`
