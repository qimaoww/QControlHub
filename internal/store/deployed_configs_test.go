package store

import (
	"context"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestDeployedConfigsResolveExactVersionsAndMetadata(t *testing.T) {
	s := openPerformanceStore(t)
	agentID, _ := seedPerformanceAgent(t, s, 0)
	ctx := context.Background()
	input := core.Config{AgentID: agentID, Name: "deployed fixture", Engine: core.EngineMihomo, Content: "mixed-port: 7890\n"}
	first, err := s.SaveAgentConfigWithClientMetadata(ctx, input, 0, ConfigClientMetadataMutation{Tag: "inbound", Content: `{"secret":"first"}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO tasks (id,agent_id,action,engine,config_id,config_version,status,created_at,finished_at)
		VALUES ('tsk_fixture',$1,'deploy','mihomo',$2,1,'succeeded',now(),now())`, agentID, first.ID); err != nil {
		t.Fatal(err)
	}
	input.Content = "mixed-port: 7891\n"
	if _, err := s.SaveAgentConfigWithClientMetadata(ctx, input, 1, ConfigClientMetadataMutation{Tag: "inbound", Content: `{"secret":"second"}`}); err != nil {
		t.Fatal(err)
	}
	got, err := s.DeployedConfigs(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("deployed = %+v, %v", got, err)
	}
	if got[0].Config.Version != 1 || got[0].Config.Content != first.Content || got[0].Metadata["inbound"] != `{"secret":"first"}` {
		t.Fatalf("mixed saved/deployed versions: %+v", got[0])
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM config_revisions WHERE config_id=$1 AND version=1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.DeployedConfigs(ctx); err != nil || len(got) != 0 {
		t.Fatalf("pruned revision exposed: %+v, %v", got, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE tasks SET config_version=2 WHERE id='tsk_fixture'`); err != nil {
		t.Fatal(err)
	}
	if got, err := s.DeployedConfigs(ctx); err != nil || len(got) != 1 || got[0].Config.Content != input.Content {
		t.Fatalf("current version = %+v, %v", got, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE configs SET deleted_at=now(),content='' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.DeployedConfigs(ctx); err != nil || len(got) != 0 {
		t.Fatalf("deleted config exposed: %+v, %v", got, err)
	}
}
