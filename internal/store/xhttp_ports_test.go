package store

import (
	"errors"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestXHTTPQueuedGroupReservesEveryListener(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET capabilities='["xray"]',supported_capabilities='["xray"]',features=features||'["managed-tls-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	protocol, _ := serverconfig.FindProtocol(core.EngineXray, serverconfig.ProtocolXHTTPFive)
	input, err := serverconfig.NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	input.Port, input.XHTTPBackendPort, input.XHTTPTLSPort = 24443, 35001, 35002
	content, err := serverconfig.Generate(core.EngineXray, input)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := serverconfig.MarshalClientMetadata(input)
	if err != nil {
		t.Fatal(err)
	}
	options := ConfigMutationOptions{Action: core.ActionDeploy, ClientMetadata: &ConfigClientMetadataMutation{Tag: input.Tag, Content: metadata}}
	config := core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "five listeners", Content: content}
	if _, _, err := db.SaveAgentConfigAndTask(ctx, config, 0, options); !errors.Is(err, ErrInvalid) {
		t.Fatal("old agent accepted group", err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET features=features||'["xhttp-presets-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.SaveAgentConfigAndTask(ctx, config, 0, options); err != nil {
		t.Fatal(err)
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, port := range []int{input.Port, input.XHTTPBackendPort, input.XHTTPTLSPort} {
		err := db.checkSharedPortsAvailableTx(ctx, tx, "", "foreign-owner", agent.ID, []int{port})
		var conflict *PortConflictError
		if !errors.As(err, &conflict) || conflict.Port != port {
			t.Fatalf("foreign user can claim group listener %d: %v", port, err)
		}
	}
	if err := db.checkSharedPortsAvailableTx(ctx, tx, "", "foreign-owner", agent.ID, []int{35003}); err != nil {
		t.Fatal("unrelated port rejected", err)
	}
}
