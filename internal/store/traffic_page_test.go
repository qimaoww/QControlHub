package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTrafficSchemaUpgradeFromMain81PreservesAPIKeys(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	admin := WithConfigScope(ctx, "", true)
	key, secret, err := db.CreateAPIKey(admin, "environment", "environment", core.APIKeyInput{Name: "main81", Permissions: []core.Permission{core.PermissionTrafficRead}}, core.AuditLogEntry{Actor: "migration-test"})
	if err != nil {
		t.Fatal(err)
	}
	// Main's schema 81 contains API keys but none of this PR's additions.
	if _, err = db.pool.Exec(ctx, `DROP TABLE traffic_batch_results, traffic_batch_requests;
 ALTER TABLE agent_engine_ownership DROP COLUMN traffic_config_confirmed;
 ALTER TABLE agent_shared_instance_ownership DROP COLUMN traffic_config_confirmed;
 DELETE FROM qcontrolhub_schema_migrations WHERE version>81;
 INSERT INTO qcontrolhub_schema_migrations(version) VALUES(81) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err = db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version int
	if err = db.pool.QueryRow(ctx, `SELECT max(version) FROM qcontrolhub_schema_migrations`).Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("migration version %d: %v", version, err)
	}
	for _, query := range []string{
		`SELECT traffic_config_confirmed FROM agent_engine_ownership LIMIT 0`,
		`SELECT traffic_config_confirmed FROM agent_shared_instance_ownership LIMIT 0`,
		`SELECT * FROM traffic_batch_requests LIMIT 0`,
		`SELECT * FROM traffic_batch_results LIMIT 0`,
	} {
		if _, err = db.pool.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	retained, err := db.APIKeyForAuthentication(ctx, secret)
	if err != nil || retained.ID != key.ID || retained.Revision != key.Revision || retained.RevokedAt != nil {
		t.Fatalf("API key changed during upgrade: %+v %v", retained, err)
	}
}

func TestTrafficPageConfirmedLifecycle(t *testing.T) {
	db, ctx, url := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "traffic-owner")
	agent := allEnginesSharedTestAgent(t, db, owner)
	save := func(port, version int) core.Config {
		t.Helper()
		c, e := db.SaveAgentConfig(owner, core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "traffic lifecycle", Content: strings.ReplaceAll(sharedEngineConfigs()[1].Content, "21002", fmt.Sprint(port))}, version)
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	assertPorts := func(want ...int) {
		t.Helper()
		ep, e := db.AppliedTrafficEndpoints(owner)
		if e != nil || len(ep) != len(want) {
			t.Fatalf("endpoints %+v %v want %v", ep, e, want)
		}
		for i, p := range want {
			if ep[i].Port != p {
				t.Fatalf("wrong applied endpoint %+v", ep)
			}
		}
		policies, e := db.CurrentPortTrafficPolicies(owner)
		if e != nil {
			t.Fatal(e)
		}
		for _, p := range policies {
			if !p.DeploymentConfirmed || !p.IsCurrentUser {
				t.Fatalf("unconfirmed or foreign policy %+v", p)
			}
			ok := false
			for _, port := range want {
				ok = ok || p.Port == port
			}
			if !ok {
				t.Fatalf("policy bypassed applied set %+v", p)
			}
		}
	}
	claim := func(c core.Config, action core.Action) core.Task {
		t.Helper()
		task, e := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, ConfigID: c.ID, ExpectedConfigVersion: c.Version, Action: action})
		if e != nil {
			t.Fatal(e)
		}
		assertPorts()
		lease, e := db.ClaimTask(ctx, agent.ID)
		if e != nil || lease == nil || lease.ID != task.ID {
			t.Fatalf("claim %+v %v", lease, e)
		}
		return *lease
	}
	complete := func(task core.Task, success bool) {
		t.Helper()
		if e := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: task.LeaseID, Success: success}); e != nil {
			t.Fatal(e)
		}
	}
	upgradeFrom80 := func() {
		t.Helper()
		// Recreate the schema-80 absence of a durable traffic confirmation.
		if _, err := db.pool.Exec(ctx, `ALTER TABLE agent_engine_ownership DROP COLUMN traffic_config_confirmed;
 ALTER TABLE agent_shared_instance_ownership DROP COLUMN traffic_config_confirmed;
 DELETE FROM qcontrolhub_schema_migrations WHERE version>80;
 INSERT INTO qcontrolhub_schema_migrations(version) VALUES(80) ON CONFLICT DO NOTHING`); err != nil {
			t.Fatal(err)
		}
		if err := db.migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	c := save(21002, 0)
	assertPorts()
	quota, e := db.CreatePortTrafficPolicy(owner, core.PortTrafficPolicyRequest{AgentID: agent.ID, Name: "undeployed quota", Engine: c.Engine, Port: 21002, Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly, CycleAnchor: time.Now().UTC(), LimitBytes: 1000})
	if e != nil {
		t.Fatal(e)
	}
	assertPorts()
	validated := claim(c, core.ActionValidate)
	complete(validated, true)
	assertPorts()
	failed := claim(c, core.ActionDeploy)
	assertPorts()
	complete(failed, false)
	assertPorts()
	// Main used to write this attempted revision when claiming a first deploy.
	if _, err := db.pool.Exec(ctx, `UPDATE agent_engine_ownership SET config_id=$2,config_version=$3 WHERE agent_id=$1 AND engine=$4`, agent.ID, c.ID, c.Version, c.Engine); err != nil {
		t.Fatal(err)
	}
	upgradeFrom80()
	assertPorts()
	if _, e = db.pool.Exec(ctx, `UPDATE port_traffic_policies SET used_bytes=17 WHERE id=$1`, quota.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = db.RepairAppliedTrafficPolicies(ctx, agent.ID); e != nil {
		t.Fatal(e)
	}
	var enabled bool
	var used, limit uint64
	if e = db.pool.QueryRow(ctx, `SELECT monitoring_enabled,used_bytes,limit_bytes FROM port_traffic_policies WHERE id=$1`, quota.ID).Scan(&enabled, &used, &limit); e != nil || enabled || used != 17 || limit != 1000 {
		t.Fatalf("repair lost preserved ledger: %v %d %d %v", enabled, used, limit, e)
	}
	task := claim(c, core.ActionDeploy)
	complete(task, true)
	assertPorts(21002)
	c = save(21003, 1)
	assertPorts(21002)
	update, e := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, ConfigID: c.ID, ExpectedConfigVersion: c.Version, Action: core.ActionDeploy})
	if e != nil {
		t.Fatal(e)
	}
	assertPorts(21002)
	lease, e := db.ClaimTask(ctx, agent.ID)
	if e != nil || lease == nil || lease.ID != update.ID {
		t.Fatalf("claim %v", e)
	}
	assertPorts(21002)
	complete(*lease, false)
	assertPorts(21002)
	upgradeFrom80()
	assertPorts(21002)
	// Reopening the same persistent database cannot promote a failed workspace.
	reopened, e := OpenWithConfigKey(ctx, url, true, testEncryptionKey("config-scope"))
	if e != nil {
		t.Fatal(e)
	}
	ep, e := reopened.AppliedTrafficEndpoints(owner)
	reopened.Close()
	if e != nil || len(ep) != 1 || ep[0].Port != 21002 {
		t.Fatalf("restart %+v %v", ep, e)
	}
	stop, e := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, Action: core.ActionStop})
	if e != nil {
		t.Fatal(e)
	}
	lease, e = db.ClaimTask(ctx, agent.ID)
	if e != nil || lease == nil || lease.ID != stop.ID {
		t.Fatalf("stop %v", e)
	}
	complete(*lease, true)
	assertPorts(21002)
	if _, e = db.pool.Exec(ctx, `UPDATE agents SET last_seen=now()-interval '1 hour' WHERE id=$1`, agent.ID); e != nil {
		t.Fatal(e)
	}
	assertPorts(21002)
	c = save(21002, 2)
	task, e = db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, ConfigID: c.ID, ExpectedConfigVersion: c.Version, Action: core.ActionDeploy})
	if e != nil {
		t.Fatal(e)
	}
	lease, e = db.ClaimTask(ctx, agent.ID)
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	complete(*lease, true)
	assertPorts(21002)
}

func TestTrafficPageOwnershipAndVisibility(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	nodeOwner, owner := sharedTestUser(t, db, ctx, "port-node-owner")
	a, alice := sharedTestUser(t, db, ctx, "port-alice")
	b, bob := sharedTestUser(t, db, ctx, "port-bob")
	agent := allEnginesSharedTestAgent(t, db, owner)
	for i, u := range []core.User{a, b} {
		access, e := db.SetUserAgentAccess(ctx, u.ID, core.AgentAccessRequest{Isolated: true, Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineXray}, Ports: []int{22001 + i}}}})
		if e != nil {
			t.Fatal(e)
		}
		acceptSharedTestInvitations(t, db, ctx, u.ID, access)
	}
	for i, account := range []context.Context{owner, alice, bob} {
		c, e := db.SaveAgentConfig(account, core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "same label", Content: strings.ReplaceAll(sharedEngineConfigs()[1].Content, "21002", fmt.Sprint(22000+i))}, 0)
		if e != nil {
			t.Fatal(e)
		}
		task, e := db.CreateTask(account, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, ConfigID: c.ID, ExpectedConfigVersion: c.Version, Action: core.ActionDeploy})
		if e != nil {
			t.Fatal(e)
		}
		lease, e := db.ClaimTask(ctx, agent.ID)
		if e != nil || lease == nil || lease.ID != task.ID {
			t.Fatalf("claim %v", e)
		}
		if e = completeSharedTestTask(db, ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); e != nil {
			t.Fatal(e)
		}
	}
	for _, item := range []struct {
		ctx   context.Context
		owner string
		all   int
	}{{owner, nodeOwner.ID, 3}, {alice, a.ID, 1}, {bob, b.ID, 1}, {WithConfigScope(ctx, "", true), "", 3}} {
		policies, e := db.CurrentPortTrafficPolicies(item.ctx)
		if e != nil || len(policies) != item.all {
			t.Fatalf("scope %+v %v", policies, e)
		}
		mine := 0
		for _, p := range policies {
			if p.OwnerID == "" || p.OwnerName == "" {
				t.Fatalf("missing ownership %+v", p)
			}
			if p.IsCurrentUser {
				mine++
				if p.OwnerID != item.owner {
					t.Fatalf("node owner substituted for config owner %+v", p)
				}
			}
		}
		want := 1
		if item.owner == "" {
			want = 0
		}
		if mine != want {
			t.Fatalf("mine %d want %d", mine, want)
		}
	}
	// A token administrator's empty identity remains distinct from database users.
	c, e := db.SaveAgentConfig(WithConfigScope(ctx, "", true), core.Config{AgentID: agent.ID, Engine: core.EngineMihomo, Name: "legacy admin", Content: "listeners: [{name: legacy, type: http, port: 23001}]\n"}, 0)
	if e != nil {
		t.Fatal(e)
	}
	task, e := db.CreateTask(WithConfigScope(ctx, "", true), core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, ConfigID: c.ID, Action: core.ActionDeploy, ExpectedConfigVersion: c.Version})
	if e != nil {
		t.Fatal(e)
	}
	lease, e := db.ClaimTask(ctx, agent.ID)
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	if e = db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); e != nil {
		t.Fatal(e)
	}
	ep, e := db.AppliedTrafficEndpoints(WithConfigScope(ctx, "", true))
	if e != nil {
		t.Fatal(e)
	}
	mine := 0
	for _, p := range ep {
		if p.IsCurrentUser {
			mine++
			if p.Port != 23001 || p.OwnerID != "" {
				t.Fatalf("legacy admin scope %+v", p)
			}
		}
	}
	if mine != 1 {
		t.Fatalf("legacy mine %d", mine)
	}
}

// A confirmed listener cannot keep a residue for another engine at that port live.
func TestTrafficRepairRejectsOtherEngineAtSamePort(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "repair-identity")
	agent := allEnginesSharedTestAgent(t, db, owner)
	c, err := db.SaveAgentConfig(owner, core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "confirmed", Content: sharedEngineConfigs()[1].Content}, 0)
	if err != nil {
		t.Fatal(err)
	}
	task, err := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, ConfigID: c.ID, ExpectedConfigVersion: c.Version, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil {
		t.Fatalf("claim %v", err)
	}
	if err = db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.pool.Exec(ctx, `UPDATE port_traffic_policies SET engine='mihomo',used_bytes=19 WHERE agent_id=$1 AND port=21002`, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.RepairAppliedTrafficPolicies(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	var used uint64
	if err = db.pool.QueryRow(ctx, `SELECT monitoring_enabled,used_bytes FROM port_traffic_policies WHERE agent_id=$1 AND port=21002`, agent.ID).Scan(&enabled, &used); err != nil || enabled || used != 19 {
		t.Fatalf("foreign engine residue %v %d %v", enabled, used, err)
	}
}

func TestTrafficPageArchiveDeploymentUsesInstanceNode(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "archive-traffic")
	agent := allEnginesSharedTestAgent(t, db, owner)
	c, err := db.CreateConfig(owner, core.Config{Name: "portable archive", Engine: core.EngineXray, Content: sharedEngineConfigs()[1].Content})
	if err != nil {
		t.Fatal(err)
	}
	if c.AgentID != "" {
		t.Fatal("archive unexpectedly attached to a node")
	}
	task, err := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, ConfigID: c.ID, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	before, err := db.AppliedTrafficEndpoints(owner)
	if err != nil || len(before) != 0 {
		t.Fatalf("queued archive visible %+v %v", before, err)
	}
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	if err = db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	ep, err := db.AppliedTrafficEndpoints(owner)
	if err != nil || len(ep) != 1 || ep[0].AgentID != agent.ID || ep[0].ConfigID != c.ID || !ep[0].IsCurrentUser {
		t.Fatalf("applied archive %+v %v", ep, err)
	}
	if _, err = db.RepairAppliedTrafficPolicies(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	policies, err := db.CurrentPortTrafficPolicies(owner)
	if err != nil || len(policies) != 1 || !policies[0].MonitoringEnabled {
		t.Fatalf("archive monitor %+v %v", policies, err)
	}
}
