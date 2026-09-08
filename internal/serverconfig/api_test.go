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
	rules := mapValue(root["routing"])["rules"].([]any)
	if mapValue(rules[0])["outboundTag"] != "api" || !strings.Contains(stringValue(mapValue(rules[1])["port"]), "40152") {
		t.Fatal("internal API route or proxy access guard missing")
	}
	ordinary := `{"inbounds":[{"tag":"api","port":443,"protocol":"vless"}]}`
	if len(DiscoverTrafficPorts(core.EngineXray, ordinary)) != 1 {
		t.Fatal("ordinary proxy named api hidden")
	}
}
