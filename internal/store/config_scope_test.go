package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func isolatedConfigScopeStore(t *testing.T) (*Store, context.Context, string) {
	t.Helper()
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	schema, err := testdb.IsolatePostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := schema.Close(cleanup); err != nil {
			t.Error(err)
		}
	})
	db, err := OpenWithConfigKey(ctx, schema.URL, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db, ctx, schema.URL
}

func TestConfigOwnerIsolationWithPostgreSQL(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	alice := WithConfigScope(ctx, "usr_alice", false)
	bob := WithConfigScope(ctx, "usr_bob", false)
	admin := WithConfigScope(ctx, "", true)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	base := core.Config{Name: "private", Engine: core.EngineMihomo, Content: "mixed-port: 21001\n"}
	legacy, err := db.CreateConfig(admin, base)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := db.CreateConfig(alice, base)
	if err != nil || archive.OwnerID != "usr_alice" {
		t.Fatalf("create owned archive: %+v %v", archive, err)
	}
	other, err := db.CreateConfig(bob, base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateConfig(alice, base)
	if err != nil || second.ID == archive.ID {
		t.Fatalf("multiple configurations per user: %+v %v", second, err)
	}
	base.AgentID = agent.ID
	base.OwnerID = "usr_bob" // Untrusted input cannot assign ownership.
	workspace, err := db.SaveAgentConfigWithClientMetadata(alice, base, 0,
		ConfigClientMetadataMutation{Tag: "private", Content: `{"client_private":"alice-secret"}`})
	if err != nil || workspace.OwnerID != "usr_alice" {
		t.Fatalf("create private workspace: %+v %v", workspace, err)
	}
	base.Content = "mixed-port: 21002\n"
	bobWorkspace, err := db.SaveAgentConfig(bob, base, 0)
	if err != nil || workspace.ID == bobWorkspace.ID || bobWorkspace.Version != 1 {
		t.Fatalf("shared-host workspaces collided: %+v %v", bobWorkspace, err)
	}
	if configs, err := db.AgentConfigs(alice, agent.ID); err != nil || len(configs) != 1 || configs[0].ID != workspace.ID {
		t.Fatalf("private node list: %+v %v", configs, err)
	}
	if configs, err := db.AgentConfigsForMonitoring(alice, agent.ID); err != nil || len(configs) != 2 {
		t.Fatalf("shared-host monitoring lost another owner's ports: %+v %v", configs, err)
	}
	if _, err := db.AgentConfig(admin, agent.ID, base.Engine); !errors.Is(err, ErrNotFound) {
		t.Fatalf("administrator's editor opened another user's workspace: %v", err)
	}
	if configs, err := db.ListConfigs(alice); err != nil || len(configs) != 2 {
		t.Fatalf("private archives: %+v %v", configs, err)
	}
	if configs, err := db.ListConfigs(WithConfigScope(ctx, "", false)); err != nil || len(configs) != 0 {
		t.Fatalf("unassigned user inherited legacy archives: %+v %v", configs, err)
	}
	if configs, err := db.ListConfigs(admin); err != nil || len(configs) != 4 {
		t.Fatalf("administrator lost oversight: %+v %v", configs, err)
	}
	for _, id := range []string{archive.ID, legacy.ID, workspace.ID} {
		t.Run("deny-"+id, func(t *testing.T) {
			if _, err := db.ListConfigRevisions(bob, id, 20); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign revision list: %v", err)
			}
			if _, err := db.ConfigRevision(bob, id, 1); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign revision content: %v", err)
			}
			if _, err := db.RestoreConfigRevision(bob, id, 1, 1); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign revision restore: %v", err)
			}
			input := core.Config{Name: "overwrite", Engine: base.Engine, Content: base.Content, Version: 1}
			if _, err := db.UpdateConfig(bob, id, input); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign update: %v", err)
			}
			if err := db.DeleteConfig(bob, id); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign delete: %v", err)
			}
			if metadata, err := db.ConfigClientMetadata(bob, id, 1); err != nil || len(metadata) != 0 {
				t.Fatalf("foreign metadata: %+v %v", metadata, err)
			}
			if _, err := db.CreateTask(bob, core.TaskRequest{AgentID: agent.ID, Engine: base.Engine, Action: core.ActionDeploy, ConfigID: id}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign deployment: %v", err)
			}
		})
	}
	base.ID = workspace.ID
	if _, err := db.SaveAgentConfig(bob, base, bobWorkspace.Version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign workspace ID accepted: %v", err)
	}
	if metadata, err := db.ConfigClientMetadata(alice, workspace.ID, 1); err != nil || !strings.Contains(metadata["private"], "alice-secret") {
		t.Fatalf("owner lost private metadata: %+v %v", metadata, err)
	}
	if ids, err := db.ExistingConfigIDs(bob, []string{archive.ID, other.ID, workspace.ID}); err != nil || len(ids) != 1 || !ids[other.ID] {
		t.Fatalf("existing configuration IDs leaked: %+v %v", ids, err)
	}
	template, err := db.CreateConfigTemplate(alice, "Private template", string(base.Engine), base.Content)
	if err != nil {
		t.Fatal(err)
	}
	if templates, err := db.ListConfigTemplates(bob); err != nil || len(templates) != 0 {
		t.Fatalf("foreign template list: %+v %v", templates, err)
	}
	if _, _, _, err := db.RenderTemplateForAgent(bob, template.ID, agent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign template rendering: %v", err)
	}
	if err := db.DeleteConfigTemplate(bob, template.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign template deletion: %v", err)
	}
	if overview, err := db.Overview(alice); err != nil || overview.Configs != 2 || overview.NodeConfigs != 1 {
		t.Fatalf("private overview: %+v %v", overview, err)
	}
}

func TestTaskOwnerIsolationAndSharedHostExclusionWithPostgreSQL(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	alice, bob := WithConfigScope(ctx, "usr_alice", false), WithConfigScope(ctx, "usr_bob", false)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	request := core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus}
	task, err := db.CreateTask(alice, request)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := db.CreateTask(alice, request)
	if err != nil || reused.ID != task.ID || !reused.Reused {
		t.Fatalf("owner task reuse: %+v %v", reused, err)
	}
	other, err := db.CreateTask(bob, request)
	if err != nil || other.ID == task.ID {
		t.Fatalf("cross-owner task reuse: %+v %v", other, err)
	}
	if tasks, err := db.ListTasks(bob, agent.ID, 100); err != nil || len(tasks) != 1 || tasks[0].ID != other.ID {
		t.Fatalf("private task list: %+v %v", tasks, err)
	}
	if _, err := db.GetTaskState(bob, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign task polling: %v", err)
	}
	if err := db.CancelTask(bob, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign task cancel: %v", err)
	}
	if _, err := db.RetryTask(bob, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign task retry: %v", err)
	}
	for _, action := range []core.Action{core.ActionReadConfig, core.ActionReadManagedConfig, core.ActionImportExisting} {
		request.Action = action
		if _, err := db.CreateTask(alice, request); !errors.Is(err, ErrForbidden) {
			t.Fatalf("non-admin shared-host action %s: %v", action, err)
		}
	}
	if _, err := db.ReadTaskConfigSnapshot(alice, task.ID, agent.ID, core.EngineMihomo); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin configuration snapshot: %v", err)
	}
	for _, id := range []string{task.ID, other.ID} {
		if err := db.CancelTask(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{
		Features: []string{core.AgentFeatureSystemBBR},
		Runtime:  map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true}},
	}); err != nil {
		t.Fatal(err)
	}
	change, err := db.ChangeAgentEngineCapability(alice, agent.ID, core.EngineMihomo, false)
	if err != nil || change.TaskID == "" {
		t.Fatalf("capability change: %+v %v", change, err)
	}
	if _, err := db.GetTask(alice, change.TaskID); err != nil {
		t.Fatalf("capability task lost submitter ownership: %v", err)
	}
	if _, err := db.GetTask(bob, change.TaskID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign capability task: %v", err)
	}
	if err := db.CancelTask(alice, change.TaskID); err != nil {
		t.Fatal(err)
	}
	tcpRequest := core.TaskRequest{AgentID: agent.ID, Action: core.ActionEnableBBR}
	tcp, err := db.CreateTask(alice, tcpRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateTask(bob, tcpRequest); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-owner TCP exclusion: %v", err)
	}
	if tasks, err := db.LatestSystemTCPTasks(bob, ""); err != nil || len(tasks) != 0 {
		t.Fatalf("foreign TCP history: %+v %v", tasks, err)
	}
	if tasks, err := db.LatestSystemTCPTasks(alice, agent.ID); err != nil || len(tasks) != 1 || tasks[0].ID != tcp.ID {
		t.Fatalf("owner TCP history: %+v %v", tasks, err)
	}
}

func TestSubStoreOwnerAndFormatIsolationWithPostgreSQL(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	alice, bob := WithConfigScope(ctx, "usr_alice", false), WithConfigScope(ctx, "usr_bob", false)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	config, err := db.SaveAgentConfig(alice, core.Config{AgentID: agent.ID, Name: "private", Engine: core.EngineMihomo, Content: "mixed-port: 21001\n"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	target, err := db.CreateSubStoreSyncTarget(alice, "Alice group", core.SubStoreSyncModeIncremental, core.SubStoreSyncFormatMihomo)
	if err != nil || target.OwnerID != "usr_alice" || target.SyncFormat != core.SubStoreSyncFormatMihomo {
		t.Fatalf("owned format target: %+v %v", target, err)
	}
	bobTarget, err := db.CreateSubStoreSyncTarget(bob, "Bob group")
	if err != nil || bobTarget.SyncFormat != core.SubStoreSyncFormatURL {
		t.Fatalf("default URL format: %+v %v", bobTarget, err)
	}
	for _, format := range []string{"clash", "invalid"} {
		if _, err := db.UpdateSubStoreSyncTarget(alice, target.ID, "Alice group", "Alice group", target.SyncMode, format); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid sync format %q: %v", format, err)
		}
	}
	selection := core.SubStoreSyncSelection{ConfigID: config.ID, AgentID: agent.ID, Engine: config.Engine, ProfileTag: "same-tag", CustomName: "Alice"}
	if _, err := db.ReplaceSubStoreSyncSelections(alice, target.ID, []core.SubStoreSyncSelection{selection}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceSubStoreSyncSelections(bob, bobTarget.ID, []core.SubStoreSyncSelection{selection}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign selection accepted: %v", err)
	}
	if _, err := db.SubStoreSyncTarget(bob, target.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign target read: %v", err)
	}
	if _, err := db.UpdateSubStoreSyncTarget(bob, target.ID, "stolen", "stolen", target.SyncMode, "url"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign target update: %v", err)
	}
	if err := db.DeleteSubStoreSyncTarget(bob, target.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign target delete: %v", err)
	}
	if _, err := db.ReplaceSubStoreSyncSelections(bob, target.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign selection clear: %v", err)
	}
	if items, err := db.ListSubStoreSyncSelections(bob, target.ID); err != nil || len(items) != 0 {
		t.Fatalf("foreign selection list: %+v %v", items, err)
	}
	for _, format := range []string{"url", "mihomo"} {
		updated, err := db.UpdateSubStoreSyncTarget(alice, target.ID, target.DisplayName, target.SubscriptionName, target.SyncMode, format)
		if err != nil || updated.SyncFormat != format || updated.SelectionCount != 1 {
			t.Fatalf("format update lost selections: %+v %v", updated, err)
		}
		updated, err = db.UpdateSubStoreSyncTarget(alice, target.ID, "Renamed", target.SubscriptionName, target.SyncMode)
		if err != nil || updated.SyncFormat != format {
			t.Fatalf("legacy update reset format: %+v %v", updated, err)
		}
	}
}
