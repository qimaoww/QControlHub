package serverconfig

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSingBoxUpstreamPresetLifecycle(t *testing.T) {
	for _, protocol := range Protocols(core.EngineSingBox) {
		if protocol.UsesEndpoint || protocol.PortForward {
			continue
		}
		t.Run(protocol.Key, func(t *testing.T) {
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			if input.Transport == "http" || input.Transport == "httpupgrade" || input.Transport == "websocket" {
				input.TransportHost = "http.example.test"
			}
			if input.TLSEnabled {
				input.TLSServerName = "tls.example.test"
				if input.Transport == "http" {
					input.TLSALPN = "h2"
				}
			}
			if input.Transport == "http" {
				input.TransportHTTPMethod = "POST"
			}
			content, err := Generate(core.EngineSingBox, input)
			if err != nil {
				t.Fatal(err)
			}
			parsed, ok := Parse(core.EngineSingBox, content)
			if !ok {
				t.Fatal("parse")
			}
			metadata, err := MarshalClientMetadata(input)
			if err != nil {
				t.Fatal(err)
			}
			if err := ApplyClientMetadata(&parsed, metadata); err != nil {
				t.Fatal(err)
			}
			if nativeProtocol(parsed.Protocol) != nativeProtocol(input.Protocol) || parsed.TransportHost != input.TransportHost || parsed.TransportHTTPMethod != input.TransportHTTPMethod || parsed.TLSServerName != input.TLSServerName || parsed.TLSALPN != input.TLSALPN || parsed.Transport != input.Transport || parsed.TransportPath != input.TransportPath {
				t.Fatalf("lost input: %+v", parsed)
			}
			// Edit one of several inbounds; native custom fields and shared state
			// must survive, including the independent exit.
			var generated map[string]any
			_ = json.Unmarshal([]byte(content), &generated)
			inbound := firstMap(generated["inbounds"])
			inbound["tcp_fast_open"] = true
			root := map[string]any{"inbounds": []any{map[string]any{"tag": "other", "type": "http", "listen_port": 1080}, inbound}, "dns": map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "system"}}}, "route": map[string]any{"final": "exit"}, "outbounds": []any{map[string]any{"type": "direct", "tag": "exit", "bind_interface": "eth0"}}}
			original, _ := json.Marshal(root)
			parsed.Tag = "edited"
			parsed.Port++
			updated, err := Generate(core.EngineSingBox, parsed)
			if err != nil {
				t.Fatal(err)
			}
			merged, err := MutateGenerated(core.EngineSingBox, string(original), updated, input.Tag, "modify")
			if err != nil {
				t.Fatal(err)
			}
			var after map[string]any
			_ = json.Unmarshal([]byte(merged), &after)
			for _, key := range []string{"dns", "route", "outbounds"} {
				if !reflect.DeepEqual(root[key], after[key]) {
					t.Fatalf("changed %s", key)
				}
			}
			entries := after["inbounds"].([]any)
			if len(entries) != 2 || mapValue(entries[0])["tag"] != "other" || mapValue(entries[1])["tcp_fast_open"] != true {
				t.Fatal("unrelated entry or custom field changed")
			}
			read := ParseAll(core.EngineSingBox, merged)
			if len(read) != 1 || read[0].Tag != "edited" || read[0].Port != input.Port+1 {
				t.Fatal("wrong entry after editing")
			}
			profile, err := BuildClientProfile(parsed, "203.0.113.8", "")
			if err != nil {
				t.Fatal(err)
			}
			out, err := BuildClientOutbound(core.EngineSingBox, parsed, "203.0.113.8", "")
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{input.RealityPrivateKey, input.PrivateKeyPath, input.CertificatePath} {
				if secret != "" && (strings.Contains(string(out), secret) || strings.Contains(profile.URI, secret)) {
					t.Fatal("server private data leaked")
				}
			}
			if _, err := SharedTrafficEndpoints(core.EngineSingBox, merged); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSingBoxPresetSecurityAndScope(t *testing.T) {
	for _, preset := range singBoxPresets {
		for _, engine := range []core.Engine{core.EngineXray, core.EngineMihomo, core.EngineShadowsocksRust} {
			if _, ok := FindProtocol(engine, preset.key); ok {
				t.Fatalf("sing-box preset leaked into %s", engine)
			}
		}
	}
	protocol, _ := FindProtocol(core.EngineSingBox, ProtocolVLESSHTTP2Reality)
	input, _ := NewPlan(protocol)
	if input.Flow != "" || input.Transport != "http" || !input.RealityEnabled {
		t.Fatal("H2 Reality uses Vision")
	}
	input.Flow = "xtls-rprx-vision"
	if _, err := Generate(core.EngineSingBox, input); err == nil {
		t.Fatal("accepted H2 Vision")
	}
	input.Flow = ""
	input.TLSEnabled = true
	if _, err := Generate(core.EngineSingBox, input); err == nil {
		t.Fatal("accepted TLS and Reality")
	}
	for _, key := range []string{"vmess-tcp", "vmess-quic", ProtocolSocks} {
		protocol, _ := FindProtocol(core.EngineSingBox, key)
		input, _ := NewPlan(protocol)
		input.Transport = "httpupgrade"
		if _, err := Generate(core.EngineSingBox, input); err == nil {
			t.Fatal("fixed scheme accepted stale transport")
		}
	}
}

func TestSingBoxImportedCustomOptionsRemainSourceOnly(t *testing.T) {
	protocol, _ := FindProtocol(core.EngineSingBox, "vmess-h2-tls")
	input, _ := NewPlan(protocol)
	generated, err := Generate(core.EngineSingBox, input)
	if err != nil {
		t.Fatal(err)
	}
	for name, customize := range map[string]func(map[string]any){
		"multiple-users": func(entry map[string]any) {
			entry["users"] = append(entry["users"].([]any), map[string]any{"name": "other", "uuid": input.Credential})
		},
		"multiple-hosts": func(entry map[string]any) {
			mapValue(entry["transport"])["host"] = []any{"a.example.test", "b.example.test"}
		},
		"custom-header": func(entry map[string]any) {
			mapValue(entry["transport"])["headers"] = map[string]any{"X-Custom": "retained"}
		},
		"custom-tls":        func(entry map[string]any) { mapValue(entry["tls"])["min_version"] = "1.3" },
		"unknown-transport": func(entry map[string]any) { mapValue(entry["transport"])["type"] = "future" },
	} {
		t.Run(name, func(t *testing.T) {
			var root map[string]any
			_ = json.Unmarshal([]byte(generated), &root)
			customize(firstMap(root["inbounds"]))
			encoded, _ := json.Marshal(root)
			if _, ok := Parse(core.EngineSingBox, string(encoded)); ok {
				t.Fatal("custom configuration was exposed as losslessly editable")
			}
			if _, err := MutateGenerated(core.EngineSingBox, string(encoded), generated, input.Tag, "modify"); err == nil || !strings.Contains(err.Error(), "源码") {
				t.Fatalf("custom configuration could be overwritten: %v", err)
			}
		})
	}
}
