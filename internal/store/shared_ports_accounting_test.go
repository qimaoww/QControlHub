package store

import (
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
		if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
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
	if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 5 {
		t.Fatalf("missing actual-port accounting: %+v %v", policies, err)
	}
	now := time.Now().UTC().Add(time.Second)
	usages := make([]core.PortTrafficUsage, 0, len(policies))
	for _, policy := range policies {
		if policy.Port == 0 || policy.SharedQuota == nil || !policy.SharedQuota.AllowsEngine(policy.Engine) {
			t.Fatalf("invalid shared runtime policy: %+v", policy)
		}
		start, end, _ := core.TrafficPeriodAt(policy.CycleAnchor, policy.Cycle, now)
		usages = append(usages, core.PortTrafficUsage{PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration,
			ReceivedBytes: 200, UsedBytes: 200, LifetimeReceivedBytes: 200, CounterEpoch: strings.Repeat("a", 32),
			CollectedAt: now, PeriodStart: start, PeriodEnd: end, EnforcementAvailable: true,
			ShareID: policy.SharedQuota.ID, ShareUsedBytes: 200})
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
	if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
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
	if policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID); err != nil || len(policies) != 0 {
		t.Fatalf("pending invitation adopted a running service: %+v %v", policies, err)
	}
	acceptSharedTestInvitations(t, db, ctx, user.ID, access)
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].Port != 21001 || policies[0].SharedQuota == nil {
		t.Fatalf("acceptance did not meter the exact deployed snapshot: %+v %v", policies, err)
	}
}
