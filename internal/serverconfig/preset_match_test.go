package serverconfig

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestManualPresetSemanticBoundaries(t *testing.T) {
	const user = `{"id":"123e4567-e89b-42d3-a456-426614174000","flow":"xtls-rprx-vision"}`
	const reality = `"security":"reality","realitySettings":{"privateKey":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE","serverNames":["example.test"],"shortIds":["ab"],"target":"example.test:443"}`
	xray := `{"inbounds":[{"tag":"manual","listen":"0.0.0.0","port":21001,"protocol":"vless","settings":{"decryption":"none","clients":[` + user + `]},"streamSettings":{"network":"raw",` + reality + `}}]}`
	sing := `{"inbounds":[{"tag":"manual","listen":"::","listen_port":21001,"type":"vless","users":[{"uuid":"123e4567-e89b-42d3-a456-426614174000","flow":"xtls-rprx-vision"}],"tls":{"enabled":true,"server_name":"example.test","reality":{"enabled":true,"private_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE","short_id":["ab"],"handshake":{"server":"example.test","server_port":443}}}}]}`
	mihomo := `listeners: [{name: manual, type: vless, listen: '::', port: 21001, users: [{uuid: 123e4567-e89b-42d3-a456-426614174000, flow: xtls-rprx-vision}], reality-config: {private-key: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE, server-names: [example.test], short-id: [ab], dest: 'example.test:443'}}]`
	tests := []struct {
		name     string
		engine   core.Engine
		source   string
		matched  bool
		protocol string
	}{
		{"xray vision", core.EngineXray, xray, true, ProtocolVLESS},
		{"xray no flow", core.EngineXray, strings.Replace(xray, `,"flow":"xtls-rprx-vision"`, "", 1), false, ProtocolVLESS},
		{"xray plain", core.EngineXray, strings.Replace(xray, reality, `"security":"none"`, 1), false, ProtocolVLESS},
		{"xray websocket", core.EngineXray, strings.Replace(xray, `"network":"raw"`, `"network":"ws","wsSettings":{"path":"/manual"}`, 1), false, ProtocolVLESS},
		{"sing vision", core.EngineSingBox, sing, true, ProtocolVLESS},
		{"sing no flow", core.EngineSingBox, strings.Replace(sing, `,"flow":"xtls-rprx-vision"`, "", 1), false, ProtocolVLESS},
		{"sing explicitly disabled TLS", core.EngineSingBox, strings.Replace(sing, `"enabled":true`, `"enabled":false`, 1), false, ProtocolVLESS},
		{"mihomo vision", core.EngineMihomo, mihomo, true, ProtocolVLESS},
		{"mihomo no flow", core.EngineMihomo, strings.Replace(mihomo, ", flow: xtls-rprx-vision", "", 1), false, ProtocolVLESS},
		{"xray traditional SS", core.EngineXray, `{"inbounds":[{"tag":"manual","port":21001,"protocol":"shadowsocks","settings":{"method":"aes-256-gcm","password":"manual-password"}}]}`, false, ProtocolSS2022},
		{"sing traditional SS", core.EngineSingBox, `{"inbounds":[{"tag":"manual","listen_port":21001,"type":"shadowsocks","method":"aes-256-gcm","password":"manual-password"}]}`, true, ProtocolShadowsocks},
		{"mihomo traditional SS", core.EngineMihomo, `listeners: [{name: manual, port: 21001, type: shadowsocks, cipher: aes-256-gcm, password: manual-password}]`, false, ProtocolSS2022},
		{"sing TLS WS", core.EngineSingBox, `{"inbounds":[{"tag":"manual","listen_port":21001,"type":"vless","users":[{"uuid":"123e4567-e89b-42d3-a456-426614174000"}],"transport":{"type":"ws","path":"/manual"},"tls":{"enabled":true,"certificate_path":"/tmp/test.crt","key_path":"/tmp/test.key"}}]}`, true, "vless-ws-tls"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Parse(tc.engine, tc.source)
			if !ok || got.SourceOnly == tc.matched || got.Protocol != tc.protocol {
				t.Fatalf("projection = %+v, %v", got, ok)
			}
			for i := 0; i < 20; i++ {
				again, ok := Parse(tc.engine, tc.source)
				if !ok || !reflect.DeepEqual(got, again) {
					t.Fatal("unstable projection")
				}
			}
			if !tc.matched {
				if _, err := Generate(tc.engine, got); err == nil {
					t.Fatal("unmatched native projection generated a preset")
				}
				if _, _, err := InboundSource(tc.engine, tc.source, "manual"); err != nil {
					t.Fatal("source editor unavailable", err)
				}
			}
		})
	}
}

func TestParseContinuesAfterInvalidRecognizedInbound(t *testing.T) {
	source := `{"inbounds":[{"tag":"missing-account","protocol":"vmess","port":21001},{"tag":"unknown-transport","protocol":"vmess","port":21002,"settings":{"clients":[{"id":"test"}]},"streamSettings":{"network":"future"}},{"tag":"valid","protocol":"vmess","port":21003,"settings":{"clients":[{"id":"test"}]}}]}`
	got, ok := Parse(core.EngineXray, source)
	if !ok || got.Tag != "valid" || got.SourceOnly {
		t.Fatalf("parse stopped at invalid entry: %+v", got)
	}
	if len(ParseAll(core.EngineXray, source)) != 1 {
		t.Fatal("invalid entry became a preset")
	}
	for _, source := range []string{"", "{", `{"inbounds":null}`} {
		if _, ok := Parse(core.EngineXray, source); ok {
			t.Fatal("invalid source recognized")
		}
	}
}

func TestMihomoManualMapAccountsHaveStableSelection(t *testing.T) {
	source := `listeners: [{name: manual, type: hysteria2, port: 21001, users: {z-last: last-password, a-first: first-password}, certificate: /tmp/test.crt, private-key: /tmp/test.key}]`
	for i := 0; i < 100; i++ {
		got, ok := Parse(core.EngineMihomo, source)
		if !ok || got.SourceOnly || got.Username != "a-first" || got.Credential != "first-password" {
			t.Fatalf("unstable account: %+v", got)
		}
	}
}

func TestXrayActiveClientsOverrideHistoricalUsers(t *testing.T) {
	source := `{"inbounds":[{"tag":"manual","port":21001,"protocol":"vmess","settings":{"clients":[{"id":"123e4567-e89b-42d3-a456-426614174000"}],"users":[{"id":"inactive"}]}}]}`
	got, ok := Parse(core.EngineXray, source)
	if !ok || got.Credential != "123e4567-e89b-42d3-a456-426614174000" {
		t.Fatal("historical inactive users overrode clients")
	}
	source = strings.Replace(source, `"clients":[{"id":"123e4567-e89b-42d3-a456-426614174000"}]`, `"clients":[]`, 1)
	if _, ok := Parse(core.EngineXray, source); ok {
		t.Fatal("empty active clients borrowed an inactive account")
	}
}

func TestPublishedPresetsRemainEditableAfterBoundMetadata(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo, core.EngineShadowsocksRust} {
		for _, protocol := range Protocols(engine) {
			t.Run(string(engine)+"/"+protocol.Key, func(t *testing.T) {
				input, err := nativeTLSFixturePlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				source, err := Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				metadata, err := MarshalClientMetadata(input)
				if err != nil {
					t.Fatal(err)
				}
				parsed, ok := Parse(engine, source)
				if !ok {
					t.Fatal("generated preset lost its native projection")
				}
				if err := ApplyClientMetadata(&parsed, metadata); err != nil {
					t.Fatal(err)
				}
				if parsed.SourceOnly || !CanEditNativePreset(engine, parsed) {
					t.Fatalf("published preset became source-only: %s", protocol.Key)
				}
			})
		}
	}
}

func TestHandwrittenNativePresetRecognition(t *testing.T) {
	for _, tc := range []struct {
		name     string
		engine   core.Engine
		source   string
		protocol string
		listen   string
	}{
		{"Xray quoted port and implicit listen", core.EngineXray,
			`{"inbounds":[{"tag":"edge","port":"21001","protocol":"vmess","settings":{"clients":[{"id":"123e4567-e89b-42d3-a456-426614174001"}]}}]}`,
			ProtocolVMess, "0.0.0.0"},
		{"sing-box explicitly disabled TLS", core.EngineSingBox,
			`{"inbounds":[{"tag":"edge","listen":"::1","listen_port":21001,"type":"vmess","users":[{"uuid":"123e4567-e89b-42d3-a456-426614174001"}],"transport":{"type":"ws","path":"/native"},"tls":{"enabled":false,"min_version":"1.3","reality":{"enabled":false}}}]}`,
			"vmess-ws", "::1"},
		{"Mihomo Snell v6 unshaped", core.EngineMihomo,
			"listeners:\n  - {type: snell, name: edge, port: 21001, version: 6, mode: unshaped, psk: fixture-snell-v6, udp: false}\n",
			ProtocolSnellV6Unshaped, "0.0.0.0"},
		{"SS Rust singleton", core.EngineShadowsocksRust,
			`{"server":"::","server_port":21001,"method":"aes-128-gcm","password":"only-for-this-test","no_delay":false}`,
			ProtocolShadowsocks, "::"},
		{"SS Rust server array", core.EngineShadowsocksRust,
			`{"shadowsocks":[{"name":"edge","address":"127.0.0.1","port":21001,"method":"chacha20-ietf-poly1305","password":"only-for-this-test","no_delay":false}]}`,
			ProtocolShadowsocks, "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Parse(tc.engine, tc.source)
			if !ok || !CanEditNativePreset(tc.engine, got) || got.Protocol != tc.protocol || got.Port != 21001 || got.Listen != tc.listen {
				t.Fatal("native parameters did not match the existing preset")
			}
			if (tc.engine == core.EngineSingBox || tc.engine == core.EngineXray) && got.Username != "" {
				t.Fatal("empty native username was replaced")
			}
			if got.TLSEnabled || got.RealityEnabled || got.SnellUDP {
				t.Fatal("inactive features were enabled")
			}
		})
	}
}

func TestNativeInboundInventoryDoesNotRequireProjectionOrPolicy(t *testing.T) {
	const source = `{"api":{"tag":"stats","services":["StatsService"]},
		"routing":{"rules":[{"type":"field","inboundTag":["internal"],"outboundTag":"stats"}]},
		"inbounds":[
			{"tag":"dup","protocol":"http","listen":"127.0.0.1","port":21001},
			{"tag":"dup","protocol":"socks","listen":"::1","port":21001},
			{"protocol":"http","listen":"127.0.0.1","port":21002},
			{"tag":"range","protocol":"http","port":"21020-21021"},
			{"tag":"socket","protocol":"vmess","listen":"/tmp/example.sock"},
			{"tag":"internal","protocol":"dokodemo-door","listen":"127.0.0.1","port":10085},
			{"tag":"qch-stat-api","protocol":"dokodemo-door","port":10086},
			{"tag":"qch-trf-custom","protocol":"http","port":21004},
			{"tag":"qch-stat-custom","protocol":"http","port":21005}
		],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`
	rows, err := DiscoverNativeInbounds(core.EngineXray, source)
	if err != nil || len(rows) != 7 {
		t.Fatalf("native row enumeration: %d, %v", len(rows), err)
	}
	if rows[0].TargetTag != "" || rows[1].TargetTag != "" || rows[0].Listen != "127.0.0.1" || rows[1].Listen != "::1" ||
		rows[2].Tag != "" || rows[2].Port != 21002 || rows[2].TargetTag != "" || rows[3].Port != 0 || rows[4].Port != 0 || rows[4].Listen != "/tmp/example.sock" {
		t.Fatal("native rows lost display identity or acquired an ambiguous target")
	}
	if rows[5].Tag != "qch-trf-custom" || rows[6].Tag != "qch-stat-custom" {
		t.Fatal("user inbounds were mistaken for generated outbounds")
	}
	for _, tc := range []struct {
		engine core.Engine
		source string
	}{
		{core.EngineXray, `{"inbounds":[`},
		{core.EngineXray, `{"inbounds":[]} false`},
		{core.EngineMihomo, "listeners: [\n"},
		{core.EngineMihomo, "listeners: []\n---\ninvalid: [\n"},
	} {
		if rows, err := DiscoverNativeInbounds(tc.engine, tc.source); err == nil || rows != nil {
			t.Fatal("damaged document was rendered as empty or custom")
		}
	}
}

func TestNativeXrayInventoryRecognizesCompositeXHTTPWithoutGuessingTLS(t *testing.T) {
	source := `{"inbounds":[
		{"protocol":"vless","tag":"edge","listen":"127.0.0.1","port":21001,"settings":{
			"clients":[{"id":"123e4567-e89b-42d3-a456-426614174001","flow":"xtls-rprx-vision"}],"decryption":"none","fallbacks":[{"dest":"127.0.0.1:21002","xver":0}]},
			"streamSettings":{"network":"tcp","security":"reality","realitySettings":{"target":"127.0.0.1:21003","serverNames":["reality.example.test"],"shortIds":["ab"],"privateKey":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE"}}},
		{"protocol":"vless","tag":"edge.xh","listen":"127.0.0.1","port":"21002","settings":{"decryption":"none","clients":[{"id":"123e4567-e89b-42d3-a456-426614174001"}]},
			"streamSettings":{"network":"splithttp","splithttpSettings":{"path":"/native","mode":"stream-up"}}},
		{"protocol":"vmess","tag":"other","port":21004,"settings":{"clients":[{"id":"123e4567-e89b-42d3-a456-426614174002"}]},"streamSettings":{"network":"kcp"}}
	]}`
	rows, err := DiscoverNativeInbounds(core.EngineXray, source)
	if err != nil || len(rows) != 2 || rows[0].Tag != "edge" || rows[1].Tag != "other" {
		t.Fatal("XHTTP children or unsupported native transport are incorrectly inventoried")
	}
	inputs := ParseAll(core.EngineXray, source)
	if len(inputs) != 1 || inputs[0].Protocol != ProtocolXHTTPFive || inputs[0].XHTTPBackendPort != 21002 || inputs[0].TransportPath != "/native" {
		t.Fatal("native XHTTP group was not restored")
	}
	if CanEditNativePreset(core.EngineXray, inputs[0]) {
		t.Fatal("composite preset invented missing TLS frontend identity")
	}
}

func TestPlainNativeVLESSStillExportsActualClientSemantics(t *testing.T) {
	source := `{"inbounds":[{"tag":"manual","listen":"::","port":21001,"protocol":"vless","settings":{"decryption":"none","clients":[{"id":"123e4567-e89b-42d3-a456-426614174000"}]}}]}`
	parsed, ok := Parse(core.EngineXray, source)
	if !ok || !parsed.SourceOnly {
		t.Fatal("plain VLESS should stay in source editor")
	}
	profile, err := BuildClientProfile(parsed, "192.0.2.1", "")
	if err != nil {
		t.Fatal(err)
	}
	uri, err := url.Parse(profile.URI)
	if err != nil {
		t.Fatal(err)
	}
	query := uri.Query()
	if (query.Get("security") != "" && query.Get("security") != "none") || query.Get("flow") != "" || query.Get("encryption") != "none" || query.Get("type") != "tcp" {
		t.Fatalf("client inherited a preset security default: %s", profile.URI)
	}
}
