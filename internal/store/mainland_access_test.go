package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestShadowsocksRustDeployTaskSnapshotsMainlandPolicy(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema, err := testdb.IsolatePostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(context.Background())
	dataStore, err := Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	if _, err := dataStore.pool.Exec(ctx, `UPDATE agents SET capabilities='["ss-rust"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	config, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Engine: core.EngineShadowsocksRust, Name: "ss-rust",
		Content: `{"server":"0.0.0.0","server_port":8388,"password":"test-password","method":"aes-256-gcm"}`,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	policy := core.MainlandAccessPolicy{AgentID: agent.ID, Engine: core.EngineShadowsocksRust, Tag: "ss-rust", Kind: "shadowsocks", Port: 8388, BlockMainlandDestination: true, BlockMainlandSource: true}
	if err := dataStore.SaveMainlandAccessPolicy(ctx, policy, config.Version); err != nil {
		t.Fatal(err)
	}
	config, err = dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Engine: core.EngineShadowsocksRust, Name: "ss-rust", Content: config.Content,
	}, config.Version)
	if err != nil {
		t.Fatal(err)
	}
	carried, err := dataStore.ListMainlandAccessPolicies(ctx, agent.ID)
	if err != nil || len(carried) != 1 || carried[0].ConfigVersion != config.Version || !carried[0].BlockMainlandSource {
		t.Fatalf("carried policy = %+v, %v", carried, err)
	}
	policy.ConfigVersion = config.Version
	if err := dataStore.ReplaceMainlandAccessPolicies(ctx, agent.ID, config.Version, []core.MainlandAccessPolicy{policy}); err != nil {
		t.Fatal(err)
	}
	task, err := dataStore.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineShadowsocksRust, Action: core.ActionDeploy, ConfigID: config.ID})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := dataStore.ClaimTask(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed == nil || claimed.ID != task.ID || len(claimed.MainlandAccessPolicies) != 1 || claimed.MainlandAccessPolicies[0].Port != 8388 ||
		!claimed.MainlandAccessPolicies[0].BlockMainlandDestination || !claimed.MainlandAccessPolicies[0].BlockMainlandSource {
		t.Fatalf("claimed ss-rust policy snapshot = %+v", claimed)
	}
}
