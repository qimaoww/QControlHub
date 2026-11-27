package serverconfig

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestHandwrittenPresetEditsRetainNativeValues(t *testing.T) {
	for _, tc := range []struct {
		engine core.Engine
		source string
	}{
		{core.EngineXray, `{"inbounds":[{"tag":"edge","port":"21001","protocol":"vmess","settings":{"clients":[{"id":"123e4567-e89b-42d3-a456-426614174001"},{"id":"123e4567-e89b-42d3-a456-426614174002"}]},"sniffing":{"enabled":false,"future":9007199254740993}}]}`},
		{core.EngineSingBox, `{"inbounds":[{"tag":"edge","listen":"127.0.0.1","listen_port":21001,"type":"vmess","users":[{"uuid":"123e4567-e89b-42d3-a456-426614174001"}],"transport":{"type":"ws","path":"/native"},"tls":{"enabled":false,"min_version":"1.3","reality":{"enabled":false}}}]}`},
		{core.EngineMihomo, "listeners: [{name: edge, type: snell, port: 21001, psk: fixture-snell-v6, version: 6, mode: unshaped, udp: false}]\n"},
		{core.EngineShadowsocksRust, `{"server":"::","server_port":21001,"method":"aes-128-gcm","password":"only-for-this-test","no_delay":false,"timeout":0,"future":9007199254740993}`},
		{core.EngineShadowsocksRust, `{"shadowsocks":[{"name":"edge","address":"::","port":21001,"method":"aes-128-gcm","password":"only-for-this-test","no_delay":false},{"server":"127.0.0.1","server_port":21003,"method":"none","password":""}],"ipv6_first":false}`},
	} {
		t.Run(string(tc.engine), func(t *testing.T) {
			original, ok := Parse(tc.engine, tc.source)
			if !ok || !CanEditNativePreset(tc.engine, original) {
				t.Fatal("handwritten source has no editable preset")
			}
			input := original
			input.Port = 21002
			generated, err := GenerateNativePresetEdit(tc.engine, input, original)
			if err != nil {
				t.Fatal(err)
			}
			var updated string
			if tc.engine == core.EngineShadowsocksRust {
				updated, err = MutateSSRustPort(tc.source, generated, original.Tag, "modify")
			} else {
				updated, err = MutateGenerated(tc.engine, tc.source, generated, original.Tag, "modify")
			}
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Replace(tc.source, "21001", "21002", 1)
			if tc.engine == core.EngineXray {
				// An explicit port edit may use canonical numeric syntax.
				want = strings.Replace(want, `"21002"`, "21002", 1)
			}
			expected, _ := managedDocument(tc.engine, want)
			actual, err := managedDocument(tc.engine, updated)
			if err != nil || !reflect.DeepEqual(expected, actual) {
				t.Fatal("one-field edit changed unrelated native values")
			}
			if tc.engine == core.EngineShadowsocksRust && strings.Contains(tc.source, `"address"`) &&
				(!strings.Contains(updated, `"port": 21002`) || strings.Contains(updated, `"server_port": 21002`)) {
				t.Fatal("SS Rust aliases were rewritten or duplicated")
			}
			reopened, ok := Parse(tc.engine, updated)
			if !ok || !CanEditNativePreset(tc.engine, reopened) || reopened.Port != input.Port || reopened.Protocol != original.Protocol {
				t.Fatal("updated source did not reopen the same preset")
			}
		})
	}
}

func TestXrayPresetAliasesRetainTheirNativeFields(t *testing.T) {
	const source = `{"inbounds":[{"tag":"edge","port":21001,"protocol":"dokodemo-door","settings":{"network":"tcp","address":"example.test","port":80,"followRedirect":false}}]}`
	before, ok := Parse(core.EngineXray, source)
	if !ok || !CanEditNativePreset(core.EngineXray, before) || before.Protocol != ProtocolPortForward || before.TargetPort != 80 {
		t.Fatal("legacy native forwarding fields did not identify the preset")
	}
	after := before
	after.TargetPort = 8080
	generated, err := GenerateNativePresetEdit(core.EngineXray, after, before)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := MutateGenerated(core.EngineXray, source, generated, before.Tag, "modify")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := managedDocument(core.EngineXray, updated)
	expected, _ := managedDocument(core.EngineXray, strings.Replace(source, `"port":80`, `"port":8080`, 1))
	if err != nil || !reflect.DeepEqual(expected, actual) {
		t.Fatal("forwarding edit missed the active legacy key or changed neighboring fields")
	}
}

func TestPresetEditPreservesAbsentSnellOptions(t *testing.T) {
	const source = `listeners: [{name: first, type: snell, listen: 127.0.0.1, port: 24001, version: 6, psk: abcdefghijkl}, {name: second, type: snell, port: 24002, version: 6, psk: abcdefghijkm, udp: false}]`
	input, ok := Parse(core.EngineMihomo, source)
	if !ok || input.SourceOnly {
		t.Fatal("handwritten Snell listener not editable")
	}
	input.Port = 24003
	generated, err := Generate(core.EngineMihomo, input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := MutateGenerated(core.EngineMihomo, source, generated, "first", "modify")
	if err != nil {
		t.Fatal(err)
	}
	want, err := managedDocument(core.EngineMihomo, strings.Replace(source, "24001", "24003", 1))
	if err != nil {
		t.Fatal(err)
	}
	got, err := managedDocument(core.EngineMihomo, result)
	if err != nil || !reflect.DeepEqual(got["listeners"], want["listeners"]) {
		t.Fatalf("edited listeners = %v; want %v (%v)", got["listeners"], want["listeners"], err)
	}
}

func TestNativeOutboundPatchDoesNotMaterializeFormDefaults(t *testing.T) {
	const source = `{"type":"socks","tag":"node","server":"old.example","server_port":1080,"extension":9007199254740993,"username":null,"bind_interface":"eth7"}`
	const baseline = `{"type":"socks","tag":"node","server":"old.example","server_port":1080,"version":"5","username":""}`
	updated := strings.Replace(baseline, `"old.example"`, `"new.example","explicit":null,"bind_interface":null`, 1)
	result, err := PatchNativePreset(core.EngineSingBox, source, baseline, updated)
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	expected := strings.NewReplacer(`"old.example"`, `"new.example","explicit":null`, `"eth7"`, `null`).Replace(source)
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("patched fragment = %s", result)
	}
}

func TestSpecialPresetEditsRetainNativeContent(t *testing.T) {
	for _, pair := range []struct {
		engine   core.Engine
		protocol string
	}{{core.EngineMihomo, ProtocolSudoku}, {core.EngineXray, ProtocolXHTTPFive}, {core.EngineXray, ProtocolXHTTPTLSReality}, {core.EngineShadowsocksRust, ProtocolSS2022}} {
		t.Run(string(pair.engine)+"/"+pair.protocol, func(t *testing.T) {
			protocol, _ := FindProtocol(pair.engine, pair.protocol)
			input, err := NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.Tag, input.Port = "selected", 24111
			input.XHTTPBackendPort, input.XHTTPTLSPort = 24112, 24113
			input.TLSMode, input.TLSServerName = "native", "fixture.example.test"
			input.RealityServerName = "reality.example.test"
			input.CertificatePath, input.PrivateKeyPath = "/tmp/fixture.crt", "/tmp/fixture.key"
			source, err := Generate(pair.engine, input)
			if err != nil {
				t.Fatal(err)
			}
			root, err := managedDocument(pair.engine, source)
			if err != nil {
				t.Fatal(err)
			}
			root["exact"] = json.Number("9007199254740993")
			if pair.engine == core.EngineMihomo {
				mapValue(firstMap(root["listeners"])["httpmask"])["native-extension"] = "keep-mask"
			}
			if pair.engine == core.EngineXray {
				mapValue(firstMap(root["inbounds"])["streamSettings"])["native-extension"] = "keep-primary"
				mapValue(root["inbounds"].([]any)[1])["native-extension"] = "keep-backend"
			}
			encoded, _ := json.Marshal(root)
			input.Tag, input.Port = "renamed", 24114
			generated, err := Generate(pair.engine, input)
			if err != nil {
				t.Fatal(err)
			}
			result, err := MutateGenerated(pair.engine, string(encoded), generated, "selected", "modify")
			if err != nil {
				t.Fatal(err)
			}
			for _, token := range []string{"9007199254740993", "24114", "renamed"} {
				if !strings.Contains(result, token) {
					t.Fatalf("lost %s: %s", token, result)
				}
			}
			for _, token := range []string{"keep-mask", "keep-primary", "keep-backend"} {
				if strings.Contains(string(encoded), token) && !strings.Contains(result, token) {
					t.Fatalf("lost %s: %s", token, result)
				}
			}
			if pair.engine == core.EngineShadowsocksRust {
				input.Tag, input.Port = "neighbor", 24115
				generated, err = Generate(pair.engine, input)
				if err != nil {
					t.Fatal(err)
				}
				result, err = MutateGenerated(pair.engine, result, generated, "", "add")
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(result, "9007199254740993") {
					t.Fatal("second add rounded native integer")
				}
			}
		})
	}
}

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

func TestMihomoInboundBindingSupportsOrderedFallbackExits(t *testing.T) {
	source := "listeners: [{name: first, type: http, port: 24111}]\nproxies: [{name: primary, type: direct}, {name: backup-a, type: direct}, {name: backup-b, type: direct}]\nrules: ['MATCH,DIRECT']\n"
	bound, err := BindManagedOutbound(core.EngineMihomo, source, "first", 24111, "DIRECT", ManagedState{}, "backup-a", "backup-b")
	if err != nil {
		t.Fatal(err)
	}
	root, err := managedDocument(core.EngineMihomo, bound)
	if err != nil {
		t.Fatal(err)
	}
	tag := managedBindingFallbackTag("first", 24111)
	groups, _ := root["proxy-groups"].([]any)
	var fallback map[string]any
	for _, raw := range groups {
		if managedTag(core.EngineMihomo, raw) == tag {
			fallback = mapValue(raw)
		}
	}
	if fallback == nil || fallback["type"] != "fallback" || strings.Join(stringSliceValue(fallback["proxies"]), ",") != "DIRECT,backup-a,backup-b" {
		t.Fatalf("native fallback group = %#v", fallback)
	}
	plan, err := PrepareAccounting(core.EngineMihomo, bound)
	if err != nil || !strings.Contains(plan.Content, "type: fallback") || !strings.Contains(plan.Content, "IN-NAME,first,") {
		t.Fatalf("fallback group was not independently compiled: %v\n%s", err, plan.Content)
	}
	if err := ValidateIndependentEgress(core.EngineMihomo, bound); err != nil {
		t.Fatalf("Mihomo rejected its generated native fallback group: %v", err)
	}
	reordered, err := BindManagedOutbound(core.EngineMihomo, bound, "first", 24111, "primary", ManagedState{}, "backup-b", "DIRECT", "backup-a")
	if err != nil {
		t.Fatal(err)
	}
	primary, backups := ManagedOutboundBindingTargets(core.EngineMihomo, reordered)
	if primary["first"] != "primary" || strings.Join(backups["first"], ",") != "backup-b,DIRECT,backup-a" {
		t.Fatalf("lost ordered binding: %v %v", primary, backups)
	}
	for _, tags := range [][]string{{"primary"}, {"missing"}, {"backup-b", "backup-b"}} {
		if _, err := BindManagedOutbound(core.EngineMihomo, source, "first", 24111, "primary", ManagedState{}, tags...); err == nil {
			t.Fatalf("accepted invalid fallback list %v", tags)
		}
	}
	updated, err := BindManagedOutbound(core.EngineMihomo, reordered, "first", 24111, "backup-a", ManagedState{}, "backup-b")
	if err != nil {
		t.Fatal(err)
	}
	updatedRoot, err := managedDocument(core.EngineMihomo, updated)
	if err != nil {
		t.Fatal(err)
	}
	updatedGroups, _ := updatedRoot["proxy-groups"].([]any)
	if len(updatedGroups) != 1 || !slices.Equal(stringSliceValue(mapValue(updatedGroups[0])["proxies"]), []string{"backup-a", "backup-b"}) {
		t.Fatalf("old Mihomo fallback group remained after rebinding: %#v", updatedGroups)
	}
	cleared, err := BindManagedOutbound(core.EngineMihomo, updated, "first", 24111, "primary", ManagedState{})
	if err != nil {
		t.Fatal(err)
	}
	clearedRoot, err := managedDocument(core.EngineMihomo, cleared)
	if err != nil {
		t.Fatal(err)
	}
	clearedGroups, _ := clearedRoot["proxy-groups"].([]any)
	if len(clearedGroups) != 0 {
		t.Fatalf("Mihomo fallback group remained after clearing fallback: %#v", clearedGroups)
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

func TestXrayInboundBindingUsesNativeFallbackTag(t *testing.T) {
	source := `{"inbounds":[{"tag":"first","port":24111,"protocol":"trojan"}],"outbounds":[{"tag":"primary","protocol":"freedom"},{"tag":"backup","protocol":"freedom"}],"observatory":{"subjectSelector":["primary"],"probeURL":"https://example.com/generate_204","probeInterval":"10s"}}`
	bound, err := BindManagedOutbound(core.EngineXray, source, "first", 24111, "primary", ManagedState{}, "backup")
	if err != nil {
		t.Fatal(err)
	}
	root, err := managedDocument(core.EngineXray, bound)
	if err != nil {
		t.Fatal(err)
	}
	routing := mapValue(root["routing"])
	balancers, _ := routing["balancers"].([]any)
	if len(balancers) != 1 || stringValue(mapValue(balancers[0])["fallbackTag"]) != "backup" {
		t.Fatalf("native Xray fallback balancer = %#v", balancers)
	}
	if got := ManagedOutboundBindings(core.EngineXray, bound)["first"]; got != "primary" {
		t.Fatalf("binding = %q", got)
	}
	_, backups := ManagedOutboundBindingTargets(core.EngineXray, bound)
	if got := strings.Join(backups["first"], ","); got != "backup" {
		t.Fatalf("fallbacks = %q", got)
	}
	plan, err := PrepareAccounting(core.EngineXray, bound)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := managedDocument(core.EngineXray, plan.Content)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(stringSliceValue(mapValue(compiled["observatory"])["subjectSelector"]), accountingTag(24111, "primary")) {
		t.Fatal("the routed Xray primary has no health observations")
	}
	recovered, err := AccountingUpdateSource(core.EngineXray, plan.Content, bound)
	if err != nil || strings.Contains(recovered, accountingPrefix) {
		t.Fatalf("failed to strip generated fallback rules or probes: %v\n%s", err, recovered)
	}
	compiledRules, _ := mapValue(compiled["routing"])["rules"].([]any)
	compiledBinding := false
	for _, raw := range compiledRules {
		rule := mapValue(raw)
		if stringValue(rule["balancerTag"]) == accountingTag(24111, managedBindingFallbackTag("first", 24111)) {
			compiledBinding = true
		}
	}
	if len(plan.Ports) != 1 || plan.Ports[0].Mark == 0 || !compiledBinding {
		t.Fatalf("fallback route was not port-isolated: %+v\n%s", plan.Ports, plan.Content)
	}
	if _, err := PrepareAccounting(core.EngineXray, plan.Content); err != nil {
		t.Fatalf("compiled native fallback could not be planned again: %v", err)
	}
	cleared, err := BindManagedOutbound(core.EngineXray, bound, "first", 24111, "backup", ManagedState{})
	if err != nil {
		t.Fatal(err)
	}
	clearedRoot, err := managedDocument(core.EngineXray, cleared)
	if err != nil {
		t.Fatal(err)
	}
	if len(mapValue(clearedRoot["routing"])["balancers"].([]any)) != 0 {
		t.Fatalf("Xray fallback balancer remained after clearing fallback: %#v", mapValue(clearedRoot["routing"])["balancers"])
	}
	if _, err := BindManagedOutbound(core.EngineXray, source, "first", 24111, "primary", ManagedState{}, "backup", "primary"); err == nil {
		t.Fatal("accepted duplicate primary as fallback")
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

func TestFallbackGroupsFollowInboundLifecycle(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray} {
		t.Run(string(engine), func(t *testing.T) {
			src := "listeners: [{name: first, type: http, port: 24111}]\nproxies: [{name: edge, type: direct}, {name: backup, type: direct}]\nrules: ['MATCH,DIRECT']\n"
			if engine == core.EngineXray {
				src = `{"inbounds":[{"tag":"first","port":24111,"protocol":"http"}],"outbounds":[{"tag":"edge","protocol":"freedom"},{"tag":"backup","protocol":"freedom"}],"observatory":{"subjectSelector":[]}}`
			}
			bind := func(source, name string, port int, primary string, backups ...string) string {
				t.Helper()
				result, err := BindManagedOutbound(engine, source, name, port, primary, ManagedState{}, backups...)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			src = bind(src, "first", 24111, "edge", "backup")
			// Rename and move the listener, then create a new listener with its old identity.
			if engine == core.EngineXray {
				src = strings.Replace(src, `"inbounds":[{"tag":"first","port":24111,"protocol":"http"}]`, `"inbounds":[{"tag":"moved","port":24112,"protocol":"http"},{"tag":"first","port":24111,"protocol":"http"}]`, 1)
			} else {
				src = strings.Replace(src, "[{name: first, type: http, port: 24111}]", "[{name: moved, type: http, port: 24112}, {name: first, type: http, port: 24111}]", 1)
			}
			src, err := ReconcilePresetInboundReferences(engine, src, "first", "moved")
			if err != nil {
				t.Fatal(err)
			}
			src = bind(src, "first", 24111, "backup", "edge")
			prim, fall := ManagedOutboundBindingTargets(engine, src)
			if prim["moved"] != "edge" || !slices.Equal(fall["moved"], []string{"backup"}) || prim["first"] != "backup" {
				t.Fatalf("sibling changed: %v %v", prim, fall)
			}
			src, err = ReconcilePresetInboundReferences(engine, src, "moved", "")
			if err != nil {
				t.Fatal(err)
			}
			if got := bind(src, "first", 24111, "backup", "edge"); got != src {
				t.Fatal("repeated bind changed source after deleting sibling")
			}
			cleared := bind(src, "first", 24111, "edge")
			if strings.Contains(cleared, fallbackPrefix) {
				t.Fatal("deleted or cleared listener left an orphan fallback group")
			}
		})
	}
}
