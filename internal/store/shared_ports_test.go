package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestNormalizeSharedPorts(t *testing.T) {
	for _, ports := range [][]int{nil, {}, {0}, {65535, 21001, 1}} {
		original := slices.Clone(ports)
		got, err := normalizeSharedPorts(ports)
		want := slices.Clone(ports)
		slices.Sort(want)
		if err != nil || !slices.Equal(got, want) || !slices.Equal(ports, original) {
			t.Fatalf("normalize %v: %v %v; want %v without input mutation", original, got, err, want)
		}
	}
	oversize := make([]int, 257)
	for i := range oversize {
		oversize[i] = i + 1
	}
	for _, ports := range [][]int{{0, 1}, {1, 0}, {0, 0}, {-1}, {65536}, {10085}, {10086}, {1, 1}, oversize} {
		if _, err := normalizeSharedPorts(ports); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid ports %v accepted: %v", ports, err)
		}
	}
}

func TestSharedPortModesLifecycle(t *testing.T) {
	for _, sender := range []string{"owner", "administrator"} {
		t.Run(sender, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			_, owner := sharedTestUser(t, db, ctx, "ports-owner")
			user, recipient := sharedTestUser(t, db, ctx, "ports-recipient")
			agent := sharedTestAgent(t, db, owner)
			save := func(ports []int, limit uint64, enabled bool) (core.AgentShare, error) {
				t.Helper()
				if sender == "owner" {
					current, err := db.AgentSharing(owner, agent.ID)
					if err != nil {
						t.Fatal(err)
					}
					saved, err := db.SetAgentSharing(owner, agent.ID, core.AgentSharingRequest{Revision: current.Revision,
						Shares: []core.AgentSharingRecipient{{Username: user.Username, Engines: []core.Engine{core.EngineMihomo}, Ports: ports, LimitBytes: limit, Enabled: &enabled}}})
					if err != nil {
						return core.AgentShare{}, err
					}
					return saved.Shares[0], nil
				}
				current, err := db.UserAgentAccess(ctx, user.ID)
				if err != nil {
					t.Fatal(err)
				}
				saved, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Revision: current.Revision, Isolated: true,
					Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineMihomo}, Ports: ports, LimitBytes: limit, Enabled: &enabled}}})
				if err != nil {
					return core.AgentShare{}, err
				}
				return saved.Shares[0], nil
			}
			assertPorts := func(want, reserved []int, policies int) {
				t.Helper()
				access, err := db.UserAgentAccess(recipient, user.ID)
				if err != nil || len(access.Shares) != 1 || !slices.Equal(access.Shares[0].Ports, want) {
					t.Fatalf("recipient port mode: %+v %v; want %v", access, err, want)
				}
				sharing, err := db.AgentSharing(owner, agent.ID)
				if err != nil || len(sharing.Shares) != 1 || !slices.Equal(sharing.Shares[0].Ports, want) {
					t.Fatalf("owner port mode: %+v %v; want %v", sharing, err, want)
				}
				var actual []int
				if err := db.pool.QueryRow(ctx, `SELECT ARRAY(SELECT port FROM agent_share_ports WHERE agent_id=$1 ORDER BY port)`, agent.ID).Scan(&actual); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(actual, reserved) {
					t.Fatalf("actual reservations = %v; want %v", actual, reserved)
				}
				got, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
				if err != nil || len(got) != policies {
					t.Fatalf("runtime policies: %+v %v; want %d", got, err, policies)
				}
				for _, policy := range got {
					if policy.Port == 0 || policy.SharedQuota == nil {
						t.Fatalf("unmetered or sentinel runtime port: %+v", policy)
					}
				}
			}
			complete := func(task core.Task, settled bool) {
				t.Helper()
				lease, err := db.ClaimTask(ctx, agent.ID)
				if err != nil || lease == nil || lease.ID != task.ID {
					t.Fatalf("claim: %+v %v; want %s", lease, err, task.ID)
				}
				if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, TrafficSettled: settled}); err != nil {
					t.Fatal(err)
				}
			}
			share, err := save(nil, 1000, true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.RespondAgentShare(recipient, share.ID, core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "accept"}); err != nil {
				t.Fatal(err)
			}
			config := sharedTestConfig(t, db, recipient, agent.ID, 21001)
			request := core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID}
			for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
				request.Action = action
				if _, err := db.CreateTask(recipient, request); !errors.Is(err, ErrConflict) {
					t.Fatalf("empty port allocation allowed %s: %v", action, err)
				}
			}
			assertPorts(nil, nil, 0)
			share, err = save([]int{0}, 1000, true)
			if err != nil {
				t.Fatalf("zero must grant unrestricted port choice: %v", err)
			}
			if share.Status != core.AgentShareAccepted {
				t.Fatal("port edit reset accepted consent")
			}
			assertPorts([]int{0}, nil, 0)
			request.Action = core.ActionValidate
			validation, err := db.CreateTask(recipient, request)
			if err != nil {
				t.Fatal(err)
			}
			assertPorts([]int{0}, nil, 0)
			if _, err := save(nil, 1000, true); err != nil {
				t.Fatal(err)
			}
			if task, err := db.GetTask(ctx, validation.ID); err != nil || task.Status != core.TaskCanceled {
				t.Fatalf("narrowed scope retained stale validation: %+v %v", task, err)
			}
			assertPorts(nil, nil, 0)
			if _, err := save([]int{0}, 1000, true); err != nil {
				t.Fatal(err)
			}
			validation, err = db.CreateTask(recipient, request)
			if err != nil {
				t.Fatal(err)
			}
			validationLease, err := db.ClaimTask(ctx, agent.ID)
			if err != nil || validationLease == nil || validationLease.ID != validation.ID {
				t.Fatalf("claim validation: %+v %v", validationLease, err)
			}
			if _, err := save([]int{21001}, 1000, true); err != nil {
				t.Fatal(err)
			}
			if task, err := db.GetTask(ctx, validation.ID); err != nil || task.Status != core.TaskFailed {
				t.Fatalf("narrowing did not invalidate a running validation: %+v %v", task, err)
			}
			if err := db.CompleteTask(ctx, agent.ID, validation.ID, core.TaskResultRequest{LeaseID: validationLease.LeaseID, Success: true}); !errors.Is(err, ErrConflict) {
				t.Fatalf("old validation lease remained valid: %v", err)
			}
			if _, err := save([]int{0}, 1000, true); err != nil {
				t.Fatal(err)
			}
			assertPorts([]int{0}, []int{21001}, 0)
			request.Action = core.ActionDeploy
			deployment, err := db.CreateTask(recipient, request)
			if err != nil || deployment.SharedTrafficID != share.ID {
				t.Fatalf("unrestricted shared deployment: %+v %v", deployment, err)
			}
			assertPorts([]int{0}, []int{21001}, 1)
			for _, ports := range [][]int{nil, {0, 21001}, {0, 0}} {
				_, err := save(ports, 1000, true)
				want := ErrInvalid
				if len(ports) == 0 {
					want = ErrConflict // Pending deployments prevent release.
				}
				if !errors.Is(err, want) {
					t.Fatalf("unsafe port edit %v: %v; want %v", ports, err, want)
				}
				assertPorts([]int{0}, []int{21001}, 1)
			}
			complete(deployment, false)
			if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET used_bytes=123 WHERE id=$1`, share.ID); err != nil {
				t.Fatal(err)
			}
			before, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			share, err = save([]int{0}, 2000, true)
			if err != nil || share.UsedBytes != 123 {
				t.Fatalf("quota edit lost usage: %+v %v", share, err)
			}
			if unchanged, err := save([]int{0}, 2000, true); err != nil || unchanged.InvitationRevision != share.InvitationRevision {
				t.Fatalf("actual port bindings made a no-op mode save change consent: %+v %v", unchanged, err)
			}
			after, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
			if err != nil || len(after) != 1 || after[0].ID != before[0].ID || after[0].SharedQuota.LimitBytes != 2000 {
				t.Fatalf("quota edit replaced accounting: %+v %v", after, err)
			}
			assertPorts([]int{0}, []int{21001}, 1)
			if _, err := save(nil, 2000, true); !errors.Is(err, ErrConflict) {
				t.Fatalf("running port was released: %v", err)
			}
			queued, err := db.CreateTask(recipient, request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := save([]int{0}, 2000, false); err != nil {
				t.Fatal(err)
			}
			if task, err := db.GetTask(ctx, queued.ID); err != nil || task.Status != core.TaskCanceled {
				t.Fatalf("revoked deployment survived: %+v %v", task, err)
			}
			policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
			if err != nil || len(policies) != 1 || !policies[0].SharedQuota.Revoked {
				t.Fatalf("revocation lost runtime enforcement: %+v %v", policies, err)
			}
			assertPorts([]int{0}, []int{21001}, 1)
			share, err = save([]int{0}, 2000, true)
			if err != nil || share.Status != core.AgentSharePending || share.UsedBytes != 123 {
				t.Fatalf("regrant bypassed consent or reset usage: %+v %v", share, err)
			}
			if _, err := db.RespondAgentShare(recipient, share.ID, core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "accept"}); err != nil {
				t.Fatal(err)
			}
			stop, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionStop})
			if err != nil {
				t.Fatal(err)
			}
			complete(stop, true)
			share, err = save(nil, 2000, true)
			if err != nil || share.UsedBytes != 123 {
				t.Fatalf("settled release lost ledger: %+v %v", share, err)
			}
			assertPorts(nil, nil, 0)
			if _, err := db.CreateTask(recipient, request); !errors.Is(err, ErrConflict) {
				t.Fatalf("cleared port scope still deploys: %v", err)
			}
		})
	}
}

func TestUnrestrictedSharedPortsRejectForeignReservationsAndSnapshots(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "ports-host-owner")
	user, recipient := sharedTestUser(t, db, ctx, "ports-borrower")
	other, _ := sharedTestUser(t, db, ctx, "ports-other")
	agent := allEnginesSharedTestAgent(t, db, owner)
	sharedTestAllocation(t, db, ctx, other.ID, agent.ID, 1000, 21001)
	access := sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, 0)
	config := sharedTestConfig(t, db, recipient, agent.ID, 22000, 21001)
	checkDenied := func() {
		t.Helper()
		for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
			if _, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: action}); !errors.Is(err, ErrConflict) {
				t.Fatalf("foreign port allowed %s: %v", action, err)
			}
		}
		var count int
		if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM agent_share_ports WHERE share_id=$1`, access.Shares[0].ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed request partially reserved ports: %d %v", count, err)
		}
		if policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID); err != nil || len(policies) != 0 {
			t.Fatalf("failed request created accounting: %+v %v", policies, err)
		}
	}
	checkDenied()
	foreign := sharedEngineConfigs()[1]
	foreign.AgentID = agent.ID
	foreign, err := db.SaveAgentConfig(owner, foreign, 0)
	if err != nil {
		t.Fatal(err)
	}
	task, err := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: foreign.Engine, ConfigID: foreign.ID, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	foreign.Content = strings.ReplaceAll(foreign.Content, "21002", "22002")
	if _, err := db.SaveAgentConfig(owner, foreign, foreign.Version); err != nil {
		t.Fatal(err)
	}
	config.Content = "listeners: [{name: a, type: http, port: 21002}]\n"
	config, err = db.SaveAgentConfig(recipient, config, config.Version)
	if err != nil {
		t.Fatal(err)
	}
	checkDenied() // A queued task must protect its exact snapshot, not the draft.
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil || lease.ID != task.ID {
		t.Fatalf("claim foreign task: %+v %v", lease, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	checkDenied() // The deployed revision still has 21002, not 22002.
	config.Content = strings.ReplaceAll(config.Content, "21002", "22002")
	config, err = db.SaveAgentConfig(recipient, config, config.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionValidate}); err != nil {
		t.Fatalf("foreign saved draft reserved a port: %v", err)
	}
}

func TestUnrestrictedSharedPortsConcurrentDeployment(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := allEnginesSharedTestAgent(t, db, ctx)
	configs := sharedEngineConfigs()[:2]
	scopes := make([]context.Context, len(configs))
	for i := range configs {
		user, scope := sharedTestUser(t, db, ctx, fmt.Sprintf("ports-race-%d", i))
		scopes[i] = scope
		access, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true,
			Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{configs[i].Engine}, Ports: []int{0}, LimitBytes: 1000}}})
		if err != nil {
			t.Fatal(err)
		}
		acceptSharedTestInvitations(t, db, ctx, user.ID, access)
		configs[i].AgentID = agent.ID
		configs[i].Content = strings.ReplaceAll(configs[i].Content, "21002", "21001")
		configs[i], err = db.SaveAgentConfig(scope, configs[i], 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, len(configs))
	for i, config := range configs {
		go func() {
			<-start
			_, err := db.CreateTask(scopes[i], core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
			results <- err
		}()
	}
	close(start)
	var success, conflict int
	for range configs {
		select {
		case err := <-results:
			if err == nil {
				success++
			} else if errors.Is(err, ErrConflict) {
				conflict++
			} else {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("two unrestricted users raced for a port: %d success, %d conflict", success, conflict)
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].Port != 21001 {
		t.Fatalf("race created conflicting runtime policies: %+v %v", policies, err)
	}
}

func TestUnrestrictedSharedPortsRejectUncertainForeignDeployment(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "ports-uncertain-owner")
	user, recipient := sharedTestUser(t, db, ctx, "ports-uncertain-recipient")
	agent := allEnginesSharedTestAgent(t, db, owner)
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, 0)
	complete := func(task core.Task, success bool) {
		t.Helper()
		lease, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || lease == nil || lease.ID != task.ID {
			t.Fatalf("claim task: %+v %v", lease, err)
		}
		if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: success, TrafficSettled: success}); err != nil {
			t.Fatal(err)
		}
	}
	config := sharedTestConfig(t, db, recipient, agent.ID, 23001)
	initial, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	complete(initial, true)
	foreign := sharedEngineConfigs()[1]
	foreign.AgentID = agent.ID
	foreign, err = db.SaveAgentConfig(owner, foreign, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, success := range []bool{true, false} {
		task, err := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: foreign.Engine, ConfigID: foreign.ID, Action: core.ActionDeploy})
		if err != nil {
			t.Fatal(err)
		}
		complete(task, success)
		if success {
			foreign.Content = strings.ReplaceAll(foreign.Content, "21002", "22002")
			foreign, err = db.SaveAgentConfig(owner, foreign, foreign.Version)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	// The failed deployment may have activated 22002, while the last known
	// successful snapshot still says 21002. Do not guess that it is free.
	config.Content = strings.ReplaceAll(config.Content, "23001", "22002")
	config, err = db.SaveAgentConfig(recipient, config, config.Version)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
		if _, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: action}); !errors.Is(err, ErrConflict) {
			t.Fatalf("uncertain foreign listener allowed %s: %v", action, err)
		}
	}
	disabled := false
	if _, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true,
		Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{0}, LimitBytes: 1000, Enabled: &disabled}}}); err != nil {
		t.Fatalf("foreign uncertainty prevented revocation of an existing allocation: %v", err)
	}
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, 0)
	stop, err := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: foreign.Engine, Action: core.ActionStop})
	if err != nil {
		t.Fatal(err)
	}
	complete(stop, true)
	if _, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy}); err != nil {
		t.Fatalf("successful stop did not release uncertain host ports: %v", err)
	}
}

func TestUnrestrictedSharedPortsKeepReservationLimit(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	other, _ := sharedTestUser(t, db, ctx, "ports-capacity-other")
	user, recipient := sharedTestUser(t, db, ctx, "ports-capacity-recipient")
	agent := sharedTestAgent(t, db, ctx)
	ports := make([]int, 256)
	for i := range ports {
		ports[i] = 23000 + i
	}
	sharedTestAllocation(t, db, ctx, other.ID, agent.ID, 0, ports...)
	access := sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 0, 0)
	config := sharedTestConfig(t, db, recipient, agent.ID, 24000)
	if _, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy}); !errors.Is(err, ErrConflict) {
		t.Fatalf("unrestricted mode bypassed actual reservation limit: %v", err)
	}
	var count int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM agent_share_ports WHERE share_id=$1`, access.Shares[0].ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed deployment retained partial reservations: %d %v", count, err)
	}
	if policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID); err != nil || len(policies) != 0 {
		t.Fatalf("capacity failure created runtime accounting: %+v %v", policies, err)
	}
}

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

func TestMigrateV59PreservesAllocatedAndEmptyPorts(t *testing.T) {
	db, ctx, databaseURL := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "ports-migration-owner")
	agent := sharedTestAgent(t, db, owner)
	users := make([]core.User, 2)
	shares := make([]core.AgentShare, 2)
	for i, ports := range [][]int{{21001, 21002}, {}} {
		user, _ := sharedTestUser(t, db, ctx, fmt.Sprintf("ports-migration-%d", i))
		users[i] = user
		access := sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, ports...)
		shares[i] = access.Shares[0]
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET used_bytes=123;
		ALTER TABLE agent_shares DROP COLUMN ports_unrestricted;
		DELETE FROM qcontrolhub_schema_migrations WHERE version>=60;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(59) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	migrated, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	for i, user := range users {
		access, err := migrated.UserAgentAccess(ctx, user.ID)
		if err != nil || len(access.Shares) != 1 {
			t.Fatalf("migration lost sharing: %+v %v", access, err)
		}
		got, prior := access.Shares[0], shares[i]
		if got.ID != prior.ID || got.Status != prior.Status || got.InvitationRevision != prior.InvitationRevision ||
			got.LimitBytes != prior.LimitBytes || got.UsedBytes != 123 || !slices.Equal(got.Ports, prior.Ports) {
			t.Fatalf("migration changed authority or ledger: %+v; was %+v", got, prior)
		}
	}
	var unrestricted int
	if err := migrated.pool.QueryRow(ctx, `SELECT count(*) FROM agent_shares WHERE ports_unrestricted`).Scan(&unrestricted); err != nil || unrestricted != 0 {
		t.Fatalf("migration implicitly granted unrestricted ports: %d %v", unrestricted, err)
	}
	sharedTestAllocation(t, migrated, ctx, users[0].ID, agent.ID, 1000, 0)
	reopened, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if access, err := reopened.UserAgentAccess(ctx, users[0].ID); err != nil || len(access.Shares) != 1 ||
		!slices.Equal(access.Shares[0].Ports, []int{0}) || access.Shares[0].UsedBytes != 123 {
		t.Fatalf("restart lost unrestricted mode or reset the ledger: %+v %v", access, err)
	}
}
