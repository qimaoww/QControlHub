package serverconfig

import (
	"encoding/json"
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
			if err := ValidateIndependentEgress(test.engine, result); err != nil {
				t.Fatalf("private exit lost: %v", err)
			}
		})
	}
}
