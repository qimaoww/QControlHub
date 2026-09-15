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

func TestReadConfigurationTaskKeepsOnlyLatestValidatedSnapshot(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("recent-read-cache"))
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	completeRead := func(action core.Action, content string) core.Task {
		t.Helper()
		created, err := dataStore.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Action: action, Engine: core.EngineMihomo})
		if err != nil {
			t.Fatalf("create read configuration task: %v", err)
		}
		claimed, err := dataStore.ClaimTask(ctx, agent.ID)
		if err != nil || claimed == nil || claimed.ID != created.ID || claimed.ConfigContent != "" {
			t.Fatalf("claim read configuration task = %+v, %v", claimed, err)
		}
		if err := dataStore.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{
			LeaseID: claimed.LeaseID, Success: true, Output: content,
		}); err != nil {
			t.Fatalf("complete read configuration task: %v", err)
		}
		stored, err := dataStore.GetTask(ctx, claimed.ID)
		if err != nil {
			t.Fatalf("get read configuration task: %v", err)
		}
		return stored
	}

	firstContent := "mixed-port: 7890\nmode: rule\nproxies: []\n"
	first := completeRead(core.ActionReadConfig, firstContent)
	if first.Status != core.TaskSucceeded || first.Output != "current configuration read and validated" || first.ConfigContent != "" {
		t.Fatalf("first read task = %+v", first)
	}
	if snapshot, err := dataStore.ReadTaskConfigSnapshot(ctx, first.ID, agent.ID, core.EngineMihomo); err != nil || snapshot != firstContent {
		t.Fatalf("first configuration snapshot = %q, %v", snapshot, err)
	}
	var storedSnapshot string
	if err := dataStore.pool.QueryRow(ctx, `SELECT config_content FROM tasks WHERE id=$1`, first.ID).Scan(&storedSnapshot); err != nil {
		t.Fatalf("read stored configuration snapshot: %v", err)
	}
	if !strings.HasPrefix(storedSnapshot, keyedEncryptedPrefix) || strings.Contains(storedSnapshot, firstContent) {
		t.Fatalf("configuration snapshot was not encrypted at rest: %q", storedSnapshot)
	}

	secondContent := "mixed-port: 7891\nmode: global\nproxies: []\n"
	second := completeRead(core.ActionReadConfig, secondContent)
	if snapshot, err := dataStore.ReadTaskConfigSnapshot(ctx, second.ID, agent.ID, core.EngineMihomo); err != nil || snapshot != secondContent {
		t.Fatalf("second configuration snapshot = %q, %v", snapshot, err)
	}
	if recent, err := dataStore.RecentReadTask(ctx, agent.ID, core.EngineMihomo, core.ActionReadConfig, time.Minute); err != nil || recent.ID != second.ID {
		t.Fatalf("recent read task = %+v, %v; want %s", recent, err, second.ID)
	}
	if _, err := dataStore.RecentReadTask(ctx, agent.ID, core.EngineXray, core.ActionReadConfig, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("recent read task for another engine error = %v, want not found", err)
	}
	if _, err := dataStore.RecentReadTask(ctx, agent.ID, core.EngineMihomo, core.ActionDeploy, time.Minute); !errors.Is(err, ErrInvalid) {
		t.Fatalf("recent read task for non-read action error = %v, want invalid", err)
	}
	if _, err := dataStore.ReadTaskConfigSnapshot(ctx, first.ID, agent.ID, core.EngineMihomo); !errors.Is(err, ErrNotFound) {
		t.Fatalf("superseded configuration snapshot error = %v, want not found", err)
	}
	if _, err := dataStore.ReadTaskConfigSnapshot(ctx, second.ID, "agt_other", core.EngineMihomo); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-agent configuration snapshot error = %v, want not found", err)
	}

	managedContent := "mixed-port: 7892\nmode: direct\nproxies: []\n"
	managed := completeRead(core.ActionReadManagedConfig, managedContent)
	if recent, err := dataStore.RecentReadTask(ctx, agent.ID, core.EngineMihomo, core.ActionReadManagedConfig, time.Minute); err != nil || recent.ID != managed.ID {
		t.Fatalf("recent managed read task = %+v, %v; want %s", recent, err, managed.ID)
	}
	if snapshot, err := dataStore.ReadTaskConfigSnapshot(ctx, second.ID, agent.ID, core.EngineMihomo); err != nil || snapshot != secondContent {
		t.Fatalf("managed read retired the independent import snapshot = %q, %v", snapshot, err)
	}

	invalid := completeRead(core.ActionReadConfig, "proxies: [")
	if invalid.Status != core.TaskFailed || !strings.Contains(invalid.Error, "invalid current configuration") {
		t.Fatalf("invalid read configuration task = %+v", invalid)
	}
	invalidUTF8 := completeRead(core.ActionReadConfig, string([]byte{0xff, 0xfe}))
	if invalidUTF8.Status != core.TaskFailed || !strings.Contains(invalidUTF8.Error, "not valid UTF-8") {
		t.Fatalf("invalid UTF-8 read configuration task = %+v", invalidUTF8)
	}
	if snapshot, err := dataStore.ReadTaskConfigSnapshot(ctx, second.ID, agent.ID, core.EngineMihomo); err != nil || snapshot != secondContent {
		t.Fatalf("last valid snapshot after rejected read = %q, %v", snapshot, err)
	}

	config, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "cache invalidation", Engine: core.EngineMihomo,
		Content: "listeners: [{name: cache, type: http, port: 7890}]\nmode: rule\nrules:\n  - MATCH,DIRECT\n",
	}, 0)
	if err != nil {
		t.Fatalf("save deployment for cache invalidation: %v", err)
	}
	deploy, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: agent.ID, Action: core.ActionDeploy, Engine: core.EngineMihomo, ConfigID: config.ID,
	})
	if err != nil {
		t.Fatalf("create deployment for cache invalidation: %v", err)
	}
	claimed, err := dataStore.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.ID != deploy.ID {
		t.Fatalf("claim deployment for cache invalidation = %+v, %v", claimed, err)
	}
	if err := dataStore.CompleteTask(ctx, agent.ID, deploy.ID, core.TaskResultRequest{
		LeaseID: claimed.LeaseID, Success: true,
	}); err != nil {
		t.Fatalf("complete deployment for cache invalidation: %v", err)
	}
	if _, err := dataStore.RecentReadTask(ctx, agent.ID, core.EngineMihomo, core.ActionReadManagedConfig, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deployment retained stale managed snapshot: %v", err)
	}
	if _, err := dataStore.RecentReadTask(ctx, agent.ID, core.EngineMihomo, core.ActionReadConfig, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deployment retained legacy managed/import snapshot: %v", err)
	}
}
