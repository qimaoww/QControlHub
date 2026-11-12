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
				if parsed.SourceOnly {
					t.Fatalf("published preset became source-only: %s", protocol.Key)
				}
			})
		}
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
