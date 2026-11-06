package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestConfigManagementAtomicRollbackAndConcurrentCAS(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	source := "listeners: [{name: first, type: http, port: 23111}, {name: second, type: http, port: 23112}]\nrules: ['MATCH,DIRECT']\n"
	saved, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Engine: core.EngineMihomo, Name: "single", Content: source}, 0)
	if err != nil {
		t.Fatal(err)
	}
	state := serverconfig.ManagedState{Disabled: []serverconfig.ManagedResource{{Section: "listeners", Tag: "first"}}}
	if _, err = db.pool.Exec(ctx, "CREATE FUNCTION reject_management_task() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'task failure fixture'; END $$; CREATE TRIGGER reject_management_task BEFORE INSERT ON tasks FOR EACH ROW EXECUTE FUNCTION reject_management_task()"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = db.SaveAgentConfigManagement(ctx, saved, 1, state, core.ActionValidate); err == nil {
		t.Fatal("failed task accepted state")
	}
	current, err := db.AgentConfig(ctx, agent.ID, saved.Engine)
	if err != nil || current.Version != 1 || current.Content != source {
		t.Fatal("failed task changed full source")
	}
	if _, err = db.ConfigRevision(ctx, saved.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed task left a revision")
	}
	if _, err = db.pool.Exec(ctx, "ALTER TABLE tasks DISABLE TRIGGER reject_management_task"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, _, err := db.SaveAgentConfigManagement(ctx, saved, 1, state, core.ActionValidate)
			results <- err
		}()
	}
	successes, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent state writes: %d/%d", successes, conflicts)
	}
	current, err = db.AgentConfig(ctx, agent.ID, saved.Engine)
	if err != nil {
		t.Fatal(err)
	}
	current.Description = "metadata-only save"
	current, err = db.SaveAgentConfig(ctx, current, current.Version)
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := db.ConfigManagementState(ctx, current.ID, current.Version)
	if err != nil || len(inherited.Disabled) != 1 {
		t.Fatal("ordinary source save lost disabled state")
	}
	current.Content = strings.ReplaceAll(current.Content, "first", "renamed")
	metadata := ConfigClientMetadataMutation{OriginalTag: "first", Tag: "renamed"}
	current, err = db.SaveAgentConfigWithClientMetadata(ctx, current, current.Version, metadata)
	if err != nil {
		t.Fatal(err)
	}
	inherited, err = db.ConfigManagementState(ctx, current.ID, current.Version)
	if err != nil || len(inherited.Disabled) != 1 || inherited.Disabled[0].Tag != "renamed" {
		t.Fatal("preset rename lost disabled association")
	}
	current.Content = "listeners: [{name: second, type: http, port: 23112}]\nrules: ['MATCH,DIRECT']\n"
	current, err = db.SaveAgentConfigWithClientMetadata(ctx, current, current.Version, ConfigClientMetadataMutation{OriginalTag: "renamed", Tag: "renamed", Delete: true})
	if err != nil {
		t.Fatal(err)
	}
	inherited, err = db.ConfigManagementState(ctx, current.ID, current.Version)
	if err != nil || len(inherited.Disabled) != 0 {
		t.Fatal("deleting disabled source left a stale state")
	}
	var stateRows int
	if err = db.pool.QueryRow(ctx, "SELECT count(*) FROM config_management_states WHERE config_id=$1", current.ID).Scan(&stateRows); err != nil || stateRows != 3 {
		t.Fatalf("history state boundaries: %d %v", stateRows, err)
	}
}

func TestConfigManagementAppliedStatusAndAllDisabledOwnership(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	source := "listeners: [{name: first, type: http, port: 23111}]\nrules: ['MATCH,DIRECT']\n"
	saved, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Engine: core.EngineMihomo, Name: "single", Content: source}, 0)
	if err != nil {
		t.Fatal(err)
	}
	status, err := db.ConfigManagementDeployment(ctx, saved.ID, saved.Version)
	if err != nil || status.State != "pending" {
		t.Fatalf("unsent state: %+v %v", status, err)
	}
	state := serverconfig.ManagedState{Disabled: []serverconfig.ManagedResource{{Section: "listeners", Tag: "first"}}}
	saved, queuedTask, err := db.SaveAgentConfigManagement(ctx, saved, saved.Version, state, core.ActionDeploy)
	if err != nil {
		t.Fatal(err)
	}
	status, err = db.ConfigManagementDeployment(ctx, saved.ID, saved.Version)
	if err != nil || status.State != "deploying" || status.TaskID != queuedTask.ID || status.TaskStatus != "pending" {
		t.Fatalf("queued deploy: %+v %v", status, err)
	}
	claim, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claim == nil {
		t.Fatalf("claim: %v", err)
	}
	if err = db.CompleteTask(ctx, agent.ID, claim.ID, core.TaskResultRequest{LeaseID: claim.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	status, err = db.ConfigManagementDeployment(ctx, saved.ID, saved.Version)
	if err != nil || status.State != "applied" || status.TaskID != queuedTask.ID || status.TaskStatus != "succeeded" {
		t.Fatalf("applied stopped revision: %+v %v", status, err)
	}
	var running bool
	if err = db.pool.QueryRow(ctx, "SELECT running FROM agent_engine_ownership WHERE agent_id=$1 AND engine=$2", agent.ID, saved.Engine).Scan(&running); err != nil || running {
		t.Fatalf("empty deployment recorded running: %v %v", running, err)
	}
	// Saving a newer target must not inherit the previous deployment receipt.
	saved, _, err = db.SaveAgentConfigManagement(ctx, saved, saved.Version, serverconfig.ManagedState{}, core.ActionValidate)
	if err != nil {
		t.Fatal(err)
	}
	claim, err = db.ClaimTask(ctx, agent.ID)
	if err != nil || claim == nil {
		t.Fatalf("validation claim: %v", err)
	}
	if err = db.CompleteTask(ctx, agent.ID, claim.ID, core.TaskResultRequest{LeaseID: claim.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	status, err = db.ConfigManagementDeployment(ctx, saved.ID, saved.Version)
	if err != nil || status.State != "pending" {
		t.Fatalf("validation claimed runtime application: %+v %v", status, err)
	}
}
