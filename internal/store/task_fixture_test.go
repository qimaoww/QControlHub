package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// assertRawTaskTerminalCleaned reads the underlying tasks row directly and
// asserts the terminal-state cleanup and unknown-outcome wording. GetTask does
// not expose lease_id or config_content, so the raw columns must be checked
// here to avoid a false positive during the downgrade-fail regression.
func assertRawTaskTerminalCleaned(t *testing.T, ctx context.Context, dataStore *Store, taskID string) {
	t.Helper()
	var status string
	var finishedAt *time.Time
	var leaseID *string
	var configContent *string
	var errMsg string
	if err := dataStore.pool.QueryRow(ctx, `
		SELECT status, finished_at, lease_id, config_content, COALESCE(error,'')
		FROM tasks WHERE id=$1`, taskID).Scan(&status, &finishedAt, &leaseID, &configContent, &errMsg); err != nil {
		t.Fatalf("read raw terminal task: %v", err)
	}
	if status != string(core.TaskFailed) {
		t.Fatalf("raw terminal task status = %q, want %q", status, core.TaskFailed)
	}
	if finishedAt == nil {
		t.Fatalf("raw terminal task finished_at is NULL")
	}
	if leaseID != nil {
		t.Fatalf("raw terminal task lease must be NULL, got %q", *leaseID)
	}
	if configContent != nil {
		t.Fatalf("raw terminal task config_content must be NULL, got %q", *configContent)
	}
	if !strings.Contains(errMsg, core.AgentFeatureMihomoDevelopmentSource) || !strings.Contains(errMsg, "cannot be safely resumed") || !strings.Contains(errMsg, "unknown whether the previous Agent executed it") {
		t.Fatalf("raw terminal task error = %q, want feature + safe-resume + unknown-outcome wording", errMsg)
	}
	if strings.Contains(errMsg, "was not executed") {
		t.Fatalf("raw terminal task error must not claim the task was never executed: %q", errMsg)
	}
}

func assertTaskOverview(t *testing.T, ctx context.Context, dataStore *Store, baseline core.Overview, activeDelta, queuedDelta, runningDelta int) {
	t.Helper()
	overview, err := dataStore.Overview(ctx)
	if err != nil {
		t.Fatalf("load task overview: %v", err)
	}
	if overview.TasksPending != baseline.TasksPending+activeDelta ||
		overview.TasksQueued != baseline.TasksQueued+queuedDelta ||
		overview.TasksRunning != baseline.TasksRunning+runningDelta {
		t.Fatalf(
			"task overview counts = active %d, queued %d, running %d; want %d, %d, %d",
			overview.TasksPending, overview.TasksQueued, overview.TasksRunning,
			baseline.TasksPending+activeDelta, baseline.TasksQueued+queuedDelta, baseline.TasksRunning+runningDelta,
		)
	}
}

func enrollTaskTestAgent(t *testing.T, ctx context.Context, dataStore *Store) (core.Agent, string) {
	t.Helper()
	enrollment, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{
		Name: "task lifecycle integration", TTLMinutes: 30, MaxUses: 1,
	})
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	publicKey := make([]byte, 32)
	if _, err := rand.Read(publicKey); err != nil {
		t.Fatal(err)
	}
	agent, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: "task-lifecycle-agent", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo},
		Features:     []string{core.AgentFeatureSelfUpgrade, core.AgentFeatureManagedConfigRead, core.AgentFeatureIndependentEgress},
		PublicKey:    base64.RawURLEncoding.EncodeToString(publicKey),
	}, enrollment.Token)
	if err != nil {
		t.Fatalf("enroll task test agent: %v", err)
	}
	return agent, enrollment.ID
}

func enrollTaskTestAgentWithSource(t *testing.T, ctx context.Context, dataStore *Store) (core.Agent, string) {
	t.Helper()
	enrollment, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{
		Name: "task lifecycle source candidate", TTLMinutes: 30, MaxUses: 1,
	})
	if err != nil {
		t.Fatalf("create source enrollment token: %v", err)
	}
	publicKey := make([]byte, 32)
	if _, err := rand.Read(publicKey); err != nil {
		t.Fatal(err)
	}
	agent, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: "task-lifecycle-source-agent", OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo},
		Features:     []string{core.AgentFeatureSelfUpgrade, core.AgentFeatureMihomoDevelopmentSource, core.AgentFeatureIndependentEgress},
		PublicKey:    base64.RawURLEncoding.EncodeToString(publicKey),
	}, enrollment.Token)
	if err != nil {
		t.Fatalf("enroll source agent: %v", err)
	}
	return agent, enrollment.ID
}

func setTaskStartedAt(t *testing.T, ctx context.Context, dataStore *Store, taskID string, startedAt time.Time) {
	t.Helper()
	if _, err := dataStore.pool.Exec(ctx, `UPDATE tasks SET started_at=$2 WHERE id=$1`, taskID, startedAt); err != nil {
		t.Fatalf("set task %s started_at: %v", taskID, err)
	}
}

func cleanupTaskTestAgent(dataStore *Store, agentID, enrollmentID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = dataStore.pool.Exec(ctx, `DELETE FROM tasks WHERE agent_id=$1`, agentID)
	_, _ = dataStore.pool.Exec(ctx, `DELETE FROM config_revisions WHERE agent_id=$1`, agentID)
	_, _ = dataStore.pool.Exec(ctx, `DELETE FROM configs WHERE agent_id=$1`, agentID)
	_, _ = dataStore.pool.Exec(ctx, `DELETE FROM agent_nonces WHERE agent_id=$1`, agentID)
	_, _ = dataStore.pool.Exec(ctx, `DELETE FROM agents WHERE id=$1`, agentID)
	_, _ = dataStore.pool.Exec(ctx, `DELETE FROM enrollment_tokens WHERE id=$1`, enrollmentID)
}
