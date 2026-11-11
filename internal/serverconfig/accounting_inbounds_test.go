package serverconfig

import (
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAccountingNormalizesImportedInbounds(t *testing.T) {
	for engine, content := range map[core.Engine]string{
		core.EngineXray:    `{"inbounds":[{"port":21001,"protocol":"socks","settings":{"auth":"password","accounts":[{"user":"fixture","pass":"fixture"}]}},{"tag":"kept","port":"21002-21003,21005","protocol":"http"}],"outbounds":[{"protocol":"freedom"},{"tag":"block","protocol":"blackhole"}],"routing":{"rules":[{"type":"field","inboundTag":["kept"],"domain":["blocked.test"],"outboundTag":"block"}]},"opaque":{"large":9007199254740993}}`,
		core.EngineSingBox: `{"inbounds":[{"type":"http","listen_port":21001},{"tag":"kept","type":"socks","listen_port":21002}],"outbounds":[{"type":"direct"}]}`,
		core.EngineMihomo:  "listeners: [{type: http, port: 21001}, {name: kept, type: socks, port: 21002}]\nrules: ['MATCH,DIRECT']\n",
	} {
		t.Run(string(engine), func(t *testing.T) {
			plan, err := PrepareIndependentEgress(engine, content)
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if engine == core.EngineXray {
				want = 4
			}
			if len(plan.Ports) != want {
				t.Fatalf("ports: %+v", plan.Ports)
			}
			seen := map[string]bool{}
			for _, p := range plan.Ports {
				if !tagPattern.MatchString(p.Inbound) || seen[p.Inbound] || len(p.Outbounds) == 0 {
					t.Fatalf("invalid ownership: %+v", p)
				}
				seen[p.Inbound] = true
			}
			if plan.Ports[1].Inbound != "kept" {
				t.Fatal("valid identity changed")
			}
			again, err := PrepareIndependentEgress(engine, plan.Content)
			if err != nil || !reflect.DeepEqual(plan, again) {
				t.Fatalf("reimport changed plan: %v", err)
			}
			source, err := PresetAccountingSource(engine, plan.Content)
			if err != nil {
				t.Fatal(err)
			}
			edited, err := PlanPresetAccounting(engine, source)
			if err != nil || !reflect.DeepEqual(plan, edited) {
				t.Fatalf("preset round trip: %v", err)
			}
			if engine == core.EngineXray {
				if !strings.Contains(plan.Content, "9007199254740993") {
					t.Fatal("lost original precision")
				}
				root, _ := decodeAccountingRoot(engine, plan.Content)
				rules := mapValue(root["routing"])["rules"].([]any)
				for _, p := range plan.Ports[1:] {
					if !accountingRuleMatches(mapValue(rules[1])["inboundTag"], p.Inbound) {
						t.Fatalf("lost split inbound policy: %+v", p)
					}
				}
			}
		})
	}
}

func TestAccountingInboundDiagnostics(t *testing.T) {
	for _, tt := range []struct{ inbounds, field string }{
		{`[{"port":"unknown","tag":"a"}]`, "inbounds[0].port"},
		{`[{"port":21001,"tag":"a"},{"port":21001,"tag":"b","listen":"127.0.0.2"}]`, "inbounds[1].port"},
		{`[{"port":21001,"tag":12}]`, "inbounds[0].tag"},
	} {
		_, err := PrepareIndependentEgress(core.EngineXray, `{"inbounds":`+tt.inbounds+`,"outbounds":[{"protocol":"freedom"}]}`)
		if err == nil || !strings.Contains(err.Error(), tt.field) {
			t.Fatalf("missing field diagnosis %s: %v", tt.field, err)
		}
	}
}

func TestAccountingInboundReferencesAndCollisions(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo} {
		t.Run(string(engine), func(t *testing.T) {
			source := `{"inbounds":[{"tag":"same","port":21001,"protocol":"http"},{"tag":"same","port":21002,"protocol":"http"},{"port":21003,"protocol":"http"}],"outbounds":[{"tag":"qch-inbound-21003","protocol":"freedom"}]}`
			if engine == core.EngineSingBox {
				source = `{"inbounds":[{"tag":"same","type":"http","listen_port":21001},{"tag":"same","type":"http","listen_port":21002},{"type":"http","listen_port":21003}],"outbounds":[{"tag":"qch-inbound-21003","type":"direct"}]}`
			}
			if engine == core.EngineMihomo {
				source = "listeners: [{name: same, type: http, port: 21001}, {name: same, type: http, port: 21002}, {type: http, port: 21003}]\nrules: ['IN-NAME,qch-inbound-21003,DIRECT', 'MATCH,DIRECT']\n"
			}
			plan, err := PrepareIndependentEgress(engine, source)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Ports[0].Inbound != "same" || plan.Ports[1].Inbound == "same" || plan.Ports[2].Inbound == "qch-inbound-21003" {
				t.Fatalf("identity collision: %+v", plan.Ports)
			}
		})
	}
	source := `{"inbounds":[{"tag":"same","port":21001},{"tag":"same","port":21002}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","inboundTag":["same"],"domain":["policy.test"],"outboundTag":"direct"}]}}`
	plan, err := PrepareIndependentEgress(core.EngineXray, source)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := decodeAccountingRoot(core.EngineXray, plan.Content)
	rules := mapValue(root["routing"])["rules"].([]any)
	for _, raw := range rules {
		rule := mapValue(raw)
		if stringValue(rule["outboundTag"]) == "direct" && rule["domain"] != nil {
			for _, p := range plan.Ports {
				if !accountingRuleMatches(rule["inboundTag"], p.Inbound) {
					t.Fatal("duplicate-tag policy lost a listener")
				}
			}
		}
	}
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		content := `{"inbounds":[{"tag":"原始 tag","port":21001,"listen_port":21001}],"outbounds":[{"protocol":"freedom","type":"direct","tag":"direct"}],"routing":{"rules":[{"type":"field","inboundTag":["原始 tag"],"domain":["policy.test"],"outboundTag":"direct"}]},"route":{"rules":[{"inbound":["原始 tag"],"outbound":"direct"}]},"dns":{"rules":[{"inbound":"原始 tag","server":"local"}]}}`
		plan, err := PrepareIndependentEgress(engine, content)
		if err != nil {
			t.Fatal(err)
		}
		root, _ := decodeAccountingRoot(engine, plan.Content)
		accountingInboundSelectors(engine, root, func(rule map[string]any, key string) {
			if accountingRuleMatches(rule[key], "原始 tag") {
				t.Fatalf("stale %s selector: %+v", engine, rule)
			}
		})
	}
}

func TestAccountingRejectsAmbiguousInboundReferences(t *testing.T) {
	for _, content := range []string{
		`{"inbounds":[{"tag":"same","type":"http","listen_port":21001},{"tag":"same","type":"http","listen_port":21002}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"rules":[{"inbound":"same","outbound":"direct"}]}}`,
		`{"inbounds":[{"type":"http","listen_port":21001}],"outbounds":[{"type":"direct","tag":"direct"}],"dns":{"rules":[{"inbound":"","server":"local"}]}}`,
	} {
		if _, err := PrepareIndependentEgress(core.EngineSingBox, content); err == nil || !strings.Contains(err.Error(), "inbounds[0].tag") {
			t.Fatalf("ambiguous inbound accepted: %v", err)
		}
	}
}

func TestAccountingPreservesXrayDNSInboundTags(t *testing.T) {
	content := `{"dns":{"tag":"global DNS","servers":["192.0.2.53",{"address":"192.0.2.54","tag":"server DNS"}]},"inbounds":[{"tag":"global DNS","port":21001,"protocol":"http"},{"tag":"server DNS","port":21002,"protocol":"http"}],"outbounds":[{"tag":"default","protocol":"freedom"},{"tag":"policy","protocol":"freedom"}],"routing":{"rules":[{"type":"field","inboundTag":["global DNS","server DNS"],"outboundTag":"policy"}]}}`
	plan, err := PrepareIndependentEgress(core.EngineXray, content)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := decodeAccountingRoot(core.EngineXray, plan.Content)
	rules := mapValue(root["routing"])["rules"].([]any)
	for _, tag := range []string{"global DNS", "server DNS"} {
		matched := false
		for _, raw := range rules {
			rule := mapValue(raw)
			if rule["outboundTag"] == "policy" && accountingRuleMatches(rule["inboundTag"], tag) {
				matched = true
			}
		}
		if !matched {
			t.Fatalf("DNS requests tagged %q lost their original exit", tag)
		}
	}
	source, err := PresetAccountingSource(core.EngineXray, plan.Content)
	if err != nil {
		t.Fatal(err)
	}
	again, err := PlanPresetAccounting(core.EngineXray, source)
	if err != nil || !reflect.DeepEqual(plan, again) {
		t.Fatalf("DNS routing changed after saving: %v", err)
	}
}

func TestAccountingRejectsOversizedExpandedImport(t *testing.T) {
	content := `{"inbounds":[{"port":"21001-21200","protocol":"http","settings":{"accounts":[{"user":"fixture","pass":"` + strings.Repeat("x", 12000) + `"}]}}],"outbounds":[{"protocol":"freedom"}]}`
	if err := core.ValidateConfig(core.EngineXray, content); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareIndependentEgress(core.EngineXray, content); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expanded import exceeded the save limit without rejection: %v", err)
	}
}
