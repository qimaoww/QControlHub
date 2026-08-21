package serverconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestGenerateServerConfigurations(t *testing.T) {
	t.Parallel()
	password, err := NewCredential(ProtocolSS2022, "2022-blake3-aes-256-gcm")
	if err != nil {
		t.Fatal(err)
	}
	uuid, err := NewCredential(ProtocolVLESS, "")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		engine core.Engine
		input  Input
		wants  []string
	}{
		{"mihomo ss2022", core.EngineMihomo, Input{Protocol: ProtocolSS2022, Tag: "ss2022-in", Listen: "0.0.0.0", Port: 8388, Username: "default", Credential: password, Method: "2022-blake3-aes-256-gcm", Transport: "raw"}, []string{"listeners:", "shadowsocks", "2022-blake3"}},
		{"xray vless", core.EngineXray, Input{Protocol: ProtocolVLESS, Tag: "vless-in", Listen: "0.0.0.0", Port: 443, Username: "user@example.com", Credential: uuid, Flow: "xtls-rprx-vision", Transport: "raw", TLSEnabled: true, CertificatePath: "/etc/tls/server.crt", PrivateKeyPath: "/etc/tls/server.key"}, []string{`"loglevel": "info"`, `"protocol": "vless"`, `"streamSettings"`, `"certificateFile"`}},
		{"singbox vmess", core.EngineSingBox, Input{Protocol: ProtocolVMess, Tag: "vmess-in", Listen: "::", Port: 443, Username: "default", Credential: uuid, Transport: "websocket", TransportPath: "/vmess", TLSEnabled: true, CertificatePath: "/etc/tls/server.crt", PrivateKeyPath: "/etc/tls/server.key"}, []string{`"type": "vmess"`, `"transport"`, `"inbounds"`}},
		{"ss-rust shadowsocks", core.EngineShadowsocksRust, Input{Protocol: ProtocolShadowsocks, Tag: "ss-rust-in", Listen: "0.0.0.0", Port: 8388, Username: "default", Credential: "correct-horse-battery-staple", Method: "chacha20-ietf-poly1305", Transport: "raw"}, []string{`"server": "0.0.0.0"`, `"server_port": 8388`, `"mode": "tcp_and_udp"`, `"no_delay": true`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err := Generate(test.engine, test.input)
			if err != nil {
				t.Fatal(err)
			}
			if err := core.ValidateConfig(test.engine, output); err != nil {
				t.Fatalf("generated configuration is invalid: %v\n%s", err, output)
			}
			for _, want := range test.wants {
				if !strings.Contains(output, want) {
					t.Errorf("generated configuration does not contain %q:\n%s", want, output)
				}
			}
		})
	}
}

func TestGenerateRejectsBadSS2022KeyLength(t *testing.T) {
	t.Parallel()
	_, err := Generate(core.EngineXray, Input{
		Protocol: ProtocolSS2022, Tag: "ss", Listen: "0.0.0.0", Port: 8388, Username: "default",
		Method: "2022-blake3-aes-256-gcm", Credential: "dG9vLXNob3J0", Transport: "raw",
	})
	if err == nil || !strings.Contains(err.Error(), "32 字节") {
		t.Fatalf("Generate() error = %v", err)
	}
}

func TestGenerateRejectsDisabledTLSForRequiredProtocols(t *testing.T) {
	t.Parallel()
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		protocol, ok := FindProtocol(engine, ProtocolTrojan)
		if !ok || !protocol.RequiresTLS {
			t.Fatalf("%s Trojan plan is not marked as requiring TLS", engine)
		}
		input, err := NewPlan(protocol)
		if err != nil {
			t.Fatal(err)
		}
		input.TLSEnabled = false
		if _, err := Generate(engine, input); err == nil || !strings.Contains(err.Error(), "必须启用 TLS") {
			t.Fatalf("Generate(%s/Trojan) error = %v", engine, err)
		}
	}
}

func TestShadowsocksRustGeneratedConfigurationsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, protocol := range Protocols(core.EngineShadowsocksRust) {
		protocol := protocol
		t.Run(protocol.Key, func(t *testing.T) {
			t.Parallel()
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			content, err := Generate(core.EngineShadowsocksRust, input)
			if err != nil {
				t.Fatal(err)
			}
			parsed, ok := Parse(core.EngineShadowsocksRust, content)
			if !ok || parsed.Protocol != input.Protocol || parsed.Listen != input.Listen || parsed.Port != input.Port || parsed.Credential != input.Credential || parsed.Method != input.Method || parsed.Transport != "raw" {
				t.Fatalf("Parse(ss-rust/%s) = %+v, %v\n%s", protocol.Key, parsed, ok, content)
			}
		})
	}
}

func TestGeneratedConfigurationsRoundTripIntoServerForm(t *testing.T) {
	t.Parallel()
	credential, err := NewCredential(ProtocolVLESS, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		input := Input{
			Protocol: ProtocolVLESS, Tag: "vless-in", Listen: "0.0.0.0", Port: 443,
			Username: "alice", Credential: credential, Flow: "xtls-rprx-vision", Transport: "grpc", TransportPath: "qcontrolhub",
			TLSEnabled: true, CertificatePath: "/etc/tls/server.crt", PrivateKeyPath: "/etc/tls/server.key",
		}
		content, err := Generate(engine, input)
		if err != nil {
			t.Fatalf("Generate(%s): %v", engine, err)
		}
		parsed, ok := Parse(engine, content)
		if !ok || parsed.Protocol != input.Protocol || parsed.Credential != input.Credential || parsed.Transport != input.Transport || parsed.TransportPath != input.TransportPath || !parsed.TLSEnabled {
			t.Errorf("Parse(%s) = %+v, %v", engine, parsed, ok)
		}
	}
}

func TestParseSkipsUnsupportedLeadingInbound(t *testing.T) {
	t.Parallel()
	uuid, err := NewCredential(ProtocolVMess, "")
	if err != nil {
		t.Fatal(err)
	}
	fixtures := map[core.Engine]string{
		core.EngineMihomo:  "listeners:\n  - name: unmanaged\n    type: socks\n    port: 1080\n  - name: managed\n    type: vmess\n    listen: 0.0.0.0\n    port: 443\n    users:\n      - username: alice\n        uuid: " + uuid + "\n",
		core.EngineXray:    `{"inbounds":[{"tag":"unmanaged","protocol":"dokodemo-door","port":1080},{"tag":"managed","protocol":"vmess","listen":"0.0.0.0","port":443,"settings":{"users":[{"email":"alice","id":"` + uuid + `"}]},"streamSettings":{"network":"raw"}}]}`,
		core.EngineSingBox: `{"inbounds":[{"tag":"unmanaged","type":"socks","listen_port":1080},{"tag":"managed","type":"vmess","listen":"0.0.0.0","listen_port":443,"users":[{"name":"alice","uuid":"` + uuid + `"}]}]}`,
	}
	for engine, content := range fixtures {
		parsed, ok := Parse(engine, content)
		if !ok || parsed.Tag != "managed" || parsed.Protocol != ProtocolVMess {
			t.Errorf("Parse(%s) = %+v, %v", engine, parsed, ok)
		}
	}
}

func TestParseAllShadowsocksRustOfficialExtendedConfiguration(t *testing.T) {
	t.Parallel()
	content := `{"servers":[{"server":"::","server_port":26663,"password":"MDEyMzQ1Njc4OWFiY2RlZg==","method":"2022-blake3-aes-128-gcm"},{"server":"127.0.0.1","server_port":26664,"password":"correct-horse-battery-staple","method":"chacha20-ietf-poly1305"}],"timeout":300,"mode":"tcp_and_udp"}`
	inputs := ParseAll(core.EngineShadowsocksRust, content)
	if len(inputs) != 2 {
		t.Fatalf("ParseAll(ss-rust) returned %d entries: %+v", len(inputs), inputs)
	}
	if inputs[0].Tag != "ss-rust-1" || inputs[1].Tag != "ss-rust-2" || inputs[0].Protocol != ProtocolSS2022 || inputs[1].Protocol != ProtocolShadowsocks || inputs[0].Port != 26663 || inputs[1].Port != 26664 {
		t.Fatalf("ParseAll(ss-rust) = %+v", inputs)
	}
	parsed, ok := Parse(core.EngineShadowsocksRust, content)
	if !ok || parsed.Tag != "ss-rust-1" || parsed.Port != 26663 {
		t.Fatalf("Parse(ss-rust) = %+v, %t", parsed, ok)
	}
}

func TestNewPlanRandomizesSensitiveValues(t *testing.T) {
	t.Parallel()
	protocol, ok := FindProtocol(core.EngineMihomo, ProtocolSS2022)
	if !ok {
		t.Fatal("SS2022 protocol not found")
	}
	first, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	if first.Port < 20000 || first.Port > 49151 || second.Port < 20000 || second.Port > 49151 || first.Credential == second.Credential || first.Tag == second.Tag {
		t.Fatalf("plans were not independently randomized: first=%+v second=%+v", first, second)
	}
	if !strings.HasPrefix(first.Username, "qch-") || !strings.HasPrefix(second.Username, "qch-") {
		t.Fatalf("plans use an unexpected username prefix: first=%q second=%q", first.Username, second.Username)
	}
}

func TestEveryPublishedServerPlanGeneratesNativeSyntax(t *testing.T) {
	t.Parallel()
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		for _, protocol := range Protocols(engine) {
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatalf("NewPlan(%s/%s): %v", engine, protocol.Key, err)
			}
			content, err := Generate(engine, input)
			if err != nil {
				t.Fatalf("Generate(%s/%s): %v", engine, protocol.Key, err)
			}
			if err := core.ValidateConfig(engine, content); err != nil {
				t.Errorf("generated %s/%s syntax: %v\n%s", engine, protocol.Key, err, content)
			}
		}
	}
}

func TestVLESSRealityPlansGenerateAndRoundTripKeys(t *testing.T) {
	t.Parallel()
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		protocol, _ := FindProtocol(engine, ProtocolVLESS)
		input, err := NewPlan(protocol)
		if err != nil {
			t.Fatal(err)
		}
		if !input.RealityEnabled || input.RealityPrivateKey == "" || input.RealityPublicKey == "" || len(input.RealityShortID) != 16 {
			t.Fatalf("NewPlan(%s) did not create Reality material: %+v", engine, input)
		}
		if strings.Contains(strings.ToLower(input.RealityServerName), "cloudflare") {
			t.Fatalf("NewPlan(%s) selected a Cloudflare Reality target: %s", engine, input.RealityServerName)
		}
		content, err := Generate(engine, input)
		if err != nil {
			t.Fatalf("Generate(%s): %v", engine, err)
		}
		if engine == core.EngineXray {
			var root map[string]any
			if err := json.Unmarshal([]byte(content), &root); err != nil {
				t.Fatal(err)
			}
			inbound := firstMap(root["inbounds"])
			reality := mapValue(mapValue(inbound["streamSettings"])["realitySettings"])
			if got := stringValue(reality["minClientVer"]); got != "0.0.0" {
				t.Fatalf("Xray Reality minClientVer = %q", got)
			}
		}
		parsed, ok := Parse(engine, content)
		if !ok || !parsed.RealityEnabled || parsed.RealityPublicKey != input.RealityPublicKey || parsed.RealityShortID != input.RealityShortID {
			t.Errorf("Parse(%s) lost Reality material: %+v, %v", engine, parsed, ok)
		}
	}
}
