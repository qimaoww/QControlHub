package serverconfig

import (
	"github.com/qimaoww/qcontrolhub/internal/core"
	"strings"
	"testing"
)

func TestCustomXrayAPIAccounting(t *testing.T) {
	content := `{"api":{"tag":"api","services":["HandlerService"]},"inbounds":[{"tag":"api","listen":"127.0.0.1","port":40152,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1"}},{"tag":"proxy","port":8443,"protocol":"vless"}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","inboundTag":["api"],"outboundTag":"api"}]}}`
	plan, err := PrepareAccounting(core.EngineXray, content)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Ports) != 1 || plan.Ports[0].Port != 8443 {
		t.Fatalf("API billed: %+v", plan.Ports)
	}
	if !strings.Contains(plan.Content, "HandlerService") || !strings.Contains(plan.Content, "StatsService") {
		t.Fatal("API services not preserved")
	}
	next, err := PrepareAccounting(core.EngineXray, plan.Content)
	if err != nil || next.Content != plan.Content {
		t.Fatalf("not idempotent: %v", err)
	}
	endpoints := DiscoverTrafficPorts(core.EngineXray, plan.Content)
	if len(endpoints) != 1 || endpoints[0].Port != 8443 {
		t.Fatalf("API discovery: %+v", endpoints)
	}
	if _, err := PrepareAccounting(core.EngineXray, strings.Replace(content, "\"listen\":\"127.0.0.1\"", "\"listen\":\"0.0.0.0\"", 1)); err == nil {
		t.Fatal("enabled stats on public API")
	}
	root := decodeTrafficConfiguration(core.EngineXray, plan.Content)
	if plan.API != "127.0.0.1:40152" || mapValue(root["api"])["listen"] != nil {
		t.Fatal("routed API mode changed")
	}
	rules := mapValue(root["routing"])["rules"].([]any)
	if mapValue(rules[0])["outboundTag"] != "api" || !strings.Contains(stringValue(mapValue(rules[1])["port"]), "40152") {
		t.Fatal("internal API route or proxy access guard missing")
	}
	ordinary := `{"inbounds":[{"tag":"api","port":443,"protocol":"vless"}]}`
	if len(DiscoverTrafficPorts(core.EngineXray, ordinary)) != 1 {
		t.Fatal("ordinary proxy named api hidden")
	}
}

func TestMultipleXrayAPIRoutesRemainStable(t *testing.T) {
	content := `{"api":{"tag":"api","services":["HandlerService"]},"inbounds":[{"tag":"api-a","listen":"127.0.0.1","port":40152,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1"}},{"tag":"api-b","listen":"::1","port":40153,"protocol":"dokodemo-door","settings":{"address":"::1"}},{"tag":"proxy","port":8443,"protocol":"vless"}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"type":"field","inboundTag":["api-a"],"outboundTag":"api"},{"type":"field","inboundTag":["api-b"],"outboundTag":"api"}]}}`
	for i := 0; i < 4; i++ {
		plan, err := PrepareAccounting(core.EngineXray, content)
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && plan.Content != content {
			t.Fatal("API route order changed")
		}
		rules := mapValue(decodeTrafficConfiguration(core.EngineXray, plan.Content)["routing"])["rules"].([]any)
		if mapValue(rules[0])["inboundTag"].([]any)[0] != "api-a" || mapValue(rules[1])["inboundTag"].([]any)[0] != "api-b" {
			t.Fatal("original API order not retained")
		}
		content = plan.Content
	}
}

func TestDirectXrayAPIAddressPreserved(t *testing.T) {
	for _, address := range []string{"127.0.0.1:40154", "[::1]:40154"} {
		content := `{"api":{"tag":"api","listen":"` + address + `","services":["HandlerService"]},"inbounds":[{"tag":"proxy","port":8443,"protocol":"vless"}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`
		plan, err := PrepareAccounting(core.EngineXray, content)
		if err != nil {
			t.Fatal(err)
		}
		if plan.API != address || !strings.Contains(plan.Content, "10085,10086,40154") {
			t.Fatal("API address or protection lost")
		}
		next, err := PrepareAccounting(core.EngineXray, plan.Content)
		if err != nil || next.Content != plan.Content {
			t.Fatalf("direct API not stable: %v", err)
		}
	}
}
