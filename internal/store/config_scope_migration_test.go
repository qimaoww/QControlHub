package store

import (
	"errors"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestMigrateV51ConfigOwnershipAndSubStoreFormat(t *testing.T) {
	db, ctx, databaseURL := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	undeployed, _ := enrollTaskTestAgent(t, ctx, db)
	workspace, err := db.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "legacy workspace", Engine: core.EngineMihomo, Content: "mixed-port: 21001\n",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET capabilities='["mihomo","ss-rust"]' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	ssWorkspace, err := db.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "legacy SS workspace", Engine: core.EngineShadowsocksRust,
		Content: `{"server":"0.0.0.0","server_port":21004,"method":"aes-256-gcm","password":"legacy-fixture"}`,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceMainlandAccessPolicies(ctx, agent.ID, ssWorkspace.Version, []core.MainlandAccessPolicy{
		{AgentID: agent.ID, Engine: ssWorkspace.Engine, Tag: "legacy-ss", Kind: "shadowsocks",
			Port: 21004, BlockMainlandSource: true, BlockMainlandDestination: true},
	}); err != nil {
		t.Fatal(err)
	}
	archive, err := db.CreateConfig(ctx, core.Config{
		Name: "legacy deployed archive", Engine: core.EngineMihomo, Content: "listeners: [{name: legacy, type: http, port: 21002}]\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	template, err := db.CreateConfigTemplate(ctx, "legacy template", "mihomo", "mixed-port: 21003\n")
	if err != nil {
		t.Fatal(err)
	}
	task, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: archive.Engine, ConfigID: archive.ID, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil {
		t.Fatalf("claim legacy deployment: %+v %v", claimed, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	target, err := db.CreateSubStoreSyncTarget(ctx, "Legacy group", core.SubStoreSyncModeManaged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceSubStoreSyncSelections(ctx, target.ID, []core.SubStoreSyncSelection{
		{AgentID: agent.ID, Engine: archive.Engine, ProfileTag: "legacy", CustomName: "Legacy node"},
		{AgentID: undeployed.ID, Engine: archive.Engine, ProfileTag: "unknown", CustomName: "Missing deployment"},
	}); err != nil {
		t.Fatal(err)
	}
	// Restore precisely the columns, keys and version present before this PR.
	// Use a fresh pool after DDL so cached prepared statements cannot mask an
	// upgrade problem.
	if _, err := db.pool.Exec(ctx, `
		ALTER TABLE configs DROP COLUMN owner_id;
		CREATE UNIQUE INDEX configs_agent_engine_unique_idx ON configs(agent_id,engine)
			WHERE agent_id IS NOT NULL AND deleted_at IS NULL;
		ALTER TABLE tasks DROP COLUMN owner_id;
		ALTER TABLE config_templates DROP COLUMN owner_id;
		ALTER TABLE substore_sync_targets DROP COLUMN owner_id;
		ALTER TABLE substore_sync_targets DROP COLUMN sync_format;
		CREATE UNIQUE INDEX substore_sync_targets_display_name_idx ON substore_sync_targets(display_name);
		ALTER TABLE substore_sync_items DROP COLUMN config_id;
		ALTER TABLE mainland_access_policies DROP COLUMN config_id;
		ALTER TABLE mainland_access_policies ADD PRIMARY KEY(agent_id,engine,tag,port);
		DELETE FROM qcontrolhub_schema_migrations WHERE version>=52;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(51) ON CONFLICT DO NOTHING;
	`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	upgraded, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatalf("migrate v51: %v", err)
	}
	defer upgraded.Close()
	admin := WithConfigScope(ctx, "", true)
	user := WithConfigScope(ctx, "usr_new", false)
	if got, err := upgraded.AgentConfig(admin, agent.ID, workspace.Engine); err != nil || got.ID != workspace.ID || got.Content != workspace.Content || got.OwnerID != "" {
		t.Fatalf("legacy workspace not preserved: %+v %v", got, err)
	}
	if _, err := upgraded.AgentConfig(user, agent.ID, workspace.Engine); !errors.Is(err, ErrNotFound) {
		t.Fatalf("new user inherited legacy workspace: %v", err)
	}
	if configs, err := upgraded.ListConfigs(user); err != nil || len(configs) != 0 {
		t.Fatalf("legacy archives exposed: %+v %v", configs, err)
	}
	if templates, err := upgraded.ListConfigTemplates(admin); err != nil || len(templates) != 1 || templates[0].ID != template.ID || templates[0].OwnerID != "" {
		t.Fatalf("legacy templates lost: %+v %v", templates, err)
	}
	if tasks, err := upgraded.ListTasks(user, agent.ID, 10); err != nil || len(tasks) != 0 {
		t.Fatalf("legacy tasks exposed: %+v %v", tasks, err)
	}
	policies, err := upgraded.ListMainlandAccessPolicies(admin, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].Tag != "legacy-ss" || policies[0].ConfigVersion != ssWorkspace.Version ||
		!policies[0].BlockMainlandSource || !policies[0].BlockMainlandDestination {
		t.Fatalf("legacy SS policy changed: %+v %v", policies, err)
	}
	var policyConfigID string
	if err := upgraded.pool.QueryRow(ctx, `SELECT config_id FROM mainland_access_policies WHERE agent_id=$1`, agent.ID).Scan(&policyConfigID); err != nil || policyConfigID != ssWorkspace.ID {
		t.Fatalf("legacy SS policy was bound to the wrong workspace: %q %v", policyConfigID, err)
	}
	if policies, err := upgraded.ListMainlandAccessPolicies(user, agent.ID); err != nil || len(policies) != 0 {
		t.Fatalf("legacy SS policy exposed: %+v %v", policies, err)
	}
	migrated, err := upgraded.SubStoreSyncTarget(admin, target.ID)
	if err != nil || migrated.SyncFormat != core.SubStoreSyncFormatURL || migrated.SyncMode != core.SubStoreSyncModeManaged || migrated.OwnerID != "" {
		t.Fatalf("legacy target defaults changed: %+v %v", migrated, err)
	}
	selections, err := upgraded.ListSubStoreSyncSelections(admin, target.ID)
	if err != nil || len(selections) != 2 {
		t.Fatalf("legacy selections lost: %+v %v", selections, err)
	}
	for _, selection := range selections {
		want := ""
		if selection.AgentID == agent.ID {
			want = archive.ID
		}
		if selection.ConfigID != want {
			t.Fatalf("selection was not pinned to its actual deployed config: %+v, want %q", selection, want)
		}
	}
	if _, err := upgraded.SaveAgentConfig(user, core.Config{
		AgentID: agent.ID, Name: "new private workspace", Engine: workspace.Engine, Content: "mixed-port: 21004\n",
	}, 0); err != nil {
		t.Fatalf("new owner cannot share the migrated host: %v", err)
	}
	if err := upgraded.migrate(ctx); err != nil {
		t.Fatalf("migration is not idempotent: %v", err)
	}
}

func TestMainlandPoliciesStayWithTheirOwnerConfiguration(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET capabilities='["mihomo","ss-rust"]' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	alice, bob := WithConfigScope(ctx, "usr_alice", false), WithConfigScope(ctx, "usr_bob", false)
	input := core.Config{AgentID: agent.ID, Name: "private SS", Engine: core.EngineShadowsocksRust,
		Content: `{"server":"0.0.0.0","server_port":21001,"method":"aes-256-gcm","password":"private-fixture"}`}
	own, err := db.SaveAgentConfig(alice, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.SaveAgentConfig(bob, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	policy := core.MainlandAccessPolicy{AgentID: agent.ID, Engine: input.Engine, Tag: "same-tag", Kind: "shadowsocks",
		Port: 21001, ConfigVersion: 1, BlockMainlandSource: true}
	if err := db.ReplaceMainlandAccessPolicies(alice, agent.ID, own.Version, []core.MainlandAccessPolicy{policy}); err != nil {
		t.Fatal(err)
	}
	policy.BlockMainlandDestination = true
	if err := db.ReplaceMainlandAccessPolicies(bob, agent.ID, other.Version, []core.MainlandAccessPolicy{policy}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAgentConfig(alice, input, own.Version); err != nil {
		t.Fatal(err)
	}
	policies, err := db.ListMainlandAccessPolicies(bob, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].ConfigVersion != 1 || !policies[0].BlockMainlandDestination {
		t.Fatalf("another user's save modified SS policies: %+v %v", policies, err)
	}
	if err := db.ReplaceMainlandAccessPolicies(alice, agent.ID, 2, nil); err != nil {
		t.Fatal(err)
	}
	policies, err = db.ListMainlandAccessPolicies(bob, agent.ID)
	if err != nil || len(policies) != 1 {
		t.Fatalf("another user's policy clear crossed ownership: %+v %v", policies, err)
	}
}
