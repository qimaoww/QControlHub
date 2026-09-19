package serverconfig

import (
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"gopkg.in/yaml.v3"
)

func TestMieruPresetRoundTripAndClient(t *testing.T) {
	for _, transport := range []string{"TCP", "UDP"} {
		t.Run(transport, func(t *testing.T) {
			input := mihomoPresetPlan(t, ProtocolMieru)
			if input.MieruTransport != "TCP" || input.TLSEnabled || input.RealityEnabled {
				t.Fatal("unexpected Mieru defaults")
			}
			input.MieruTransport = transport
			input.Username = "default"
			input.Credential = "password: # [quoted]"
			content, err := Generate(core.EngineMihomo, input)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := yaml.Unmarshal([]byte(content), &root); err != nil {
				t.Fatal(err)
			}
			listener := firstMap(root["listeners"])
			if listener["type"] != "mieru" || listener["transport"] != transport || mapValue(listener["users"])[input.Username] != input.Credential {
				t.Fatalf("incorrect native listener: %#v", listener)
			}
			parsed, ok := Parse(core.EngineMihomo, content)
			if !ok || parsed.Username != input.Username || parsed.Credential != input.Credential || parsed.MieruTransport != transport {
				t.Fatal("Mieru authentication or transport lost during parsing")
			}
			again, err := Generate(core.EngineMihomo, parsed)
			if err != nil || again != content {
				t.Fatalf("Mieru round trip changed server configuration: %v", err)
			}
			all := ParseAll(core.EngineMihomo, content)
			if len(all) != 1 || all[0].MieruTransport != transport {
				t.Fatal("Mieru missing from workspace inbounds")
			}
			profile, err := BuildClientProfileNamed(parsed, "2001:db8::1", "", "Edge: [Mieru]")
			if err != nil || profile.MihomoError != "" || profile.Format != "Mihomo Mieru YAML" || !profile.SubscriptionCompatible || profile.URI != profile.Mihomo || strings.ContainsAny(profile.URI, "\r\n") {
				t.Fatalf("Mieru client export unavailable: %v %+v", err, profile)
			}
			var proxy map[string]any
			if err := yaml.Unmarshal([]byte(profile.Mihomo), &proxy); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"type": "mieru", "name": "Edge: [Mieru]", "server": "2001:db8::1", "port": input.Port,
				"transport": transport, "username": input.Username, "password": input.Credential, "udp": true}
			if !reflect.DeepEqual(proxy, want) {
				t.Fatalf("client options = %#v, want %#v", proxy, want)
			}
			for _, field := range []ClientField{{Label: "用户名", Value: input.Username}, {Label: "传输", Value: transport}} {
				found := false
				for _, got := range profile.Fields {
					found = found || got == field
				}
				if !found {
					t.Fatalf("missing client field: %+v", field)
				}
			}
			ports := DiscoverTrafficPorts(core.EngineMihomo, content)
			if len(ports) != 1 || string(ports[0].Protocol) != strings.ToLower(transport) || ports[0].Port != input.Port {
				t.Fatalf("wrong physical transport accounting: %+v", ports)
			}
			accounting, err := PlanPresetAccounting(core.EngineMihomo, content)
			if err != nil || len(accounting.Ports) != 1 {
				t.Fatalf("cannot prepare independent accounting: %v", err)
			}
			if _, ok := Parse(core.EngineMihomo, accounting.Content); !ok {
				t.Fatal("accounting configuration cannot be reopened")
			}
			protocol, _ := FindProtocol(core.EngineMihomo, ProtocolMieru)
			regenerated, err := RegeneratePlan(protocol, input)
			if err != nil || regenerated.MieruTransport != transport || regenerated.Credential == input.Credential || regenerated.Username == input.Username {
				t.Fatalf("regeneration lost transport or reused credentials: %v", err)
			}
		})
	}
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		if _, ok := FindProtocol(engine, ProtocolMieru); ok {
			t.Fatalf("Mieru published for %s", engine)
		}
	}
}

func TestMieruRejectsUnsupportedInputs(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*Input)
	}{
		{"transport", func(input *Input) { input.MieruTransport = "tcp,udp" }},
		{"username", func(input *Input) { input.Username = " " }},
		{"password", func(input *Input) { input.Credential = "short" }},
		{"TLS", func(input *Input) { input.TLSEnabled = true }},
		{"Reality", func(input *Input) { input.RealityEnabled = true }},
		{"WebSocket", func(input *Input) { input.Transport, input.TransportPath = "websocket", "/path" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := mihomoPresetPlan(t, ProtocolMieru)
			test.edit(&input)
			if _, err := Generate(core.EngineMihomo, input); err == nil {
				t.Fatal("invalid server input accepted")
			}
			if _, err := BuildClientProfile(input, "edge.example.com", ""); err == nil {
				t.Fatal("invalid client input accepted")
			}
		})
	}
}

func TestMieruCustomListenersRemainSourceOnly(t *testing.T) {
	input := mihomoPresetPlan(t, ProtocolMieru)
	content, err := Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"multiple users", func(in map[string]any) { mapValue(in["users"])["second"] = input.Credential }},
		{"traffic pattern", func(in map[string]any) { in["traffic-pattern"] = "custom" }},
		{"user hint", func(in map[string]any) { in["user-hint-is-mandatory"] = true }},
		{"oversized port", func(in map[string]any) { in["port"] = 65536 }},
		{"negative port", func(in map[string]any) { in["port"] = -1 }},
		{"port range", func(in map[string]any) { in["port"] = "20000-20010" }},
		{"missing transport", func(in map[string]any) { delete(in, "transport") }},
		{"invalid transport", func(in map[string]any) { in["transport"] = "QUIC" }},
		{"missing password", func(in map[string]any) { in["users"] = map[string]any{"user": ""} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var root map[string]any
			if err := yaml.Unmarshal([]byte(content), &root); err != nil {
				t.Fatal(err)
			}
			test.edit(firstMap(root["listeners"]))
			custom, err := yaml.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := Parse(core.EngineMihomo, string(custom)); ok || len(ParseAll(core.EngineMihomo, string(custom))) != 0 {
				t.Fatal("custom Mieru configuration exposed as a lossy preset")
			}
			if _, err := MutateGenerated(core.EngineMihomo, string(custom), content, input.Tag, "modify"); err == nil {
				t.Fatal("direct preset modification overwrote a custom Mieru listener")
			}
			if _, err := DeletePresetInbound(core.EngineMihomo, string(custom), input.Tag); err != nil {
				t.Fatalf("source-only listener cannot be explicitly deleted: %v", err)
			}
		})
	}
}

func TestMieruMutationUpdatesAndRemovesTransport(t *testing.T) {
	input := mihomoPresetPlan(t, ProtocolMieru)
	current, err := Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	input.MieruTransport = "UDP"
	updated, err := Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := MutateGenerated(core.EngineMihomo, current, updated, input.Tag, "modify")
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok := Parse(core.EngineMihomo, merged)
	if !ok || parsed.MieruTransport != "UDP" {
		t.Fatal("transport edit was not saved")
	}
	other := mihomoPresetPlan(t, ProtocolSnell)
	updated, err = Generate(core.EngineMihomo, other)
	if err != nil {
		t.Fatal(err)
	}
	merged, err = MutateGenerated(core.EngineMihomo, merged, updated, input.Tag, "modify")
	if err != nil || strings.Contains(merged, "transport:") || strings.Contains(merged, "users:") {
		t.Fatalf("switching protocol retained Mieru fields: %v\n%s", err, merged)
	}
}
