package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
