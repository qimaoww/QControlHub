package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
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
	firstContent := "mixed-port: 7890\nmode: rule\nrules:\n  - MATCH,DIRECT\n"
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

	secondContent := "mixed-port: 7891\nmode: global\nrules:\n  - MATCH,DIRECT\n"
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

func TestCreateTaskCoalescesEquivalentActiveRequestsWithPostgreSQL(t *testing.T) {
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
	config, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "coalesced task v1", Engine: core.EngineMihomo,
		Content: "mixed-port: 7890\nmode: rule\nrules:\n  - MATCH,DIRECT\n",
	}, 0)
	if err != nil {
		t.Fatalf("save initial configuration: %v", err)
	}
	baseline, err := dataStore.Overview(ctx)
	if err != nil {
		t.Fatalf("load baseline overview: %v", err)
	}

	request := core.TaskRequest{AgentID: agent.ID, Action: core.ActionValidate, Engine: core.EngineMihomo, ConfigID: config.ID}
	type outcome struct {
		task core.Task
		err  error
	}
	const writers = 8
	start := make(chan struct{})
	results := make(chan outcome, writers)
	var wait sync.WaitGroup
	for range writers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			task, createErr := dataStore.CreateTask(ctx, request)
			results <- outcome{task: task, err: createErr}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	firstID := ""
	created := 0
	reused := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("create equivalent task: %v", result.err)
		}
		if firstID == "" {
			firstID = result.task.ID
		} else if result.task.ID != firstID {
			t.Fatalf("equivalent task IDs differ: %s and %s", firstID, result.task.ID)
		}
		if result.task.Reused {
			reused++
		} else {
			created++
		}
	}
	if created != 1 || reused != writers-1 {
		t.Fatalf("equivalent task outcomes = %d created, %d reused; want 1 and %d", created, reused, writers-1)
	}
	assertTaskOverview(t, ctx, dataStore, baseline, 1, 1, 0)

	updated, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "coalesced task v2", Engine: core.EngineMihomo,
		Content: "mixed-port: 7891\nmode: global\nrules:\n  - MATCH,DIRECT\n",
	}, config.Version)
	if err != nil {
		t.Fatalf("save updated configuration: %v", err)
	}
	versioned, err := dataStore.CreateTask(ctx, request)
	if err != nil || versioned.Reused || versioned.ID == firstID || versioned.ConfigVersion != updated.Version {
		t.Fatalf("new configuration version task = %+v, %v", versioned, err)
	}
	statusTask, err := dataStore.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Action: core.ActionStatus, Engine: core.EngineMihomo})
	if err != nil || statusTask.Reused {
		t.Fatalf("create distinct status task = %+v, %v", statusTask, err)
	}
	statusDuplicate, err := dataStore.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Action: core.ActionStatus, Engine: core.EngineMihomo})
	if err != nil || !statusDuplicate.Reused || statusDuplicate.ID != statusTask.ID {
		t.Fatalf("reuse status task = %+v, %v", statusDuplicate, err)
	}
	assertTaskOverview(t, ctx, dataStore, baseline, 3, 3, 0)
}

func TestDeployAdvancesLatestDeploymentWithPostgreSQL(t *testing.T) {
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
	config, err := dataStore.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Name: "deployment target", Engine: core.EngineMihomo,
		Content: "mixed-port: 7890\nmode: rule\nrules:\n  - MATCH,DIRECT\n",
	}, 0)
	if err != nil {
		t.Fatalf("save deployment configuration: %v", err)
	}
	completeDeploy := func() core.Task {
		t.Helper()
		created, createErr := dataStore.CreateTask(ctx, core.TaskRequest{
			AgentID: agent.ID, Action: core.ActionDeploy, Engine: core.EngineMihomo, ConfigID: config.ID,
		})
		if createErr != nil {
			t.Fatalf("create deployment task: %v", createErr)
		}
		claimed, claimErr := dataStore.ClaimTask(ctx, agent.ID)
		if claimErr != nil || claimed == nil || claimed.ID != created.ID {
			t.Fatalf("claim deployment task = %+v, %v", claimed, claimErr)
		}
		if completeErr := dataStore.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{
			LeaseID: claimed.LeaseID, Success: true, Output: "deployment result",
		}); completeErr != nil {
			t.Fatalf("complete deployment task: %v", completeErr)
		}
		stored, getErr := dataStore.GetTask(ctx, claimed.ID)
		if getErr != nil || stored.Status != core.TaskSucceeded {
			t.Fatalf("stored deployment task = %+v, %v", stored, getErr)
		}
		return stored
	}

	completeDeploy()
	deployments, err := dataStore.LatestDeployments(ctx)
	if err != nil {
		t.Fatalf("list deployments after deployment: %v", err)
	}
	real := completeDeploy()
	deployments, err = dataStore.LatestDeployments(ctx)
	if err != nil {
		t.Fatalf("list deployments after real completion: %v", err)
	}
	var actual core.Deployment
	for _, deployment := range deployments {
		if deployment.AgentID == agent.ID {
			actual = deployment
			break
		}
	}
	if actual.ConfigID != config.ID || actual.ConfigVersion != config.Version {
		t.Fatalf("deployment task %s deployment = %+v", real.ID, deployments)
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

func TestRequeueStaleTasksUsesActionSpecificLeaseAndAttemptLimit(t *testing.T) {
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
	regular, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: agent.ID, Action: core.ActionStatus, Engine: core.EngineMihomo,
	})
	if err != nil {
		t.Fatalf("create regular task: %v", err)
	}
	regularLease, err := dataStore.ClaimTask(ctx, agent.ID)
	if err != nil || regularLease == nil || regularLease.ID != regular.ID {
		t.Fatalf("claim regular task = %+v, %v", regularLease, err)
	}
	setTaskStartedAt(t, ctx, dataStore, regular.ID, time.Now().Add(-3*time.Minute))
	if err := dataStore.RequeueStaleTasks(ctx, 2*time.Minute, 6*time.Minute, 3); err != nil {
		t.Fatalf("requeue regular task: %v", err)
	}
	requeued, err := dataStore.GetTask(ctx, regular.ID)
	if err != nil || requeued.Status != core.TaskPending || requeued.Attempt != 1 || requeued.StartedAt != nil || requeued.FinishedAt != nil {
		t.Fatalf("requeued regular task = %+v, %v", requeued, err)
	}
	if err := dataStore.CompleteTask(ctx, agent.ID, regular.ID, core.TaskResultRequest{LeaseID: regularLease.LeaseID, Success: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale regular lease error = %v, want conflict", err)
	}
	if err := dataStore.CancelTask(ctx, regular.ID); err != nil {
		t.Fatalf("cancel requeued regular task: %v", err)
	}

	install, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: agent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: core.CoreVersionStable,
	})
	if err != nil {
		t.Fatalf("create install task: %v", err)
	}
	firstInstallLease, err := dataStore.ClaimTask(ctx, agent.ID)
	if err != nil || firstInstallLease == nil || firstInstallLease.ID != install.ID || firstInstallLease.Attempt != 1 {
		t.Fatalf("claim install task = %+v, %v", firstInstallLease, err)
	}
	setTaskStartedAt(t, ctx, dataStore, install.ID, time.Now().Add(-3*time.Minute))
	if err := dataStore.RequeueStaleTasks(ctx, 2*time.Minute, 6*time.Minute, 3); err != nil {
		t.Fatalf("check active install lease: %v", err)
	}
	stillRunning, err := dataStore.GetTask(ctx, install.ID)
	if err != nil || stillRunning.Status != core.TaskRunning {
		t.Fatalf("three-minute install task = %+v, %v; want running", stillRunning, err)
	}
	setTaskStartedAt(t, ctx, dataStore, install.ID, time.Now().Add(-7*time.Minute))
	if err := dataStore.RequeueStaleTasks(ctx, 2*time.Minute, 6*time.Minute, 3); err != nil {
		t.Fatalf("requeue first install lease: %v", err)
	}
	firstRequeue, err := dataStore.GetTask(ctx, install.ID)
	if err != nil || firstRequeue.Status != core.TaskPending || firstRequeue.Attempt != 1 {
		t.Fatalf("first install requeue = %+v, %v", firstRequeue, err)
	}
	if err := dataStore.CompleteTask(ctx, agent.ID, install.ID, core.TaskResultRequest{LeaseID: firstInstallLease.LeaseID, Success: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale install lease error = %v, want conflict", err)
	}

	secondInstallLease, err := dataStore.ClaimTask(ctx, agent.ID)
	if err != nil || secondInstallLease == nil || secondInstallLease.ID != install.ID || secondInstallLease.Attempt != 2 {
		t.Fatalf("claim second install attempt = %+v, %v", secondInstallLease, err)
	}
	setTaskStartedAt(t, ctx, dataStore, install.ID, time.Now().Add(-7*time.Minute))
	if err := dataStore.RequeueStaleTasks(ctx, 2*time.Minute, 6*time.Minute, 3); err != nil {
		t.Fatalf("requeue second install lease: %v", err)
	}
	secondRequeue, err := dataStore.GetTask(ctx, install.ID)
	if err != nil || secondRequeue.Status != core.TaskPending || secondRequeue.Attempt != 2 {
		t.Fatalf("second install requeue = %+v, %v", secondRequeue, err)
	}

	thirdInstallLease, err := dataStore.ClaimTask(ctx, agent.ID)
	if err != nil || thirdInstallLease == nil || thirdInstallLease.ID != install.ID || thirdInstallLease.Attempt != 3 {
		t.Fatalf("claim third install attempt = %+v, %v", thirdInstallLease, err)
	}
	setTaskStartedAt(t, ctx, dataStore, install.ID, time.Now().Add(-7*time.Minute))
	if err := dataStore.RequeueStaleTasks(ctx, 2*time.Minute, 6*time.Minute, 3); err != nil {
		t.Fatalf("expire third install lease: %v", err)
	}
	failed, err := dataStore.GetTask(ctx, install.ID)
	if err != nil || failed.Status != core.TaskFailed || failed.Attempt != 3 || failed.FinishedAt == nil || failed.Error != "agent did not report a result before the execution lease expired" {
		t.Fatalf("expired install task = %+v, %v", failed, err)
	}
	if err := dataStore.CompleteTask(ctx, agent.ID, install.ID, core.TaskResultRequest{LeaseID: thirdInstallLease.LeaseID, Success: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired third lease error = %v, want conflict", err)
	}
	failedInstalls, err := dataStore.ListTasksFiltered(ctx, agent.ID, core.TaskFailed, core.ActionInstall, 10)
	if err != nil || len(failedInstalls) != 1 || failedInstalls[0].ID != install.ID {
		t.Fatalf("failed install filter = %+v, %v", failedInstalls, err)
	}
}

func TestReadConfigurationTaskKeepsOnlyLatestValidatedSnapshot(t *testing.T) {
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
	completeRead := func(content string) core.Task {
		t.Helper()
		created, err := dataStore.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Action: core.ActionReadConfig, Engine: core.EngineMihomo})
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
	first := completeRead(firstContent)
	if first.Status != core.TaskSucceeded || first.Output != "current configuration read and validated" || first.ConfigContent != "" {
		t.Fatalf("first read task = %+v", first)
	}
	if snapshot, err := dataStore.ReadTaskConfigSnapshot(ctx, first.ID, agent.ID, core.EngineMihomo); err != nil || snapshot != firstContent {
		t.Fatalf("first configuration snapshot = %q, %v", snapshot, err)
	}

	secondContent := "mixed-port: 7891\nmode: global\nproxies: []\n"
	second := completeRead(secondContent)
	if snapshot, err := dataStore.ReadTaskConfigSnapshot(ctx, second.ID, agent.ID, core.EngineMihomo); err != nil || snapshot != secondContent {
		t.Fatalf("second configuration snapshot = %q, %v", snapshot, err)
	}
	if recent, err := dataStore.RecentReadTask(ctx, agent.ID, core.EngineMihomo, time.Minute); err != nil || recent.ID != second.ID {
		t.Fatalf("recent read task = %+v, %v; want %s", recent, err, second.ID)
	}
	if _, err := dataStore.RecentReadTask(ctx, agent.ID, core.EngineXray, time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("recent read task for another engine error = %v, want not found", err)
	}
	if _, err := dataStore.ReadTaskConfigSnapshot(ctx, first.ID, agent.ID, core.EngineMihomo); !errors.Is(err, ErrNotFound) {
		t.Fatalf("superseded configuration snapshot error = %v, want not found", err)
	}
	if _, err := dataStore.ReadTaskConfigSnapshot(ctx, second.ID, "agt_other", core.EngineMihomo); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-agent configuration snapshot error = %v, want not found", err)
	}

	invalid := completeRead("proxies: [")
	if invalid.Status != core.TaskFailed || !strings.Contains(invalid.Error, "invalid current configuration") {
		t.Fatalf("invalid read configuration task = %+v", invalid)
	}
	invalidUTF8 := completeRead(string([]byte{0xff, 0xfe}))
	if invalidUTF8.Status != core.TaskFailed || !strings.Contains(invalidUTF8.Error, "not valid UTF-8") {
		t.Fatalf("invalid UTF-8 read configuration task = %+v", invalidUTF8)
	}
	if snapshot, err := dataStore.ReadTaskConfigSnapshot(ctx, second.ID, agent.ID, core.EngineMihomo); err != nil || snapshot != secondContent {
		t.Fatalf("last valid snapshot after rejected read = %q, %v", snapshot, err)
	}
}

func TestEnrollmentStaysOfflineUntilFirstHeartbeat(t *testing.T) {
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
	if agent.Status != "offline" || !agent.LastSeen.Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("newly enrolled agent = %+v, want offline before its first heartbeat", agent)
	}
	stored, err := dataStore.GetAgent(ctx, agent.ID)
	if err != nil || stored.Status != "offline" {
		t.Fatalf("stored newly enrolled agent = %+v, %v", stored, err)
	}
	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Version: "test-heartbeat"}); err != nil {
		t.Fatal(err)
	}
	stored, err = dataStore.GetAgent(ctx, agent.ID)
	if err != nil || stored.Status != "online" || stored.Version != "test-heartbeat" {
		t.Fatalf("agent after first heartbeat = %+v, %v", stored, err)
	}
}

func TestCreateTaskValidatesCoreSourceWithPostgreSQL(t *testing.T) {
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
	// legacyAgent advertises only self-upgrade, so it is an older Agent that
	// cannot negotiate core_source and must be refused the mirror task.
	legacyAgent, legacyEnrollment := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, legacyAgent.ID, legacyEnrollment)
	if _, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: legacyAgent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror),
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy Agent mirror task error = %v, want ErrConflict", err)
	}
	official, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: legacyAgent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceOfficial),
	})
	if err != nil || official.CoreSource != string(core.CoreSourceOfficial) {
		t.Fatalf("legacy official install task = %+v, %v", official, err)
	}
	defaulted, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: legacyAgent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment,
	})
	if err != nil || defaulted.CoreSource != string(core.CoreSourceOfficial) {
		t.Fatalf("legacy default install task = %+v, %v", defaulted, err)
	}
	if defaulted.ID != official.ID || !defaulted.Reused {
		t.Fatalf("omitted development install must reuse the explicit official task: %+v, want reuse of %s", defaulted, official.ID)
	}

	// sourceAgent advertises the negotiated feature and accepts mirror, which is
	// persisted and preserved on retry.
	sourceAgent, sourceEnrollment := enrollTaskTestAgentWithSource(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, sourceAgent.ID, sourceEnrollment)
	mirror, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: sourceAgent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror),
	})
	if err != nil || mirror.CoreSource != string(core.CoreSourceMirror) {
		t.Fatalf("source Agent mirror install task = %+v, %v", mirror, err)
	}
	persisted, err := dataStore.GetTask(ctx, mirror.ID)
	if err != nil || persisted.CoreSource != string(core.CoreSourceMirror) {
		t.Fatalf("persisted mirror source = %+v, %v", persisted, err)
	}
	if err := dataStore.CancelTask(ctx, mirror.ID); err != nil {
		t.Fatalf("cancel mirror task: %v", err)
	}
	retried, err := dataStore.RetryTask(ctx, mirror.ID)
	if err != nil || retried.CoreSource != string(core.CoreSourceMirror) {
		t.Fatalf("retried mirror task = %+v, %v", retried, err)
	}

	invalidRequests := []core.TaskRequest{
		{AgentID: sourceAgent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: core.CoreVersionStable, CoreSource: string(core.CoreSourceMirror)},
		{AgentID: sourceAgent.ID, Action: core.ActionInstall, Engine: core.EngineXray, CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror)},
		{AgentID: sourceAgent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: core.CoreVersionDevelopment, CoreSource: "private"},
		{AgentID: legacyAgent.ID, Action: core.ActionStatus, Engine: core.EngineMihomo, CoreSource: string(core.CoreSourceMirror)},
	}
	for _, request := range invalidRequests {
		if _, err := dataStore.CreateTask(ctx, request); !errors.Is(err, ErrInvalid) {
			t.Fatalf("CreateTask(%+v) error = %v, want ErrInvalid", request, err)
		}
	}
}

func TestEffectiveSourceReuseWithPostgreSQL(t *testing.T) {
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

	// An explicit official request must reuse an existing legacy row whose
	// core_source is empty (NULL), i.e. a legacy omitted-source install.
	legacy, legacyEnrollment := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, legacy.ID, legacyEnrollment)
	legacyID, err := core.NewID("tsk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.pool.Exec(ctx, `
		INSERT INTO tasks (id,agent_id,action,engine,core_version,status,created_at)
		VALUES ($1,$2,'install','mihomo','development','pending',now())`, legacyID, legacy.ID); err != nil {
		t.Fatalf("insert legacy empty-source task: %v", err)
	}
	official, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: legacy.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceOfficial),
	})
	if err != nil || official.ID != legacyID || !official.Reused {
		t.Fatalf("explicit official must reuse legacy empty-source task: %+v, %v; want reuse of %s", official, err, legacyID)
	}

	// An omitted source request must reuse an existing explicit-official row.
	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	explicit, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: agent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceOfficial),
	})
	if err != nil {
		t.Fatalf("create explicit official task: %v", err)
	}
	omitted, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: agent.ID, Action: core.ActionInstall, Engine: core.EngineMihomo,
		CoreVersion: core.CoreVersionDevelopment,
	})
	if err != nil || omitted.ID != explicit.ID || !omitted.Reused || omitted.CoreSource != string(core.CoreSourceOfficial) {
		t.Fatalf("omitted source must reuse explicit official task: %+v, %v; want reuse of %s", omitted, err, explicit.ID)
	}
}

func TestHeartbeatClearsStaleFeaturesWithPostgreSQL(t *testing.T) {
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

	agent, enrollmentID := enrollTaskTestAgentWithSource(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	current, err := dataStore.GetAgent(ctx, agent.ID)
	if err != nil || !containsFeature(current.Features, core.AgentFeatureMihomoDevelopmentSource) {
		t.Fatalf("agent did not start advertising the source feature: %+v, %v", current.Features, err)
	}

	// A complete heartbeat with an omitted/empty feature list must clear the
	// stale capability; it must not inherit a previous session's value.
	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Version: "legacy"}); err != nil {
		t.Fatalf("legacy empty-feature heartbeat: %v", err)
	}
	current, err = dataStore.GetAgent(ctx, agent.ID)
	if err != nil || len(current.Features) != 0 {
		t.Fatalf("agent features after empty-feature heartbeat = %+v, %v; want empty", current.Features, err)
	}

	// Re-advertising a non-empty feature set and then a metrics-only refresh
	// must preserve those features.
	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{
		Version:  "v2",
		Features: []string{core.AgentFeatureSelfUpgrade, core.AgentFeaturePortTraffic},
	}); err != nil {
		t.Fatalf("re-advertise feature heartbeat: %v", err)
	}
	if err := dataStore.UpdateAgentMetrics(ctx, agent.ID, core.HostMetrics{CPUAvailable: true, CPUPercent: 10}); err != nil {
		t.Fatalf("metrics-only refresh: %v", err)
	}
	current, err = dataStore.GetAgent(ctx, agent.ID)
	if err != nil || !containsFeature(current.Features, core.AgentFeatureSelfUpgrade) || containsFeature(current.Features, core.AgentFeatureMihomoDevelopmentSource) {
		t.Fatalf("metrics-only refresh clobbered features: %+v, %v", current.Features, err)
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
		Features:     []string{core.AgentFeatureSelfUpgrade},
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
		Features:     []string{core.AgentFeatureSelfUpgrade, core.AgentFeatureMihomoDevelopmentSource},
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
