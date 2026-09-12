package store

import (
	"context"
	"errors"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestMigrateV52AgentSharingPreservesDeployedVersionAndLegacyAccess(t *testing.T) {
	db, ctx, databaseURL := isolatedConfigScopeStore(t)
	user, alice := sharedTestUser(t, db, ctx, "legacy-sharing-owner")
	agent := sharedTestAgent(t, db, ctx)
	config := sharedTestConfig(t, db, alice, agent.ID, 21001)
	task, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	config.Content = "listeners: [{name: draft, type: trojan, port: 21002}]"
	if _, err := db.SaveAgentConfig(alice, config, config.Version); err != nil {
		t.Fatal(err)
	}
	downgradeSharingSchemaForTest(t, db, ctx)
	db.Close()
	migrated, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	access, err := migrated.UserAgentAccess(ctx, user.ID)
	if err != nil || access.Isolated || access.Revision != 1 || len(access.Shares) != 0 {
		t.Fatalf("migration changed legacy access: %+v %v", access, err)
	}
	var owner string
	var version int
	var running bool
	if err := migrated.pool.QueryRow(ctx, `SELECT owner_id,config_version,running FROM agent_engine_ownership
		WHERE agent_id=$1 AND engine=$2`, agent.ID, config.Engine).Scan(&owner, &version, &running); err != nil {
		t.Fatal(err)
	}
	if owner != user.ID || version != 1 || !running {
		t.Fatalf("migration adopted a draft instead of the deployed revision: %s v%d running=%v", owner, version, running)
	}
	sharedTestAllocation(t, migrated, ctx, user.ID, agent.ID, 1000, 21001)
	policies, err := migrated.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].Port != 21001 || policies[0].SharedQuota == nil {
		t.Fatalf("migrated running configuration was not covered: %+v %v", policies, err)
	}
}

func downgradeSharingSchemaForTest(t *testing.T, db *Store, ctx context.Context) {
	t.Helper()
	if _, err := db.pool.Exec(ctx, `
		ALTER TABLE port_traffic_policies DROP COLUMN share_id, DROP COLUMN share_used_bytes, DROP COLUMN share_generation;
		DROP TABLE agent_share_ports;
		DROP TABLE agent_shares;
		DROP TABLE agent_engine_ownership;
		ALTER TABLE panel_users DROP COLUMN agent_isolation, DROP COLUMN agent_access_revision;
		ALTER TABLE tasks DROP COLUMN shared_traffic_id;
		DELETE FROM qcontrolhub_schema_migrations WHERE version>=53;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(52) ON CONFLICT DO NOTHING;
	`); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateV52AgentSharingKeepsFailedDeploymentsUncertain(t *testing.T) {
	for _, outcome := range []string{"failed", "running", "stopped-then-started", "first-deploy-failed"} {
		t.Run(outcome, func(t *testing.T) {
			db, ctx, databaseURL := isolatedConfigScopeStore(t)
			user, alice := sharedTestUser(t, db, ctx, "legacy-uncertain-owner")
			agent := sharedTestAgent(t, db, ctx)
			config := sharedTestConfig(t, db, alice, agent.ID, 21001)
			run := func(success bool) {
				t.Helper()
				task, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
				if err != nil {
					t.Fatal(err)
				}
				claimed, err := db.ClaimTask(ctx, agent.ID)
				if err != nil || claimed == nil {
					t.Fatalf("claim: %+v %v", claimed, err)
				}
				if !success && outcome == "running" {
					return
				}
				if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: success}); err != nil {
					t.Fatal(err)
				}
			}
			if outcome != "first-deploy-failed" {
				run(true)
			}
			run(false)
			if outcome == "stopped-then-started" {
				// Legacy versions could start a partially replaced config after
				// stopping it. A successful start proves no config identity.
				if _, err := db.pool.Exec(ctx, `INSERT INTO tasks(id,agent_id,engine,action,status,owner_id,created_at,started_at,finished_at)
					VALUES('legacy-restart',$1,$2,'start','succeeded',$3,now(),now(),now())`, agent.ID, config.Engine, user.ID); err != nil {
					t.Fatal(err)
				}
			}
			downgradeSharingSchemaForTest(t, db, ctx)
			db.Close()
			migrated, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("config-scope"))
			if err != nil {
				t.Fatal(err)
			}
			defer migrated.Close()
			var configUncertain bool
			if err := migrated.pool.QueryRow(ctx, `SELECT config_uncertain FROM agent_engine_ownership WHERE agent_id=$1 AND engine=$2`,
				agent.ID, config.Engine).Scan(&configUncertain); err != nil || !configUncertain {
				t.Fatalf("migration trusted unknown deployed content: %v %v", configUncertain, err)
			}
			_, err = migrated.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true,
				Shares: []core.AgentShareRequest{{AgentID: agent.ID, LimitBytes: 1000, Ports: []int{21001}}}})
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("migration allowed billing an uncertain listener set: %v", err)
			}
		})
	}
}
