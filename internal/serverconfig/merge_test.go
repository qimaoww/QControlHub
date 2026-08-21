package serverconfig

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestMergeGeneratedRemovesStaleManagedInboundFields(t *testing.T) {
	t.Parallel()
	credential, err := NewCredential(ProtocolSS2022, "2022-blake3-aes-256-gcm")
	if err != nil {
		t.Fatal(err)
	}
	generatedInput := Input{
		Protocol: ProtocolSS2022, Tag: "replacement", Listen: "0.0.0.0", Port: 8388,
		Method: "2022-blake3-aes-256-gcm", Credential: credential, Transport: "raw",
	}
	fixtures := []struct {
		name         string
		engine       core.Engine
		current      string
		staleMarkers []string
		keptMarkers  []string
	}{
		{
			name: "Mihomo websocket TLS to raw SS2022", engine: core.EngineMihomo,
			current:      "future-root: keep\nlisteners:\n  - name: managed\n    type: vmess\n    listen: 0.0.0.0\n    port: 443\n    users:\n      - username: alice\n        uuid: 123e4567-e89b-42d3-a456-426614174000\n    ws-path: /obsolete-ws\n    certificate: /obsolete.crt\n    private-key: /obsolete.key\n    future-inbound: keep\n  - name: secondary\n    type: http\n    port: 8080\n",
			staleMarkers: []string{"/obsolete-ws", "/obsolete.crt", "/obsolete.key", "123e4567-e89b-42d3-a456-426614174000"},
			keptMarkers:  []string{"future-root: keep", "future-inbound: keep", "name: secondary"},
		},
		{
			name: "Xray websocket TLS to raw SS2022", engine: core.EngineXray,
			current:      `{"futureRoot":"keep","inbounds":[{"tag":"managed","listen":"0.0.0.0","port":443,"protocol":"vmess","settings":{"users":[{"email":"alice","id":"123e4567-e89b-42d3-a456-426614174000"}]},"streamSettings":{"network":"ws","security":"tls","wsSettings":{"path":"/obsolete-ws"},"tlsSettings":{"certificates":[{"certificateFile":"/obsolete.crt","keyFile":"/obsolete.key"}]}},"futureInbound":"keep"},{"tag":"secondary","protocol":"dokodemo-door","port":8080}]}`,
			staleMarkers: []string{"/obsolete-ws", "/obsolete.crt", "/obsolete.key", "123e4567-e89b-42d3-a456-426614174000"},
			keptMarkers:  []string{`"futureRoot": "keep"`, `"futureInbound": "keep"`, `"tag": "secondary"`},
		},
		{
			name: "sing-box websocket TLS to raw SS2022", engine: core.EngineSingBox,
			current:      `{"futureRoot":"keep","inbounds":[{"tag":"managed","type":"vmess","listen":"0.0.0.0","listen_port":443,"users":[{"name":"alice","uuid":"123e4567-e89b-42d3-a456-426614174000"}],"transport":{"type":"ws","path":"/obsolete-ws"},"tls":{"enabled":true,"certificate_path":"/obsolete.crt","key_path":"/obsolete.key"},"futureInbound":"keep"},{"tag":"secondary","type":"socks","listen_port":1080}]}`,
			staleMarkers: []string{"/obsolete-ws", "/obsolete.crt", "/obsolete.key", "123e4567-e89b-42d3-a456-426614174000"},
			keptMarkers:  []string{`"futureRoot": "keep"`, `"futureInbound": "keep"`, `"tag": "secondary"`},
		},
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			generated, err := Generate(fixture.engine, generatedInput)
			if err != nil {
				t.Fatal(err)
			}
			merged, err := MergeGenerated(fixture.engine, fixture.current, generated)
			if err != nil {
				t.Fatal(err)
			}
			if err := core.ValidateConfig(fixture.engine, merged); err != nil {
				t.Fatalf("merged configuration is invalid: %v\n%s", err, merged)
			}
			parsed, ok := Parse(fixture.engine, merged)
			if !ok || parsed.Protocol != ProtocolSS2022 || parsed.Tag != generatedInput.Tag || parsed.Transport != "raw" || parsed.TLSEnabled {
				t.Fatalf("merged inbound = %+v, parsed=%t\n%s", parsed, ok, merged)
			}
			for _, marker := range fixture.staleMarkers {
				if strings.Contains(merged, marker) {
					t.Errorf("stale managed field %q survived merge:\n%s", marker, merged)
				}
			}
			for _, marker := range fixture.keptMarkers {
				if !strings.Contains(merged, marker) {
					t.Errorf("advanced field %q was lost:\n%s", marker, merged)
				}
			}
		})
	}
}

func TestMergeGeneratedForcesCentralLogOutput(t *testing.T) {
	t.Parallel()
	credential, err := NewCredential(ProtocolSS2022, "2022-blake3-aes-256-gcm")
	if err != nil {
		t.Fatal(err)
	}
	input := Input{
		Protocol: ProtocolSS2022, Tag: "managed", Listen: "0.0.0.0", Port: 8388,
		Method: "2022-blake3-aes-256-gcm", Credential: credential, Transport: "raw",
	}
	fixtures := []struct {
		engine  core.Engine
		current string
		want    string
	}{
		{core.EngineMihomo, "log-level: error\nlisteners: []\nrules: []\n", "log-level: info"},
		{core.EngineXray, `{"log":{"loglevel":"error","access":"/var/log/xray-access.log"},"inbounds":[],"outbounds":[]}`, `"loglevel": "info"`},
		{core.EngineSingBox, `{"log":{"level":"error","output":"/var/log/sing-box.log"},"inbounds":[],"outbounds":[]}`, `"level": "info"`},
	}
	for _, fixture := range fixtures {
		generated, err := Generate(fixture.engine, input)
		if err != nil {
			t.Fatal(err)
		}
		merged, err := MergeGenerated(fixture.engine, fixture.current, generated)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(merged, fixture.want) || strings.Contains(merged, "/var/log/") {
			t.Fatalf("%s centralized log configuration = %s", fixture.engine, merged)
		}
	}
}

func TestMutateGeneratedEnforcesAddModifyDeleteForEveryEngine(t *testing.T) {
	t.Parallel()
	credential, err := NewCredential(ProtocolSS2022, "2022-blake3-aes-256-gcm")
	if err != nil {
		t.Fatal(err)
	}
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		engine := engine
		t.Run(string(engine), func(t *testing.T) {
			t.Parallel()
			current, err := Generate(engine, Input{
				Protocol: ProtocolSS2022, Tag: "managed", Listen: "0.0.0.0", Port: 8388,
				Method: "2022-blake3-aes-256-gcm", Credential: credential, Transport: "raw",
			})
			if err != nil {
				t.Fatal(err)
			}
			generated, err := Generate(engine, Input{
				Protocol: ProtocolSS2022, Tag: "replacement", Listen: "0.0.0.0", Port: 8389,
				Method: "2022-blake3-aes-256-gcm", Credential: credential, Transport: "raw",
			})
			if err != nil {
				t.Fatal(err)
			}
			modified, err := MutateGenerated(engine, current, generated, "managed", "modify")
			if err != nil {
				t.Fatal(err)
			}
			parsed, ok := Parse(engine, modified)
			if !ok || parsed.Tag != "replacement" || parsed.Port != 8389 {
				t.Fatalf("modified %s configuration = %+v, parsed=%t\n%s", engine, parsed, ok, modified)
			}
			if _, err := MutateGenerated(engine, current, generated, "missing", "modify"); err == nil {
				t.Fatalf("%s missing modify target was appended", engine)
			}
			added, err := MutateGenerated(engine, current, generated, "", "add")
			if err != nil || !strings.Contains(added, "managed") || !strings.Contains(added, "replacement") {
				t.Fatalf("added %s configuration error=%v\n%s", engine, err, added)
			}
			all := ParseAll(engine, added)
			if len(all) != 2 || all[0].Tag != "managed" || all[1].Tag != "replacement" {
				t.Fatalf("parsed %s inbounds = %+v", engine, all)
			}
			if _, err := MutateGenerated(engine, added, generated, "", "add"); err == nil {
				t.Fatalf("%s duplicate add was accepted", engine)
			}
			deleted, err := MutateGenerated(engine, current, generated, "managed", "delete")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(deleted, "managed") {
				t.Fatalf("deleted %s configuration retained target:\n%s", engine, deleted)
			}
			if err := core.ValidateConfig(engine, deleted); err != nil {
				t.Fatalf("deleted %s configuration is structurally invalid: %v\n%s", engine, err, deleted)
			}
		})
	}
}

func TestMutateGeneratedEnforcesSingleShadowsocksRustServer(t *testing.T) {
	t.Parallel()
	current := `{"future":{"enabled":true},"server":"0.0.0.0","server_port":8388,"password":"current-password","method":"chacha20-ietf-poly1305","mode":"tcp_and_udp","timeout":300,"no_delay":true}`
	replacement, err := Generate(core.EngineShadowsocksRust, Input{
		Protocol: ProtocolSS2022, Tag: "replacement", Listen: "127.0.0.1", Port: 8389,
		Method: "2022-blake3-aes-128-gcm", Credential: "MDEyMzQ1Njc4OWFiY2RlZg==", Transport: "raw",
	})
	if err != nil {
		t.Fatal(err)
	}
	modified, err := MutateGenerated(core.EngineShadowsocksRust, current, replacement, "ss-rust", "modify")
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok := Parse(core.EngineShadowsocksRust, modified)
	if !ok || parsed.Protocol != ProtocolSS2022 || parsed.Port != 8389 || parsed.Credential != "MDEyMzQ1Njc4OWFiY2RlZg==" || !strings.Contains(modified, `"future"`) {
		t.Fatalf("modified ss-rust configuration = %+v, parsed=%t\n%s", parsed, ok, modified)
	}
	if _, err := MutateGenerated(core.EngineShadowsocksRust, current, replacement, "missing", "modify"); err == nil {
		t.Fatal("ss-rust modify accepted a missing target")
	}
	if _, err := MutateGenerated(core.EngineShadowsocksRust, current, replacement, "", "add"); err == nil {
		t.Fatal("ss-rust duplicate add was accepted")
	}
	empty := `{"future":{"enabled":true}}`
	added, err := MutateGenerated(core.EngineShadowsocksRust, empty, replacement, "", "add")
	if err != nil {
		t.Fatal(err)
	}
	if parsed, ok := Parse(core.EngineShadowsocksRust, added); !ok || parsed.Port != 8389 {
		t.Fatalf("added ss-rust configuration = %q, parsed=%+v, ok=%t", added, parsed, ok)
	}
	if _, err := MutateGenerated(core.EngineShadowsocksRust, added, replacement, "", "add"); err == nil {
		t.Fatal("ss-rust second add was accepted")
	}
	deleted, err := MutateGenerated(core.EngineShadowsocksRust, current, replacement, "ss-rust", "delete")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(deleted, `"server"`) || !strings.Contains(deleted, `"future"`) {
		t.Fatalf("deleted ss-rust configuration = %s", deleted)
	}
}

func TestMutateGeneratedPreservesShadowsocksRustExtendedEntries(t *testing.T) {
	t.Parallel()
	current := `{"servers":[{"server":"::","server_port":26663,"password":"MDEyMzQ1Njc4OWFiY2RlZg==","method":"2022-blake3-aes-128-gcm","id":"primary","future_entry":true},{"server":"127.0.0.1","server_port":26664,"password":"correct-horse-battery-staple","method":"chacha20-ietf-poly1305"}],"timeout":300,"fast_open":false,"future_root":{"keep":true}}`
	replacement, err := Generate(core.EngineShadowsocksRust, Input{
		Protocol: ProtocolShadowsocks, Tag: "replacement", Listen: "127.0.0.1", Port: 26665,
		Method: "aes-256-gcm", Credential: "replacement-password", Transport: "raw",
	})
	if err != nil {
		t.Fatal(err)
	}
	modified, err := MutateGenerated(core.EngineShadowsocksRust, current, replacement, "primary", "modify")
	if err != nil {
		t.Fatal(err)
	}
	inputs := ParseAll(core.EngineShadowsocksRust, modified)
	if len(inputs) != 2 || inputs[0].Port != 26665 || inputs[0].Protocol != ProtocolShadowsocks || inputs[1].Port != 26664 || !strings.Contains(modified, `"future_entry": true`) || !strings.Contains(modified, `"future_root"`) {
		t.Fatalf("modified extended ss-rust = %+v\n%s", inputs, modified)
	}
	added, err := MutateGenerated(core.EngineShadowsocksRust, current, replacement, "", "add")
	if err != nil {
		t.Fatal(err)
	}
	if len(ParseAll(core.EngineShadowsocksRust, added)) != 3 {
		t.Fatalf("extended ss-rust add did not append an entry:\n%s", added)
	}
	deleted, err := MutateGenerated(core.EngineShadowsocksRust, current, replacement, "primary", "delete")
	if err != nil {
		t.Fatal(err)
	}
	inputs = ParseAll(core.EngineShadowsocksRust, deleted)
	if len(inputs) != 1 || inputs[0].Port != 26664 || !strings.Contains(deleted, `"future_root"`) {
		t.Fatalf("deleted extended ss-rust = %+v\n%s", inputs, deleted)
	}
}
