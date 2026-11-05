package serverconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestManagedRuntimeRetainsCompleteSourceAndExactNumbers(t *testing.T) {
	source := `{"unknown":{"integer":9007199254740993},"inbounds":[{"tag":"a","port":12000},{"tag":"b","port":12000,"listen":"::1"}],"outbounds":[{"tag":"default"},{"tag":"unused"}],"routing":{"rules":[{"inboundTag":["a"],"outboundTag":"unused"},{"inboundTag":["a","b"],"outboundTag":"default"},{"domain":["example.test"],"outboundTag":"default"}]}}`
	state := ManagedState{Disabled: []ManagedResource{{Section: "inbounds", Tag: "a"}, {Section: "outbounds", Tag: "unused"}}}
	runtime, err := CompileManagedConfig(core.EngineXray, source, state)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runtime, "9007199254740993") {
		t.Fatal("integer precision lost")
	}
	var result struct {
		Inbounds  []struct{ Tag string }
		Outbounds []struct{ Tag string }
		Routing   struct{ Rules []json.RawMessage }
	}
	if err = json.Unmarshal([]byte(runtime), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Inbounds) != 1 || result.Inbounds[0].Tag != "b" || len(result.Outbounds) != 1 || len(result.Routing.Rules) != 2 {
		t.Fatalf("wrong compiled scope: %s", runtime)
	}
	restored, err := CompileManagedConfig(core.EngineXray, source, ManagedState{})
	if err != nil || restored != source {
		t.Fatal("re-enabling failed to restore exact complete source")
	}
}

func TestManagedRuntimeRejectsAmbiguousAndReferencedObjects(t *testing.T) {
	source := `{"inbounds":[{"tag":"a"},{"tag":"duplicate"},{"tag":"duplicate"}],"outbounds":[{"tag":"default"},{"tag":"shared"}],"routing":{"rules":[{"outboundTag":"shared"}]}}`
	for _, ref := range []ManagedResource{{"inbounds", "missing"}, {"inbounds", "duplicate"}, {"outbounds", "default"}, {"outbounds", "shared"}, {"bad", "a"}} {
		if _, err := CompileManagedConfig(core.EngineXray, source, ManagedState{Disabled: []ManagedResource{ref}}); err == nil {
			t.Errorf("accepted unsafe state: %+v", ref)
		}
	}
	if _, err := CompileManagedConfig(core.EngineXray, source, ManagedState{Disabled: []ManagedResource{{"inbounds", "a"}, {"inbounds", "a"}}}); err == nil {
		t.Fatal("accepted duplicate identity")
	}
}

func TestManagedRuntimeAllInboundsDisabledPreservesGlobals(t *testing.T) {
	cases := []struct {
		engine          core.Engine
		source, section string
	}{
		{core.EngineXray, `{"inbounds":[{"tag":"a"}],"outbounds":[{"tag":"direct"}],"log":{"loglevel":"warning"}}`, "inbounds"},
		{core.EngineSingBox, `{"inbounds":[{"tag":"a"}],"outbounds":[{"tag":"direct","type":"direct"}],"experimental":{"cache_file":{"enabled":true}}}`, "inbounds"},
		{core.EngineMihomo, "listeners:\n  - name: a\n    type: socks\n    port: 12000\nrules: [MATCH,DIRECT]\nprofile:\n  store-selected: true\n", "listeners"},
	}
	for _, tt := range cases {
		t.Run(string(tt.engine), func(t *testing.T) {
			result, err := CompileManagedConfig(tt.engine, tt.source, ManagedState{Disabled: []ManagedResource{{tt.section, "a"}}})
			if err != nil {
				t.Fatal(err)
			}
			root, err := managedDocument(tt.engine, result)
			if err != nil || len(root[tt.section].([]any)) != 0 {
				t.Fatalf("listeners remain: %s %v", result, err)
			}
			if len(root) < 2 {
				t.Fatal("removed unrelated global settings")
			}
		})
	}
}

func TestManagedRuntimeProtectsInternalAPI(t *testing.T) {
	source := `{"api":{"tag":"api"},"inbounds":[{"tag":"api-listener","protocol":"dokodemo-door"}],"routing":{"rules":[{"inboundTag":["api-listener"],"outboundTag":"api"}]}}`
	if _, err := CompileManagedConfig(core.EngineXray, source, ManagedState{Disabled: []ManagedResource{{"inbounds", "api-listener"}}}); err == nil {
		t.Fatal("internal API listener was removed")
	}
}

func TestManagedSSRustSingleAndMultiplePorts(t *testing.T) {
	for _, source := range []string{
		"{\"id\":\"first\",\"server\":\"127.0.0.1\",\"server_port\":21111,\"password\":\"fixture\",\"method\":\"aes-256-gcm\",\"timeout\":314,\"future\":9007199254740993}",
		"{\"timeout\":314,\"future\":9007199254740993,\"servers\":[{\"id\":\"first\",\"server\":\"127.0.0.1\",\"server_port\":21111,\"password\":\"fixture\",\"method\":\"aes-256-gcm\"},{\"id\":\"second\",\"server\":\"::1\",\"server_port\":21111,\"password\":\"fixture\",\"method\":\"aes-256-gcm\"}]}",
	} {
		objects, err := ManagedObjects(core.EngineShadowsocksRust, source, ManagedState{})
		if err != nil || objects[0].Tag != "first" {
			t.Fatalf("identity: %+v %v", objects, err)
		}
		runtime, err := CompileManagedConfig(core.EngineShadowsocksRust, source, ManagedState{Disabled: []ManagedResource{{"servers", "first"}}})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(runtime, "\"first\"") || !strings.Contains(runtime, "9007199254740993") || !strings.Contains(runtime, "314") {
			t.Fatalf("source parameters lost: %s", runtime)
		}
		restored, err := CompileManagedConfig(core.EngineShadowsocksRust, source, ManagedState{})
		if err != nil || restored != source {
			t.Fatal("original single-file configuration did not restore")
		}
	}
}

func TestManagedRuntimeSharedOutboundReferencesAndExplicitDefault(t *testing.T) {
	source := "{\"inbounds\":[],\"outbounds\":[{\"type\":\"direct\",\"tag\":\"old\"},{\"type\":\"direct\",\"tag\":\"new\"},{\"type\":\"socks\",\"tag\":\"one\",\"detour\":\"two\"},{\"type\":\"direct\",\"tag\":\"two\"}],\"route\":{\"final\":\"new\"}}"
	state := ManagedState{Disabled: []ManagedResource{{"outbounds", "old"}, {"outbounds", "two"}, {"outbounds", "one"}}}
	if _, err := CompileManagedConfig(core.EngineSingBox, source, state); err != nil {
		t.Fatal(err)
	}
	state.Disabled = []ManagedResource{{"outbounds", "two"}}
	if _, err := CompileManagedConfig(core.EngineSingBox, source, state); err == nil {
		t.Fatal("shared outbound dependency was silently dropped")
	}
}

func TestManagedRuntimeRejectsOutboundChainAndBalancerReferences(t *testing.T) {
	for _, reference := range []string{
		`"proxySettings":{"tag":"egress-b","transportLayer":true}`,
		`"streamSettings":{"sockopt":{"dialerProxy":"egress-b"}}`,
	} {
		source := `{"inbounds":[],"outbounds":[{"tag":"default",` + reference + `},{"tag":"egress-b"}]}`
		if _, err := CompileManagedConfig(core.EngineXray, source, ManagedState{Disabled: []ManagedResource{{"outbounds", "egress-b"}}}); err == nil {
			t.Fatalf("accepted live outbound chain: %s", reference)
		}
	}
	source := `{"outbounds":[{"tag":"default"},{"tag":"egress-b"}],"routing":{"balancers":[{"tag":"pool","selector":["egress-"]}],"rules":[{"balancerTag":"pool"}]}}`
	if _, err := CompileManagedConfig(core.EngineXray, source, ManagedState{Disabled: []ManagedResource{{"outbounds", "egress-b"}}}); err == nil {
		t.Fatal("balancer membership changed silently")
	}
}

func TestManagedObjectListenerTransportsAndRemotePorts(t *testing.T) {
	for _, tt := range []struct {
		engine   core.Engine
		source   string
		networks []string
	}{
		{core.EngineXray, `{"inbounds":[{"tag":"http","protocol":"http","port":21000},{"tag":"quic","protocol":"vless","port":21000,"streamSettings":{"network":"quic"}}],"outbounds":[{"tag":"exit","protocol":"socks","settings":{"servers":[{"address":"example.test","port":443}]}}]}`, []string{"TCP", "UDP", ""}},
		{core.EngineSingBox, `{"inbounds":[{"tag":"http","type":"http","listen_port":21000},{"tag":"wg","type":"wireguard","listen_port":21000}]}`, []string{"TCP", "UDP"}},
		{core.EngineMihomo, "listeners: [{name: http, type: http, port: 21000}, {name: tunnel, type: tunnel, port: 21000, network: [udp]}]", []string{"TCP", "UDP"}},
		{core.EngineShadowsocksRust, `{"servers":[{"id":"one","server":"::1","server_port":21000,"mode":"udp_only"},{"id":"two","server":"127.0.0.1","server_port":21000}],"mode":"tcp_and_udp"}`, []string{"UDP", "TCP / UDP"}},
	} {
		t.Run(string(tt.engine), func(t *testing.T) {
			objects, err := ManagedObjects(tt.engine, tt.source, ManagedState{})
			if err != nil || len(objects) != len(tt.networks) {
				t.Fatalf("projection: %+v %v", objects, err)
			}
			for i, object := range objects {
				if object.Network != tt.networks[i] {
					t.Errorf("%s transport=%q want=%q", object.Tag, object.Network, tt.networks[i])
				}
				if !object.Inbound && (object.Port != 0 || object.Listen != "") {
					t.Fatal("remote port presented as local listener")
				}
			}
		})
	}
}

func TestManagedLegacyPositionalIdentityCannotMoveSilently(t *testing.T) {
	source := `{"servers":[{"server":"127.0.0.1","server_port":21001},{"server":"::1","server_port":21002}],"timeout":300}`
	state := ManagedState{Disabled: []ManagedResource{{"servers", "ss-rust-1"}}}
	if err := ValidateManagedSourceUpdate(core.EngineShadowsocksRust, source, strings.Replace(source, "300", "301", 1), state); err != nil {
		t.Fatal(err)
	}
	swapped := `{"servers":[{"server":"::1","server_port":21002},{"server":"127.0.0.1","server_port":21001}],"timeout":300}`
	if err := ValidateManagedSourceUpdate(core.EngineShadowsocksRust, source, swapped, state); err == nil {
		t.Fatal("positional disabled state retargeted another listener")
	}
	pinned := `{"servers":[{"server":"::1","server_port":21002},{"id":"ss-rust-1","server":"127.0.0.1","server_port":21001}],"timeout":300}`
	if err := ValidateManagedSourceUpdate(core.EngineShadowsocksRust, source, pinned, state); err != nil {
		t.Fatal(err)
	}
}
