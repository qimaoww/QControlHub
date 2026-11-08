package serverconfig

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestPresetEditRetainsNativeAccountsAndExactNumbers(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineMihomo} {
		t.Run(string(engine), func(t *testing.T) {
			source := `{"inbounds":[{"tag":"first","listen":"127.0.0.1","port":24111,"protocol":"vmess","settings":{"clients":[{"id":"123e4567-e89b-42d3-a456-426614174000","email":"first","level":9007199254740993},{"id":"123e4567-e89b-42d3-a456-426614174001","email":"second"}],"disableInsecureEncryption":true},"streamSettings":{"network":"ws","wsSettings":{"path":"/ws","headers":{"Host":"custom.example","X-Custom":"keep"}}},"sniffing":{"enabled":true}}],"outbounds":[{"tag":"direct","protocol":"freedom"}]} `
			if engine == core.EngineMihomo {
				source = "listeners: [{name: first, type: vmess, listen: 127.0.0.1, port: 24111, users: [{username: first, uuid: 123e4567-e89b-42d3-a456-426614174000, alterId: 0, future: 9007199254740993}, {username: second, uuid: 123e4567-e89b-42d3-a456-426614174001, alterId: 0}], future: {nested: keep}}]\nrules: ['MATCH,DIRECT']\n"
			}
			inputs := ParseAll(engine, source)
			if len(inputs) != 1 {
				t.Fatalf("cannot parse fixture: %+v", inputs)
			}
			input := inputs[0]
			input.Port = 24112
			generated, err := Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			result, err := MutateGenerated(engine, source, generated, "first", "modify")
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{"9007199254740993", "second", "24112"} {
				if !strings.Contains(result, value) {
					t.Fatalf("lost %s: %s", value, result)
				}
			}
			if engine == core.EngineXray {
				for _, value := range []string{"X-Custom", "custom.example", "disableInsecureEncryption", "sniffing"} {
					if !strings.Contains(result, value) {
						t.Fatalf("lost %s", value)
					}
				}
			}
		})
	}
}

func TestPatchNativeOutboundPreservesUnrepresentedFields(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo} {
		t.Run(string(engine), func(t *testing.T) {
			source := `{"tag":"edge","type":"vless","server":"old.example","server_port":443,"uuid":"123e4567-e89b-42d3-a456-426614174000","tls":{"enabled":true,"future":9007199254740993},"future":{"nested":[1,2,3]}}`
			before := `{"tag":"edge","type":"vless","server":"old.example","server_port":443,"tls":{"enabled":true}}`
			after := strings.Replace(before, "old.example", "new.example", 1)
			if engine == core.EngineXray {
				source = `{"tag":"edge","protocol":"vless","settings":{"vnext":[{"address":"old.example","port":443,"users":[{"id":"first","future":9007199254740993},{"id":"second"}]}]},"future":{"nested":[1,2,3]}}`
				before = `{"tag":"edge","protocol":"vless","settings":{"vnext":[{"address":"old.example","port":443,"users":[{"id":"first"}]}]}}`
				after = strings.Replace(before, "old.example", "new.example", 1)
			}
			result, err := PatchNativePreset(engine, source, before, after)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{"9007199254740993", "new.example", "nested"} {
				if !strings.Contains(result, value) {
					t.Fatalf("lost %s: %s", value, result)
				}
			}
		})
	}
}

func TestOutboundRenameAndExplicitReplacement(t *testing.T) {
	source := `{"inbounds":[],"outbounds":[{"tag":"old","protocol":"freedom"},{"tag":"next","protocol":"freedom"}],"routing":{"rules":[{"inboundTag":["old"],"domain":["old"],"outboundTag":"old"}],"balancers":[{"tag":"pool","selector":["old"]}]},"future":9007199254740993}`
	renamed, _, err := MutateManagedOutbound(core.EngineXray, source, "outbounds", "old", `{"tag":"renamed","protocol":"freedom"}`, "modify", ManagedState{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(renamed, `"outboundTag":"renamed"`) || !strings.Contains(renamed, `"inboundTag":["old"]`) || !strings.Contains(renamed, "9007199254740993") {
		t.Fatal(renamed)
	}
	replaced, err := ReplaceManagedOutboundReferences(core.EngineXray, source, "old", "next", ManagedState{})
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := MutateManagedOutbound(core.EngineXray, replaced, "outbounds", "old", "", "delete", ManagedState{})
	if err != nil {
		t.Fatal(err)
	}
	root, err := managedDocument(core.EngineXray, result)
	if err != nil {
		t.Fatal(err)
	}
	if managedTag(core.EngineXray, root["outbounds"].([]any)[0]) != "next" {
		t.Fatal("implicit default changed")
	}
}

func TestMihomoGroupLifecycleRetainsGraphAndIndependentMarks(t *testing.T) {
	source := "listeners: [{name: first, type: http, listen: 127.0.0.1, port: 24111}, {name: neighbor, type: http, port: 24112}]\nproxies: [{name: edge, type: socks5, server: 127.0.0.1, port: 1080}]\nproxy-groups: [{name: fallback, type: fallback, proxies: [edge, DIRECT], url: 'https://example.test', interval: 300}, {name: selected, type: select, proxies: [fallback, edge]}]\nrules: ['DOMAIN,private.example,edge', 'MATCH,selected']\nfuture: 9007199254740993\n"
	plan, err := PrepareAccounting(core.EngineMihomo, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Ports) != 2 || len(plan.Ports[0].Outbounds) != 2 {
		t.Fatalf("wrong graph: %+v", plan)
	}
	if _, err = PrepareAccounting(core.EngineMihomo, plan.Content); err != nil {
		t.Fatal("graph not idempotent", err)
	}
	restored, err := PresetAccountingSource(core.EngineMihomo, plan.Content)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(restored, accountingPrefix) || !strings.Contains(restored, "9007199254740993") {
		t.Fatal(restored)
	}
	renamed, _, err := MutateManagedOutbound(core.EngineMihomo, restored, "proxies", "edge", "name: renamed\ntype: socks5\nserver: 127.0.0.1\nport: 1080\n", "modify", ManagedState{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = PrepareAccounting(core.EngineMihomo, renamed); err != nil {
		t.Fatal(err)
	}
	replaced, err := ReplaceManagedOutboundReferences(core.EngineMihomo, renamed, "renamed", "selected", ManagedState{})
	if err != nil {
		t.Fatal(err)
	}
	// A user-chosen replacement that introduces a group cycle is rejected;
	// the previous valid candidate remains available for another choice.
	if _, err = PrepareAccounting(core.EngineMihomo, replaced); err == nil {
		t.Fatal("accepted replacement group cycle")
	}
	bound, err := BindManagedOutbound(core.EngineMihomo, restored, "first", 24111, "edge", ManagedState{})
	if err != nil {
		t.Fatal(err)
	}
	if ManagedOutboundBindings(core.EngineMihomo, bound)["first"] != "edge" {
		t.Fatal("binding not returned")
	}
	if _, err = PrepareAccounting(core.EngineMihomo, bound); err != nil {
		t.Fatal(err)
	}
}

func TestNativeOutboundBindingsKeepConditionalAndNeighborRoutes(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		t.Run(string(engine), func(t *testing.T) {
			source := `{"inbounds":[{"tag":"a","type":"http","listen_port":24111},{"tag":"b","type":"http","listen_port":24112}],"outbounds":[{"tag":"original","type":"direct"},{"tag":"next","type":"direct"}],"route":{"rules":[{"domain":["private.example"],"outbound":"original"},{"inbound":["b"],"outbound":"original"}],"final":"original"},"future":9007199254740993}`
			if engine == core.EngineXray {
				source = `{"inbounds":[{"tag":"a","protocol":"http","port":24111},{"tag":"b","protocol":"http","port":24112}],"outbounds":[{"tag":"original","protocol":"freedom"},{"tag":"next","protocol":"freedom"}],"routing":{"rules":[{"type":"field","domain":["private.example"],"outboundTag":"original"},{"type":"field","inboundTag":["b"],"outboundTag":"original"}]},"future":9007199254740993}`
			}
			bound, err := BindManagedOutbound(engine, source, "a", 24111, "next", ManagedState{})
			if err != nil {
				t.Fatal(err)
			}
			bindings := ManagedOutboundBindings(engine, bound)
			if bindings["a"] != "next" || bindings["b"] != "original" || !strings.Contains(bound, "9007199254740993") || !strings.Contains(bound, "private.example") {
				t.Fatal(bound)
			}
			if _, err = BindManagedOutbound(engine, source, "a", 24112, "next", ManagedState{}); err == nil {
				t.Fatal("guessed identity by neighbor port")
			}
		})
	}
}

func TestNativeProtocolSwitchAndReferenceIdentity(t *testing.T) {
	current := `{"tag":"edge","type":"vless","server":"old.example","uuid":"fixture","future":{"exact":9007199254740993}}`
	before := `{"tag":"edge","type":"vless","server":"old.example","uuid":"fixture"}`
	after := `{"tag":"edge","type":"socks","server":"new.example","server_port":1080}`
	patched, err := PatchNativePreset(core.EngineSingBox, current, before, after)
	if err != nil || !strings.Contains(patched, "9007199254740993") || strings.Contains(patched, "uuid") {
		t.Fatalf("protocol switch lost native fields or retained obsolete auth: %v %s", err, patched)
	}
	source := `{"inbounds":[],"outbounds":[{"tag":"direct","type":"direct"},{"tag":"qch-personal","type":"socks","server":"a.example","server_port":1080}],"route":{"final":"direct"},"label":"arbitrary,comma,qch-personal"}`
	if _, _, err = MutateManagedOutbound(core.EngineSingBox, source, "outbounds", "qch-personal", "", "delete", ManagedState{}); err != nil {
		t.Fatal("custom label text mistaken for system ownership or dependency", err)
	}
	if !managedReferences(map[string]any{"sub-rules": map[string]any{"private": []any{"MATCH,qch-personal"}}}, "qch-personal") {
		t.Fatal("nested native sub-rule dependency lost")
	}
}
