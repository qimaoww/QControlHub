package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTrafficDeploymentAcknowledgesDeletedArchive(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	config, err := db.CreateConfig(ctx, core.Config{
		Name: "deleted during deployment", Engine: core.EngineMihomo, Content: sharedEngineConfigs()[0].Content,
	})
	if err != nil {
		t.Fatal(err)
	}
	claim := func() core.Task {
		t.Helper()
		task, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
		if err != nil {
			t.Fatal(err)
		}
		lease, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || lease == nil || lease.ID != task.ID {
			t.Fatalf("claim: %+v %v", lease, err)
		}
		return *lease
	}
	first := claim()
	if err = db.CompleteTask(ctx, agent.ID, first.ID, core.TaskResultRequest{LeaseID: first.LeaseID, Success: true, TrafficSettled: true}); err != nil {
		t.Fatal(err)
	}
	before, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(before) != 1 {
		t.Fatalf("initial deployment not monitored: %+v %v", before, err)
	}
	config.Content = strings.ReplaceAll(config.Content, "21001", "21002")
	config, err = db.UpdateConfig(ctx, config.ID, config)
	if err != nil {
		t.Fatal(err)
	}
	lease := claim()
	if err = db.DeleteConfig(ctx, config.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.CompleteTask(ctx, agent.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, TrafficSettled: true}); err != nil {
		t.Fatalf("deleted archive prevented deployment acknowledgement: %v", err)
	}
	completed, err := db.GetTaskState(ctx, lease.ID)
	if err != nil || completed.Status != core.TaskSucceeded {
		t.Fatalf("deployment result not retained: %+v %v", completed, err)
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].ID != before[0].ID || policies[0].Port != 21001 || !policies[0].MonitoringEnabled {
		t.Fatalf("missing revision changed accounting policies: %+v %v", policies, err)
	}
}

func TestSharedTrafficSyncUsesAppliedRevisionAndRejectsStaleSelection(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "sync-owner")
	a, alice := sharedTestUser(t, db, ctx, "sync-alice")
	b, bob := sharedTestUser(t, db, ctx, "sync-bob")
	agent := allEnginesSharedTestAgent(t, db, owner)
	allocate := func(user core.User, ports []int) string {
		t.Helper()
		access, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true, Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineXray}, Ports: ports}}})
		if err != nil {
			t.Fatal(err)
		}
		return acceptSharedTestInvitations(t, db, ctx, user.ID, access).Shares[0].ID
	}
	aShare := allocate(a, []int{21001, 21002, 21003})
	allocate(b, []int{22001})
	save := func(account context.Context, port, version int) core.Config {
		t.Helper()
		content := strings.ReplaceAll(sharedEngineConfigs()[1].Content, "21002", strconv.Itoa(port))
		c, err := db.SaveAgentConfig(account, core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "same core", Content: content}, version)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	deploy := func(account context.Context, c core.Config, success bool) {
		t.Helper()
		task, err := db.CreateTask(account, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, ConfigID: c.ID, ExpectedConfigVersion: c.Version, Action: core.ActionDeploy})
		if err != nil {
			t.Fatal(err)
		}
		lease, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || lease == nil || lease.ID != task.ID {
			t.Fatalf("claim: %+v %v", lease, err)
		}
		if err = completeSharedTestTask(db, ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: success, TrafficSettled: true}); err != nil {
			t.Fatal(err)
		}
	}
	preview := func(account context.Context) TrafficSyncPreview {
		t.Helper()
		p, err := db.PreviewPortTrafficSync(account)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	c := save(alice, 21001, 0)
	deploy(alice, c, true)
	deploy(bob, save(bob, 22001, 0), true)
	initial := preview(alice)
	if len(initial.Endpoints) != 1 || initial.Endpoints[0].Port != 21001 || initial.Endpoints[0].ShareID != aShare {
		t.Fatalf("wrong instance: %+v", initial)
	}
	ownerPreview := preview(owner)
	if len(ownerPreview.Endpoints) != 2 {
		t.Fatalf("owner cannot inspect allocations: %+v", ownerPreview)
	}
	for _, candidate := range ownerPreview.Candidates {
		if candidate.ShareID == "" || candidate.BlockedReason == "" {
			t.Fatalf("owner offered another user's sync: %+v", candidate)
		}
	}
	c = save(alice, 21002, 1)
	pending := preview(alice)
	if len(pending.Candidates) != 2 || len(pending.Endpoints) != 1 || pending.Endpoints[0].Port != 21001 {
		t.Fatalf("draft replaced runtime: %+v", pending)
	}
	var change core.TrafficSyncCandidate
	for _, candidate := range pending.Candidates {
		if candidate.Port == 21002 {
			change = candidate
		}
	}
	if change.PreviousPort != 21001 || change.DeploymentStatus != "pending" || change.BlockedReason == "" {
		t.Fatalf("missing pending change: %+v", change)
	}
	choose := func(port int) []core.TrafficSyncSelection {
		return []core.TrafficSyncSelection{{AgentID: agent.ID, Port: port}}
	}
	if _, err := db.SyncPortTrafficSnapshot(alice, initial.Snapshot, choose(21001)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale scan accepted: %v", err)
	}
	if _, err := db.SyncPortTrafficSnapshot(alice, pending.Snapshot, choose(21002)); !errors.Is(err, ErrConflict) {
		t.Fatalf("undeployed port accepted: %v", err)
	}
	// Retain actual old policy/history ownership through the replacement.
	old, err := db.AgentPortTrafficPolicies(alice, agent.ID)
	if err != nil || len(old) != 1 {
		t.Fatalf("policies: %+v %v", old, err)
	}
	if _, err = db.pool.Exec(ctx, `INSERT INTO port_traffic_daily_usage(policy_id,reset_generation,usage_date,agent_id,name,engine,port,protocol,used_bytes,first_reported_at,last_reported_at)
 VALUES($1,1,CURRENT_DATE,$2,'history','xray',21001,'both',123,now(),now())`, old[0].ID, agent.ID); err != nil {
		t.Fatal(err)
	}
	deploy(alice, c, false)
	failed := preview(alice)
	for _, candidate := range failed.Candidates {
		if candidate.DeploymentStatus == "applied" && candidate.BlockedReason == "" {
			t.Fatal("failed deployment treated as confirmed")
		}
	}
	deploy(alice, c, true)
	applied := preview(alice)
	if len(applied.Endpoints) != 1 || applied.Endpoints[0].Port != 21002 {
		t.Fatalf("new deployment not discovered: %+v", applied)
	}
	for i := 0; i < 2; i++ {
		if changed, err := db.SyncPortTrafficSnapshot(alice, applied.Snapshot, choose(21002)); err != nil || len(changed) != 0 {
			t.Fatalf("repeat changed counters: %v %v", changed, err)
		}
	}
	history, err := db.ListPortTrafficDailyUsage(alice, agent.ID, old[0].ID, time.Now().UTC())
	if err != nil || len(history) != 1 || history[0].UsedBytes != 123 {
		t.Fatalf("history lost: %+v %v", history, err)
	}
	if _, err := db.SyncPortTrafficSnapshot(bob, preview(bob).Snapshot, choose(21002)); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-user sync: %v", err)
	}
	c = save(alice, 21003, 2)
	if _, err := db.SyncPortTrafficSnapshot(alice, applied.Snapshot, choose(21002)); !errors.Is(err, ErrConflict) {
		t.Fatalf("open dialog race: %v", err)
	}
	deploy(alice, c, true)
	final := preview(alice)
	if len(final.Endpoints) != 1 || final.Endpoints[0].Port != 21003 {
		t.Fatalf("A→B→C uses wrong version: %+v", final)
	}
	active, err := db.AgentPortTrafficPolicies(alice, agent.ID)
	if err != nil || len(active) != 1 || active[0].Port != 21003 {
		t.Fatalf("retired rules still active: %+v %v", active, err)
	}
	bobActive, err := db.AgentPortTrafficPolicies(bob, agent.ID)
	if err != nil || len(bobActive) != 1 || bobActive[0].Port != 22001 {
		t.Fatalf("other user changed: %+v %v", bobActive, err)
	}
	c = save(alice, 22001, 3)
	if _, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, ConfigID: c.ID, Action: core.ActionDeploy}); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign port deployed: %v", err)
	}
}
