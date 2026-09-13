package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestPresetAutoInstallTaskLifecycle(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	input := core.Config{AgentID: agent.ID, Engine: core.EngineMihomo, Name: "auto install",
		Content: "listeners: [{name: first, type: socks, port: 21001}]\nrules: ['MATCH,DIRECT']\n"}
	options := ConfigMutationOptions{Action: core.ActionValidate, InstallIfMissing: true}
	if _, _, err := db.SaveAgentConfigAndTask(ctx, input, 0, options); !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy Agent accepted auto install: %v", err)
	}
	if _, err := db.AgentConfig(ctx, agent.ID, input.Engine); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected auto install left a configuration: %v", err)
	}
	features := []string{core.AgentFeatureIndependentEgress, core.AgentFeaturePresetAutoInstall}
	heartbeat := func(installed bool, advertised []string) {
		t.Helper()
		if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: advertised,
			Runtime: map[core.Engine]core.RuntimeState{input.Engine: {Installed: installed, Version: "keep-development"}}}); err != nil {
			t.Fatal(err)
		}
	}
	heartbeat(false, features)
	saved, task, err := db.SaveAgentConfigAndTask(ctx, input, 0, options)
	if err != nil || !task.InstallIfMissing || task.ConfigVersion != saved.Version || task.CoreVersion != "" {
		t.Fatalf("save compound task: %+v %v", task, err)
	}
	request := core.TaskRequest{AgentID: agent.ID, Engine: input.Engine, Action: options.Action,
		ConfigID: saved.ID, ExpectedConfigVersion: saved.Version, InstallIfMissing: true}
	reused, err := db.CreateTask(ctx, request)
	if err != nil || !reused.Reused || reused.ID != task.ID || !reused.InstallIfMissing {
		t.Fatalf("reuse compound task: %+v %v", reused, err)
	}
	request.InstallIfMissing = false
	plain, err := db.CreateTask(ctx, request)
	if err != nil || plain.Reused || plain.ID == task.ID || plain.InstallIfMissing {
		t.Fatalf("plain validation reused installation: %+v %v", plain, err)
	}
	if err := db.CancelTask(ctx, plain.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.ID != task.ID || !claimed.InstallIfMissing || claimed.ConfigContent != saved.Content {
		t.Fatalf("claim compound snapshot: %+v %v", claimed, err)
	}
	resumed, err := db.RunningTask(ctx, agent.ID)
	if err != nil || resumed == nil || !resumed.InstallIfMissing || resumed.LeaseID != claimed.LeaseID {
		t.Fatalf("resume compound task: %+v %v", resumed, err)
	}
	setTaskStartedAt(t, ctx, db, task.ID, time.Now().Add(-3*time.Minute))
	if err := db.RequeueStaleTasks(ctx, 2*time.Minute, 6*time.Minute, 3); err != nil {
		t.Fatal(err)
	}
	if running, err := db.GetTaskState(ctx, task.ID); err != nil || running.Status != core.TaskRunning || !running.InstallIfMissing {
		t.Fatalf("compound task lost its installation lease: %+v %v", running, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Error: "validation failed after install"}); err != nil {
		t.Fatal(err)
	}
	heartbeat(true, features)
	retry, err := db.RetryTask(ctx, task.ID)
	if err != nil || !retry.InstallIfMissing || retry.ConfigVersion != saved.Version {
		t.Fatalf("retry lost original operation/revision: %+v %v", retry, err)
	}
	if err := db.CancelTask(ctx, retry.ID); err != nil {
		t.Fatal(err)
	}
	input.Content = strings.ReplaceAll(input.Content, "21001", "21002")
	if _, err := db.SaveAgentConfig(ctx, input, saved.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RetryTask(ctx, task.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry silently switched to a later draft: %v", err)
	}
}

func TestPresetAutoInstallNegotiatesBeforeDispatchAndResume(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	features := []string{core.AgentFeatureIndependentEgress, core.AgentFeaturePresetAutoInstall}
	setFeatures := func(values []string) {
		t.Helper()
		if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: values}); err != nil {
			t.Fatal(err)
		}
	}
	setFeatures(features)
	_, task, err := db.SaveAgentConfigAndTask(ctx, core.Config{AgentID: agent.ID, Engine: core.EngineMihomo,
		Name: "negotiated install", Content: "listeners: [{name: a, type: socks, port: 21001}]\nrules: ['MATCH,DIRECT']\n"},
		0, ConfigMutationOptions{Action: core.ActionDeploy, InstallIfMissing: true})
	if err != nil {
		t.Fatal(err)
	}
	setFeatures(features[:1])
	if claimed, err := db.ClaimTask(ctx, agent.ID); err != nil || claimed != nil {
		t.Fatalf("old Agent received install flag: %+v %v", claimed, err)
	}
	setFeatures(features)
	if claimed, err := db.ClaimTask(ctx, agent.ID); err != nil || claimed == nil || claimed.ID != task.ID {
		t.Fatalf("upgraded Agent could not claim: %+v %v", claimed, err)
	}
	setFeatures(features[:1])
	if resumed, err := db.RunningTask(ctx, agent.ID); err != nil || resumed != nil {
		t.Fatalf("downgraded Agent resumed compound task: %+v %v", resumed, err)
	}
	failed, err := db.GetTask(ctx, task.ID)
	if err != nil || failed.Status != core.TaskFailed || !strings.Contains(failed.Error, core.AgentFeaturePresetAutoInstall) || !strings.Contains(failed.Error, "unknown") {
		t.Fatalf("downgrade result: %+v %v", failed, err)
	}
}

func TestPresetAutoInstallRequiresHostOwnershipAndCurrentPermission(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET features=features||'["preset-auto-install-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	user, account := sharedTestUser(t, db, ctx, "preset-owner")
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 0, 21001)
	input := core.Config{AgentID: agent.ID, Engine: core.EngineMihomo, Name: "shared add",
		Content: "listeners: [{name: a, type: socks, port: 21001}]\nrules: ['MATCH,DIRECT']\n"}
	options := ConfigMutationOptions{Action: core.ActionValidate, InstallIfMissing: true}
	if _, _, err := db.SaveAgentConfigAndTask(account, input, 0, options); !errors.Is(err, ErrForbidden) {
		t.Fatalf("shared recipient installed a host core: %v", err)
	}
	if _, err := db.AgentConfig(account, agent.ID, input.Engine); !errors.Is(err, ErrNotFound) {
		t.Fatalf("forbidden install left a saved config: %v", err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET owner_id=$2 WHERE id=$1`, agent.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	_, task, err := db.SaveAgentConfigAndTask(account, input, 0, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE panel_users SET permissions=permissions-'tasks.execute' WHERE id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	if claimed, err := db.ClaimTask(ctx, agent.ID); err != nil || claimed != nil {
		t.Fatalf("revoked user dispatched install: %+v %v", claimed, err)
	}
	if canceled, err := db.GetTask(ctx, task.ID); err != nil || canceled.Status != core.TaskCanceled {
		t.Fatalf("revoked task not canceled: %+v %v", canceled, err)
	}
}

func TestPresetAutoInstallMigratesVersion60(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	task, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `ALTER TABLE tasks DROP COLUMN install_if_missing;
		DELETE FROM qcontrolhub_schema_migrations WHERE version>=61;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(60) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if migrated, err := db.GetTask(ctx, task.ID); err != nil || migrated.InstallIfMissing || migrated.Status != task.Status {
		t.Fatalf("legacy task changed during migration: %+v %v", migrated, err)
	}
}
