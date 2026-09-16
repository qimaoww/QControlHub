package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func storeQualityResult(t *testing.T) *core.IPQualityResult {
	t.Helper()
	result, err := core.ParseIPQualityReports([]byte(`{"Head":{"IP":"203.0.113.1","Version":"fixture"},"Info":{},"Type":{},"Score":{"IPQS":"null"},"Factor":{},"Media":{},"Mail":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	return &result
}

func qualityHeartbeat(t *testing.T, db *Store, ctx context.Context, agentID string) {
	t.Helper()
	if err := db.Heartbeat(ctx, agentID, core.HeartbeatRequest{Features: []string{
		core.AgentFeatureIPQuality, core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureIndependentEgress,
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestIPQualityTaskLifecycleAndHistory(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	input := core.TaskRequest{AgentID: agent.ID, Action: core.ActionIPQuality}
	if _, err := db.CreateTask(ctx, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("old Agent accepted quality task: %v", err)
	}
	qualityHeartbeat(t, db, ctx, agent.ID)
	task, err := db.CreateTask(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 6 {
		group.Add(1)
		go func() {
			defer group.Done()
			reused, err := db.CreateTask(ctx, input)
			if err != nil || !reused.Reused || reused.ID != task.ID {
				t.Errorf("duplicate task: %+v %v", reused, err)
			}
		}()
	}
	group.Wait()
	if _, err := db.pool.Exec(ctx, `UPDATE tasks SET created_at='2026-09-15T16:30:00Z' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.Action != core.ActionIPQuality || claimed.Engine != "" {
		t.Fatalf("claim = %+v %v", claimed, err)
	}
	result := core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, IPQuality: storeQualityResult(t)}
	stale := result
	stale.LeaseID = strings.Repeat("x", 32)
	if err := db.CompleteTask(ctx, agent.ID, task.ID, stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("accepted old lease: %v", err)
	}
	if err := db.CompleteTask(ctx, agent.ID, task.ID, result); err != nil {
		t.Fatal(err)
	}
	records, err := db.ListIPQualityRecords(ctx, "2026-09-16", "Asia/Shanghai")
	if err != nil || len(records) != 1 || records[0].Result == nil || records[0].Status != core.TaskSucceeded {
		t.Fatalf("history = %+v %v", records, err)
	}
	if records, err := db.ListIPQualityRecords(ctx, "2026-09-15", "Asia/Shanghai"); err != nil || len(records) != 0 {
		t.Fatalf("wrong local day: %+v %v", records, err)
	}
	if records, err := db.ListIPQualityRecords(ctx, "2026-09-15", "UTC"); err != nil || len(records) != 1 {
		t.Fatalf("UTC day: %+v %v", records, err)
	}
	stored, err := db.GetTask(ctx, task.ID)
	if err != nil || stored.Output != "IPQuality report saved" || strings.Contains(stored.Output, "203.0.113.1") {
		t.Fatalf("ordinary task poll exposed report bytes: %+v %v", stored, err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE tasks SET finished_at=now()-interval '365 days' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PruneTasks(ctx, time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM ip_quality_reports`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retention left reports: %d %v", count, err)
	}
}

func TestIPQualityInvalidResultsAndFeatureDowngrade(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	qualityHeartbeat(t, db, ctx, agent.ID)
	input := core.TaskRequest{AgentID: agent.ID, Action: core.ActionIPQuality}
	task, err := db.CreateTask(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{}); err != nil {
		t.Fatal(err)
	}
	if claimed, err := db.ClaimTask(ctx, agent.ID); err != nil || claimed != nil {
		t.Fatalf("legacy Agent claimed quality task: %+v %v", claimed, err)
	}
	qualityHeartbeat(t, db, ctx, agent.ID)
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil {
		t.Fatalf("claim = %+v %v", claimed, err)
	}
	setTaskStartedAt(t, ctx, db, task.ID, time.Now().Add(-3*time.Minute))
	if err := db.RequeueStaleTasks(ctx, 2*time.Minute, 6*time.Minute, 3); err != nil {
		t.Fatal(err)
	}
	if stored, _ := db.GetTask(ctx, task.ID); stored.Status != core.TaskRunning {
		t.Fatal("ordinary two-minute lease interrupted quality check")
	}
	if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, Output: "not a report"}); err != nil {
		t.Fatal(err)
	}
	if stored, _ := db.GetTask(ctx, task.ID); stored.Status != core.TaskFailed {
		t.Fatal("Agent success flag bypassed report validation")
	}
	retried, err := db.RetryTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClaimTask(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{}); err != nil {
		t.Fatal(err)
	}
	if running, err := db.RunningTask(ctx, agent.ID); err != nil || running != nil {
		t.Fatalf("resumed after feature downgrade: %+v %v", running, err)
	}
	if stored, _ := db.GetTask(ctx, retried.ID); stored.Status != core.TaskFailed || !strings.Contains(stored.Error, core.AgentFeatureIPQuality) {
		t.Fatalf("downgrade result = %+v", stored)
	}
}

func TestIPQualityUnstorableReportsSettleWithoutBlockingTasks(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	qualityHeartbeat(t, db, ctx, agent.ID)
	for _, test := range []struct{ name, info string }{
		{"nul-value", `{"Organization":"provider\u0000value"}`},
		{"nul-key", `{"nested":[{"\u0000":"value"}]}`},
		{"unpaired-surrogate", `{"Organization":"\ud800"}`},
		{"numeric-overflow", `{"provider":1e200000}`},
		{"expanded-size", `{"provider":[1e100000,1e100000,1e100000]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := &core.IPQualityResult{Reports: []json.RawMessage{json.RawMessage(
				`{"Head":{"IP":"203.0.113.1"},"Info":` + test.info + `,"Type":{},"Score":{},"Factor":{},"Media":{},"Mail":{}}`)}}
			// All are syntactically valid, bounded JSON. Persistence owns the
			// additional JSONB text, numeric and stored-size constraints.
			if _, err := core.NormalizeIPQualityResult(report); err != nil {
				t.Fatal(err)
			}
			task, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Action: core.ActionIPQuality})
			if err != nil {
				t.Fatal(err)
			}
			lease, err := db.ClaimTask(ctx, agent.ID)
			if err != nil || lease == nil || lease.ID != task.ID {
				t.Fatalf("claim: %+v %v", lease, err)
			}
			if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{
				LeaseID: lease.LeaseID, Success: true, IPQuality: report,
			}); err != nil {
				t.Fatalf("unsavable result prevented acknowledgement: %v", err)
			}
			stored, err := db.GetTask(ctx, task.ID)
			if err != nil || stored.Status != core.TaskFailed || stored.Output != "" ||
				!strings.Contains(stored.Error, "invalid IPQuality report") {
				t.Fatalf("unsavable report was not failed: %+v %v", stored, err)
			}
			if running, err := db.RunningTask(ctx, agent.ID); err != nil || running != nil {
				t.Fatalf("unsavable report retained a running lease: %+v %v", running, err)
			}
		})
	}
	var count int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM ip_quality_reports`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid reports were retained: %d %v", count, err)
	}
	task, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Action: core.ActionIPQuality})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil || lease.ID != task.ID {
		t.Fatalf("valid follow-up claim: %+v %v", lease, err)
	}
	report := storeQualityResult(t)
	if err := db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{
		LeaseID: lease.LeaseID, Success: true, IPQuality: report,
	}); err != nil {
		t.Fatal(err)
	}
	if stored, err := db.GetTask(ctx, task.ID); err != nil || stored.Status != core.TaskSucceeded {
		t.Fatalf("invalid reports blocked a valid follow-up: %+v %v", stored, err)
	}
}

func TestIPQualityStorageFailureRemainsRetryable(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	qualityHeartbeat(t, db, ctx, agent.ID)
	task, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Action: core.ActionIPQuality})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil {
		t.Fatalf("claim: %+v %v", lease, err)
	}
	// This schema belongs only to this test. Simulate a persistence failure,
	// not bad report data, and verify that the same lease can retry it.
	if _, err := db.pool.Exec(ctx, `ALTER TABLE ip_quality_reports RENAME TO ip_quality_reports_unavailable`); err != nil {
		t.Fatal(err)
	}
	result := core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, IPQuality: storeQualityResult(t)}
	if err := db.CompleteTask(ctx, agent.ID, task.ID, result); err == nil {
		t.Fatal("storage failure was swallowed as a completed task")
	}
	if stored, err := db.GetTask(ctx, task.ID); err != nil || stored.Status != core.TaskRunning {
		t.Fatalf("storage failure ended the retryable lease: %+v %v", stored, err)
	}
	if _, err := db.pool.Exec(ctx, `ALTER TABLE ip_quality_reports_unavailable RENAME TO ip_quality_reports`); err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteTask(ctx, agent.ID, task.ID, result); err != nil {
		t.Fatalf("same lease could not retry after recovery: %v", err)
	}
	if stored, err := db.GetTask(ctx, task.ID); err != nil || stored.Status != core.TaskSucceeded {
		t.Fatalf("recovered storage did not complete the task: %+v %v", stored, err)
	}
}

func TestIPQualityScheduleIsolationAndRevocation(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	alice, aliceCtx := sharedTestUser(t, db, ctx, "quality-owner")
	bob, bobCtx := sharedTestUser(t, db, ctx, "quality-recipient")
	agent, _ := enrollTaskTestAgent(t, aliceCtx, db)
	qualityHeartbeat(t, db, ctx, agent.ID)
	sharedTestAllocation(t, db, ctx, bob.ID, agent.ID, 0, 25000)
	if _, err := db.CreateTask(bobCtx, core.TaskRequest{AgentID: agent.ID, Action: core.ActionIPQuality}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("share recipient could run a host check: %v", err)
	}
	if _, err := db.SetIPQualitySchedule(bobCtx, agent.ID, true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("share recipient could schedule checks: %v", err)
	}
	if err := db.QueueDueIPQualityChecks(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if tasks, _ := db.ListTasks(ctx, agent.ID, 100); len(tasks) != 0 {
		t.Fatal("daily checks were not opt-in")
	}
	schedule, err := db.SetIPQualitySchedule(aliceCtx, agent.ID, true)
	if err != nil || !schedule.Enabled {
		t.Fatalf("enable: %+v %v", schedule, err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond).Add(time.Second)
	var group sync.WaitGroup
	for range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := db.QueueDueIPQualityChecks(ctx, now); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	tasks, err := db.ListTasks(aliceCtx, agent.ID, 100)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("daily scheduler duplicated work: %+v %v", tasks, err)
	}
	next, err := db.SetIPQualitySchedule(aliceCtx, agent.ID, true)
	if err != nil || !next.NextRunAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("idempotent enable reset due time: %+v %v", next, err)
	}
	day := tasks[0].CreatedAt.Format(time.DateOnly)
	if records, err := db.ListIPQualityRecords(bobCtx, day, "UTC"); err != nil || len(records) != 0 {
		t.Fatalf("share leaked host report: %+v %v", records, err)
	}
	if schedules, err := db.ListIPQualitySchedules(bobCtx); err != nil || len(schedules) != 0 {
		t.Fatalf("share leaked scheduling policy: %+v %v", schedules, err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET admin_hidden=true WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	if records, err := db.ListIPQualityRecords(WithConfigScope(ctx, "", true), day, "UTC"); err != nil || len(records) != 0 {
		t.Fatalf("admin discovered hidden report: %+v %v", records, err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE panel_users SET permissions='["agents.read","tasks.execute"]' WHERE id=$1`, alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueueDueIPQualityChecks(ctx, now.Add(25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	schedules, err := db.ListIPQualitySchedules(aliceCtx)
	if err != nil || len(schedules) != 1 || schedules[0].Enabled {
		t.Fatalf("revoked grants kept scheduling work: %+v %v", schedules, err)
	}
	if claimed, err := db.ClaimTask(ctx, agent.ID); err != nil || claimed != nil {
		t.Fatalf("revoked task was dispatched: %+v %v", claimed, err)
	}
}
