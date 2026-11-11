package serverconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAccountingSourceFromRevisionRecoversMarkedSingBoxRuntime(t *testing.T) {
	const source = `{"log":{"level":"warn"},"inbounds":[{"type":"http","tag":"listener","listen":"127.0.0.1","listen_port":21001}],"outbounds":[{"type":"direct","tag":"original"}],"route":{"rules":[{"domain":["example.test"],"outbound":"original"}]}}`
	apiPlan, err := PrepareAccounting(core.EngineSingBox, source)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := PrepareMarkedSingBoxAccounting(apiPlan.Content)
	if err != nil || apiPlan.Source != "core-api" || runtime.Source != "nft-dual" {
		t.Fatalf("expected alternate runtime backends: %v", err)
	}
	// Legacy source saves could retain the Agent's marked runtime copies after
	// changing a listener port. The archive still holds the core-API source.
	stale := strings.Replace(runtime.Content, `"listen_port": 21001`, `"listen_port": 22001`, 1)
	if _, err := PrepareIndependentEgress(core.EngineSingBox, stale); err == nil {
		t.Fatal("stale runtime was already valid")
	}
	for _, revision := range []string{source, apiPlan.Content} {
		recovered, err := AccountingSourceFromRevision(core.EngineSingBox, stale, revision)
		if err != nil {
			t.Fatalf("verified alternate runtime was not recovered: %v", err)
		}
		if strings.Contains(recovered, accountingPrefix) || strings.Contains(recovered, "routing_mark") {
			t.Fatal("recovered source retained generated exits")
		}
		plan, err := PrepareAccounting(core.EngineSingBox, recovered)
		if err != nil || len(plan.Ports) != 1 || plan.Ports[0].Port != 22001 || !strings.Contains(recovered, "example.test") {
			t.Fatalf("recovery changed the edited listener or original routes: %v", err)
		}
		for _, corrupted := range []string{
			strings.Replace(stale, `"routing_mark":`, `"unexpected": true, "routing_mark":`, 1),
			strings.ReplaceAll(stale, `"action": "route"`, `"action": "route", "domain": ["untrusted.test"]`),
		} {
			if _, err := AccountingSourceFromRevision(core.EngineSingBox, corrupted, revision); err == nil {
				t.Fatal("recovery trusted an edited generated object")
			}
		}
	}
}

// A complete submitted plan must be recoverable from a broken saved source.
func TestAccountingUpdateRepairsInvalidPreviousFromVerifiedSubmission(t *testing.T) {
	const source = `{"inbounds":[{"type":"http","tag":"a","listen_port":21001}],"outbounds":[{"type":"direct","tag":"exit","bind_interface":"lo"}],"route":{"final":"exit","rules":[{"domain":["example.test"],"outbound":"exit"}]}}`
	for _, marked := range []bool{false, true} {
		prepare := func(content string) (AccountingPlan, error) { return PrepareAccounting(core.EngineSingBox, content) }
		if marked {
			prepare = PrepareMarkedSingBoxAccounting
		}
		plan, err := prepare(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, previous := range []string{`{}`, `{"inbounds":[]}`, strings.Replace(plan.Content, `"tag": "exit"`, `"tag": "missing"`, 1)} {
			updated, err := AccountingUpdateSource(core.EngineSingBox, plan.Content, previous)
			if err != nil {
				t.Fatalf("valid current plan blocked by invalid previous: %v", err)
			}
			if strings.Contains(updated, accountingPrefix) {
				t.Fatal("generated copies retained in editable source")
			}
			again, err := prepare(updated)
			if err != nil || again.Content != plan.Content {
				t.Fatalf("repair changed routing semantics: %v", err)
			}
		}
		edited := strings.ReplaceAll(plan.Content, `"bind_interface": "lo"`, `"bind_interface": "eth0"`)
		edited = strings.Replace(edited, `"bind_interface": "eth0"`, `"bind_interface": "lo"`, 1)
		if _, err := AccountingUpdateSource(core.EngineSingBox, edited, `{}`); err == nil {
			t.Fatal("trusted an edited generated outbound")
		}
	}
}

func TestAccountingUpdateRegeneratesOriginalEdits(t *testing.T) {
	for engine, content := range map[core.Engine]string{
		core.EngineXray:    `{"inbounds":[{"tag":"a","port":1080,"protocol":"socks"}],"outbounds":[{"tag":"direct","protocol":"freedom","settings":{"domainStrategy":"AsIs"}}]}`,
		core.EngineSingBox: `{"inbounds":[{"tag":"a","listen_port":1080,"type":"socks"}],"outbounds":[{"tag":"direct","type":"direct","bind_interface":"eth0"}]}`,
		core.EngineMihomo:  "listeners: [{name: a, type: socks, port: 1080}]\nproxies: [{name: exit, type: direct, interface-name: eth0}]\nrules: ['MATCH,exit']\n",
	} {
		t.Run(string(engine), func(t *testing.T) {
			plan, err := PrepareAccounting(engine, content)
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := AccountingUpdateSource(engine, plan.Content, `{}`)
			if err != nil {
				t.Fatal(err)
			}
			rebuilt, err := PrepareAccounting(engine, recovered)
			if err != nil || rebuilt.Content != plan.Content {
				t.Fatalf("broken source recovery changed plan: %v", err)
			}
			from, to := "eth0", "eth1"
			if engine == core.EngineXray {
				from, to = "AsIs", "UseIP"
			}
			edited := strings.Replace(plan.Content, from, to, 1)
			source, err := AccountingUpdateSource(engine, edited, plan.Content)
			if err != nil {
				t.Fatal(err)
			}
			updated, err := PrepareAccounting(engine, source)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(updated.Content, from) || strings.Count(updated.Content, to) != 2 {
				t.Fatalf("stale clone retained: %s", updated.Content)
			}
			// Editing generated copies is never silently ignored.
			if _, err := AccountingUpdateSource(engine, strings.ReplaceAll(plan.Content, from, to), plan.Content); err == nil {
				t.Fatal("accepted edited generated copy")
			}
		})
	}
}

func TestAccountingUpdateRemovesObsoleteInboundAndRoutes(t *testing.T) {
	content := `{"inbounds":[{"tag":"a","listen_port":1080,"type":"socks"},{"tag":"b","listen_port":1081,"type":"socks"}],"outbounds":[{"tag":"direct","type":"direct","bind_interface":"eth0"}],"route":{"rules":[{"domain":["old.example"],"outbound":"direct"}]}}`
	for _, marked := range []bool{false, true} {
		prepare := func(value string) (AccountingPlan, error) { return PrepareAccounting(core.EngineSingBox, value) }
		if marked {
			prepare = PrepareMarkedSingBoxAccounting
		}
		plan, err := prepare(content)
		if err != nil {
			t.Fatal(err)
		}
		var root map[string]any
		decoder := json.NewDecoder(strings.NewReader(plan.Content))
		decoder.UseNumber()
		if err := decoder.Decode(&root); err != nil {
			t.Fatal(err)
		}
		root["inbounds"] = root["inbounds"].([]any)[:1]
		for _, raw := range mapValue(root["route"])["rules"].([]any) {
			rule := mapValue(raw)
			if rule["outbound"] == "direct" {
				rule["domain"] = []string{"new.example"}
			}
		}
		data, _ := json.Marshal(root)
		source, err := AccountingUpdateSource(core.EngineSingBox, string(data), plan.Content)
		if err != nil {
			t.Fatal(err)
		}
		updated, err := prepare(source)
		if err != nil {
			t.Fatal(err)
		}
		if len(updated.Ports) != 1 || strings.Contains(updated.Content, "qch-trf-1081-") || strings.Contains(updated.Content, "old.example") || strings.Count(updated.Content, "new.example") != 2 {
			t.Fatalf("stale generated routing: %s", updated.Content)
		}
		if _, err := prepare(updated.Content); err != nil {
			t.Fatalf("updated plan is not idempotent: %v", err)
		}
	}
}

func TestSSRustSourceEditReplansVerifiedMarks(t *testing.T) {
	for _, content := range []string{
		`{"id":"a","server":"0.0.0.0","server_port":1080,"method":"aes-256-gcm","password":"secret"}`,
		`{"dns":"1.1.1.1","servers":[{"id":"a","server_port":1080,"method":"aes-256-gcm","password":"secret"},{"id":"b","server_port":1081,"method":"aes-256-gcm","password":"secret"}]}`,
	} {
		plan, err := PlanPresetAccounting(core.EngineShadowsocksRust, content)
		if err != nil {
			t.Fatal(err)
		}
		edited := strings.Replace(plan.Content, `"server_port": 1080`, `"server_port": 2080`, 1)
		source, err := AccountingUpdateSource(core.EngineShadowsocksRust, edited, plan.Content)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(source, "outbounds") || strings.Contains(source, "outbound_fwmark") {
			t.Fatal("source contains leftover generated artifacts")
		}
		updated, err := PlanPresetAccounting(core.EngineShadowsocksRust, source)
		if err != nil || updated.Ports[0].Port != 2080 || updated.Ports[0].Mark == plan.Ports[0].Mark {
			t.Fatalf("port edit: %+v %v", updated.Ports, err)
		}
		for _, mark := range []string{`123`, `"1363346488"`, `4294967296`, `-1`, `1.5`} {
			var root map[string]any
			if err := json.Unmarshal([]byte(plan.Content), &root); err != nil {
				t.Fatal(err)
			}
			root["outbound_fwmark"] = json.RawMessage(mark)
			data, _ := json.Marshal(root)
			if _, err := AccountingUpdateSource(core.EngineShadowsocksRust, string(data), plan.Content); err == nil {
				t.Fatalf("accepted edited mark %s", mark)
			}
		}
	}
}

func TestAccountingUpdateRetainsEditedSingBoxStatsAPI(t *testing.T) {
	plan, err := PrepareAccounting(core.EngineSingBox, `{"inbounds":[{"type":"socks","tag":"a","listen_port":21001}],"outbounds":[{"type":"direct","tag":"direct"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	root, err := decodeAccountingRoot(core.EngineSingBox, plan.Content)
	if err != nil {
		t.Fatal(err)
	}
	stats := mapValue(mapValue(mapValue(root["experimental"])["v2ray_api"])["stats"])
	stats["users"] = []any{"preserve-custom-user"}
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	source, err := AccountingUpdateSource(core.EngineSingBox, string(data), plan.Content)
	if err != nil || !strings.Contains(source, "preserve-custom-user") || !strings.Contains(source, "v2ray_api") || strings.Contains(source, accountingPrefix) {
		t.Fatalf("edited stats API was discarded or retained generated exits: %s %v", source, err)
	}
}

func TestAccountingRuntimeRoundTripAgainstEditableSource(t *testing.T) {
	for engine, source := range map[core.Engine]string{
		core.EngineMihomo:  "listeners: [{name: a, type: socks, port: 1080}]\nproxies: [{name: edge, type: socks5, server: 127.0.0.1, port: 1081}]\nrules: ['MATCH,edge']\n",
		core.EngineXray:    `{"inbounds":[{"tag":"a","port":1080,"protocol":"socks"}],"outbounds":[{"tag":"edge","protocol":"freedom"}]}`,
		core.EngineSingBox: `{"inbounds":[{"tag":"a","listen_port":1080,"type":"socks"}],"outbounds":[{"tag":"edge","type":"direct"}]}`,
	} {
		t.Run(string(engine), func(t *testing.T) {
			plan, err := PrepareAccounting(engine, source)
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := AccountingUpdateSource(engine, plan.Content, source)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(recovered, accountingPrefix) {
				t.Fatal("runtime copies entered source")
			}
			again, err := PrepareAccounting(engine, recovered)
			if err != nil || again.Content != plan.Content {
				t.Fatalf("runtime changed: %v", err)
			}
			edited := strings.Replace(plan.Content, accountingPrefix, accountingPrefix+"unknown-", 1)
			if _, err := AccountingUpdateSource(engine, edited, source); err == nil {
				t.Fatal("unverified artifact trusted")
			}
		})
	}
}
