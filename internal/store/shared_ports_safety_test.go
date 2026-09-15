package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
