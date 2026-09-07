package serverconfig

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAccountingPlansAreIdempotentAndPerPort(t *testing.T) {
	for engine, content := range map[core.Engine]string{
		core.EngineXray:            `{"inbounds":[{"tag":"a","port":1080,"protocol":"socks"},{"tag":"b","port":1081,"protocol":"socks"}],"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"blocked","protocol":"blackhole"}],"routing":{"rules":[{"type":"field","ip":["geoip:private"],"outboundTag":"blocked"},{"type":"field","inboundTag":["a"],"domain":["example.com"],"outboundTag":"direct"}]},"custom_integer":9007199254740993}`,
		core.EngineSingBox:         `{"inbounds":[{"tag":"a","listen_port":1080,"type":"socks"},{"tag":"b","listen_port":1081,"type":"socks"}],"outbounds":[{"tag":"direct","type":"direct"}],"route":{"rules":[{"ip_is_private":true,"action":"reject"},{"inbound":["a"],"domain":["example.com"],"outbound":"direct"}]}}`,
		core.EngineShadowsocksRust: `{"servers":[{"server_port":1080,"password":"test","method":"aes-256-gcm"},{"server_port":1081,"password":"test","method":"aes-256-gcm"}]}`,
		core.EngineMihomo:          "listeners:\n  - {name: a, type: socks, port: 1080}\n  - {name: b, type: socks, port: 1081}\nrules:\n  - IP-CIDR,127.0.0.0/8,REJECT\n  - DOMAIN,example.com,DIRECT\n  - MATCH,DIRECT\n",
	} {
		t.Run(string(engine), func(t *testing.T) {
			plan, err := PrepareAccounting(engine, content)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Ports) != 2 {
				t.Fatalf("ports %+v", plan.Ports)
			}
			again, err := PrepareAccounting(engine, plan.Content)
			if err != nil || !reflect.DeepEqual(plan, again) {
				t.Fatalf("non-idempotent: %v\n%s\n%s", err, plan.Content, again.Content)
			}
			if engine == core.EngineXray && !strings.Contains(plan.Content, "9007199254740993") {
				t.Fatal("integer precision lost")
			}
			if plan.Source == "core-api" {
				if len(plan.Ports[0].Outbounds) != 1 || plan.Ports[0].Outbounds[0] == plan.Ports[1].Outbounds[0] {
					t.Fatal("outbound attribution overlaps")
				}
				var root map[string]any
				_ = json.Unmarshal([]byte(plan.Content), &root)
				key, target := "route", "action"
				if engine == core.EngineXray {
					key, target = "routing", "outboundTag"
				}
				rules := mapValue(root[key])["rules"].([]any)
				first := mapValue(rules[1])[target]
				if first != "reject" && first != "blocked" {
					t.Fatalf("security rule reordered: %v", rules)
				}
			} else if plan.Ports[0].Mark == 0 || plan.Ports[0].Mark == plan.Ports[1].Mark {
				t.Fatal("marks overlap")
			}
		})
	}
}

func TestAccountingRejectsAmbiguousConfigurations(t *testing.T) {
	for engine, contents := range map[core.Engine][]string{
		core.EngineXray:            {`{"inbounds":[{"tag":"a","port":"1000-2000"}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`, `{"inbounds":[{"tag":"a","port":1080}],"outbounds":[{"tag":"direct","protocol":"freedom","mux":{}}]}`},
		core.EngineShadowsocksRust: {`{"server_port":1080,"outbound_fwmark":123}`, `{"servers":[{"server_port":1080},{"server_port":1080}]}`},
		core.EngineMihomo:          {"mixed-port: 1080\n", "listeners: [{name: a, port: 1080}]\nproxy-groups: []\n"},
	} {
		for _, content := range contents {
			if _, err := PrepareAccounting(engine, content); err == nil {
				t.Fatalf("accepted %s %s", engine, content)
			}
		}
	}
}

func TestMarkedSingBoxAccountingIdempotent(t *testing.T) {
	content := `{"inbounds":[{"type":"socks","tag":"a","listen_port":1080}],"outbounds":[{"type":"direct","tag":"direct"}]}`
	plan, err := PrepareMarkedSingBoxAccounting(content)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Source != "nft-dual" || plan.Ports[0].Mark == 0 || strings.Contains(plan.Content, "v2ray_api") {
		t.Fatalf("bad fallback: %+v", plan)
	}
	again, err := PrepareMarkedSingBoxAccounting(plan.Content)
	if err != nil || !reflect.DeepEqual(plan, again) {
		t.Fatalf("fallback not idempotent: %v\n%s\n%s", err, plan.Content, again.Content)
	}
}
