package serverconfig

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSharedInstanceRemovesOnlyLocalStatisticsAPI(t *testing.T) {
	for _, test := range []struct {
		engine core.Engine
		input  string
	}{
		{core.EngineXray, `{"inbounds":[{"tag":"shared","protocol":"http","port":21001}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`},
		{core.EngineSingBox, `{"inbounds":[{"tag":"shared","type":"http","listen_port":21001}],"outbounds":[{"tag":"direct","type":"direct"}]}`},
	} {
		t.Run(string(test.engine), func(t *testing.T) {
			plan, err := PrepareAccounting(test.engine, test.input)
			if err != nil {
				t.Fatal(err)
			}
			result, err := SharedInstanceRuntimeContent(test.engine, plan.Content)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(result), &root); err != nil {
				t.Fatal(err)
			}
			if test.engine == core.EngineXray && root["api"] != nil {
				t.Fatal("shared Xray instance kept its fixed statistics listener")
			}
			if test.engine == core.EngineSingBox && mapValue(root["experimental"])["v2ray_api"] != nil {
				t.Fatal("shared sing-box instance kept its fixed statistics listener")
			}
			ports, err := SharedTrafficEndpoints(test.engine, result)
			if err != nil || len(ports) != 1 || ports[0].Port != 21001 {
				t.Fatalf("private ingress changed: %+v %v", ports, err)
			}
			dual, err := PrepareSharedAccounting(test.engine, result)
			if err != nil || dual.Source != "nft-dual" || dual.Ports[0].Mark != uint32(0x51430000)|21001 {
				t.Fatalf("shared target is unmetered: %+v %v", dual, err)
			}
			again, err := PrepareSharedAccounting(test.engine, dual.Content)
			if err != nil || !reflect.DeepEqual(dual, again) {
				t.Fatalf("shared plan is not idempotent: %v", err)
			}
			if err := ValidateIndependentEgress(test.engine, result); err != nil {
				t.Fatalf("private exit lost: %v", err)
			}
		})
	}
}

func TestSharedProcessPlanPreservesExplicitRoutingMarks(t *testing.T) {
	for engine, source := range map[core.Engine]string{
		core.EngineMihomo:          "routing-mark: 17\nlisteners: [{name: in, type: http, port: 24101}]\n",
		core.EngineXray:            `{"inbounds":[{"tag":"in","protocol":"http","port":24101}],"outbounds":[{"tag":"a","protocol":"freedom","streamSettings":{"sockopt":{"mark":17}}}]}`,
		core.EngineSingBox:         `{"inbounds":[{"tag":"in","type":"http","listen_port":24101}],"outbounds":[{"tag":"a","type":"direct","routing_mark":17}]}`,
		core.EngineShadowsocksRust: `{"server":"0.0.0.0","server_port":24101,"method":"aes-256-gcm","password":"fixture","outbound_fwmark":17}`,
	} {
		t.Run(string(engine), func(t *testing.T) {
			plan, err := SharedProcessPlan(engine, source)
			if err != nil || plan.Content != source || len(plan.Ports) != 1 || plan.Ports[0].Mark != 0 || plan.Source != "process" {
				t.Fatalf("%+v %v", plan, err)
			}
			runtime, err := SharedInstanceRuntimeContent(engine, source)
			if err != nil || runtime != source {
				t.Fatalf("process accounting rewrote routing: %s %v", runtime, err)
			}
		})
	}
}
