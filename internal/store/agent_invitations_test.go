package store

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAgentInvitationConsentLifecycle(t *testing.T) {
	for _, sender := range []string{"owner", "administrator"} {
		t.Run(sender, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			owner, alice := sharedTestUser(t, db, ctx, "invite-owner")
			user, bob := sharedTestUser(t, db, ctx, "invite-recipient")
			_, stranger := sharedTestUser(t, db, ctx, "invite-stranger")
			agent := sharedTestAgent(t, db, alice)
			save := func(enabled, reinvite bool, limit uint64) core.AgentShare {
				t.Helper()
				if sender == "owner" {
					sharing, err := db.AgentSharing(alice, agent.ID)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := db.SetAgentSharing(alice, agent.ID, core.AgentSharingRequest{Revision: sharing.Revision,
						Shares: []core.AgentSharingRecipient{{Username: user.Username, Engines: []core.Engine{core.EngineMihomo}, Enabled: &enabled, Reinvite: reinvite, LimitBytes: limit, Ports: []int{21001}}}}); err != nil {
						t.Fatal(err)
					}
				} else {
					access, err := db.UserAgentAccess(ctx, user.ID)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Revision: access.Revision, Isolated: true,
						Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineMihomo}, Enabled: &enabled, Reinvite: reinvite, LimitBytes: limit, Ports: []int{21001}}}}); err != nil {
						t.Fatal(err)
					}
				}
				access, err := db.UserAgentAccess(bob, user.ID)
				if err != nil || len(access.Shares) != 1 {
					t.Fatalf("invitation not visible to recipient: %+v %v", access, err)
				}
				return access.Shares[0]
			}
			respond := func(share core.AgentShare, decision string) core.AgentShare {
				t.Helper()
				access, err := db.RespondAgentShare(bob, share.ID, core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: decision})
				if err != nil {
					t.Fatal(err)
				}
				return access.Shares[0]
			}
			denied := func() {
				t.Helper()
				if _, err := db.GetAgent(bob, agent.ID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("unaccepted share exposes Agent: %v", err)
				}
				if agents, err := db.ListAgents(bob); err != nil || len(agents) != 0 {
					t.Fatalf("unaccepted share exposes list: %+v %v", agents, err)
				}
				if _, err := db.SaveAgentConfig(bob, core.Config{AgentID: agent.ID, Engine: core.EngineMihomo, Content: "listeners: []"}, 0); !errors.Is(err, ErrNotFound) {
					t.Fatalf("unaccepted share allows workspace write: %v", err)
				}
				if _, err := db.CreateTask(bob, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus}); !errors.Is(err, ErrNotFound) {
					t.Fatalf("unaccepted share executes a task: %v", err)
				}
			}
			pending := save(true, false, 1000)
			if pending.Status != core.AgentSharePending || pending.InvitationRevision != 1 || pending.OwnerUsername != owner.Username {
				t.Fatalf("new grant did not require consent or identify its owner: %+v", pending)
			}
			denied()
			if policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID); err != nil || len(policies) != 0 {
				t.Fatalf("invitation activated runtime policies: %+v %v", policies, err)
			}
			for _, actor := range []context.Context{alice, ctx, stranger} {
				if _, err := db.RespondAgentShare(actor, pending.ID, core.AgentShareResponseRequest{Revision: 1, Decision: "accept"}); !errors.Is(err, ErrNotFound) {
					t.Fatalf("another principal answered an invitation: %v", err)
				}
			}
			for _, input := range []core.AgentShareResponseRequest{{Decision: "accept"}, {Revision: 1, Decision: "accepted"}} {
				if _, err := db.RespondAgentShare(bob, pending.ID, input); !errors.Is(err, ErrInvalid) {
					t.Fatalf("invalid invitation response: %v", err)
				}
			}
			oldOwner, _ := db.AgentSharing(alice, agent.ID)
			oldAdmin, _ := db.UserAgentAccess(ctx, user.ID)
			rejected := respond(pending, "reject")
			if rejected.Status != core.AgentShareRejected || rejected.InvitationRevision <= pending.InvitationRevision {
				t.Fatalf("rejection was not durable: %+v", rejected)
			}
			denied()
			if _, err := db.SetAgentSharing(alice, agent.ID, core.AgentSharingRequest{Revision: oldOwner.Revision}); !errors.Is(err, ErrConflict) {
				t.Fatalf("recipient response did not invalidate owner's stale form: %v", err)
			}
			if _, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true, Revision: oldAdmin.Revision}); !errors.Is(err, ErrConflict) {
				t.Fatalf("recipient response did not invalidate admin's stale form: %v", err)
			}
			// Saving or adjusting a rejected grant must never silently resend.
			unchanged := save(true, false, 1000)
			if unchanged.Status != core.AgentShareRejected || unchanged.InvitationRevision != rejected.InvitationRevision {
				t.Fatalf("no-op save reset rejection: %+v", unchanged)
			}
			edited := save(true, false, 2000)
			if edited.Status != core.AgentShareRejected {
				t.Fatalf("quota edit bypassed rejection: %+v", edited)
			}
			pending = save(true, true, 2000)
			if pending.Status != core.AgentSharePending || pending.ID != rejected.ID {
				t.Fatalf("reinvitation lost ledger identity: %+v", pending)
			}
			if _, err := db.RespondAgentShare(bob, pending.ID, core.AgentShareResponseRequest{Revision: rejected.InvitationRevision, Decision: "accept"}); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale invitation accepted changed terms: %v", err)
			}
			accepted := respond(pending, "accept")
			if accepted.Status != core.AgentShareAccepted {
				t.Fatalf("acceptance not recorded: %+v", accepted)
			}
			if _, err := db.GetAgent(bob, agent.ID); err != nil {
				t.Fatalf("accepted invitation did not allow access: %v", err)
			}
			if _, err := db.RespondAgentShare(bob, accepted.ID, core.AgentShareResponseRequest{Revision: pending.InvitationRevision, Decision: "reject"}); !errors.Is(err, ErrConflict) {
				t.Fatalf("replayed response reversed acceptance: %v", err)
			}
			if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET used_bytes=123 WHERE id=$1`, accepted.ID); err != nil {
				t.Fatal(err)
			}
			edited = save(true, false, 3000)
			if edited.Status != core.AgentShareAccepted || edited.UsedBytes != 123 {
				t.Fatalf("active allowance edit reset consent/accounting: %+v", edited)
			}
			save(false, false, 3000)
			denied()
			pending = save(true, false, 3000)
			if pending.Status != core.AgentSharePending || pending.UsedBytes != 123 || pending.ID != accepted.ID {
				t.Fatalf("restored share bypassed fresh consent or reset usage: %+v", pending)
			}
			denied()
			accepted = respond(pending, "accept")
			rejected = respond(accepted, "reject") // Recipients may also leave.
			if rejected.Status != core.AgentShareRejected || rejected.UsedBytes != 123 {
				t.Fatalf("leaving active sharing lost its ledger: %+v", rejected)
			}
			denied()
		})
	}
}

func TestAgentInvitationResponsesAndEditsSerialize(t *testing.T) {
	for _, change := range []string{"opposite-response", "owner-edit", "admin-edit"} {
		t.Run(change, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			_, alice := sharedTestUser(t, db, ctx, "race-owner")
			user, bob := sharedTestUser(t, db, ctx, "race-recipient")
			agent := sharedTestAgent(t, db, alice)
			sharing, err := db.SetAgentSharing(alice, agent.ID, core.AgentSharingRequest{Revision: 1,
				Shares: []core.AgentSharingRecipient{{Username: user.Username, Engines: []core.Engine{core.EngineMihomo}, LimitBytes: 1000, Ports: []int{21001}}}})
			if err != nil {
				t.Fatal(err)
			}
			share := sharing.Shares[0]
			access, _ := db.UserAgentAccess(ctx, user.ID)
			timeout, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			errs := make(chan error, 2)
			go func() {
				_, err := db.RespondAgentShare(WithConfigScope(timeout, user.ID, false), share.ID,
					core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "accept"})
				errs <- err
			}()
			go func() {
				var err error
				switch change {
				case "owner-edit":
					_, err = db.SetAgentSharing(WithConfigScope(timeout, scopeForConfig(alice).OwnerID, false), agent.ID,
						core.AgentSharingRequest{Revision: sharing.Revision, Shares: []core.AgentSharingRecipient{{Username: user.Username, Engines: []core.Engine{core.EngineMihomo}, LimitBytes: 2000, Ports: []int{21001}}}})
				case "admin-edit":
					_, err = db.SetUserAgentAccess(timeout, user.ID, core.AgentAccessRequest{Isolated: true, Revision: access.Revision,
						Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineMihomo}, LimitBytes: 2000, Ports: []int{21001}}}})
				default:
					_, err = db.RespondAgentShare(bob, share.ID, core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "reject"})
				}
				errs <- err
			}()
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
				t.Fatalf("concurrent invitation mutation: %d successes, %d conflicts", succeeded, conflicted)
			}
		})
	}
}

func TestAgentInvitationRejectInvalidatesRuntimeAndOldLeases(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, bob := sharedTestUser(t, db, ctx, "runtime-recipient")
	agent := sharedTestAgent(t, db, ctx)
	access := sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, 21001)
	config := sharedTestConfig(t, db, bob, agent.ID, 21001)
	task, err := db.CreateTask(bob, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	if running, err := db.ClaimTask(ctx, agent.ID); err != nil || running == nil {
		t.Fatalf("initial claim: %+v %v", running, err)
	}
	queued, err := db.CreateTask(bob, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionStatus})
	if err != nil {
		t.Fatal(err)
	}
	share := access.Shares[0]
	access, err = db.RespondAgentShare(bob, share.ID, core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "reject"})
	if err != nil {
		t.Fatal(err)
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || !policies[0].SharedQuota.Revoked {
		t.Fatalf("rejection left runtime authorization active: %+v %v", policies, err)
	}
	if ownPolicies, err := db.ListPortTrafficPolicies(bob); err != nil || len(ownPolicies) != 0 {
		t.Fatalf("rejected share exposes traffic page: %+v %v", ownPolicies, err)
	}
	if _, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true, Revision: access.Revision,
		Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineMihomo}, Reinvite: true, LimitBytes: 1000, Ports: []int{21001}}}}); err != nil {
		t.Fatal(err)
	}
	access, _ = db.UserAgentAccess(ctx, user.ID)
	acceptSharedTestInvitations(t, db, ctx, user.ID, access)
	for _, prior := range []struct {
		id     string
		status core.TaskStatus
	}{{task.ID, core.TaskFailed}, {queued.ID, core.TaskCanceled}} {
		result, err := db.GetTask(ctx, prior.id)
		if err != nil || result.Status != prior.status {
			t.Fatalf("reacceptance resurrected old task: %+v %v", result, err)
		}
	}
	if task, err := db.ClaimTask(ctx, agent.ID); err != nil || task != nil {
		t.Fatalf("old pending work was dispatched: %+v %v", task, err)
	}
	if task, err := db.RunningTask(ctx, agent.ID); err != nil || task != nil {
		t.Fatalf("old running lease was resumed: %+v %v", task, err)
	}
}

func TestAgentInvitationConsentIsCheckedAtClaimAndResume(t *testing.T) {
	for _, status := range []core.AgentShareStatus{core.AgentSharePending, core.AgentShareRejected} {
		for _, phase := range []string{"claim", "resume"} {
			t.Run(string(status)+"/"+phase, func(t *testing.T) {
				db, ctx, _ := isolatedConfigScopeStore(t)
				user, bob := sharedTestUser(t, db, ctx, "dispatch-recipient")
				agent := sharedTestAgent(t, db, ctx)
				access := sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, 21001)
				config := sharedTestConfig(t, db, bob, agent.ID, 21001)
				task, err := db.CreateTask(bob, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
				if err != nil {
					t.Fatal(err)
				}
				if phase == "resume" {
					if _, err := db.ClaimTask(ctx, agent.ID); err != nil {
						t.Fatal(err)
					}
				}
				// Deliberately bypass proactive cancellation to exercise the
				// final dispatch/resume guard against legacy or stale work.
				if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET status=$2 WHERE id=$1`, access.Shares[0].ID, status); err != nil {
					t.Fatal(err)
				}
				var dispatched *core.Task
				want := core.TaskCanceled
				if phase == "claim" {
					dispatched, err = db.ClaimTask(ctx, agent.ID)
				} else {
					dispatched, err = db.RunningTask(ctx, agent.ID)
					want = core.TaskFailed
				}
				if err != nil || dispatched != nil {
					t.Fatalf("unaccepted work dispatched: %+v %v", dispatched, err)
				}
				if result, err := db.GetTask(ctx, task.ID); err != nil || result.Status != want {
					t.Fatalf("unaccepted task not invalidated: %+v %v", result, err)
				}
			})
		}
	}
}

func TestAgentInvitationEditsAndResponsesPreserveTrafficLockOrder(t *testing.T) {
	db, parent, _ := isolatedConfigScopeStore(t)
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	_, alice := sharedTestUser(t, db, ctx, "traffic-inviter")
	bob, bobScope := sharedTestUser(t, db, ctx, "traffic-bob")
	carol, _ := sharedTestUser(t, db, ctx, "traffic-carol")
	agent := sharedTestAgent(t, db, alice)
	sharing, err := db.SetAgentSharing(alice, agent.ID, core.AgentSharingRequest{Revision: 1, Shares: []core.AgentSharingRecipient{
		{Username: bob.Username, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21001}, LimitBytes: 1000},
		{Username: carol.Username, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21002}, LimitBytes: 1000},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Retained legacy policies can continue reporting while consent is pending.
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, share := range sharing.Shares {
		if err := bindSharedTrafficPortsTx(ctx, tx, share.ID, agent.ID, []core.PortTrafficEndpoint{
			{Engine: core.EngineMihomo, Port: share.Ports[0], Name: share.Username, Protocol: core.TrafficProtocolBoth},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 2 {
		t.Fatalf("retained policies: %+v %v", policies, err)
	}
	now := time.Now().UTC()
	report := func(total uint64) error {
		var usages []core.PortTrafficUsage
		for _, policy := range policies {
			start, end, _ := core.TrafficPeriodAt(policy.CycleAnchor, policy.Cycle, now)
			usages = append(usages, core.PortTrafficUsage{PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration,
				ShareID: policy.SharedQuota.ID, ShareUsedBytes: total, PeriodStart: start, PeriodEnd: end})
		}
		return db.UpdatePortTrafficUsage(ctx, agent.ID, usages, now.Add(time.Duration(total)*time.Millisecond))
	}
	if err := report(1); err != nil {
		t.Fatal(err)
	}
	for _, share := range sharing.Shares {
		if _, err := db.RespondAgentShare(WithConfigScope(ctx, share.UserID, false), share.ID,
			core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "accept"}); err != nil {
			t.Fatalf("usage update expired recipient consent: %v", err)
		}
	}
	errs := make(chan error, 2)
	go func() {
		for i := uint64(1); i <= 30; i++ {
			if err := report(i * 10); err != nil {
				errs <- err
				return
			}
		}
		errs <- nil
	}()
	go func() {
		for i := uint64(1); i <= 30; i++ {
			latest, err := db.AgentSharing(alice, agent.ID)
			if err != nil {
				errs <- err
				return
			}
			// Deliberately request the opposite order to traffic's grant locks.
			sort.Slice(latest.Shares, func(i, j int) bool { return latest.Shares[i].ID > latest.Shares[j].ID })
			request := core.AgentSharingRequest{Revision: latest.Revision}
			for _, share := range latest.Shares {
				request.Shares = append(request.Shares, core.AgentSharingRecipient{Username: share.Username, Engines: share.Engines, Ports: share.Ports, LimitBytes: 1000 + i})
			}
			if _, err := db.SetAgentSharing(alice, agent.ID, request); err != nil {
				errs <- err
				return
			}
		}
		errs <- nil
	}()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	access, err := db.UserAgentAccess(bobScope, bob.ID)
	if err != nil || access.Shares[0].UsedBytes != 300 || access.Shares[0].Status != core.AgentShareAccepted {
		t.Fatalf("concurrent grant edits lost accounting or consent: %+v %v", access, err)
	}
}
