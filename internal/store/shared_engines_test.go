package store

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func sharedEngineConfigs() []core.Config {
	return []core.Config{
		{Engine: core.EngineMihomo, Name: "mihomo", Content: "listeners: [{name: a, type: http, port: 21001}]\n"},
		{Engine: core.EngineXray, Name: "xray", Content: `{"inbounds":[{"tag":"a","protocol":"http","port":21002}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`},
		{Engine: core.EngineSingBox, Name: "sing-box", Content: `{"inbounds":[{"tag":"a","type":"http","listen_port":21003}],"outbounds":[{"type":"direct","tag":"direct"}]}`},
		{Engine: core.EngineShadowsocksRust, Name: "ss-rust", Content: `{"server":"0.0.0.0","server_port":21004,"method":"aes-256-gcm","password":"test-only"}`},
	}
}

func allEnginesSharedTestAgent(t *testing.T, db *Store, ctx context.Context) core.Agent {
	t.Helper()
	agent := sharedTestAgent(t, db, ctx)
	runtime := map[core.Engine]core.RuntimeState{}
	for _, engine := range core.AllEngines() {
		runtime[engine] = core.RuntimeState{Installed: true, Version: "test", ServiceStatus: "inactive", CoreLogError: "owner-private-log"}
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET capabilities=$2,supported_capabilities=$2,runtime=$3 WHERE id=$1`,
		agent.ID, core.AllEngines(), runtime); err != nil {
		t.Fatal(err)
	}
	agent.Capabilities, agent.SupportedCapabilities = core.AllEngines(), core.AllEngines()
	return agent
}

func TestSharedEngineAllocationScopesResourcesAndInvalidatesOldLeases(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "engine-owner")
	user, recipient := sharedTestUser(t, db, ctx, "engine-recipient")
	agent := allEnginesSharedTestAgent(t, db, owner)
	save := func(engines []core.Engine) core.AgentSharing {
		t.Helper()
		current, err := db.AgentSharing(owner, agent.ID)
		if err != nil {
			t.Fatal(err)
		}
		saved, err := db.SetAgentSharing(owner, agent.ID, core.AgentSharingRequest{Revision: current.Revision,
			Shares: []core.AgentSharingRecipient{{Username: user.Username, Engines: engines, Ports: []int{21001, 21002, 21003, 21004}, LimitBytes: 1000}}})
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	sharing := save(core.AllEngines())
	accept := func() {
		t.Helper()
		share := sharing.Shares[0]
		if _, err := db.RespondAgentShare(recipient, share.ID, core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "accept"}); err != nil {
			t.Fatal(err)
		}
	}
	accept()
	configs := sharedEngineConfigs()
	for index, config := range configs {
		config.AgentID = agent.ID
		saved, err := db.SaveAgentConfig(recipient, config, 0)
		if err != nil {
			t.Fatal(err)
		}
		configs[index] = saved
		task, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: saved.Engine, ConfigID: saved.ID, Action: core.ActionDeploy})
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
	running, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil {
		t.Fatalf("claim status: %+v %v", lease, err)
	}
	queued, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineSingBox, Action: core.ActionStatus})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET used_bytes=123 WHERE id=$1`, sharing.Shares[0].ID); err != nil {
		t.Fatal(err)
	}
	sharing = save([]core.Engine{core.EngineXray})
	if sharing.Shares[0].Status != core.AgentShareAccepted || sharing.Shares[0].UsedBytes != 123 {
		t.Fatalf("narrowing lost consent or ledger: %+v", sharing)
	}
	visible, err := db.GetAgent(recipient, agent.ID)
	if err != nil || !slices.Equal(visible.Capabilities, []core.Engine{core.EngineXray}) ||
		!slices.Equal(visible.SupportedCapabilities, visible.Capabilities) || len(visible.Runtime) != 1 ||
		visible.Runtime[core.EngineXray].CoreLogError != "" {
		t.Fatalf("Agent disclosed unassigned engines or host logs: %+v %v", visible, err)
	}
	if listed, err := db.ListAgents(recipient); err != nil || len(listed) != 1 || !slices.Equal(listed[0].Capabilities, visible.Capabilities) {
		t.Fatalf("list bypassed engine scope: %+v %v", listed, err)
	}
	denied := configs[0]
	for name, check := range map[string]func() error{
		"workspace": func() error { _, err := db.AgentConfig(recipient, agent.ID, denied.Engine); return err },
		"save":      func() error { _, err := db.SaveAgentConfig(recipient, denied, denied.Version); return err },
		"revisions": func() error { _, err := db.ListConfigRevisions(recipient, denied.ID, 20); return err },
		"revision":  func() error { _, err := db.ConfigRevision(recipient, denied.ID, 1); return err },
		"restore":   func() error { _, err := db.RestoreConfigRevision(recipient, denied.ID, 1, 1); return err },
		"task": func() error {
			_, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: denied.Engine, Action: core.ActionStatus})
			return err
		},
		"retry": func() error { _, err := db.RetryTask(recipient, running.ID); return err },
	} {
		if err := check(); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s bypassed engine allocation: %v", name, err)
		}
	}
	if listed, err := db.AgentConfigs(recipient, agent.ID); err != nil || len(listed) != 1 || listed[0].Engine != core.EngineXray {
		t.Fatalf("configuration list bypass: %+v %v", listed, err)
	}
	if listed, err := db.LatestDeployments(recipient); err != nil || len(listed) != 1 || listed[0].Engine != core.EngineXray {
		t.Fatalf("deployment list bypass: %+v %v", listed, err)
	}
	if listed, err := db.DeployedConfigs(recipient); err != nil || len(listed) != 1 || listed[0].Config.Engine != core.EngineXray {
		t.Fatalf("client/Sub-Store source bypass: %+v %v", listed, err)
	}
	if listed, err := db.ListPortTrafficPolicies(recipient); err != nil || len(listed) != 1 || listed[0].Engine != core.EngineXray {
		t.Fatalf("traffic scope bypass: %+v %v", listed, err)
	}
	if overview, err := db.Overview(recipient); err != nil || overview.NodeConfigs != 1 {
		t.Fatalf("overview engine leak: %+v %v", overview, err)
	}
	for id, wanted := range map[string]core.TaskStatus{running.ID: core.TaskFailed, queued.ID: core.TaskCanceled} {
		got, err := db.GetTask(ctx, id)
		if err != nil || got.Status != wanted {
			t.Fatalf("revoked engine task: %+v %v", got, err)
		}
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 4 {
		t.Fatalf("revocation discarded accounting: %+v %v", policies, err)
	}
	for _, policy := range policies {
		if policy.SharedQuota.AllowsEngine(policy.Engine) != (policy.Engine == core.EngineXray) {
			t.Fatalf("runtime policy has the wrong engine authority: %+v", policy)
		}
	}
	oldRevision := sharing.Shares[0].InvitationRevision
	sharing = save(core.AllEngines())
	if sharing.Shares[0].Status != core.AgentSharePending {
		t.Fatal("expanding engines silently retained acceptance")
	}
	if _, err := db.GetAgent(recipient, agent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expanded invitation granted access before acceptance: %v", err)
	}
	if _, err := db.RespondAgentShare(recipient, sharing.Shares[0].ID, core.AgentShareResponseRequest{Revision: oldRevision, Decision: "accept"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("old invitation accepted new engines: %v", err)
	}
	accept()
	if resumed, err := db.RunningTask(ctx, agent.ID); err != nil || resumed != nil {
		t.Fatalf("regrant restored old lease: %+v %v", resumed, err)
	}
	if claimed, err := db.ClaimTask(ctx, agent.ID); err != nil || claimed != nil {
		t.Fatalf("regrant restored old queued task: %+v %v", claimed, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, running.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("old lease completion was accepted: %v", err)
	}
}

func TestSharedEngineAllocationRejectsImplicitDuplicateAndUnsupportedEngines(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, _ := sharedTestUser(t, db, ctx, "bad-engines")
	agent := sharedTestAgent(t, db, ctx)
	for _, engines := range [][]core.Engine{nil, {}, {core.EngineMihomo, core.EngineMihomo}, {core.EngineXray}, {"invalid"}} {
		_, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true,
			Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: engines}}})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid engine allocation %v accepted: %v", engines, err)
		}
	}
}

func TestSharedEnginePoliciesRequireEverySafetyFeature(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, recipient := sharedTestUser(t, db, ctx, "feature-agreement")
	agent := sharedTestAgent(t, db, ctx)
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, 21001)
	config := sharedTestConfig(t, db, recipient, agent.ID, 21001)
	if _, err := db.CreateTask(recipient, core.TaskRequest{
		AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy,
	}); err != nil {
		t.Fatal(err)
	}
	required := []string{core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureIndependentEgress}
	for _, missing := range required {
		t.Run(missing, func(t *testing.T) {
			features := slices.DeleteFunc(slices.Clone(required), func(feature string) bool { return feature == missing })
			if _, err := db.pool.Exec(ctx, `UPDATE agents SET features=$2 WHERE id=$1`, agent.ID, features); err != nil {
				t.Fatal(err)
			}
			if supportsSharedEngines(features) {
				t.Errorf("shared execution allowed without %s", missing)
			}
			policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
			if err != nil || len(policies) != 1 || policies[0].SharedQuota == nil || !policies[0].SharedQuota.Revoked {
				t.Errorf("shared port remained enabled without %s: %+v %v", missing, policies, err)
			}
			if _, err := db.SetUserAgentAccess(ctx, user.ID, core.AgentAccessRequest{Isolated: true,
				Shares: []core.AgentShareRequest{{AgentID: agent.ID, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21001}, LimitBytes: 1000}},
			}); !errors.Is(err, ErrConflict) {
				t.Errorf("allocation accepted without %s: %v", missing, err)
			}
		})
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET features=$2 WHERE id=$1`, agent.ID, required); err != nil {
		t.Fatal(err)
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || !policies[0].SharedQuota.AllowsEngine(core.EngineMihomo) {
		t.Fatalf("complete feature agreement did not restore the accepted policy: %+v %v", policies, err)
	}
}

func TestMigrateV56RequiresExplicitEnginesWithoutResettingUsage(t *testing.T) {
	db, ctx, databaseURL := isolatedConfigScopeStore(t)
	user, recipient := sharedTestUser(t, db, ctx, "v56-engines")
	agent := sharedTestAgent(t, db, ctx)
	access := sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, 21001)
	config := sharedTestConfig(t, db, recipient, agent.ID, 21001)
	task, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionDeploy, ConfigID: config.ID})
	if err != nil {
		t.Fatal(err)
	}
	if lease, err := db.ClaimTask(ctx, agent.ID); err != nil || lease == nil {
		t.Fatalf("claim: %+v %v", lease, err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET used_bytes=321;
		ALTER TABLE agent_shares DROP COLUMN engines;
		DELETE FROM qcontrolhub_schema_migrations WHERE version>=57;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(56) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	migrated, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	got, err := migrated.UserAgentAccess(recipient, user.ID)
	if err != nil || len(got.Shares) != 1 {
		t.Fatalf("read migrated share: %+v %v", got, err)
	}
	share := got.Shares[0]
	if share.ID != access.Shares[0].ID || share.Status != core.AgentSharePending || len(share.Engines) != 0 ||
		share.UsedBytes != 321 || share.LimitBytes != 1000 || !slices.Equal(share.Ports, []int{21001}) {
		t.Fatalf("migration widened access or lost ledger: %+v", share)
	}
	if _, err := migrated.RespondAgentShare(recipient, share.ID, core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "accept"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("unallocated legacy engines accepted: %v", err)
	}
	if prior, err := migrated.GetTask(ctx, task.ID); err != nil || prior.Status != core.TaskFailed {
		t.Fatalf("legacy lease survived migration: %+v %v", prior, err)
	}
	policies, err := migrated.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || !policies[0].SharedQuota.Revoked || policies[0].SharedQuota.AllowsEngine(config.Engine) {
		t.Fatalf("legacy runtime scope survived migration: %+v %v", policies, err)
	}
}
