package store

import (
	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"strings"
	"testing"
)

func TestCertificateSyncUsesAppliedSharedSnapshotAndRespectsLifecycle(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "sync-node-owner")
	user, recipient := sharedTestUser(t, db, ctx, "sync-recipient")
	agent := allEnginesSharedTestAgent(t, db, owner)
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 10000, 21001)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET features=features || '["panel-certificates-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	pair, _ := certificates.SelfSigned([]string{"sync.example.test"}, "ecdsa256", 90)
	cert, err := db.SaveCertificatePair(owner, "", "sync.example.test", "import", pair)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SetCertificateGrant(owner, cert.ID, user.Username, false); err != nil {
		t.Fatal(err)
	}
	protocol, _ := serverconfig.FindProtocol(core.EngineMihomo, serverconfig.ProtocolTrojan)
	plan, _ := serverconfig.NewPlan(protocol)
	plan.Port, plan.PanelCertificateID, plan.TLSServerName = 21001, cert.ID, "sync.example.test"
	if err = db.ResolveCertificatePlan(recipient, cert.ID, core.EngineMihomo, &plan); err != nil {
		t.Fatal(err)
	}
	content, _ := serverconfig.Generate(core.EngineMihomo, plan)
	metadata, _ := serverconfig.MarshalClientMetadata(plan)
	config, err := db.SaveAgentConfigWithClientMetadata(recipient, core.Config{AgentID: agent.ID, Engine: core.EngineMihomo, Name: "TLS", Content: content}, 0, ConfigClientMetadataMutation{Tag: plan.Tag, Content: metadata})
	if err != nil {
		t.Fatal(err)
	}
	finish := func(task *core.Task) {
		t.Helper()
		if err := completeSharedTestTask(db, ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: task.LeaseID, Success: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionDeploy, ConfigID: config.ID}); err != nil {
		t.Fatal(err)
	}
	task, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || task == nil {
		t.Fatalf("initial claim: %v", err)
	}
	finish(task)
	// Save a different draft. Renewal must neither deploy it nor overwrite it.
	draft := config
	draft.Content = strings.ReplaceAll(content, "sync.example.test", "draft.example.test")
	draft, err = db.SaveAgentConfig(recipient, draft, config.Version)
	if err != nil {
		t.Fatal(err)
	}
	replace := func(days int) {
		t.Helper()
		next, _ := certificates.SelfSigned([]string{"sync.example.test"}, "ecdsa256", days)
		if _, err := db.SaveCertificatePair(owner, cert.ID, "", "import", next); err != nil {
			t.Fatal(err)
		}
	}
	replace(91)
	if err = db.syncCertificateBindings(ctx); err != nil {
		t.Fatal(err)
	}
	// A second worker must not create duplicate work or corrupt pending status.
	if err = db.syncCertificateBindings(ctx); err != nil {
		t.Fatal(err)
	}
	task, err = db.ClaimTask(ctx, agent.ID)
	if err != nil || task == nil || !task.CertificateSync || task.ConfigVersion != config.Version || task.ConfigContent != content || len(task.Certificates) != 1 || task.Certificates[0].Version != 2 {
		t.Fatalf("sync did not retain deployed snapshot: task=%v err=%v", task != nil, err)
	}
	// An update while the older immutable task is running needs another pass.
	replace(92)
	resumed, err := db.RunningTask(ctx, agent.ID)
	if err != nil || resumed == nil || resumed.ID != task.ID || resumed.Certificates[0].Version != 2 {
		t.Fatal("reconnect changed pinned certificate or rejected active sync", err)
	}
	finish(task)
	saved, err := db.AgentConfig(recipient, agent.ID, config.Engine)
	if err != nil || saved.Version != draft.Version || saved.Content != draft.Content {
		t.Fatal("sync overwrote edited draft", err)
	}
	if err = db.syncCertificateBindings(ctx); err != nil {
		t.Fatal(err)
	}
	task, err = db.ClaimTask(ctx, agent.ID)
	if err != nil || task == nil || task.Certificates[0].Version != 3 {
		t.Fatal("continuous update lost", err)
	}
	finish(task)
	// Stopping an instance must suppress future automatic deployments.
	if _, err = db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionStop}); err != nil {
		t.Fatal(err)
	}
	task, err = db.ClaimTask(ctx, agent.ID)
	if err != nil || task == nil {
		t.Fatal(err)
	}
	finish(task)
	replace(93)
	if err = db.syncCertificateBindings(ctx); err != nil {
		t.Fatal(err)
	}
	if task, err = db.ClaimTask(ctx, agent.ID); err != nil || task != nil {
		t.Fatal("renewal restarted stopped instance", err)
	}
	// Starting explicitly retains the applied pair. Then revoking authorization
	// prevents even a queued automatic sync from obtaining the replacement key.
	if _, err = db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionStart}); err != nil {
		t.Fatal(err)
	}
	task, err = db.ClaimTask(ctx, agent.ID)
	if err != nil || task == nil {
		t.Fatal(err)
	}
	finish(task)
	if _, err = db.pool.Exec(ctx, `UPDATE certificate_bindings SET retry_at=now() WHERE config_id=$1`, config.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.syncCertificateBindings(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.SetCertificateGrant(owner, cert.ID, user.Username, true); err != nil {
		t.Fatal(err)
	}
	if task, err = db.ClaimTask(ctx, agent.ID); err != nil || task != nil {
		t.Fatal("revoked grant received new key", err)
	}
}

func TestCertificateSyncBaseTargetsRemainIsolatedAcrossNodes(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "base-sync-owner")
	pair, _ := certificates.SelfSigned([]string{"same.example.test"}, "ecdsa256", 90)
	cert, err := db.SaveCertificatePair(owner, "", "same name", "import", pair)
	if err != nil {
		t.Fatal(err)
	}
	protocol, _ := serverconfig.FindProtocol(core.EngineMihomo, serverconfig.ProtocolTrojan)
	plan, _ := serverconfig.NewPlan(protocol)
	plan.Port, plan.PanelCertificateID, plan.TLSServerName = 21001, cert.ID, "same.example.test"
	if err = db.ResolveCertificatePlan(owner, cert.ID, core.EngineMihomo, &plan); err != nil {
		t.Fatal(err)
	}
	content, _ := serverconfig.Generate(core.EngineMihomo, plan)
	metadata, _ := serverconfig.MarshalClientMetadata(plan)
	nodes := []core.Agent{}
	for i := 0; i < 2; i++ {
		agent := allEnginesSharedTestAgent(t, db, owner)
		nodes = append(nodes, agent)
		if _, err = db.pool.Exec(ctx, `UPDATE agents SET features=features || '["panel-certificates-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
			t.Fatal(err)
		}
		config, err := db.SaveAgentConfigWithClientMetadata(owner, core.Config{AgentID: agent.ID, Engine: core.EngineMihomo, Name: "same config", Content: content}, 0, ConfigClientMetadataMutation{Tag: plan.Tag, Content: metadata})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionDeploy, ConfigID: config.ID}); err != nil {
			t.Fatal(err)
		}
		task, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || task == nil {
			t.Fatal(err)
		}
		if err = db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: task.LeaseID, Success: true}); err != nil {
			t.Fatal(err)
		}
	}
	replacement, _ := certificates.SelfSigned([]string{"same.example.test"}, "ecdsa256", 91)
	if _, err = db.SaveCertificatePair(owner, cert.ID, "", "import", replacement); err != nil {
		t.Fatal(err)
	}
	if err = db.syncCertificateBindings(ctx); err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		task, err := db.ClaimTask(ctx, node.ID)
		if err != nil || task == nil || !task.CertificateSync || task.Certificates[0].Version != 2 {
			t.Fatal("multi-node sync missing", err)
		}
		if err = db.CompleteTask(ctx, node.ID, task.ID, core.TaskResultRequest{LeaseID: task.LeaseID, Success: true}); err != nil {
			t.Fatal(err)
		}
	}
	bindings, err := db.CertificateBindings(owner, cert.ID)
	if err != nil || len(bindings) != 2 {
		t.Fatal("same-name node bindings merged", err)
	}
	listed, err := db.ListCertificates(owner)
	if err != nil || len(listed) != 1 || listed[0].BindingStatuses["synced"] != 2 {
		t.Fatal("API sync aggregate incorrect", err)
	}
	// Disabling one target's synchronization cannot affect the other node.
	if _, err = db.pool.Exec(ctx, `UPDATE certificate_bindings SET enabled=false WHERE config_id=$1`, bindings[0].ConfigID); err != nil {
		t.Fatal(err)
	}
	replacement, _ = certificates.SelfSigned([]string{"same.example.test"}, "ecdsa256", 92)
	if _, err = db.SaveCertificatePair(owner, cert.ID, "", "import", replacement); err != nil {
		t.Fatal(err)
	}
	if err = db.syncCertificateBindings(ctx); err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		task, err := db.ClaimTask(ctx, binding.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if binding.ConfigID == bindings[0].ConfigID {
			if task != nil {
				t.Fatal("disabled binding received deployment")
			}
		} else if task == nil || task.Certificates[0].Version != 3 {
			t.Fatal("unrelated node sync was suppressed")
		}
	}
}
