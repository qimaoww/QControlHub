package serverconfig

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestDeleteHistoricalAccountingAndPreserveBindings(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		t.Run(string(engine), func(t *testing.T) {
			protocol := "freedom"
			if engine == core.EngineSingBox {
				protocol = "direct"
			}
			fixture, _ := decodeAccountingRoot(engine, boundAccountingFixture(engine, protocol))
			for _, raw := range fixture["inbounds"].([]any) {
				mapValue(raw)["type"] = "socks"
				mapValue(raw)["protocol"] = "socks"
			}
			encoded, _ := json.Marshal(fixture)
			plan, err := PrepareAccounting(engine, string(encoded))
			if err != nil {
				t.Fatal(err)
			}
			root, err := decodeAccountingRoot(engine, plan.Content)
			if err != nil {
				t.Fatal(err)
			}
			// Older accounting plans generated identical exits before outbound marks.
			for _, raw := range root["outbounds"].([]any) {
				out := mapValue(raw)
				if strings.HasPrefix(stringValue(out["tag"]), accountingPrefix) {
					delete(out, "routing_mark")
					delete(out, "streamSettings")
				}
			}
			data, _ := json.Marshal(root)
			source, err := PresetAccountingSource(engine, string(data))
			if err != nil {
				t.Fatal(err)
			}
			source, err = DeletePresetInbound(engine, source, "a")
			if err != nil {
				t.Fatal(err)
			}
			source, err = ReconcilePresetInboundReferences(engine, source, "a", "")
			if err != nil {
				t.Fatal(err)
			}
			rebuilt, err := PlanPresetAccounting(engine, source)
			if err != nil {
				t.Fatal(err)
			}
			again, err := PrepareAccounting(engine, rebuilt.Content)
			if err != nil || !reflect.DeepEqual(rebuilt, again) {
				t.Fatalf("recompile changed content: %v", err)
			}
			if len(rebuilt.Ports) != 1 || rebuilt.Ports[0].Inbound != "b" || !strings.Contains(rebuilt.Content, "policy.test") || strings.Contains(rebuilt.Content, "qch-trf-443-") {
				t.Fatal("deletion lost the other binding/custom policy or retained the removed port")
			}
			source, err = PresetAccountingSource(engine, rebuilt.Content)
			if err != nil {
				t.Fatal(err)
			}
			source, err = DeletePresetInbound(engine, source, "b")
			if err != nil {
				t.Fatal(err)
			}
			source, err = ReconcilePresetInboundReferences(engine, source, "b", "")
			if err != nil {
				t.Fatal(err)
			}
			if len(DiscoverTrafficPorts(engine, source)) != 0 || strings.Contains(source, accountingPrefix) || engine == core.EngineSingBox && strings.Contains(source, "v2ray_api") {
				t.Fatal("final deletion retained listener statistics")
			}
		})
	}
}
