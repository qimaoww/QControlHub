package store

import (
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientConnectionLogParser(t *testing.T) {
	for _, tt := range []struct {
		name                                      string
		engine                                    core.Engine
		message, ip, transport, protocol, inbound string
	}{
		{"xray", core.EngineXray, "2026/09/21 12:00:00 from tcp:198.51.100.9:50123 accepted tcp:8.8.8.8:443 [entry -> direct] email: ignored", "198.51.100.9", "tcp", "", "entry"},
		{"xray unknown transport", core.EngineXray, "from [2001:db8::9]:50123 accepted udp:8.8.8.8:53", "2001:db8::9", "", "", ""},
		{"xray older access", core.EngineXray, "198.51.100.9:50123 accepted tcp:example.invalid:443 [entry >> direct]", "198.51.100.9", "", "", "entry"},
		{"sing tcp", core.EngineSingBox, "INFO [123 0ms] inbound/vless[entry]: inbound connection from 198.51.100.9:50123", "198.51.100.9", "tcp", "vless", "entry"},
		{"sing udp", core.EngineSingBox, "INFO inbound/hysteria2[entry]: inbound packet connection from [2001:db8::9]:50123", "2001:db8::9", "udp", "hysteria2", "entry"},
		{"sing authenticated udp", core.EngineSingBox, "+0800 2026-09-21 12:00:00 INFO [123 1m2s] inbound/shadowsocks[entry]: [user-a] inbound packet connection from [::ffff:198.51.100.9]:50123", "198.51.100.9", "udp", "shadowsocks", "entry"},
		{"sing relative timestamp", core.EngineSingBox, "INFO[0001] [123 0ms] inbound/naive[entry]: [user-a] inbound connection from 198.51.100.9:50123", "198.51.100.9", "tcp", "naive", "entry"},
		{"mihomo", core.EngineMihomo, "time=now level=info msg=\"[TCP] 198.51.100.9:50123 --> 8.8.8.8:443 match Match using DIRECT\"", "198.51.100.9", "tcp", "", ""},
		{"mihomo json", core.EngineMihomo, `{"level":"info","message":"[UDP] [2001:db8::9]:50123 --> 8.8.8.8:53 using DIRECT"}`, "2001:db8::9", "udp", "", ""},
		{"mihomo process", core.EngineMihomo, "[TCP] 198.51.100.9:50123(curl, uid=1000) --> example.invalid:443 using DIRECT", "198.51.100.9", "tcp", "", ""},
		{"mihomo formatted", core.EngineMihomo, `time="2026-09-21T12:00:00+08:00" level=info msg="[TCP] 198.51.100.9:50123 --> 8.8.8.8:443 using DIRECT"`, "198.51.100.9", "tcp", "", ""},
		{"ss tcp", core.EngineShadowsocksRust, "DEBUG established tcp tunnel [::ffff:198.51.100.9]:50123 <-> 8.8.8.8:443 with options", "198.51.100.9", "tcp", "shadowsocks", ""},
		{"ss udp", core.EngineShadowsocksRust, "DEBUG created udp association for [2001:db8::9]:50123 with session 123", "2001:db8::9", "udp", "shadowsocks", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, ok := clientConnectionFromLog(core.CoreLogEntry{Engine: tt.engine, Message: tt.message})
			if !ok || c.ClientIP != tt.ip || c.ClientPort != 50123 || c.Transport != tt.transport || c.Protocol != tt.protocol || c.Inbound != tt.inbound || c.LocalPort != 0 {
				t.Fatalf("connection=%+v parsed=%v", c, ok)
			}
		})
	}
}

func TestClientConnectionLogParserRejectsNonClientAddresses(t *testing.T) {
	for _, tt := range []struct {
		engine  core.Engine
		message string
	}{
		{core.EngineSingBox, "outbound/direct[direct]: outbound connection to 8.8.8.8:443"},
		{core.EngineSingBox, "inbound/vless[entry]: inbound connection to 8.8.8.8:443"},
		{core.EngineSingBox, "INFO [123 0ms] inbound/shadowsocks[entry]: [alice] inbound packet connection to 8.8.8.8:443"},
		{core.EngineSingBox, "ERROR outbound/direct[direct]: dial failed: inbound/vless[entry]: inbound connection from 8.8.8.8:443"},
		{core.EngineSingBox, "inbound/vless[entry]: inbound connection to example.invalid:443 inbound/vless[fake]: inbound connection from 8.8.8.8:443"},
		{core.EngineXray, "from invalid accepted tcp:8.8.8.8:443"},
		{core.EngineXray, "from 198.51.100.9:1 rejected tcp:8.8.8.8:443"},
		{core.EngineXray, "from 0.0.0.0:1 accepted tcp:8.8.8.8:443"},
		{core.EngineXray, "from [::ffff:0.0.0.0]:1 accepted tcp:8.8.8.8:443"},
		{core.EngineXray, "from [ff02::1]:1 accepted tcp:8.8.8.8:443"},
		{core.EngineXray, "from [fe80::1%eth0]:1 accepted tcp:8.8.8.8:443"},
		{core.EngineXray, "from 198.51.100.9:0 accepted tcp:8.8.8.8:443"},
		{core.EngineMihomo, "[DNS] 198.51.100.9 --> 8.8.8.8"},
		{core.EngineMihomo, "[TCP] dial DIRECT 198.51.100.9:123 --> 8.8.8.8:443 error: timeout"},
		{core.EngineMihomo, "[TCP] Mihomo --> 8.8.8.8:443 using DIRECT"},
		{core.EngineMihomo, "[TCP] Mihomo --> example.invalid:443 using [TCP] 8.8.8.8:443 --> 9.9.9.9:443 using DIRECT"},
		{core.EngineMihomo, `time=now level=warning msg="dial failed: [TCP] 8.8.8.8:443 --> 9.9.9.9:443 using DIRECT"`},
		{core.EngineShadowsocksRust, "DEBUG udp relay 198.51.100.9:123 <- 8.8.8.8:53 received 100 bytes"},
		{core.EngineShadowsocksRust, "DEBUG established tcp tunnel hostname:123 <-> 8.8.8.8:443"},
	} {
		if c, ok := clientConnectionFromLog(core.CoreLogEntry{Engine: tt.engine, Message: tt.message}); ok {
			t.Fatalf("parsed non-client message %q: %+v", tt.message, c)
		}
	}
}
