package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestUnrestrictedSharedPortsAllEnginesRemainMetered(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, recipient := sharedTestUser(t, db, ctx, "ports-all-engines")
	agent := allEnginesSharedTestAgent(t, db, ctx)
	access, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true,
		Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: core.AllEngines(), Ports: []int{0}, LimitBytes: 1000}}})
	if err != nil {
		t.Fatal(err)
	}
	acceptSharedTestInvitations(t, db, ctx, user.ID, access)
	var request core.TaskRequest
	for _, config := range sharedEngineConfigs() {
		config.AgentID = agent.ID
		saved, err := db.SaveAgentConfig(recipient, config, 0)
		if err != nil {
			t.Fatal(err)
		}
		request = core.TaskRequest{AgentID: agent.ID, Engine: saved.Engine, ConfigID: saved.ID, Action: core.ActionDeploy}
		task, err := db.CreateTask(recipient, request)
		if err != nil {
			t.Fatal(err)
		}
		lease, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || lease == nil || lease.ID != task.ID {
			t.Fatalf("claim %s: %+v %v", saved.Engine, lease, err)
		}
		if err := completeSharedTestTask(db, ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
			t.Fatal(err)
		}
	}
	// A successful first deployment must not turn unlimited authority into
	// an allowlist of its first listeners. Retain the old billing binding.
	changed, err := db.AgentConfig(recipient, agent.ID, core.EngineMihomo)
	if err != nil {
		t.Fatal(err)
	}
	changed.Content = strings.ReplaceAll(changed.Content, "21001", "22001")
	changed, err = db.SaveAgentConfig(recipient, changed, changed.Version)
	if err != nil {
		t.Fatal(err)
	}
	task, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: changed.Engine, ConfigID: changed.ID, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil || lease.ID != task.ID {
		t.Fatalf("claim changed port: %+v %v", lease, err)
	}
	if err := completeSharedTestTask(db, ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 4 {
		t.Fatalf("missing actual-port accounting: %+v %v", policies, err)
	}
	var retired bool
	if err := db.pool.QueryRow(ctx, `SELECT NOT monitoring_enabled FROM port_traffic_policies WHERE agent_id=$1 AND port=21001`, agent.ID).Scan(&retired); err != nil || !retired {
		t.Fatalf("old shared port must retain history without live monitoring: %v %v", retired, err)
	}
	now := time.Now().UTC().Add(time.Second)
	usages := make([]core.PortTrafficUsage, 0, len(policies))
	for _, policy := range policies {
		if policy.Port == 0 || policy.SharedQuota == nil || !policy.SharedQuota.AllowsEngine(policy.Engine) {
			t.Fatalf("invalid shared runtime policy: %+v", policy)
		}
		start, end, _ := core.TrafficPeriodAt(policy.CycleAnchor, policy.Cycle, now)
		usages = append(usages, core.PortTrafficUsage{PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration,
			ReceivedBytes: 250, UsedBytes: 250, LifetimeReceivedBytes: 250, CounterEpoch: strings.Repeat("a", 32),
			CollectedAt: now, PeriodStart: start, PeriodEnd: end, EnforcementAvailable: true,
			ShareID: policy.SharedQuota.ID, ShareUsedBytes: 250})
	}
	for range 2 {
		if err := db.UpdatePortTrafficUsage(ctx, agent.ID, usages, now); err != nil {
			t.Fatal(err)
		}
	}
	access, err = db.UserAgentAccess(recipient, user.ID)
	if err != nil || len(access.Shares) != 1 || !slices.Equal(access.Shares[0].Ports, []int{0}) || access.Shares[0].UsedBytes != 1000 {
		t.Fatalf("unrestricted usage lost or counted twice: %+v %v", access, err)
	}
	for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
		request.Action = action
		if _, err := db.CreateTask(recipient, request); !errors.Is(err, ErrConflict) {
			t.Fatalf("unrestricted port mode bypassed exhausted allowance: %v", err)
		}
	}
}

func TestUnrestrictedSharedPortsStillRequireFixedNonReservedListeners(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, recipient := sharedTestUser(t, db, ctx, "ports-listener-safety")
	agent := sharedTestAgent(t, db, ctx)
	access := sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 0, 0)
	for _, content := range []string{
		"listeners: [{name: a, type: http, port: 0}]",
		"listeners: [{name: a, type: http, port: 10085}]",
		"listeners: [{name: a, type: http, port: 10086}]",
		"listeners: [{name: a, type: http, port: 21001}]\ntun: {enable: true}",
		"listeners: [{name: a, type: http, port: 21001}]\nexternal-controller: 0.0.0.0:9090",
	} {
		config, err := db.CreateConfig(recipient, core.Config{Engine: core.EngineMihomo, Name: "unsafe listener", Content: content})
		if err != nil {
			t.Fatal(err)
		}
		for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
			if _, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: action}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("unrestricted port authority bypassed fixed listener validation: %v, content=%s", err, content)
			}
		}
	}
	var count int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM agent_share_ports WHERE share_id=$1`, access.Shares[0].ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid configuration reserved actual ports: %d %v", count, err)
	}
}

func TestUnrestrictedSharedPortsAdoptExactRunningSnapshot(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, recipient := sharedTestUser(t, db, ctx, "ports-adopt")
	agent := sharedTestAgent(t, db, recipient)
	config := sharedTestConfig(t, db, recipient, agent.ID, 21001)
	task, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil {
		t.Fatalf("claim: %+v %v", lease, err)
	}
	if err := completeSharedTestTask(db, ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	config.Content = strings.ReplaceAll(config.Content, "21001", "21002")
	if _, err := db.SaveAgentConfig(recipient, config, config.Version); err != nil {
		t.Fatal(err)
	}
	// Simulate a legacy pre-ownership service. Acceptance must adopt the
	// running snapshot, never reserve or meter a newer saved draft.
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET owner_id='' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	access, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true,
		Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{0}, LimitBytes: 1000}}})
	if err != nil {
		t.Fatal(err)
	}
	if policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID); err != nil || len(policies) != 1 || policies[0].SharedQuota != nil {
		t.Fatalf("pending invitation adopted a running service: %+v %v", policies, err)
	}
	acceptSharedTestInvitations(t, db, ctx, user.ID, access)
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].Port != 21001 || policies[0].SharedQuota == nil {
		t.Fatalf("acceptance did not meter the exact deployed snapshot: %+v %v", policies, err)
	}
}

func TestBorrowedPortClaimsSettlementAndAccounting(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	original, alice := sharedTestUser(t, db, ctx, "borrow-original")
	borrower, bob := sharedTestUser(t, db, ctx, "borrow-actual")
	agent := sharedTestAgent(t, db, ctx)
	allocation := sharedTestAllocation(t, db, ctx, original.ID, agent.ID, 10000, 21001)
	borrowed := sharedTestAllocation(t, db, ctx, borrower.ID, agent.ID, 10000, 0)
	a := sharedTestConfig(t, db, alice, agent.ID, 21001)
	b := sharedTestConfig(t, db, bob, agent.ID, 21001)
	create := func(scope context.Context, config core.Config, action core.Action) (core.Task, error) {
		return db.CreateTask(scope, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: action})
	}
	complete := func(task core.Task) {
		t.Helper()
		lease, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || lease == nil || lease.ID != task.ID {
			t.Fatalf("claim: %+v %v", lease, err)
		}
		if err := completeSharedTestTask(db, ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, TrafficSettled: true}); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := create(bob, b, core.ActionDeploy)
	if err != nil {
		t.Fatal(err)
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].SharedQuota.ID != borrowed.Shares[0].ID {
		t.Fatalf("borrowed accounting: %+v %v", policies, err)
	}
	canceledPolicy := policies[0].ID
	if _, err := create(alice, a, core.ActionValidate); !errors.Is(err, ErrConflict) {
		t.Fatalf("allocation owner bypassed pending claim: %v", err)
	}
	if err := db.CancelTask(bob, pending.ID); err != nil {
		t.Fatal(err)
	}
	active, err := create(alice, a, core.ActionDeploy)
	if err != nil {
		t.Fatalf("canceled claim was not released: %v", err)
	}
	complete(active)
	policies, err = db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].ID == canceledPolicy {
		t.Fatalf("canceled claim reused metering identity: %+v %v", policies, err)
	}
	old := policies[0]
	if _, err := create(bob, b, core.ActionDeploy); !errors.Is(err, ErrConflict) {
		t.Fatalf("live deployment bypassed: %v", err)
	}
	stop, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: a.Engine, Action: core.ActionStop})
	if err != nil {
		t.Fatal(err)
	}
	complete(stop)
	active, err = create(bob, b, core.ActionDeploy)
	if err != nil {
		t.Fatalf("settled stopped port unavailable: %v", err)
	}
	complete(active)
	if _, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: a.Engine, Action: core.ActionStart}); !errors.Is(err, ErrConflict) {
		t.Fatalf("original allocation reclaimed active borrower: %v", err)
	}
	policies, err = db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].ID == old.ID || policies[0].SharedQuota.ID != borrowed.Shares[0].ID {
		t.Fatalf("handoff metering: %+v %v", policies, err)
	}
	now := time.Now().UTC().Add(time.Second)
	var samples []core.PortTrafficUsage
	for _, p := range []core.PortTrafficPolicy{old, policies[0]} {
		start, end, _ := core.TrafficPeriodAt(p.CycleAnchor, p.Cycle, now)
		samples = append(samples, core.PortTrafficUsage{PolicyID: p.ID, ResetGeneration: p.ResetGeneration, ShareID: p.SharedQuota.ID,
			ShareUsedBytes: 123, ReceivedBytes: 123, UsedBytes: 123, LifetimeReceivedBytes: 123, CounterEpoch: strings.Repeat("b", 32), CollectedAt: now,
			PeriodStart: start, PeriodEnd: end, EnforcementAvailable: true})
	}
	for range 2 {
		if err := db.UpdatePortTrafficUsage(ctx, agent.ID, samples, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct {
		id   string
		want uint64
	}{{allocation.Shares[0].ID, 0}, {borrowed.Shares[0].ID, 123}} {
		var used uint64
		if err := db.pool.QueryRow(ctx, `SELECT used_bytes FROM agent_shares WHERE id=$1`, item.id).Scan(&used); err != nil || used != item.want {
			t.Fatalf("wrong ledger: %d want %d: %v", used, item.want, err)
		}
	}
	var assigned string
	if err := db.pool.QueryRow(ctx, `SELECT share_id FROM agent_share_ports WHERE agent_id=$1 AND port=21001`, agent.ID).Scan(&assigned); err != nil || assigned != allocation.Shares[0].ID {
		t.Fatalf("original allocation changed: %s %v", assigned, err)
	}
	// Editing quota retains a borrowed policy; narrowing authority cannot orphan
	// live metering simply because there is no allocation row for the borrower.
	sharedTestAllocation(t, db, ctx, borrower.ID, agent.ID, 20000, 0)
	if _, err := db.SetUserAgentAccess(ctx, borrower.ID, core.AgentAccessRequest{Isolated: true, Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{b.Engine}, Ports: nil, LimitBytes: 20000}}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("scope narrowing orphaned borrowed metering: %v", err)
	}
	b.Content = strings.ReplaceAll(b.Content, "21001", "21002")
	b, err = db.SaveAgentConfig(bob, b, b.Version)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := create(bob, b, core.ActionDeploy)
	if err != nil {
		t.Fatal(err)
	}
	complete(changed)
	returned, err := create(alice, a, core.ActionDeploy)
	if err != nil {
		t.Fatalf("retired endpoint still blocked after port change: %v", err)
	}
	complete(returned)
}
