package serverconfig

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAccountingUntaggedOutbounds(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		t.Run(string(engine), func(t *testing.T) {
			content := `{"inbounds":[{"tag":"a","port":1080,"protocol":"http"},{"tag":"b","port":1081,"protocol":"http"}],"outbounds":[{"protocol":"freedom"},{"protocol":"blackhole","tag":"qch-outbound-1"},{"protocol":"freedom","tag":""}],"routing":{"rules":[{"type":"field","domain":["blocked.test"],"outboundTag":"qch-outbound-1"}]}}`
			if engine == core.EngineSingBox {
				content = `{"inbounds":[{"tag":"a","listen_port":1080,"type":"http"},{"tag":"b","listen_port":1081,"type":"http"}],"outbounds":[{"type":"direct"},{"type":"block","tag":"qch-outbound-1"},{"type":"direct","tag":""}],"route":{"rules":[{"domain":["blocked.test"],"outbound":"qch-outbound-1"}]}}`
			}
			plan, err := PrepareAccounting(engine, content)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			_ = json.Unmarshal([]byte(plan.Content), &root)
			outs := root["outbounds"].([]any)
			for i, want := range []string{"qch-outbound-2", "qch-outbound-1", "qch-outbound-3"} {
				if got := mapValue(outs[i])["tag"]; got != want {
					t.Fatalf("outbound %d tag/order changed: %v != %s", i, got, want)
				}
			}
			if len(plan.Ports) != 2 || len(plan.Ports[0].Outbounds) != 2 || plan.Ports[0].Outbounds[0] == plan.Ports[1].Outbounds[0] {
				t.Fatalf("lost per-port attribution: %+v", plan.Ports)
			}
			again, err := PrepareAccounting(engine, plan.Content)
			if err != nil || !reflect.DeepEqual(plan, again) {
				t.Fatalf("not idempotent: %v", err)
			}
			// Preset edits must recover normalized originals and never duplicate clones.
			source, err := PresetAccountingSource(engine, plan.Content)
			if err != nil {
				t.Fatal(err)
			}
			rebuilt, err := PrepareAccounting(engine, source)
			if err != nil || !reflect.DeepEqual(plan, rebuilt) {
				t.Fatalf("preset round trip changed accounting: %v", err)
			}
			if engine == core.EngineSingBox {
				marked, err := PrepareMarkedSingBoxAccounting(content)
				if err != nil || marked.Source != "nft-dual" || marked.Ports[0].Mark == marked.Ports[1].Mark {
					t.Fatalf("marked fallback: %+v %v", marked.Ports, err)
				}
				again, err := PrepareMarkedSingBoxAccounting(marked.Content)
				if err != nil || !reflect.DeepEqual(marked, again) {
					t.Fatalf("marked fallback not idempotent: %v", err)
				}
			}
		})
	}
}

func TestAccountingOutboundTagFailures(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		for _, outs := range []string{
			`[{"tag":"same","type":"direct","protocol":"freedom"},{"tag":"same","type":"direct","protocol":"freedom"}]`,
			`[{"tag":"same","type":"direct","protocol":"freedom"},{"tag":"same","type":"block","protocol":"blackhole"}]`,
			`[{"tag":12}]`, `[{"tag":null}]`, `[null]`, `[]`,
		} {
			content := `{"inbounds":[{"tag":"a","port":1080,"listen_port":1080}],"outbounds":` + outs + `}`
			if _, err := PrepareAccounting(engine, content); err == nil {
				t.Fatalf("accepted ambiguous/invalid %s %s", engine, outs)
			} else if strings.Contains(outs, "same") && !strings.Contains(err.Error(), "outbounds[1].tag duplicates") {
				t.Fatalf("no actionable duplicate location: %v", err)
			}
		}
		// Auto-generated names must not turn a dangling route into a valid one.
		content := `{"inbounds":[{"tag":"a","port":1080,"listen_port":1080}],"outbounds":[{"protocol":"freedom","type":"direct"}],"routing":{"rules":[{"outboundTag":"qch-outbound-1"}]},"route":{"rules":[{"outbound":"qch-outbound-1"}]}}`
		if _, err := PrepareAccounting(engine, content); err == nil || !strings.Contains(err.Error(), "unknown route target") {
			t.Fatalf("captured dangling route: %v", err)
		}
	}
}

func TestProtocolOutboundsHaveIndependentIdentityAndMarks(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		for _, protocol := range []string{"vless", "vmess", "trojan", "shadowsocks", "socks", "http"} {
			t.Run(string(engine)+"/"+protocol, func(t *testing.T) {
				kind, mux := "protocol", "mux"
				if engine == core.EngineSingBox {
					kind, mux = "type", "multiplex"
				}
				out := map[string]any{kind: protocol, "server": "192.0.2.10", "password": "fixture-only", mux: map[string]any{"enabled": false}}
				root := map[string]any{"inbounds": []any{map[string]any{"tag": "a", "port": 443, "listen_port": 443}, map[string]any{"tag": "b", "port": 8443, "listen_port": 8443}}, "outbounds": []any{out}}
				data, _ := json.Marshal(root)
				plan, err := PrepareAccounting(engine, string(data))
				if engine == core.EngineSingBox {
					plan, err = PrepareMarkedSingBoxAccounting(string(data))
				}
				if err != nil {
					t.Fatal(err)
				}
				if plan.Source != "nft-dual" {
					t.Fatal("protocol exit must use verified per-port socket marks in this test")
				}
				var again AccountingPlan
				if engine == core.EngineXray {
					again, err = PrepareAccounting(engine, plan.Content)
				} else {
					again, err = PrepareMarkedSingBoxAccounting(plan.Content)
				}
				if err != nil || !reflect.DeepEqual(plan, again) {
					t.Fatalf("protocol marks not idempotent: %v", err)
				}
				_ = json.Unmarshal([]byte(plan.Content), &root)
				for _, entry := range plan.Ports {
					if len(entry.Outbounds) != 1 {
						t.Fatalf("missing protocol exit: %+v", entry)
					}
					found := false
					for _, raw := range root["outbounds"].([]any) {
						clone := mapValue(raw)
						if clone["tag"] == entry.Outbounds[0] {
							found = true
							if clone[kind] != protocol || clone["password"] != out["password"] || mapValue(clone[mux])["enabled"] != false {
								t.Fatalf("protocol options changed: %s", protocol)
							}
							if engine == core.EngineSingBox && intValue(clone["routing_mark"]) != int(entry.Mark) {
								t.Fatal("protocol exit not marked")
							}
							if engine == core.EngineXray && intValue(mapValue(mapValue(clone["streamSettings"])["sockopt"])["mark"]) != int(entry.Mark) {
								t.Fatal("Xray protocol exit not marked")
							}
						}
					}
					if !found {
						t.Fatal("independent protocol exit missing")
					}
				}
				if plan.Ports[0].Outbounds[0] == plan.Ports[1].Outbounds[0] || engine == core.EngineSingBox && plan.Ports[0].Mark == plan.Ports[1].Mark {
					t.Fatal("protocol outbounds shared between ports")
				}
				out[mux] = map[string]any{"enabled": true}
				root["outbounds"] = []any{out}
				data, _ = json.Marshal(root)
				if _, err := PrepareAccounting(engine, string(data)); err == nil {
					t.Fatal("enabled multiplexing accepted")
				}
			})
		}
	}
}

func TestXrayProtocolMarksMigrateAndRejectConflicts(t *testing.T) {
	content := `{"inbounds":[{"tag":"a","port":443,"protocol":"vless"}],"outbounds":[{"tag":"proxy","protocol":"vless","streamSettings":{"network":"tcp","sockopt":{"tcpFastOpen":true}}},{"tag":"direct","protocol":"freedom"}]}`
	plan, err := PrepareAccounting(core.EngineXray, content)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = json.Unmarshal([]byte(plan.Content), &root)
	for _, raw := range root["outbounds"].([]any) {
		out := mapValue(raw)
		if strings.HasPrefix(stringValue(out["tag"]), accountingPrefix) {
			stream := mapValue(out["streamSettings"])
			sockopt := mapValue(stream["sockopt"])
			if intValue(sockopt["mark"]) != int(plan.Ports[0].Mark) {
				t.Fatal("mixed direct and protocol exits must both be marked")
			}
			// Reconstruct the exact unmarked layout emitted by the previous Agent.
			delete(sockopt, "mark")
			if len(sockopt) == 0 {
				delete(stream, "sockopt")
			}
			if len(stream) == 0 {
				delete(out, "streamSettings")
			}
		}
	}
	legacy, _ := json.Marshal(root)
	migrated, err := PrepareAccounting(core.EngineXray, string(legacy))
	if err != nil || !reflect.DeepEqual(plan, migrated) {
		t.Fatalf("legacy protocol exits did not migrate: %v", err)
	}
	source, err := AccountingUpdateSource(core.EngineXray, string(legacy), string(legacy))
	if err != nil {
		t.Fatal(err)
	}
	migrated, err = PrepareAccounting(core.EngineXray, source)
	if err != nil || !reflect.DeepEqual(plan, migrated) {
		t.Fatalf("legacy editor update changed routes or marks: %v", err)
	}
	conflict := strings.Replace(content, `"tcpFastOpen":true`, `"mark":123`, 1)
	if _, err := PrepareAccounting(core.EngineXray, conflict); err == nil || !strings.Contains(err.Error(), "custom outbound socket mark") {
		t.Fatalf("custom policy-routing mark overwritten: %v", err)
	}
	tampered := strings.Replace(plan.Content, `"mark": 1363345851`, `"mark": 123`, 1)
	if tampered == plan.Content {
		t.Fatal("test did not mutate a generated mark")
	}
	if _, err := PrepareAccounting(core.EngineXray, tampered); err == nil {
		t.Fatal("accepted tampered generated mark")
	}
}
