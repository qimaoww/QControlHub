package serverconfig

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func boundAccountingFixture(engine core.Engine, protocol string) string {
	root := map[string]any{
		"inbounds": []any{
			map[string]any{"tag": "a", "port": 443, "listen_port": 443},
			map[string]any{"tag": "b", "port": 8443, "listen_port": 8443},
		},
	}
	if engine == core.EngineXray {
		root["outbounds"] = []any{map[string]any{"tag": "direct", "protocol": "freedom"}, map[string]any{"tag": "exit", "protocol": protocol}}
		root["routing"] = map[string]any{"rules": []any{
			map[string]any{"type": "field", "domain": []string{"policy.test"}, "outboundTag": "direct"},
			map[string]any{"type": "field", "inboundTag": []string{"a"}, "outboundTag": "exit"},
			map[string]any{"type": "field", "inboundTag": []string{"b"}, "outboundTag": "exit"},
		}}
	} else {
		root["outbounds"] = []any{map[string]any{"tag": "direct", "type": "direct"}, map[string]any{"tag": "exit", "type": protocol}}
		root["route"] = map[string]any{"final": "direct", "rules": []any{
			map[string]any{"domain": []string{"policy.test"}, "outbound": "direct"},
			map[string]any{"inbound": "a", "outbound": "exit"},
			map[string]any{"action": "route", "inbound": []string{"b"}, "outbound": "exit"},
		}}
	}
	data, _ := json.Marshal(root)
	return string(data)
}

func TestBoundOutboundsAlwaysHaveIndependentMarks(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		direct := "freedom"
		if engine == core.EngineSingBox {
			direct = "direct"
		}
		for _, protocol := range []string{direct, "vless"} {
			t.Run(string(engine)+"/"+protocol, func(t *testing.T) {
				plan, err := PrepareAccounting(engine, boundAccountingFixture(engine, protocol))
				if err != nil {
					t.Fatal(err)
				}
				if plan.Source != "nft-dual" || len(plan.Ports) != 2 || plan.Ports[0].Mark != 0x514301bb || plan.Ports[1].Mark != 0x514320fb {
					t.Fatalf("binding did not require per-port marks: %+v", plan)
				}
				root, err := decodeAccountingRoot(engine, plan.Content)
				if err != nil {
					t.Fatal(err)
				}
				for _, port := range plan.Ports {
					if len(port.Outbounds) != 2 {
						t.Fatal("policy and fallback exits must both retain attribution")
					}
					for _, tag := range port.Outbounds {
						found := false
						for _, raw := range root["outbounds"].([]any) {
							out := mapValue(raw)
							if out["tag"] != tag {
								continue
							}
							mark := out["routing_mark"]
							if engine == core.EngineXray {
								mark = mapValue(mapValue(out["streamSettings"])["sockopt"])["mark"]
							}
							if intValue(mark) != int(port.Mark) {
								t.Fatal("actual exit is missing its port mark")
							}
							found = true
						}
						if !found {
							t.Fatal("accounting plan references a missing exit")
						}
					}
				}
				if engine == core.EngineSingBox && (plan.API != "" || mapValue(root["experimental"])["v2ray_api"] != nil) {
					t.Fatal("bound sing-box exit still depends on an optional native API")
				}
				again, err := PrepareAccounting(engine, plan.Content)
				if err != nil || !reflect.DeepEqual(plan, again) {
					t.Fatalf("redeploy changed binding marks: %v", err)
				}
				source, err := PresetAccountingSource(engine, plan.Content)
				if err != nil {
					t.Fatal(err)
				}
				rebuilt, err := PlanPresetAccounting(engine, source)
				if err != nil || !reflect.DeepEqual(plan, rebuilt) {
					t.Fatalf("editing lost binding marks: %v", err)
				}
				// Generated copies may remain in an edited source request;
				// only the original port changes before the server replans.
				portKey := `"port": 443`
				if engine == core.EngineSingBox {
					portKey = `"listen_port": 443`
				}
				edited := strings.Replace(plan.Content, portKey, strings.Replace(portKey, "443", "9443", 1), 1)
				source, err = AccountingUpdateSource(engine, edited, plan.Content)
				if err != nil {
					t.Fatal(err)
				}
				updated, err := PlanPresetAccounting(engine, source)
				if err != nil || updated.Ports[0].Port != 9443 || updated.Ports[0].Mark != 0x514324e3 ||
					updated.Ports[1].Mark != plan.Ports[1].Mark || strings.Contains(updated.Content, "qch-trf-443-") {
					t.Fatalf("port edit left stale marks: %+v %v", updated.Ports, err)
				}
			})
		}
	}
}

func TestBoundOutboundMarksRejectConflicts(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		protocol := "freedom"
		if engine == core.EngineSingBox {
			protocol = "direct"
		}
		t.Run(string(engine), func(t *testing.T) {
			source := boundAccountingFixture(engine, protocol)
			plan, err := PrepareAccounting(engine, source)
			if err != nil {
				t.Fatal(err)
			}
			for _, generated := range []bool{false, true} {
				content := source
				if generated {
					content = plan.Content
				}
				root, _ := decodeAccountingRoot(engine, content)
				outs := root["outbounds"].([]any)
				out := mapValue(outs[0])
				if generated {
					out = mapValue(outs[len(outs)-1])
				}
				if engine == core.EngineXray {
					out["streamSettings"] = map[string]any{"sockopt": map[string]any{"mark": 123}}
				} else {
					out["routing_mark"] = 123
				}
				data, _ := json.Marshal(root)
				if _, err := PrepareAccounting(engine, string(data)); err == nil {
					t.Fatal("custom or tampered mark was silently overwritten")
				}
				if generated {
					if _, err := AccountingUpdateSource(engine, string(data), plan.Content); err == nil {
						t.Fatal("source edit accepted a changed generated mark")
					}
				}
			}
		})
	}
}

func TestConditionalAndGeneratedRoutesDoNotForceMarks(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		source := `{"inbounds":[{"tag":"a","port":443,"listen_port":443}],"outbounds":[{"tag":"direct","protocol":"freedom","type":"direct"}],"routing":{"rules":[{"type":"field","inboundTag":["a"],"domain":["policy.test"],"outboundTag":"direct"}]},"route":{"rules":[{"inbound":["a"],"domain":["policy.test"],"outbound":"direct"}]}}`
		plan, err := PrepareAccounting(engine, source)
		if err != nil || plan.Source != "core-api" || plan.Ports[0].Mark != 0 {
			t.Fatalf("conditional route changed unrelated accounting: %+v %v", plan, err)
		}
		again, err := PrepareAccounting(engine, plan.Content)
		if err != nil || !reflect.DeepEqual(plan, again) {
			t.Fatalf("generated fallbacks mistaken for explicit binding: %v", err)
		}
	}
}
