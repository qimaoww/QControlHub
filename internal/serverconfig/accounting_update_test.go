package serverconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
