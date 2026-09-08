package serverconfig

import (
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestEveryPresetHasIndependentAccounting(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo, core.EngineShadowsocksRust} {
		for _, protocol := range Protocols(engine) {
			t.Run(string(engine)+"/"+protocol.Key, func(t *testing.T) {
				input, err := NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				input.Tag, input.Port = "first", 21001
				content, err := Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				plan, err := PlanPresetAccounting(engine, content)
				if err != nil {
					t.Fatal(err)
				}
				if ports := DiscoverTrafficPorts(engine, plan.Content); len(ports) != 1 || ports[0].Port != 21001 {
					t.Fatalf("internal API leaked into billable ports: %+v", ports)
				}
				source, err := PresetAccountingSource(engine, plan.Content)
				if err != nil {
					t.Fatal(err)
				}
				input.Tag, input.Port = "second", 21002
				second, err := Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				merged, err := MutateGenerated(engine, source, second, "", "add")
				if err != nil {
					t.Fatal(err)
				}
				plan, err = PlanPresetAccounting(engine, merged)
				if err != nil {
					t.Fatal(err)
				}
				if len(plan.Ports) != 2 {
					t.Fatalf("ports: %+v", plan.Ports)
				}
				// Editing a deployed preset must rebuild its old per-port copies.
				source, err = PresetAccountingSource(engine, plan.Content)
				if err != nil {
					t.Fatal(err)
				}
				input.Port = 21003
				changed, err := Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				merged, err = MutateGenerated(engine, source, changed, "second", "modify")
				if err != nil {
					t.Fatal(err)
				}
				updated, err := PlanPresetAccounting(engine, merged)
				if err != nil {
					t.Fatal(err)
				}
				if len(updated.Ports) != 2 || updated.Ports[1].Port != 21003 {
					t.Fatalf("stale port mapping: %+v", updated.Ports)
				}
				source, err = PresetAccountingSource(engine, updated.Content)
				if err != nil {
					t.Fatal(err)
				}
				merged, err = MutateGenerated(engine, source, changed, "second", "delete")
				if err != nil {
					t.Fatal(err)
				}
				remaining, err := PlanPresetAccounting(engine, merged)
				if err != nil || len(remaining.Ports) != 1 || remaining.Ports[0].Port != 21001 {
					t.Fatalf("delete mapping: %+v, %v", remaining.Ports, err)
				}
				if plan.Source == "nft-dual" {
					if plan.Ports[0].Mark == 0 || plan.Ports[0].Mark == plan.Ports[1].Mark {
						t.Fatal("shared marks")
					}
				} else {
					for _, a := range plan.Ports[0].Outbounds {
						for _, b := range plan.Ports[1].Outbounds {
							if a == b {
								t.Fatal("shared outbound")
							}
						}
					}
				}
			})
		}
	}
}
