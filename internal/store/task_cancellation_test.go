package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTaskCancelRetryAndFiltersWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	firstContent := "listeners: [{name: first, type: http, port: 7890}]\nmode: rule\nrules:\n  - MATCH,DIRECT\n"
	config, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "task lifecycle v1", Engine: core.EngineMihomo, Content: firstContent,
	}, 0)
	if err != nil {
		t.Fatalf("save initial agent config: %v", err)
	}
	globalConfig, err := dataStore.CreateConfig(ctx, core.Config{
		Name: "migration ownership guard", Engine: core.EngineMihomo, Content: firstContent,
	})
	if err != nil {
		t.Fatalf("save global configuration: %v", err)
	}
	defer dataStore.DeleteConfig(context.Background(), globalConfig.ID)
	if _, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: agent.ID, Action: core.ActionImportExisting, Engine: core.EngineMihomo, ConfigID: globalConfig.ID,
	}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "saved snapshot") {
		t.Fatalf("create migration from global configuration error = %v, want node snapshot rejection", err)
	}
	existingConfigs, err := dataStore.ExistingConfigIDs(ctx, []string{config.ID, "cfg_missing"})
	if err != nil || !existingConfigs[config.ID] || existingConfigs["cfg_missing"] {
		t.Fatalf("existing task configuration IDs = %+v, %v", existingConfigs, err)
	}
	baselineOverview, err := dataStore.Overview(ctx)
	if err != nil {
		t.Fatalf("load baseline overview: %v", err)
	}
	upgrade, err := dataStore.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Action: core.ActionUpgradeAgent})
	if err != nil || upgrade.Engine != "" {
		t.Fatalf("create Agent upgrade task = %+v, %v", upgrade, err)
	}
	if err := dataStore.CancelTask(ctx, upgrade.ID); err != nil {
		t.Fatalf("cancel Agent upgrade task: %v", err)
	}
	original, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: agent.ID, Action: core.ActionValidate, Engine: core.EngineMihomo, ConfigID: config.ID,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	assertTaskOverview(t, ctx, dataStore, baselineOverview, 1, 1, 0)
	if err := dataStore.CancelTask(ctx, original.ID); err != nil {
		t.Fatalf("cancel pending task: %v", err)
	}
	assertTaskOverview(t, ctx, dataStore, baselineOverview, 0, 0, 0)
	canceled, err := dataStore.GetTask(ctx, original.ID)
	if err != nil || canceled.Status != core.TaskCanceled || canceled.FinishedAt == nil || canceled.Error != "canceled by administrator" {
		t.Fatalf("canceled task = %+v, %v", canceled, err)
	}
	if err := dataStore.CancelTask(ctx, original.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancel completed task error = %v, want conflict", err)
	}
	var payloadCleared bool
	if err := dataStore.pool.QueryRow(ctx, `SELECT config_content IS NULL AND lease_id IS NULL FROM tasks WHERE id=$1`, original.ID).Scan(&payloadCleared); err != nil || !payloadCleared {
		t.Fatalf("canceled task payload cleared = %v, %v", payloadCleared, err)
	}

	secondContent := "listeners: [{name: second, type: http, port: 7891}]\nmode: rule\nrules:\n  - MATCH,DIRECT\n"
	updated, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "task lifecycle v2", Engine: core.EngineMihomo, Content: secondContent,
	}, config.Version)
	if err != nil || updated.Version != 2 {
		t.Fatalf("save current agent config = %+v, %v", updated, err)
	}
	retried, err := dataStore.RetryTask(ctx, original.ID)
	if err != nil || retried.ID == original.ID || retried.Status != core.TaskPending || retried.ConfigVersion != updated.Version {
		t.Fatalf("retry canceled task = %+v, %v", retried, err)
	}
	assertTaskOverview(t, ctx, dataStore, baselineOverview, 1, 1, 0)
	claimed, err := dataStore.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.ID != retried.ID || claimed.ConfigContent != secondContent || claimed.ConfigVersion != updated.Version || claimed.Attempt != 1 || claimed.LeaseID == "" {
		t.Fatalf("claimed retry = %+v, %v", claimed, err)
	}
	assertTaskOverview(t, ctx, dataStore, baselineOverview, 1, 0, 1)
	if err := dataStore.CancelTask(ctx, claimed.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancel running task error = %v, want conflict", err)
	}
	if err := dataStore.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{
		LeaseID: claimed.LeaseID, Success: true, Output: "validated current configuration",
	}); err != nil {
		t.Fatalf("complete retried task: %v", err)
	}
	assertTaskOverview(t, ctx, dataStore, baselineOverview, 0, 0, 0)
	if _, err := dataStore.RetryTask(ctx, claimed.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("retry succeeded task error = %v, want conflict", err)
	}

	canceledTasks, err := dataStore.ListTasksFiltered(ctx, agent.ID, core.TaskCanceled, core.ActionValidate, 10)
	if err != nil || len(canceledTasks) != 1 || canceledTasks[0].ID != original.ID {
		t.Fatalf("canceled task filter = %+v, %v", canceledTasks, err)
	}
	succeededTasks, err := dataStore.ListTasksFiltered(ctx, agent.ID, core.TaskSucceeded, core.ActionValidate, 10)
	if err != nil || len(succeededTasks) != 1 || succeededTasks[0].ID != retried.ID {
		t.Fatalf("succeeded task filter = %+v, %v", succeededTasks, err)
	}
	emptyTasks, err := dataStore.ListTasksFiltered(ctx, agent.ID, core.TaskSucceeded, core.ActionStatus, 10)
	if err != nil || len(emptyTasks) != 0 {
		t.Fatalf("empty action filter = %+v, %v", emptyTasks, err)
	}
	missingID, err := core.NewID("tsk")
	if err != nil {
		t.Fatal(err)
	}
	if err := dataStore.CancelTask(ctx, missingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancel missing task error = %v, want not found", err)
	}
	if _, err := dataStore.RetryTask(ctx, missingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retry missing task error = %v, want not found", err)
	}
}
