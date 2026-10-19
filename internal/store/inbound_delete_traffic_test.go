package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestDeletedInboundRetiresQuotaAndRejectsOldDiscovery(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "delete-owner")
	agent := allEnginesSharedTestAgent(t, db, owner)
	content := `{"inbounds":[{"tag":"keep","protocol":"socks","port":21001},{"tag":"remove","protocol":"socks","port":21002}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`
	save := func(value string, version int) core.Config {
		t.Helper()
		c, e := db.SaveAgentConfig(owner, core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "delete lifecycle", Content: value}, version)
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	deploy := func(c core.Config, success bool) core.Task {
		t.Helper()
		task, e := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, Action: core.ActionDeploy, ConfigID: c.ID, ExpectedConfigVersion: c.Version})
		if e != nil {
			t.Fatal(e)
		}
		lease, e := db.ClaimTask(ctx, agent.ID)
		if e != nil || lease == nil || lease.ID != task.ID {
			t.Fatalf("claim: %v", e)
		}
		if e := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: success}); e != nil {
			t.Fatal(e)
		}
		return *lease
	}
	c := save(content, 0)
	oldTask := deploy(c, true)
	policies, e := db.AgentPortTrafficPolicies(owner, agent.ID)
	if e != nil || len(policies) != 2 {
		t.Fatalf("initial policies %+v %v", policies, e)
	}
	var retired core.PortTrafficPolicy
	for _, p := range policies {
		if p.Port == 21002 {
			retired = p
		}
	}
	if _, e := db.pool.Exec(ctx, `UPDATE port_traffic_policies SET quota_enabled=true,limit_bytes=100000,used_bytes=123,received_bytes=123,receive_bps=100,send_bps=50 WHERE id=$1`, retired.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := db.pool.Exec(ctx, `INSERT INTO port_traffic_daily_usage(policy_id,reset_generation,usage_date,agent_id,name,engine,port,protocol,used_bytes,first_reported_at,last_reported_at) VALUES($1,1,CURRENT_DATE,$2,'remove','xray',21002,'both',123,now(),now())`, retired.ID, agent.ID); e != nil {
		t.Fatal(e)
	}
	before := c
	c = save(`{"inbounds":[{"tag":"keep","protocol":"socks","port":21001}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`, 1)
	assertActive := func(want int) {
		t.Helper()
		p, e := db.AgentPortTrafficPolicies(owner, agent.ID)
		if e != nil || len(p) != want {
			t.Fatalf("active %+v %v want %d", p, e, want)
		}
	}
	assertActive(2) // draft must not change the running monitor set
	deploy(c, false)
	assertActive(2)
	deploy(c, true)
	assertActive(1) // TrafficSettled=false must not retain a live card
	if e := db.CompleteTask(ctx, agent.ID, oldTask.ID, core.TaskResultRequest{LeaseID: oldTask.LeaseID, Success: true}); !errors.Is(e, ErrConflict) {
		t.Fatalf("old result accepted %v", e)
	}
	stale := configurationTrafficEndpoints(before, before.Content, before.Version, "")
	if _, e := db.ReconcileAgentPortTrafficEndpoints(ctx, agent.ID, stale, false); e != nil {
		t.Fatal(e)
	}
	assertActive(1)
	for i := 0; i < 2; i++ {
		if changed, e := db.RepairAppliedTrafficPolicies(ctx, agent.ID); e != nil || len(changed) != 0 {
			t.Fatalf("repeated repair changed policies: %v %v", changed, e)
		}
	}
	var enabled, quota bool
	var used, rate, generation uint64
	if e := db.pool.QueryRow(ctx, `SELECT monitoring_enabled,quota_enabled,used_bytes,receive_bps+send_bps,reset_generation FROM port_traffic_policies WHERE id=$1`, retired.ID).Scan(&enabled, &quota, &used, &rate, &generation); e != nil {
		t.Fatal(e)
	}
	if enabled || !quota || used != 123 || rate != 0 || generation <= retired.ResetGeneration {
		t.Fatalf("retirement lost history/quota or retained rate: %v %v %d %d %d", enabled, quota, used, rate, generation)
	}
	history, e := db.ListPortTrafficDailyUsage(owner, agent.ID, retired.ID, time.Now().UTC())
	if e != nil || len(history) != 1 || history[0].UsedBytes != 123 {
		t.Fatalf("history %+v %v", history, e)
	}
	// Emulate old residue, then the durable repair used after panel restart.
	if _, e := db.pool.Exec(ctx, `UPDATE port_traffic_policies SET monitoring_enabled=true,config_id='',config_version=0,inbound_tag='' WHERE id=$1`, retired.ID); e != nil {
		t.Fatal(e)
	}
	if changed, e := db.RepairAppliedTrafficPolicies(ctx, agent.ID); e != nil || len(changed) != 1 || changed[0] != agent.ID {
		t.Fatalf("repair must notify the connected agent: %v %v", changed, e)
	}
	assertActive(1)
	// A new inbound on the same port must get a new reporting generation.
	c = save(`{"inbounds":[{"tag":"new-owner","protocol":"socks","port":21002}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`, 2)
	deploy(c, true)
	assertActive(1)
	p, e := db.AgentPortTrafficPolicies(owner, agent.ID)
	if e != nil || p[0].InboundTag != "new-owner" || p[0].ResetGeneration <= generation || p[0].UsedBytes != 123 {
		t.Fatalf("reuse %+v %v", p, e)
	}
	start, end, _ := core.TrafficPeriodAt(p[0].CycleAnchor, p[0].Cycle, time.Now().UTC())
	if e := db.UpdatePortTrafficUsage(context.Background(), agent.ID, []core.PortTrafficUsage{{PolicyID: retired.ID, ResetGeneration: retired.ResetGeneration, ReceivedBytes: 999, UsedBytes: 999, PeriodStart: start, PeriodEnd: end, EnforcementAvailable: true}}, time.Now().UTC()); e != nil {
		t.Fatal(e)
	}
	p, _ = db.AgentPortTrafficPolicies(owner, agent.ID)
	if p[0].UsedBytes != 123 {
		t.Fatal("old sample contaminated reused listener")
	}
	c = save(`{"inbounds":[],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`, 3)
	deploy(c, true)
	assertActive(0)
	endpoints, e := db.AppliedTrafficEndpoints(owner)
	if e != nil || len(endpoints) != 0 {
		t.Fatalf("empty %+v %v", endpoints, e)
	}
}

func TestSharedInboundDeletionRejectsLateIdentityAndKeepsOtherInstances(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "retire-node-owner")
	user, alice := sharedTestUser(t, db, ctx, "retire-alice")
	other, bob := sharedTestUser(t, db, ctx, "retire-bob")
	agent := allEnginesSharedTestAgent(t, db, owner)
	allocate := func(u core.User, ports []int) string {
		t.Helper()
		access, err := db.SetUserAgentAccess(ctx, u.ID, core.AgentAccessRequest{Isolated: true, Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineXray}, Ports: ports, LimitBytes: 10000}}})
		if err != nil {
			t.Fatal(err)
		}
		return acceptSharedTestInvitations(t, db, ctx, u.ID, access).Shares[0].ID
	}
	share := allocate(user, []int{21001, 21002})
	allocate(other, []int{22001})
	content := func(port int) string {
		return strings.ReplaceAll(sharedEngineConfigs()[1].Content, "21002", fmt.Sprint(port))
	}
	save := func(account context.Context, value string, version int) core.Config {
		t.Helper()
		c, err := db.SaveAgentConfig(account, core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "shared deletion", Content: value}, version)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	deploy := func(account context.Context, c core.Config, result core.TaskResultRequest) core.Task {
		t.Helper()
		task, err := db.CreateTask(account, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, Action: core.ActionDeploy, ConfigID: c.ID, ExpectedConfigVersion: c.Version})
		if err != nil {
			t.Fatal(err)
		}
		lease, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || lease == nil || lease.ID != task.ID {
			t.Fatalf("claim %v %v", lease, err)
		}
		result.LeaseID = lease.LeaseID
		if result.InstanceStatus != nil {
			result.InstanceStatus.TaskID = task.ID
		}
		if err := completeSharedTestTask(db, ctx, agent.ID, task.ID, result); err != nil {
			t.Fatal(err)
		}
		return task
	}
	base := save(owner, content(23001), 0)
	deploy(owner, base, core.TaskResultRequest{Success: true})
	b := save(bob, content(22001), 0)
	deploy(bob, b, core.TaskResultRequest{Success: true})
	a := save(alice, content(21001), 0)
	old := deploy(alice, a, core.TaskResultRequest{Success: true})
	a = save(alice, content(21002), 1)
	deploy(alice, a, core.TaskResultRequest{Success: true})
	a = save(alice, `{"inbounds":[],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`, 2)
	// Explicit rollback evidence retains the prior confirmed monitoring set.
	deploy(alice, a, core.TaskResultRequest{Error: "rolled back", InstanceStatus: &core.SharedInstanceStatus{Engine: core.EngineXray, ShareID: share, ConfigID: a.ID, ConfigVersion: 2, Status: "active", ObservedAt: time.Now().UTC()}})
	if p, err := db.AgentPortTrafficPolicies(alice, agent.ID); err != nil || len(p) != 1 || p[0].Port != 21002 {
		t.Fatalf("rollback %+v %v", p, err)
	}
	deploy(alice, a, core.TaskResultRequest{Success: true})
	// A legacy observation has no sample clock and identifies an old deployment.
	// It must not restore that deployment even after a reconnect.
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, SharedInstances: []core.SharedInstanceStatus{{Engine: core.EngineXray, ShareID: share, ConfigID: a.ID, ConfigVersion: 1, TaskID: old.ID, AppliedTaskID: old.ID, Status: "active"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RepairAppliedTrafficPolicies(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if p, err := db.AgentPortTrafficPolicies(alice, agent.ID); err != nil || len(p) != 0 {
		t.Fatalf("retired shared monitor revived %+v %v", p, err)
	}
	for account, port := range map[context.Context]int{owner: 23001, bob: 22001} {
		p, err := db.AgentPortTrafficPolicies(account, agent.ID)
		found := false
		for _, policy := range p {
			if policy.Port == port {
				found = true
			}
		}
		if err != nil || !found {
			t.Fatalf("other instance altered %+v %v", p, err)
		}
	}
	var version int
	if err := db.pool.QueryRow(ctx, `SELECT config_version FROM agent_shared_instance_ownership WHERE share_id=$1 AND engine='xray'`, share).Scan(&version); err != nil || version != 3 {
		t.Fatalf("late identity changed applied version %d %v", version, err)
	}
}
