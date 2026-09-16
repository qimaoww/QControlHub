//go:build ignore

// Generates disposable browser-fixture credentials from the real preset
// catalog. Never reads node configuration or contacts a production service.
package main

import (
	"encoding/json"
	"os"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func main() {
	type entry struct {
		Engine    core.Engine           `json:"engine"`
		Protocol  serverconfig.Protocol `json:"protocol"`
		Plan      serverconfig.Input    `json:"plan"`
		SavedPlan *serverconfig.Input   `json:"saved_plan,omitempty"`
	}
	var catalog []entry
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo, core.EngineShadowsocksRust} {
		for _, protocol := range serverconfig.Protocols(engine) {
			plan, err := serverconfig.NewPlan(protocol)
			if err != nil {
				panic(err)
			}
			fixture := entry{Engine: engine, Protocol: protocol, Plan: plan}
			if protocol.Key == serverconfig.ProtocolWireGuard {
				// Exercise the real Go wire format: a JS-only zero would hide
				// an accidentally omitted field and the form's default of 25.
				saved := plan
				saved.WireGuardKeepalive = 0
				fixture.SavedPlan = &saved
			}
			catalog = append(catalog, fixture)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(catalog); err != nil {
		panic(err)
	}
}
