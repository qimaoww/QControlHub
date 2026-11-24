package serverconfig

import (
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSocksPresetRoundTrip(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo} {
		t.Run(string(engine), func(t *testing.T) {
			protocol, ok := FindProtocol(engine, ProtocolSocks)
			if !ok || !protocol.RequiresUsername {
				t.Fatal("authenticated SOCKS5 preset missing")
			}
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.Username, input.Credential = "socks-user", "socks-password-123"
			source, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			parsed, ok := Parse(engine, source)
			if !ok || !CanEditNativePreset(engine, parsed) || parsed.Username != input.Username || parsed.Credential != input.Credential {
				t.Fatal("credentials did not survive native configuration")
			}
			regenerated, err := Generate(engine, parsed)
			if err != nil || regenerated != source {
				t.Fatalf("native round trip changed configuration: %v", err)
			}
			profile, err := BuildClientProfile(parsed, "2001:db8::8", "")
			if err != nil {
				t.Fatal(err)
			}
			uri, err := url.Parse(profile.URI)
			if err != nil || uri.User == nil {
				t.Fatal("SOCKS5 URI missing authentication")
			}
			password, _ := uri.User.Password()
			if err != nil || uri.Scheme != "socks5" || uri.User.Username() != input.Username || password != input.Credential {
				t.Fatal("SOCKS5 URI lost escaped credentials")
			}
			for _, target := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo} {
				out, err := BuildClientOutbound(target, parsed, "2001:db8::8", "")
				if err != nil {
					t.Fatal(err)
				}
				var entry map[string]any
				if err := json.Unmarshal(out, &entry); err != nil {
					t.Fatal(err)
				}
				username, password := entry["username"], entry["password"]
				if target == core.EngineXray {
					user := firstMap(firstMap(mapValue(entry["settings"])["servers"])["users"])
					username, password = user["user"], user["pass"]
				}
				if username != input.Username || password != input.Credential {
					t.Fatalf("%s outbound lost authentication", target)
				}
			}
			if _, err := PlanPresetAccounting(engine, source); err != nil {
				t.Fatalf("SOCKS5 accounting failed: %v", err)
			}
			for name, change := range map[string]func(*Input){
				"empty username": func(p *Input) { p.Username = "" },
				"empty password": func(p *Input) { p.Credential = "" },
				"tls":            func(p *Input) { p.TLSEnabled = true },
				"reality":        func(p *Input) { p.RealityEnabled = true },
				"transport":      func(p *Input) { p.Transport = "websocket" },
				"flow":           func(p *Input) { p.Flow = "xtls-rprx-vision" },
			} {
				t.Run(name, func(t *testing.T) {
					invalid := input
					change(&invalid)
					if _, err := Generate(engine, invalid); err == nil {
						t.Fatal("invalid SOCKS5 preset accepted")
					}
				})
			}
		})
	}
	if _, ok := FindProtocol(core.EngineShadowsocksRust, ProtocolSocks); ok {
		t.Fatal("SOCKS5 exposed for unsupported core")
	}
}

func TestSocksNativeFixturesAndPreservedOptions(t *testing.T) {
	for name, fixture := range map[string]struct {
		engine core.Engine
		source string
	}{
		"xray accounts":    {core.EngineXray, `{"inbounds":[{"tag":"socks","protocol":"socks","listen":"0.0.0.0","port":21001,"settings":{"auth":"password","accounts":[{"user":"native","pass":"native-password-123"}],"udp":true,"ip":"0.0.0.0","userLevel":7}}],"outbounds":[{"protocol":"freedom","tag":"direct"}],"custom":42}`},
		"xray users alias": {core.EngineXray, `{"inbounds":[{"tag":"socks","protocol":"socks","port":21001,"settings":{"auth":"password","users":[{"user":"native","pass":"native-password-123"}],"udp":true}}],"outbounds":[{"protocol":"freedom"}],"custom":42}`},
		"sing-box":         {core.EngineSingBox, `{"inbounds":[{"type":"socks","tag":"socks","listen":"::","listen_port":21001,"users":[{"username":"native","password":"native-password-123"}],"tcp_fast_open":true}],"outbounds":[{"type":"direct"}],"custom":42}`},
		"mihomo":           {core.EngineMihomo, "listeners: [{name: socks, type: socks, listen: '::', port: 21001, udp: true, users: [{username: native, password: native-password-123}], routing-mark: 42}]\nrules: ['MATCH,DIRECT']\ncustom: 42\n"},
	} {
		t.Run(name, func(t *testing.T) {
			input, ok := Parse(fixture.engine, fixture.source)
			if !ok || !CanEditNativePreset(fixture.engine, input) || input.Username != "native" || input.Credential != "native-password-123" {
				t.Fatal("independent native fixture not recognized")
			}
			generated, err := Generate(fixture.engine, input)
			if err != nil {
				t.Fatal(err)
			}
			unchanged, err := MutateGenerated(fixture.engine, fixture.source, generated, input.Tag, "modify")
			before, _ := managedDocument(fixture.engine, fixture.source)
			after, _ := managedDocument(fixture.engine, unchanged)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("no-op changed native options: %v", err)
			}
			input.Username, input.Credential, input.Port = "changed", "changed-password-456", 21002
			generated, err = Generate(fixture.engine, input)
			if err != nil {
				t.Fatal(err)
			}
			changed, err := MutateGenerated(fixture.engine, fixture.source, generated, input.Tag, "modify")
			if err != nil {
				t.Fatal(err)
			}
			parsed, ok := Parse(fixture.engine, changed)
			if !ok || parsed.Username != input.Username || parsed.Credential != input.Credential || parsed.Port != input.Port {
				t.Fatal("edit did not update native credentials and port")
			}
			after, _ = managedDocument(fixture.engine, changed)
			if !reflect.DeepEqual(before["custom"], after["custom"]) || !reflect.DeepEqual(before["outbounds"], after["outbounds"]) || !reflect.DeepEqual(before["rules"], after["rules"]) {
				t.Fatal("edit changed unrelated options")
			}
			if strings.Contains(name, "alias") && strings.Contains(changed, `"accounts"`) {
				t.Fatal("edit shadowed the original users alias")
			}
		})
	}
}

func TestSocksUnsupportedNativeAuthentication(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo} {
		protocol, _ := FindProtocol(engine, ProtocolSocks)
		input, _ := NewPlan(protocol)
		source, _ := Generate(engine, input)
		for _, variant := range []string{"anonymous", "multiple", "extra-field", "udp-disabled"} {
			if engine == core.EngineSingBox && variant == "udp-disabled" {
				continue
			}
			t.Run(string(engine)+"/"+variant, func(t *testing.T) {
				root, _ := managedDocument(engine, source)
				entry := firstMap(root["inbounds"])
				if engine == core.EngineMihomo {
					entry = firstMap(root["listeners"])
				}
				owner, key := entry, "users"
				if engine == core.EngineXray {
					owner, key = mapValue(entry["settings"]), "accounts"
				}
				switch variant {
				case "anonymous":
					delete(owner, key)
				case "multiple":
					owner[key] = append(owner[key].([]any), firstMap(owner[key]))
				case "extra-field":
					firstMap(owner[key])["unknown"] = true
				case "udp-disabled":
					owner["udp"] = false
				}
				encoded, _ := json.Marshal(root)
				for _, parsed := range ParseAll(engine, string(encoded)) {
					if CanEditNativePreset(engine, parsed) {
						t.Fatal("unsupported native options exposed for editing")
					}
				}
			})
		}
	}
}
