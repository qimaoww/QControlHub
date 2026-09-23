package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func sharedTestUser(t *testing.T, db *Store, ctx context.Context, name string) (core.User, context.Context) {
	t.Helper()
	user, err := db.CreateUser(ctx, core.UserRequest{Username: name, Role: core.RoleUser, Permissions: core.GrantablePermissions()}, "test-only-hash")
	if err != nil {
		t.Fatal(err)
	}
	return user, WithConfigScope(ctx, user.ID, false)
}

func sharedTestAgent(t *testing.T, db *Store, ctx context.Context) core.Agent {
	t.Helper()
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET features='["shared-traffic-v1","shared-engines-v1","independent-egress-v1"]' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	return agent
}

func sharedTestAllocation(t *testing.T, db *Store, ctx context.Context, userID, agentID string, limit uint64, ports ...int) core.AgentAccess {
	t.Helper()
	access, err := db.SetUserAgentAccess(ctx, userID, core.AgentAccessRequest{Isolated: true, Shares: []core.AgentShareRequest{{
		AgentID: agentID, Engines: []core.Engine{core.EngineMihomo}, LimitBytes: limit, Ports: ports,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return acceptSharedTestInvitations(t, db, ctx, userID, access)
}

func acceptSharedTestInvitations(t *testing.T, db *Store, ctx context.Context, userID string, access core.AgentAccess) core.AgentAccess {
	t.Helper()
	for _, share := range access.Shares {
		if !share.Enabled || share.Status != core.AgentSharePending {
			continue
		}
		var err error
		access, err = db.RespondAgentShare(WithConfigScope(ctx, userID, false), share.ID,
			core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "accept"})
		if err != nil {
			t.Fatal(err)
		}
	}
	return access
}

func sharedTestConfig(t *testing.T, db *Store, ctx context.Context, agentID string, ports ...int) core.Config {
	t.Helper()
	content := "listeners:\n"
	for _, port := range ports {
		content += fmt.Sprintf("  - {name: p%d, type: trojan, port: %d}\n", port, port)
	}
	config, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agentID, Engine: core.EngineMihomo, Name: "private", Content: content}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func TestAgentIsolationCoversListsAndDirectStoreCalls(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, alice := sharedTestUser(t, db, ctx, "isolated-alice")
	other, bob := sharedTestUser(t, db, ctx, "isolated-bob")
	allowed, denied := sharedTestAgent(t, db, ctx), sharedTestAgent(t, db, ctx)
	access, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true,
		Shares: []core.AgentShareRequest{{AgentID: allowed.ID, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21001}}, {AgentID: denied.ID, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21002}}}})
	if err != nil {
		t.Fatal(err)
	}
	acceptSharedTestInvitations(t, db, ctx, user.ID, access)
	sharedTestAllocation(t, db, ctx, other.ID, allowed.ID, 1000, 21003)
	own := sharedTestConfig(t, db, alice, allowed.ID, 21001)
	hidden := sharedTestConfig(t, db, alice, denied.ID, 21002)
	sharedTestConfig(t, db, bob, allowed.ID, 21003)
	task, err := db.CreateTask(alice, core.TaskRequest{AgentID: denied.ID, Engine: core.EngineMihomo, Action: core.ActionStatus})
	if err != nil {
		t.Fatal(err)
	}
	sharedTestAllocation(t, db, ctx, user.ID, allowed.ID, 1000, 21001)
	if agents, err := db.ListAgentsWithEnrollmentCommands(alice); err != nil || len(agents) != 1 || agents[0].ID != allowed.ID || agents[0].EnrollmentCommandAvailable {
		t.Fatalf("agent list: %+v %v", agents, err)
	}
	for _, check := range []func() error{
		func() error { _, err := db.GetAgent(alice, denied.ID); return err },
		func() error { _, err := db.AgentName(alice, denied.ID); return err },
		func() error { _, err := db.AgentConfig(alice, denied.ID, core.EngineMihomo); return err },
		func() error { _, err := db.ConfigRevision(alice, hidden.ID, 1); return err },
		func() error { _, err := db.RestoreConfigRevision(alice, hidden.ID, 1, 1); return err },
		func() error { _, err := db.GetTask(alice, task.ID); return err },
		func() error { return db.CancelTask(alice, task.ID) },
		func() error {
			_, err := db.CreateTask(alice, core.TaskRequest{AgentID: denied.ID, Engine: core.EngineMihomo, Action: core.ActionStatus})
			return err
		},
	} {
		if err := check(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("invisible Agent resource did not return not found: %v", err)
		}
	}
	if configs, err := db.ListAgentConfigs(alice); err != nil || len(configs) != 1 || configs[0].ID != own.ID {
		t.Fatalf("config list: %+v %v", configs, err)
	}
	if overview, err := db.Overview(alice); err != nil || overview.Agents != 1 || overview.NodeConfigs != 1 || overview.TasksPending != 0 {
		t.Fatalf("overview leaks revoked scope: %+v %v", overview, err)
	}
	if _, err := db.CreateTask(alice, core.TaskRequest{AgentID: allowed.ID, Engine: core.EngineMihomo, Action: core.ActionStop}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("isolated user changed a shared host service: %v", err)
	}
	if _, err := db.SetUserAgentAccess(alice, user.ID, core.AgentAccessRequest{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("isolated user removed own isolation: %v", err)
	}
	if _, err := db.ListUsers(alice); !errors.Is(err, ErrForbidden) {
		t.Fatalf("isolated user read fleet users: %v", err)
	}
	if _, err := db.SyncSelectedPortTrafficEndpoints(alice, nil, []core.TrafficSyncSelection{{AgentID: allowed.ID, Port: 21001}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("shared user bypassed accounting via sync: %v", err)
	}
}

func TestSharedTrafficCumulativeQuotaAndPortOwnership(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, alice := sharedTestUser(t, db, ctx, "quota-alice")
	other, bob := sharedTestUser(t, db, ctx, "quota-bob")
	agent := sharedTestAgent(t, db, ctx)
	access := sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, 21001, 21002)
	sharedTestAllocation(t, db, ctx, other.ID, agent.ID, 5000, 21003)
	config := sharedTestConfig(t, db, alice, agent.ID, 21001, 21002)
	bobConfig := sharedTestConfig(t, db, bob, agent.ID, 21003)
	request := core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy}
	task, err := db.CreateTask(alice, request)
	if err != nil || task.SharedTrafficID != access.Shares[0].ID {
		t.Fatalf("shared deployment: %+v %v", task, err)
	}
	bobRequest := request
	bobRequest.ConfigID = bobConfig.ID
	if _, err := db.CreateTask(bob, bobRequest); !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent foreign deployment replaced a pending core: %v", err)
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.SharedTrafficID != task.SharedTrafficID {
		t.Fatalf("shared claim: %+v %v", claimed, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateTask(bob, bobRequest); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign deployment replaced a running core: %v", err)
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 2 {
		t.Fatalf("shared monitor count: %+v %v", policies, err)
	}
	if rows, err := db.ListPortTrafficPolicies(bob); err != nil || len(rows) != 0 {
		t.Fatalf("shared traffic leaked another user's port: %+v %v", rows, err)
	}
	clock := time.Now().UTC().Add(time.Second)
	report := func(policy core.PortTrafficPolicy, total uint64, epoch string) core.PortTrafficUsage {
		start, end, _ := core.TrafficPeriodAt(policy.CycleAnchor, policy.Cycle, clock)
		return core.PortTrafficUsage{PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration,
			ReceivedBytes: total, UsedBytes: total, LifetimeReceivedBytes: total, CounterEpoch: epoch,
			CollectedAt: clock, PeriodStart: start, PeriodEnd: end, EnforcementAvailable: true,
			ShareID: policy.SharedQuota.ID, ShareUsedBytes: total}
	}
	usages := []core.PortTrafficUsage{report(policies[0], 100, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), report(policies[1], 200, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")}
	for attempt := 0; attempt < 2; attempt++ {
		if err := db.UpdatePortTrafficUsage(ctx, agent.ID, usages, clock.Add(time.Duration(attempt)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	access, _ = db.UserAgentAccess(ctx, user.ID)
	if access.Shares[0].UsedBytes != 300 {
		t.Fatalf("duplicate samples multiplied usage: %+v", access)
	}
	for _, mutate := range []func() error{
		func() error { _, err := db.ResetPortTrafficPolicy(ctx, policies[0].ID); return err },
		func() error { _, err := db.DeletePortTrafficMonitoring(ctx, policies[0].ID); return err },
		func() error { _, err := db.DeletePortTrafficPolicy(ctx, policies[0].ID); return err },
	} {
		if err := mutate(); !errors.Is(err, ErrConflict) {
			t.Fatalf("shared cumulative accounting was mutable: %v", err)
		}
	}
	// Calendar/epoch counters may reset, but shared usage is monotonic and
	// separate. The new native epoch must not reset or re-add the package.
	clock = clock.Add(3 * time.Second)
	usages[0] = report(policies[0], 900, "cccccccccccccccccccccccccccccccc")
	if err := db.UpdatePortTrafficUsage(ctx, agent.ID, usages[:1], clock); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateTask(alice, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("exhausted allocation deployed: %v", err)
	}
	access = sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 2000, 21001, 21002)
	if access.Shares[0].UsedBytes != 1100 || access.Shares[0].LimitBytes != 2000 {
		t.Fatalf("top-up reset cumulative usage: %+v", access)
	}
	if _, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true}); err != nil {
		t.Fatal(err)
	}
	policies, _ = db.AgentPortTrafficPolicies(ctx, agent.ID)
	if !policies[0].SharedQuota.Revoked {
		t.Fatal("revocation did not reach runtime policies")
	}
	if _, err := db.GetAgent(alice, agent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked cached principal still sees Agent: %v", err)
	}
	access = sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 2000, 21001, 21002)
	if access.Shares[0].UsedBytes != 1100 {
		t.Fatal("regrant reset cumulative usage")
	}
	if _, err := db.SetUserAgentAccess(ctx, other.ID, core.AgentAccessRequest{Isolated: true, Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21001}}}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("another user stole a reserved port: %v", err)
	}
}

func TestSharedTrafficConcurrentReportsPreserveCombinedUsage(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, alice := sharedTestUser(t, db, ctx, "parallel-alice")
	agent := sharedTestAgent(t, db, ctx)
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 10000, 22001, 22002)
	config := sharedTestConfig(t, db, alice, agent.ID, 22001, 22002)
	if _, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy}); err != nil {
		t.Fatal(err)
	}
	policies, _ := db.AgentPortTrafficPolicies(ctx, agent.ID)
	now := time.Now().UTC()
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 1; i <= 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			policy := policies[i%2]
			start, end, _ := core.TrafficPeriodAt(policy.CycleAnchor, policy.Cycle, now)
			errs <- db.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{{
				PolicyID: policy.ID, ResetGeneration: 1, ShareID: policy.SharedQuota.ID, ShareUsedBytes: uint64(i * 10),
				PeriodStart: start, PeriodEnd: end,
			}}, now.Add(time.Duration(i)*time.Second))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	access, err := db.UserAgentAccess(ctx, user.ID)
	if err != nil || access.Shares[0].UsedBytes != 230 {
		t.Fatalf("lost or duplicated concurrent traffic: %+v %v", access, err)
	}
}

func TestAgentIsolationCreationRevisionAndHostBoundaries(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, err := db.CreateUser(ctx, core.UserRequest{Username: "safe-new-user", Role: core.RoleUser, AgentIsolation: true}, "test-only-hash")
	if err != nil {
		t.Fatal(err)
	}
	alice := WithConfigScope(ctx, user.ID, false)
	agent := sharedTestAgent(t, db, ctx)
	if items, err := db.ListAgents(alice); err != nil || len(items) != 0 {
		t.Fatalf("new isolated user had an unrestricted access window: %+v %v", items, err)
	}
	access, _ := db.UserAgentAccess(ctx, user.ID)
	request := core.AgentAccessRequest{Revision: access.Revision, Isolated: true,
		Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{22001}}}}
	errs := make(chan error, 2)
	for range 2 {
		go func() { _, err := db.SetUserAgentAccess(ctx, user.ID, request); errs <- err }()
	}
	var succeeded, conflicted int
	for range 2 {
		err := <-errs
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrConflict) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("stale allocation overwrote concurrent save: %d success, %d conflicts", succeeded, conflicted)
	}
	access, _ = db.UserAgentAccess(ctx, user.ID)
	acceptSharedTestInvitations(t, db, ctx, user.ID, access)
	for _, check := range []func() error{
		func() error { return db.SetAgentName(alice, agent.ID, "unauthorized") },
		func() error { return db.SetAgentRegionCode(alice, agent.ID, "US") },
		func() error { return db.SetAgentKomariUUID(alice, agent.ID, "unauthorized") },
		func() error { return db.SetAgentClientAddress(alice, agent.ID, "foreign.example") },
		func() error { return db.SetAgentEngineCapability(alice, agent.ID, core.EngineMihomo, false) },
		func() error { _, err := db.EnrollmentCommandForAgent(alice, agent.ID); return err },
		func() error { _, err := db.ListCoreLogs(alice, CoreLogQuery{AgentID: agent.ID}); return err },
		func() error { _, err := db.AgentConfigsForMonitoring(alice, agent.ID); return err },
	} {
		if err := check(); !errors.Is(err, ErrForbidden) {
			t.Fatalf("shared user reached a host-wide operation: %v", err)
		}
	}
	if _, err := db.CreateEnrollmentToken(alice, core.EnrollmentTokenRequest{Name: "my-own-node"}); err != nil {
		t.Fatalf("personal enrollment denied: %v", err)
	}
	if _, err := db.SaveSubStoreSyncSettings(alice, "https://substore.example/private"); err != nil {
		t.Fatalf("personal Sub-Store denied: %v", err)
	}
	if _, err := db.SavePanelSettings(alice, core.DefaultPanelSettings()); err != nil {
		t.Fatalf("personal settings denied: %v", err)
	}
	if entries, err := db.ListCoreLogs(alice, CoreLogQuery{}); err != nil || len(entries) != 0 {
		t.Fatalf("personal logs exposed a shared host: %+v %v", entries, err)
	}
	sharedTestConfig(t, db, alice, agent.ID, 22222)
	if configs, err := db.AgentConfigsForMonitoring(ctx, agent.ID); err != nil || len(configs) != 0 {
		t.Fatalf("isolated draft created arbitrary host monitors: %+v %v", configs, err)
	}
}

func TestSharedUserCanSaveAndValidateWhileAnotherConfigRuns(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "shared-draft-owner")
	user, recipient := sharedTestUser(t, db, ctx, "shared-draft-recipient")
	agent := sharedTestAgent(t, db, owner)
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 0, 21002)
	owned := sharedTestConfig(t, db, owner, agent.ID, 21001)
	deployed, err := db.CreateTask(owner, core.TaskRequest{
		AgentID: agent.ID, Engine: owned.Engine, ConfigID: owned.ID, Action: core.ActionDeploy,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil || lease.ID != deployed.ID {
		t.Fatalf("claim owner deployment: %+v %v", lease, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, deployed.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	draft := core.Config{AgentID: agent.ID, Engine: core.EngineMihomo, Name: "recipient inbound",
		Content: "listeners: [{name: recipient, type: http, port: 21002}]\n"}
	saved, validation, err := db.SaveAgentConfigAndTask(recipient, draft, 0,
		ConfigMutationOptions{Action: core.ActionValidate})
	if err != nil || saved.Version != 1 || validation.Action != core.ActionValidate {
		t.Fatalf("shared user could not save and validate a private inbound: %+v %+v %v", saved, validation, err)
	}
	var runningOwner, runningConfig string
	if err := db.pool.QueryRow(ctx, `SELECT owner_id,config_id FROM agent_engine_ownership WHERE agent_id=$1 AND engine=$2`,
		agent.ID, core.EngineMihomo).Scan(&runningOwner, &runningConfig); err != nil {
		t.Fatal(err)
	}
	if runningOwner != scopeForConfig(owner).OwnerID || runningConfig != owned.ID {
		t.Fatalf("validation changed the running user's configuration: owner=%s config=%s", runningOwner, runningConfig)
	}
	if _, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: saved.Engine,
		ConfigID: saved.ID, Action: core.ActionDeploy}); !errors.Is(err, ErrConflict) {
		t.Fatalf("shared user replaced a different user's running configuration: %v", err)
	}
	current, err := db.AgentConfig(recipient, agent.ID, core.EngineMihomo)
	if err != nil || current.ID != saved.ID || current.Version != 1 {
		t.Fatalf("rejected deployment lost the user's saved draft: %+v %v", current, err)
	}
}

func TestSharedXrayDeploymentKeepsOwnerInstanceRunning(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "parallel-xray-owner")
	user, recipient := sharedTestUser(t, db, ctx, "parallel-xray-recipient")
	agent := allEnginesSharedTestAgent(t, db, owner)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET features=features||'["shared-core-instances-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	access, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true,
		Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineXray}, Ports: []int{21002}}}})
	if err != nil {
		t.Fatal(err)
	}
	acceptSharedTestInvitations(t, db, ctx, user.ID, access)
	ownerConfig := core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "owner",
		Content: `{"inbounds":[{"tag":"owner","protocol":"http","port":21001}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`}
	ownerConfig, err = db.SaveAgentConfig(owner, ownerConfig, 0)
	if err != nil {
		t.Fatal(err)
	}
	ownerTask, err := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineXray, ConfigID: ownerConfig.ID, Action: core.ActionDeploy})
	if err != nil || ownerTask.SharedInstance {
		t.Fatalf("owner deployment: %+v %v", ownerTask, err)
	}
	ownerLease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || ownerLease == nil || ownerLease.ID != ownerTask.ID {
		t.Fatalf("claim owner deployment: %+v %v", ownerLease, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, ownerTask.ID, core.TaskResultRequest{LeaseID: ownerLease.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	privateConfig := core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "recipient",
		Content: `{"inbounds":[{"tag":"recipient","protocol":"http","port":21002}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`}
	privateConfig, privateTask, err := db.SaveAgentConfigAndTask(recipient, privateConfig, 0,
		ConfigMutationOptions{Action: core.ActionDeploy})
	if err != nil || !privateTask.SharedInstance || privateTask.SharedTrafficID != access.Shares[0].ID {
		t.Fatalf("shared deployment was not isolated: %+v %+v %v", privateConfig, privateTask, err)
	}
	privateLease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || privateLease == nil || privateLease.ID != privateTask.ID || !privateLease.SharedInstance {
		t.Fatalf("claim shared deployment: %+v %v", privateLease, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, privateTask.ID, core.TaskResultRequest{LeaseID: privateLease.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	var ownerConfigID, privateConfigID string
	if err := db.pool.QueryRow(ctx, `SELECT config_id FROM agent_engine_ownership WHERE agent_id=$1 AND engine='xray'`, agent.ID).Scan(&ownerConfigID); err != nil {
		t.Fatal(err)
	}
	if err := db.pool.QueryRow(ctx, `SELECT config_id FROM agent_shared_instance_ownership
		WHERE agent_id=$1 AND engine='xray' AND share_id=$2`, agent.ID, access.Shares[0].ID).Scan(&privateConfigID); err != nil {
		t.Fatal(err)
	}
	if ownerConfigID != ownerConfig.ID || privateConfigID != privateConfig.ID {
		t.Fatalf("independent deployments lost owner identity: base=%s shared=%s", ownerConfigID, privateConfigID)
	}
}

func TestEverySharedEngineUsesAnIndependentInstance(t *testing.T) {
	for _, candidate := range sharedEngineConfigs() {
		t.Run(string(candidate.Engine), func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			_, owner := sharedTestUser(t, db, ctx, "parallel-owner")
			user, recipient := sharedTestUser(t, db, ctx, "parallel-recipient")
			agent := allEnginesSharedTestAgent(t, db, owner)
			if _, err := db.pool.Exec(ctx, `UPDATE agents SET features=features||'["shared-core-instances-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
				t.Fatal(err)
			}
			port := serverconfig.DiscoverTrafficPorts(candidate.Engine, candidate.Content)[0].Port
			access, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true,
				Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{candidate.Engine}, Ports: []int{port}}}})
			if err != nil {
				t.Fatal(err)
			}
			acceptSharedTestInvitations(t, db, ctx, user.ID, access)
			ownerConfig := candidate
			ownerConfig.AgentID, ownerConfig.Name = agent.ID, "owner"
			ownerConfig.Content = strings.ReplaceAll(ownerConfig.Content, fmt.Sprint(port), fmt.Sprint(port+1000))
			ownerConfig, err = db.SaveAgentConfig(owner, ownerConfig, 0)
			if err != nil {
				t.Fatal(err)
			}
			ownerTask, err := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: candidate.Engine,
				ConfigID: ownerConfig.ID, Action: core.ActionDeploy})
			if err != nil {
				t.Fatal(err)
			}
			ownerLease, err := db.ClaimTask(ctx, agent.ID)
			if err != nil || ownerLease == nil || ownerLease.ID != ownerTask.ID {
				t.Fatalf("claim owner deployment: %+v %v", ownerLease, err)
			}
			if err := db.CompleteTask(ctx, agent.ID, ownerTask.ID, core.TaskResultRequest{LeaseID: ownerLease.LeaseID, Success: true}); err != nil {
				t.Fatal(err)
			}
			candidate.AgentID, candidate.Name = agent.ID, "recipient"
			saved, task, err := db.SaveAgentConfigAndTask(recipient, candidate, 0,
				ConfigMutationOptions{Action: core.ActionDeploy})
			if err != nil || !task.SharedInstance || task.SharedTrafficID != access.Shares[0].ID {
				t.Fatalf("shared deployment: %+v %+v %v", saved, task, err)
			}
			lease, err := db.ClaimTask(ctx, agent.ID)
			if err != nil || lease == nil || lease.ID != task.ID || !lease.SharedInstance {
				t.Fatalf("claim shared deployment: %+v %v", lease, err)
			}
			if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
				t.Fatal(err)
			}
			var baseID, sharedID string
			if err := db.pool.QueryRow(ctx, `SELECT config_id FROM agent_engine_ownership WHERE agent_id=$1 AND engine=$2`,
				agent.ID, candidate.Engine).Scan(&baseID); err != nil {
				t.Fatal(err)
			}
			if err := db.pool.QueryRow(ctx, `SELECT config_id FROM agent_shared_instance_ownership WHERE agent_id=$1 AND engine=$2 AND share_id=$3`,
				agent.ID, candidate.Engine, access.Shares[0].ID).Scan(&sharedID); err != nil {
				t.Fatal(err)
			}
			if baseID != ownerConfig.ID || sharedID != saved.ID {
				t.Fatalf("foreign instance replaced owner: base=%s shared=%s", baseID, sharedID)
			}
			ownerName := "owner connection"
			if err := db.SetConfigClientPreferences(owner, agent.ID, ClientProfileScope{
				ConfigID: ownerConfig.ID, Version: ownerConfig.Version,
				Label: core.ClientProfileNameLabel(candidate.Engine, "", port+1000),
			}, nil, &ownerName, nil); err != nil {
				t.Fatalf("recipient deployment blocked owner profile changes: %v", err)
			}
			if visible, err := db.GetAgent(recipient, agent.ID); err != nil || visible.Runtime[candidate.Engine].ServiceStatus != "active" {
				t.Fatalf("recipient cannot see own active instance: %+v %v", visible.Runtime[candidate.Engine], err)
			}
			for _, scoped := range []struct {
				ctx  context.Context
				want string
			}{{owner, ownerConfig.ID}, {recipient, saved.ID}} {
				deployments, err := db.LatestDeployments(scoped.ctx)
				if err != nil || len(deployments) != 1 || deployments[0].ConfigID != scoped.want {
					t.Fatalf("owner-scoped deployment: %+v %v", deployments, err)
				}
				deployed, err := db.DeployedConfigs(scoped.ctx)
				if err != nil || len(deployed) != 1 || deployed[0].Config.ID != scoped.want {
					t.Fatalf("owner-scoped deployed config: %+v %v", deployed, err)
				}
			}
			if _, err := db.PruneTasks(ctx, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if deployments, err := db.LatestDeployments(owner); err != nil || len(deployments) != 1 || deployments[0].ConfigID != ownerConfig.ID {
				t.Fatalf("recipient deployment pruned owner's history: %+v %v", deployments, err)
			}
			updated := saved
			updated.Description = "later draft"
			if _, err := db.SaveAgentConfig(recipient, updated, saved.Version); err != nil {
				t.Fatal(err)
			}
			if _, err := db.PruneConfigRevisions(ctx, 1); err != nil {
				t.Fatal(err)
			}
			if revision, err := db.ConfigRevision(recipient, saved.ID, saved.Version); err != nil || revision.Version != saved.Version {
				t.Fatalf("active shared instance revision was pruned: %+v %v", revision, err)
			}
			if _, err := db.pool.Exec(ctx, `UPDATE agent_engine_ownership SET running=false
				WHERE agent_id=$1 AND engine=$2`, agent.ID, candidate.Engine); err != nil {
				t.Fatal(err)
			}
			if deployments, err := db.LatestDeployments(owner); err != nil || len(deployments) != 1 || deployments[0].ConfigID != ownerConfig.ID {
				t.Fatalf("stopped user-owned node lost its deployment history: %+v %v", deployments, err)
			}
			if _, err := db.pool.Exec(ctx, `UPDATE agent_engine_ownership SET running=true
				WHERE agent_id=$1 AND engine=$2`, agent.ID, candidate.Engine); err != nil {
				t.Fatal(err)
			}
			policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			var policy *core.PortTrafficPolicy
			for index := range policies {
				if policies[index].SharedQuota != nil && policies[index].SharedQuota.ID == access.Shares[0].ID {
					policy = &policies[index]
					break
				}
			}
			if policy == nil {
				t.Fatal("shared instance has no metered port")
			}
			heartbeat := core.HeartbeatRequest{
				Features: []string{core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines,
					core.AgentFeatureIndependentEgress, core.AgentFeatureSharedCoreInstances},
				Runtime: map[core.Engine]core.RuntimeState{candidate.Engine: {Installed: true}},
				SharedInstances: []core.SharedInstanceStatus{{Engine: candidate.Engine,
					ShareID: access.Shares[0].ID, Status: "inactive"}},
			}
			if err := db.Heartbeat(ctx, agent.ID, heartbeat); err != nil {
				t.Fatal(err)
			}
			if visible, err := db.GetAgent(recipient, agent.ID); err != nil || visible.Runtime[candidate.Engine].ServiceStatus != "inactive" {
				t.Fatalf("recipient cannot see own stopped instance: %+v %v", visible.Runtime[candidate.Engine], err)
			}
			access, err = db.UserAgentAccess(ctx, user.ID)
			if err != nil {
				t.Fatal(err)
			}
			release := core.AgentAccessRequest{Revision: access.Revision, Isolated: true,
				Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{candidate.Engine}}}}
			if _, err := db.SetUserAgentAccess(ctx, user.ID, release); !errors.Is(err, ErrConflict) {
				t.Fatalf("port released without a settled traffic report: %v", err)
			}
			now := time.Now().UTC()
			start, end, err := core.TrafficPeriodAt(policy.CycleAnchor, policy.Cycle, now)
			if err != nil {
				t.Fatal(err)
			}
			heartbeat.TrafficUsage = []core.PortTrafficUsage{{PolicyID: policy.ID,
				ResetGeneration: policy.ResetGeneration, CollectedAt: now,
				CounterEpoch: strings.Repeat("a", 32), PeriodStart: start, PeriodEnd: end,
				EnforcementAvailable: true, ShareID: access.Shares[0].ID}}
			if err := db.Heartbeat(ctx, agent.ID, heartbeat); err != nil {
				t.Fatal(err)
			}
			if _, err := db.SetUserAgentAccess(ctx, user.ID, release); err != nil {
				t.Fatalf("settled stopped instance did not release its port: %v", err)
			}
		})
	}
}

func TestSharedTrafficUncertainDeploymentAndSettledRelease(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, alice := sharedTestUser(t, db, ctx, "uncertain-alice")
	other, bob := sharedTestUser(t, db, ctx, "uncertain-bob")
	agent := sharedTestAgent(t, db, ctx)
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, 21001)
	sharedTestAllocation(t, db, ctx, other.ID, agent.ID, 1000, 21002)
	config := sharedTestConfig(t, db, alice, agent.ID, 21001)
	foreign := sharedTestConfig(t, db, bob, agent.ID, 21002)
	task, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Error: "uncertain partial execution"}); err != nil {
		t.Fatal(err)
	}
	request := core.TaskRequest{AgentID: agent.ID, Engine: foreign.Engine, ConfigID: foreign.ID, Action: core.ActionDeploy}
	if _, err := db.CreateTask(bob, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("lost/failed result allowed another tenant to replace the core: %v", err)
	}
	for _, action := range []core.Action{core.ActionStart, core.ActionRestart} {
		if _, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: action}); !errors.Is(err, ErrConflict) {
			t.Fatalf("uncertain deployment allowed a raw %s: %v", action, err)
		}
	}
	for _, settled := range []bool{false, true} {
		stop, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionStop})
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || claimed == nil {
			t.Fatalf("claim stop: %+v %v", claimed, err)
		}
		if err := db.CompleteTask(ctx, agent.ID, stop.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, TrafficSettled: settled}); err != nil {
			t.Fatal(err)
		}
		_, err = db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true,
			Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineMihomo}, LimitBytes: 1000}}})
		if !settled && !errors.Is(err, ErrConflict) || settled && err != nil {
			t.Fatalf("port release settled=%v: %v", settled, err)
		}
		if _, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionStart}); !errors.Is(err, ErrConflict) {
			t.Fatalf("stopping an uncertain config allowed starting it without rebinding billing: %v", err)
		}
	}
	if _, err := db.CreateTask(bob, request); err != nil {
		t.Fatalf("settled stop did not allow explicit reassignment: %v", err)
	}
}

func TestSharedTrafficCapabilityEnableRequiresDeployment(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, alice := sharedTestUser(t, db, ctx, "shared-capability")
	agent := sharedTestAgent(t, db, ctx)
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: []string{core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureIndependentEgress},
		Runtime: map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, 21001)
	config := sharedTestConfig(t, db, alice, agent.ID, 21001)
	deploy, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, deploy.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	change, err := db.ChangeAgentEngineCapability(ctx, agent.ID, config.Engine, false)
	if err != nil || change.TaskID == "" {
		t.Fatalf("disable shared core: %+v %v", change, err)
	}
	claimed, err = db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil {
		t.Fatalf("claim stop: %+v %v", claimed, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, change.TaskID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, TrafficSettled: true}); err != nil {
		t.Fatal(err)
	}
	change, err = db.ChangeAgentEngineCapability(ctx, agent.ID, config.Engine, true)
	if err != nil || !change.Enabled || change.TaskID != "" {
		t.Fatalf("enable should restore deploy access without starting retained shared config: %+v %v", change, err)
	}
	if _, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy}); err != nil {
		t.Fatalf("re-enabled shared core could not be deployed: %v", err)
	}
}

func TestAgentIsolationAdoptsOnlyKnownAllocatedDeployments(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, alice := sharedTestUser(t, db, ctx, "adopt-alice")
	agent := sharedTestAgent(t, db, alice)
	config := sharedTestConfig(t, db, alice, agent.ID, 21001)
	request := core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy}
	task, err := db.CreateTask(ctx, request) // Administrator acts for the owner.
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a pre-ownership account/Agent, never reopen unrestricted access
	// through the production allocation API.
	if _, err := db.pool.Exec(ctx, `WITH changed AS (UPDATE agents SET owner_id='' WHERE id=$1)
		UPDATE panel_users SET agent_isolation=false WHERE id=$2`, agent.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, 21001)
	if prior, err := db.GetTask(ctx, task.ID); err != nil || prior.Status != core.TaskCanceled {
		t.Fatalf("admin-created unshared task survived isolation: %+v %v", prior, err)
	}
	if _, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("regular account isolation was disabled: %v", err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET owner_id=$2 WHERE id=$1`, agent.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	task, err = db.CreateTask(ctx, request)
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
	if _, err := db.pool.Exec(ctx, `WITH changed AS (UPDATE agents SET owner_id='' WHERE id=$1)
		UPDATE panel_users SET agent_isolation=false WHERE id=$2`, agent.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("running unallocated core escaped sharing enforcement: %v", err)
	}
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, 21001)
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].SharedQuota == nil {
		t.Fatalf("adopted running deployment has no accounting: %+v %v", policies, err)
	}
}

func TestSharedTrafficRevocationDisableAndDowngrade(t *testing.T) {
	for _, change := range []string{"revoke", "disable", "downgrade"} {
		t.Run(change, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			user, alice := sharedTestUser(t, db, ctx, "reconnect-alice")
			agent := sharedTestAgent(t, db, ctx)
			sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, 21001)
			config := sharedTestConfig(t, db, alice, agent.ID, 21001)
			task, err := db.CreateTask(alice, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ClaimTask(ctx, agent.ID); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "revoke":
				_, err = db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true})
			case "disable":
				_, err = db.SetUserDisabled(ctx, user.ID, true)
			case "downgrade":
				_, err = db.pool.Exec(ctx, `UPDATE agents SET features='[]' WHERE id=$1`, agent.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if running, err := db.RunningTask(ctx, agent.ID); err != nil || running != nil {
				t.Fatalf("unauthorized task resumed: %+v %v", running, err)
			}
			if result, err := db.GetTask(ctx, task.ID); err != nil || result.Status != core.TaskFailed {
				t.Fatalf("unauthorized task status: %+v %v", result, err)
			}
		})
	}
}
