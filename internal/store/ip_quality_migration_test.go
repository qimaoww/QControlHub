package store

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestIPQualityMigratesVersion61AndPreservesData(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	task, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus})
	if err != nil {
		t.Fatal(err)
	}
	before, err := db.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Rebuild the previous shape only inside this test's isolated schema.
	if _, err := db.pool.Exec(ctx, `DROP TABLE ip_quality_reports;
		DROP TABLE ip_quality_schedules;
		DROP INDEX tasks_ip_quality_history_idx;
		ALTER TABLE tasks DROP CONSTRAINT tasks_action_check;
		ALTER TABLE tasks ADD CONSTRAINT tasks_action_check CHECK
			(action IN ('validate','deploy','import-existing','read-config','read-managed-config','start','stop','restart','status','install','upgrade-agent','enable-bbr','disable-bbr','configure-tcp'));
		ALTER TABLE tasks DROP CONSTRAINT tasks_engine_check;
		ALTER TABLE tasks ADD CONSTRAINT tasks_engine_check CHECK
			(engine IN ('mihomo','xray','sing-box','ss-rust') OR (action IN ('upgrade-agent','enable-bbr','disable-bbr','configure-tcp') AND engine=''));
		DELETE FROM qcontrolhub_schema_migrations WHERE version>=62;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(61) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version int
	var indexExists bool
	if err := db.pool.QueryRow(ctx, `SELECT max(version),to_regclass('tasks_ip_quality_history_idx') IS NOT NULL
		FROM qcontrolhub_schema_migrations`).Scan(&version, &indexExists); err != nil ||
		version != currentSchemaVersion || !indexExists {
		t.Fatalf("incomplete migration: version=%d index=%v error=%v", version, indexExists, err)
	}
	if after, err := db.GetTask(ctx, task.ID); err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("migration changed an existing task: %+v %v", after, err)
	}
	if schedules, err := db.ListIPQualitySchedules(ctx); err != nil || len(schedules) != 0 {
		t.Fatalf("migration enabled checks without consent: %+v %v", schedules, err)
	}
	if err := db.CancelTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	qualityHeartbeat(t, db, ctx, agent.ID)
	check, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Action: core.ActionIPQuality})
	if err != nil {
		t.Fatalf("upgraded constraints rejected IPQuality: %v", err)
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.ID != check.ID {
		t.Fatalf("claim = %+v %v", claimed, err)
	}
	report := storeQualityResult(t)
	if err := db.CompleteTask(ctx, agent.ID, check.ID, core.TaskResultRequest{
		LeaseID: claimed.LeaseID, Success: true, IPQuality: report,
	}, storeQualityArchives(t, report)...); err != nil {
		t.Fatal(err)
	}
	schedule, err := db.SetIPQualitySchedule(ctx, agent.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	// A repeated application must retain modern reports and the opt-in policy.
	if _, err := db.pool.Exec(ctx, `DELETE FROM qcontrolhub_schema_migrations WHERE version=$1`, currentSchemaVersion); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	history, err := db.ListIPQualityRecords(ctx, check.CreatedAt.Format(time.DateOnly), "UTC")
	if err != nil || len(history) != 1 || history[0].Result == nil || len(history[0].Result.Reports) != 1 {
		t.Fatalf("migration lost the report: %+v %v", history, err)
	}
	// jsonb may reorder keys; compare the decoded provider values.
	var saved, expected map[string]any
	if err := json.Unmarshal(history[0].Result.Reports[0], &saved); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(storeQualityResult(t).Reports[0], &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, expected) {
		t.Fatalf("migration changed provider values: %+v", saved)
	}
	schedules, err := db.ListIPQualitySchedules(ctx)
	if err != nil || len(schedules) != 1 || schedules[0].AgentID != schedule.AgentID ||
		!schedules[0].Enabled || !schedules[0].NextRunAt.Equal(schedule.NextRunAt) {
		t.Fatalf("migration changed the schedule: %+v %v", schedules, err)
	}
}
