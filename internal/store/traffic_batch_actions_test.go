package store

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTrafficBatchActionsAndReceipts(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "batch-owner")
	agent := allEnginesSharedTestAgent(t, db, owner)
	c, e := db.SaveAgentConfig(owner, core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "batch", Content: `{"inbounds":[{"tag":"first","protocol":"http","port":21001},{"tag":"second","protocol":"http","port":21002},{"tag":"nearby","protocol":"http","port":21003}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`}, 0)
	if e != nil {
		t.Fatal(e)
	}
	task, e := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, ConfigID: c.ID, ExpectedConfigVersion: c.Version, Action: core.ActionDeploy})
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
	policies, e := db.CurrentPortTrafficPolicies(owner)
	if e != nil || len(policies) != 3 {
		t.Fatalf("policies %+v %v", policies, e)
	}
	ids := []string{policies[0].ID, policies[1].ID}
	untouched := policies[2]
	run := func(input core.TrafficBatchRequest) []core.TrafficBatchResult {
		t.Helper()
		r, e := db.BatchPortTraffic(owner, input)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	read := func(id string) core.PortTrafficPolicy {
		t.Helper()
		p, e := scanTrafficPolicy(db.pool.QueryRow(ctx, `SELECT `+trafficPolicyColumns+` FROM port_traffic_policies WHERE id=$1`, id))
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	limit := uint64(8192)
	input := core.TrafficBatchRequest{RequestID: "batch-quota-00000001", Action: "quota", IDs: ids, Quota: &core.TrafficQuotaPatch{LimitBytes: &limit}}
	for _, r := range run(input) {
		if r.Status != "succeeded" {
			t.Fatalf("quota %+v", r)
		}
	}
	for i, id := range ids {
		p := read(id)
		before := policies[i]
		if p.Name != before.Name || p.Engine != before.Engine || p.Port != before.Port || p.ConfigID != before.ConfigID || p.ConfigVersion != before.ConfigVersion || p.Cycle != before.Cycle || !p.CycleAnchor.Equal(before.CycleAnchor) || p.AutoBlock != before.AutoBlock || p.LimitBytes != limit || !p.QuotaEnabled {
			t.Fatalf("quota overwrote metadata %+v", p)
		}
	}
	if got := read(untouched.ID); !reflect.DeepEqual(got, untouchedPolicy(untouched)) {
		t.Fatalf("nearby record changed\ngot %+v\nwant %+v", got, untouched)
	}
	if _, e = db.pool.Exec(ctx, `UPDATE port_traffic_policies SET used_bytes=123,received_bytes=123,blocked=true WHERE id=ANY($1::text[])`, ids); e != nil {
		t.Fatal(e)
	}
	// A real database failure on one item does not roll back another item.
	if _, e = db.pool.Exec(ctx, `ALTER TABLE port_traffic_policies ADD CONSTRAINT fixture_batch_failure CHECK(id<>`+quoteLiteral(ids[1])+` OR used_bytes<>0)`); e != nil {
		t.Fatal(e)
	}
	before := read(ids[0])
	reset := core.TrafficBatchRequest{RequestID: "batch-reset-00000001", Action: "reset", IDs: ids}
	results := run(reset)
	if results[0].Status != "succeeded" || results[1].Status != "failed" {
		t.Fatalf("partial %+v", results)
	}
	after := read(ids[0])
	if after.UsedBytes != 0 || after.Blocked || after.ResetGeneration != before.ResetGeneration+1 {
		t.Fatalf("reset semantics %+v", after)
	}
	run(reset)
	if p := read(ids[0]); p.ResetGeneration != after.ResetGeneration {
		t.Fatal("duplicate request repeated successful reset")
	}
	if _, e = db.pool.Exec(ctx, `ALTER TABLE port_traffic_policies DROP CONSTRAINT fixture_batch_failure`); e != nil {
		t.Fatal(e)
	}
	retry := core.TrafficBatchRequest{RequestID: "batch-retry-00000001", Action: "reset", IDs: []string{ids[1]}}
	if r := run(retry); r[0].Status != "succeeded" {
		t.Fatalf("retry %+v", r)
	}
	if p := read(ids[0]); p.ResetGeneration != after.ResetGeneration {
		t.Fatal("retry touched successful item")
	}
	// Two requests racing for the same receipt commit precisely one reset.
	concurrent := core.TrafficBatchRequest{RequestID: "batch-concurrent-001", Action: "reset", IDs: ids}
	before = read(ids[0])
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := db.BatchPortTraffic(owner, concurrent)
			if e == nil && (len(r) != 2 || r[0].Status != "succeeded") {
				e = fmt.Errorf("concurrent results %+v", r)
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if p := read(ids[0]); p.ResetGeneration != before.ResetGeneration+1 {
		t.Fatal("concurrent duplicate reset more than once")
	}
	conflicting := concurrent
	conflicting.Action = "delete"
	if _, e := db.BatchPortTraffic(owner, conflicting); e == nil {
		t.Fatal("request ID reused for a different action")
	}
	for _, r := range run(core.TrafficBatchRequest{RequestID: "batch-remove-quota01", Action: "remove-quota", IDs: ids}) {
		if r.Status != "succeeded" {
			t.Fatal(r)
		}
	}
	for _, id := range ids {
		p := read(id)
		if p.QuotaEnabled || p.AutoBlock || p.Blocked || !p.MonitoringEnabled {
			t.Fatalf("remove quota %+v", p)
		}
	}
	for _, id := range ids {
		if _, e = db.pool.Exec(ctx, `INSERT INTO port_traffic_daily_usage(policy_id,reset_generation,usage_date,agent_id,name,engine,port,protocol,used_bytes,first_reported_at,last_reported_at) VALUES($1,1,CURRENT_DATE,$2,'history','xray',21001,'both',12,now(),now())`, id, agent.ID); e != nil {
			t.Fatal(e)
		}
	}
	for _, r := range run(core.TrafficBatchRequest{RequestID: "batch-delete-0000001", Action: "delete", IDs: ids}) {
		if r.Status != "succeeded" {
			t.Fatal(r)
		}
	}
	for _, id := range ids {
		p := read(id)
		if p.MonitoringEnabled || p.UsedBytes != 0 {
			t.Fatalf("delete %+v", p)
		}
		var count int
		if e = db.pool.QueryRow(ctx, `SELECT count(*) FROM port_traffic_daily_usage WHERE policy_id=$1`, id).Scan(&count); e != nil || count != 0 {
			t.Fatalf("history %d %v", count, e)
		}
	}
	if p := read(untouched.ID); p.ResetGeneration != untouched.ResetGeneration || !p.MonitoringEnabled {
		t.Fatal("nearby port affected")
	}
}

func untouchedPolicy(p core.PortTrafficPolicy) core.PortTrafficPolicy {
	p.TrafficOwnership = core.TrafficOwnership{}
	p.DeploymentConfirmed = false
	p.ServiceStatus = ""
	return p
}
func quoteLiteral(value string) string { return "'" + value + "'" } // test-generated hexadecimal IDs only

func TestTrafficBatchRevalidatesPermissionAndDeployment(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "batch-right-owner")
	user, recipient := sharedTestUser(t, db, ctx, "batch-recipient")
	agent := allEnginesSharedTestAgent(t, db, owner)
	access, e := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true, Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineXray}, Ports: []int{21002}}}})
	if e != nil {
		t.Fatal(e)
	}
	acceptSharedTestInvitations(t, db, ctx, user.ID, access)
	c, e := db.SaveAgentConfig(recipient, core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "recipient", Content: sharedEngineConfigs()[1].Content}, 0)
	if e != nil {
		t.Fatal(e)
	}
	task, e := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, ConfigID: c.ID, Action: core.ActionDeploy, ExpectedConfigVersion: c.Version})
	if e != nil {
		t.Fatal(e)
	}
	lease, e := db.ClaimTask(ctx, agent.ID)
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	if e = completeSharedTestTask(db, ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); e != nil {
		t.Fatal(e)
	}
	shared, e := db.CurrentPortTrafficPolicies(owner)
	if e != nil || len(shared) != 1 {
		t.Fatalf("shared %+v %v", shared, e)
	}
	ghost, e := db.CreatePortTrafficPolicy(owner, core.PortTrafficPolicyRequest{AgentID: agent.ID, Name: "unapplied", Engine: core.EngineXray, Port: 21003, Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly, CycleAnchor: time.Now().UTC(), LimitBytes: 100})
	if e != nil {
		t.Fatal(e)
	}
	for _, account := range []context.Context{owner, recipient, WithConfigScope(ctx, "", true)} {
		r, e := db.BatchPortTraffic(account, core.TrafficBatchRequest{RequestID: "batch-permissions-01", Action: "delete", IDs: []string{shared[0].ID, ghost.ID, "trf_0000000000000000"}})
		if e != nil {
			t.Fatal(e)
		}
		for _, result := range r {
			if result.Status != "not_executed" {
				t.Fatalf("unsafe target executed %+v", r)
			}
		}
	}
	var enabled bool
	if e = db.pool.QueryRow(ctx, `SELECT monitoring_enabled FROM port_traffic_policies WHERE id=$1`, shared[0].ID).Scan(&enabled); e != nil || !enabled {
		t.Fatal("shared accounting disabled")
	}
}
