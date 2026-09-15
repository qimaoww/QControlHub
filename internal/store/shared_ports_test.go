package store

import (
	"errors"
	"slices"
	"testing"

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
