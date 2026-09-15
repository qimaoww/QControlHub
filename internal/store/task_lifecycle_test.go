package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClaimAndResumeFeatureDowngradeWithPostgreSQL(t *testing.T) {
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
	downgradeFeatures := func(ctx context.Context, agentID string) {
		t.Helper()
		features := []string{core.AgentFeatureSelfUpgrade, core.AgentFeaturePortTraffic, core.AgentFeatureCoreLogs}
		encoded, _ := json.Marshal(features)
		if _, err := dataStore.pool.Exec(ctx, `UPDATE agents SET features=$2 WHERE id=$1`, agentID, encoded); err != nil {
			t.Fatalf("downgrade agent features: %v", err)
		}
	}

	// A downgraded Agent must not have a pending mirror task claimed, while an
	// explicit official or omitted source task stays dispatchable.
	source, sourceEnrollment := enrollTaskTestAgentWithSource(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, source.ID, sourceEnrollment)
	mirror, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: source.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror),
	})
	if err != nil {
		t.Fatalf("create mirror task: %v", err)
	}
	official, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: source.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceOfficial),
	})
	if err != nil {
		t.Fatalf("create official task: %v", err)
	}
	downgradeFeatures(ctx, source.ID)
	claimed, err := dataStore.ClaimTask(ctx, source.ID)
	if err != nil {
		t.Fatalf("claim after downgrade: %v", err)
	}
	if claimed == nil || claimed.ID != official.ID {
		t.Fatalf("ClaimTask after downgrade = %+v, want official task %s", claimed, official.ID)
	}
	if got, err := dataStore.GetTask(ctx, mirror.ID); err != nil || got.Status != core.TaskPending {
		t.Fatalf("mirror task after downgrade = %+v, %v; want pending", got, err)
	}

	// A downgraded Agent that reconnects must not resume a running mirror task;
	// the running task is failed atomically with a feature reason.
	source2, source2Enrollment := enrollTaskTestAgentWithSource(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, source2.ID, source2Enrollment)
	runningMirror, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: source2.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror),
	})
	if err != nil {
		t.Fatalf("create running mirror task: %v", err)
	}
	claimedRunning, err := dataStore.ClaimTask(ctx, source2.ID)
	if err != nil || claimedRunning == nil || claimedRunning.ID != runningMirror.ID {
		t.Fatalf("claim running mirror = %+v, %v; want %s", claimedRunning, err, runningMirror.ID)
	}
	downgradeFeatures(ctx, source2.ID)
	resumed, err := dataStore.RunningTask(ctx, source2.ID)
	if err != nil {
		t.Fatalf("RunningTask after downgrade: %v", err)
	}
	if resumed != nil {
		t.Fatalf("RunningTask delivered a downgraded mirror task: %+v", resumed)
	}
	failed, err := dataStore.GetTask(ctx, runningMirror.ID)
	if err != nil || failed.Status != core.TaskFailed {
		t.Fatalf("downgraded running mirror task = %+v, %v; want failed", failed, err)
	}
	assertRawTaskTerminalCleaned(t, ctx, dataStore, runningMirror.ID)

	// Cross-agent claims must never pick another Agent's pending mirror task.
	legacy, legacyEnrollment := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, legacy.ID, legacyEnrollment)
	if claimed, err := dataStore.ClaimTask(ctx, legacy.ID); err != nil || claimed != nil {
		t.Fatalf("ClaimTask(legacy) = %+v, %v; want nil", claimed, err)
	}
}
