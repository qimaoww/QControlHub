package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestMigrateV55SharingRequiresConsentAndPreservesLedger(t *testing.T) {
	db, ctx, databaseURL := isolatedConfigScopeStore(t)
	user, bob := sharedTestUser(t, db, ctx, "migration-consent")
	agent := sharedTestAgent(t, db, ctx)
	access := sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, 21001)
	share := access.Shares[0]
	config := sharedTestConfig(t, db, bob, agent.ID, 21001)
	task, err := db.CreateTask(bob, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	if running, err := db.ClaimTask(ctx, agent.ID); err != nil || running == nil {
		t.Fatalf("claim: %+v %v", running, err)
	}
	queued, err := db.CreateTask(bob, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionStatus})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET used_bytes=123 WHERE id=$1`, share.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `ALTER TABLE agent_shares DROP COLUMN status, DROP COLUMN invitation_revision;
		DELETE FROM qcontrolhub_schema_migrations WHERE version>=56;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(55) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	migrated, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	access, err = migrated.UserAgentAccess(bob, user.ID)
	if err != nil || len(access.Shares) != 1 {
		t.Fatalf("migration lost invitation: %+v %v", access, err)
	}
	pending := access.Shares[0]
	if pending.Status != core.AgentSharePending || pending.ID != share.ID || pending.UsedBytes != 123 ||
		pending.LimitBytes != 1000 || len(pending.Ports) != 1 || pending.Ports[0] != 21001 {
		t.Fatalf("migration lost consent/accounting boundary: %+v", pending)
	}
	if _, err := migrated.GetAgent(bob, agent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy share bypassed consent: %v", err)
	}
	if policies, err := migrated.AgentPortTrafficPolicies(ctx, agent.ID); err != nil || len(policies) != 1 || !policies[0].SharedQuota.Revoked {
		t.Fatalf("legacy runtime policy still grants access: %+v %v", policies, err)
	}
	for _, prior := range []struct {
		id     string
		status core.TaskStatus
	}{{task.ID, core.TaskFailed}, {queued.ID, core.TaskCanceled}} {
		if got, err := migrated.GetTask(ctx, prior.id); err != nil || got.Status != prior.status {
			t.Fatalf("legacy task was not invalidated: %+v %v", got, err)
		}
	}
	sharedTestAllocation(t, migrated, ctx, user.ID, agent.ID, pending.LimitBytes, pending.Ports...)
	reopened, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if access, err := reopened.UserAgentAccess(bob, user.ID); err != nil || access.Shares[0].Status != core.AgentShareAccepted || access.Shares[0].UsedBytes != 123 {
		t.Fatalf("restart repeated migration or reset usage: %+v %v", access, err)
	}
}

func TestMigrateV54PreservesPrivateBackendsAndDeployedProfilePreferences(t *testing.T) {
	db, ctx, databaseURL := isolatedConfigScopeStore(t)
	_, alice := sharedTestUser(t, db, ctx, "migration-alice")
	_, bob := sharedTestUser(t, db, ctx, "migration-bob")
	agent := sharedTestAgent(t, db, alice)
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
	preferences := map[string]string{
		core.ClientProfileNameLabel(core.EngineMihomo, "", 21001):    "Alice private label",
		core.ClientProfileAddressLabel(core.EngineMihomo, "", 21001): "alice.example.test",
		core.ClientProfileFamilyLabel(core.EngineMihomo, "", 21001):  "ipv4",
	}
	labels, _ := json.Marshal(preferences)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET labels=$2 WHERE id=$1`, agent.ID, labels); err != nil {
		t.Fatal(err)
	}
	// The latest editor body is not the deployed revision.
	config.Content = "listeners: [{name: draft, type: trojan, port: 21002}]"
	if _, err := db.SaveAgentConfig(alice, config, config.Version); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		scope context.Context
		url   string
		key   string
	}{{ctx, "https://substore.example/admin", "legacy-admin"},
		{alice, "https://SUBSTORE.example:443/alice/", "legacy-alice"},
		{bob, "https://substore.example/bob", "legacy-bob"}} {
		if _, err := db.SaveSubStoreSyncSettings(item.scope, item.url); err != nil {
			t.Fatal(err)
		}
		target, err := db.CreateSubStoreSyncTarget(item.scope, "private-group")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.pool.Exec(ctx, `UPDATE substore_sync_targets SET backend_key=$1 WHERE id=$2`, item.key, target.ID); err != nil {
			t.Fatal(err)
		}
		owner := scopeForConfig(item.scope).OwnerID
		if owner == "" {
			_, err = db.pool.Exec(ctx, `UPDATE substore_sync_settings SET backend_key=$1 WHERE id=1`, item.key)
		} else {
			_, err = db.pool.Exec(ctx, `UPDATE user_substore_settings SET backend_key=$1 WHERE owner_id=$2`, item.key, owner)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.pool.Exec(ctx, `DROP TABLE config_client_preferences;
		ALTER TABLE panel_users DROP COLUMN auth_revision;
		DELETE FROM qcontrolhub_schema_migrations WHERE version>=55;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(54) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	migrated, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	deployed, err := migrated.DeployedConfigs(alice)
	if err != nil || len(deployed) != 1 || deployed[0].Config.Version != 1 {
		t.Fatalf("deployed revision lost: %+v %v", deployed, err)
	}
	for label, want := range preferences {
		if deployed[0].Preferences[label] != want {
			t.Fatalf("migration lost %s", label)
		}
	}
	other, err := migrated.DeployedConfigs(bob)
	if err != nil || len(other) != 0 {
		t.Fatalf("migrated preferences crossed accounts: %+v %v", other, err)
	}
	for _, scope := range []context.Context{ctx, alice, bob} {
		settings, err := migrated.SubStoreSyncSettings(scope)
		if err != nil || settings.BackendKey != subStoreBackendKey(settings.EndpointURL) {
			t.Fatalf("backend key not canonical: %+v %v", settings, err)
		}
		targets, err := migrated.ListSubStoreSyncTargets(WithConfigScope(scope, scopeForConfig(scope).OwnerID, true))
		if err != nil || len(targets) != 1 {
			t.Fatalf("backend claim lost its owner: %+v %v", targets, err)
		}
		var backendKey string
		if err := migrated.pool.QueryRow(ctx, `SELECT backend_key FROM substore_sync_targets WHERE id=$1`, targets[0].ID).Scan(&backendKey); err != nil || backendKey != settings.BackendKey {
			t.Fatalf("backend claim key: %s %v", backendKey, err)
		}
	}
}

func TestMigrateV52AgentSharingPreservesDeployedVersionAndLegacyAccess(t *testing.T) {
	db, ctx, databaseURL := isolatedConfigScopeStore(t)
	user, alice := sharedTestUser(t, db, ctx, "legacy-sharing-owner")
	agent := sharedTestAgent(t, db, alice)
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
	if err != nil || !access.Isolated || access.Revision != 1 || len(access.Shares) != 0 {
		t.Fatalf("migration did not default the legacy account to private access: %+v %v", access, err)
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
		UPDATE agents SET owner_id='';
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
			agent := sharedTestAgent(t, db, alice)
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
			access, err := migrated.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true,
				Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineMihomo}, LimitBytes: 1000, Ports: []int{21001}}}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = migrated.RespondAgentShare(alice, access.Shares[0].ID,
				core.AgentShareResponseRequest{Revision: access.Shares[0].InvitationRevision, Decision: "accept"})
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("migration allowed billing an uncertain listener set: %v", err)
			}
		})
	}
}
