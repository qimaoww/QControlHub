package serverconfig

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func wireGuardEndpointFixture(t *testing.T) (Input, string) {
	t.Helper()
	protocol, ok := FindProtocol(core.EngineSingBox, ProtocolWireGuard)
	if !ok {
		t.Fatal("missing WireGuard endpoint preset")
	}
	input, err := NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	input.Tag, input.Port = "wg", 51820
	content, err := Generate(core.EngineSingBox, input)
	if err != nil {
		t.Fatal(err)
	}
	return input, content
}

func TestSingBoxEndpointPresetAccessPolicyAndDeletion(t *testing.T) {
	input, content := wireGuardEndpointFixture(t)
	policy := MainlandAccessPolicy{Engine: core.EngineSingBox, Tag: input.Tag, Port: input.Port, BlockMainlandDestination: true}
	updated, err := ApplyMainlandAccessPolicyWithPrefixes(core.EngineSingBox, content, policy, nil)
	if err != nil {
		t.Fatalf("endpoint cannot pass the preset save path: %v", err)
	}
	policies := DiscoverMainlandAccessPolicies(core.EngineSingBox, updated)
	if len(policies) != 1 || !policies[0].BlockMainlandDestination {
		t.Fatalf("endpoint policy missing: %+v", policies)
	}
	if parsed, ok := Parse(core.EngineSingBox, updated); !ok || !parsed.BlockMainlandDestination {
		t.Fatal("endpoint form lost its saved access policy")
	}
	deleted, err := DeletePresetInbound(core.EngineSingBox, content, input.Tag)
	if err != nil {
		t.Fatalf("identity-only endpoint deletion failed: %v", err)
	}
	plan, err := PrepareIndependentEgress(core.EngineSingBox, deleted)
	if err != nil || plan.Source != "disabled" || len(DiscoverTrafficPorts(core.EngineSingBox, deleted)) != 0 {
		t.Fatalf("final endpoint deletion is not deployable: %+v %v", plan.Ports, err)
	}
}

func TestSingBoxEndpointMutationChecksBothLists(t *testing.T) {
	_, current := wireGuardEndpointFixture(t)
	for name, generated := range map[string]string{
		"port": `{"inbounds":[{"tag":"web","type":"socks","listen_port":51820}]}`,
		"tag":  `{"inbounds":[{"tag":"wg","type":"socks","listen_port":1080}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := MutateGenerated(core.EngineSingBox, current, generated, "", "add"); err == nil {
				t.Fatal("inbound bypassed the cross-list identity check")
			}
		})
	}
	_, generated := wireGuardEndpointFixture(t)
	for _, current := range []string{"{}", `{"inbounds":[{"tag":"web","type":"socks","listen_port":1080}]}`} {
		if _, err := MergeGenerated(core.EngineSingBox, current, generated); err != nil {
			t.Fatalf("endpoint upsert cannot append to a document without endpoints: %v", err)
		}
	}
}

func TestSingBoxEndpointRejectsLossyPresetEdits(t *testing.T) {
	_, content := wireGuardEndpointFixture(t)
	for name, mutate := range map[string]func(map[string]any){
		"system interface": func(entry map[string]any) { entry["system"] = true },
		"custom dialer":    func(entry map[string]any) { entry["detour"] = "proxy" },
		"unknown field":    func(entry map[string]any) { entry["vendor_option"] = true },
		"remote peer": func(entry map[string]any) {
			peer := mapValue(entry["peers"].([]any)[0])
			peer["address"], peer["port"] = "192.0.2.1", 51820
		},
		"reserved peer bytes": func(entry map[string]any) {
			mapValue(entry["peers"].([]any)[0])["reserved"] = []int{1, 2, 3}
		},
		"multiple peers": func(entry map[string]any) {
			entry["peers"] = append(entry["peers"].([]any), entry["peers"].([]any)[0])
		},
	} {
		t.Run(name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal([]byte(content), &root); err != nil {
				t.Fatal(err)
			}
			mutate(mapValue(root["endpoints"].([]any)[0]))
			raw, _ := json.Marshal(root)
			custom := string(raw)
			if all := ParseAll(core.EngineSingBox, custom); len(all) != 0 {
				t.Fatal("custom endpoint was exposed as a lossless preset")
			}
			if _, err := MutateGenerated(core.EngineSingBox, custom, content, "wg", "modify"); err == nil {
				t.Fatal("preset silently overwrote a custom endpoint")
			}
		})
	}
}

func TestSingBoxEndpointAccountingUsesRealDedicatedExits(t *testing.T) {
	_, content := wireGuardEndpointFixture(t)
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "endpoint only", true: "mixed listeners"}[mixed], func(t *testing.T) {
			source := content
			wantPorts := 1
			if mixed {
				var err error
				source, err = MutateGenerated(core.EngineSingBox, source, `{"inbounds":[{"tag":"web","type":"socks","listen_port":1080}]}`, "", "add")
				if err != nil {
					t.Fatal(err)
				}
				wantPorts = 2
			}
			plan, err := PrepareIndependentEgress(core.EngineSingBox, source)
			if err != nil || plan.Source != "nft-dual" || len(plan.Ports) != wantPorts {
				t.Fatalf("endpoint accounting: %+v %v", plan.Ports, err)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(plan.Content), &root); err != nil {
				t.Fatal(err)
			}
			for _, port := range plan.Ports {
				if port.Mark == 0 || len(port.Outbounds) == 0 {
					t.Fatal("accounting reports marks without dedicated outbounds")
				}
				for _, tag := range port.Outbounds {
					found := false
					for _, raw := range root["outbounds"].([]any) {
						out := mapValue(raw)
						if out["tag"] == tag && uint32(intValue(out["routing_mark"])) == port.Mark {
							found = true
						}
					}
					if !found {
						t.Fatal("reported mark is not installed on its outbound")
					}
				}
			}
			again, err := PrepareIndependentEgress(core.EngineSingBox, plan.Content)
			if err != nil || !reflect.DeepEqual(plan, again) {
				t.Fatalf("endpoint accounting is not idempotent: %v", err)
			}
			files, err := SplitConfigFiles(core.EngineSingBox, plan.Content)
			if err != nil || len(files) != wantPorts+1 {
				t.Fatalf("endpoint has no paired source file: %v", err)
			}
			last := files[len(files)-1]
			if last.Path != "endpoints/wg.json" || !strings.Contains(last.Content, plan.Ports[len(plan.Ports)-1].Outbounds[0]) {
				t.Fatal("endpoint and its outbound were not paired")
			}
		})
	}
}

func TestSingBoxTailscaleNeverClaimsPortOnlyDualAccounting(t *testing.T) {
	content := `{"endpoints":[{"type":"tailscale","tag":"ts","listen_port":41641,"state_directory":"/var/lib/tailscale"}],"outbounds":[{"type":"direct","tag":"direct"}]}`
	if _, err := PrepareIndependentEgress(core.EngineSingBox, content); err == nil {
		t.Fatal("Tailscale relay traffic was advertised as port-attributed dual accounting")
	}
}

func TestSingBoxNullNativeListsDoNotDisableAccounting(t *testing.T) {
	for _, content := range []string{
		`{"endpoints":null}`,
		`{"inbounds":[],"endpoints":null}`,
		`{"inbounds":null,"endpoints":[]}`,
	} {
		if _, err := PrepareIndependentEgress(core.EngineSingBox, content); err == nil {
			t.Fatalf("malformed native list was accepted as an empty deployment: %s", content)
		}
	}
}
