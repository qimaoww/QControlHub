package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestIPQualityScheduleWaitsForReconnectWithoutCatchUpBurst(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	qualityHeartbeat(t, db, ctx, agent.ID)
	if _, err := db.SetIPQualitySchedule(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET last_seen=now()-interval '10 minutes' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	request := core.TaskRequest{AgentID: agent.ID, Action: core.ActionIPQuality}
	if _, err := db.CreateTask(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("offline Agent accepted a manual check: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond).Add(7 * 24 * time.Hour)
	if err := db.QueueDueIPQualityChecks(ctx, now); err != nil {
		t.Fatal(err)
	}
	if tasks, err := db.ListTasks(ctx, agent.ID, 100); err != nil || len(tasks) != 0 {
		t.Fatalf("offline schedule queued work: %+v %v", tasks, err)
	}
	qualityHeartbeat(t, db, ctx, agent.ID)
	for range 3 {
		if err := db.QueueDueIPQualityChecks(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	tasks, err := db.ListTasks(ctx, agent.ID, 100)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("missed days created a catch-up burst: %+v %v", tasks, err)
	}
	schedules, err := db.ListIPQualitySchedules(ctx)
	if err != nil || len(schedules) != 1 || !schedules[0].NextRunAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("next run was not based on actual submission: %+v %v", schedules, err)
	}
	if _, err := db.SetIPQualitySchedule(ctx, agent.ID, false); err != nil {
		t.Fatal(err)
	}
	if task, err := db.GetTask(ctx, tasks[0].ID); err != nil || task.Status != core.TaskPending {
		t.Fatalf("disabling a schedule canceled an already submitted task: %+v %v", task, err)
	}
}

func TestIPQualityScheduleSkipsUnavailableCandidatesBeforeBatchLimit(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	qualityHeartbeat(t, db, ctx, agent.ID)
	if _, err := db.SetIPQualitySchedule(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	// More than a full batch of due, downgraded or recently offline nodes must
	// not prevent the healthy node from being considered on every scan.
	if _, err := db.pool.Exec(ctx, `INSERT INTO agents
		(id,name,os,arch,capabilities,features,public_key,last_seen,enrolled_at)
		SELECT 'agt_quality_unavailable_'||n,'unavailable','linux','amd64','[]',
			CASE WHEN n<=100 THEN '[]'::jsonb ELSE '["ip-quality-v2"]'::jsonb END,
			decode(lpad(to_hex(n),64,'0'),'hex'),
			CASE WHEN n<=100 THEN now() ELSE now()-interval '90 seconds' END,now()
		FROM generate_series(1,200) n;
		INSERT INTO ip_quality_schedules(agent_id,owner_id,enabled,next_run_at)
		SELECT id,'',true,now()-interval '2 days' FROM agents WHERE id LIKE 'agt_quality_unavailable_%'`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueueDueIPQualityChecks(ctx, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	tasks, err := db.ListTasks(ctx, "", 500)
	if err != nil || len(tasks) != 1 || tasks[0].AgentID != agent.ID {
		t.Fatalf("unavailable nodes starved a healthy schedule: %+v %v", tasks, err)
	}
}

func TestIPQualityUsesNodeOwnersOnlineThreshold(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, ownerCtx := sharedTestUser(t, db, ctx, "quality-online-owner")
	agent, _ := enrollTaskTestAgent(t, ownerCtx, db)
	qualityHeartbeat(t, db, ctx, agent.ID)
	settings, err := db.PanelSettings(ownerCtx)
	if err != nil {
		t.Fatal(err)
	}
	settings.AgentOfflineThresholdSeconds = 180
	if _, err := db.SavePanelSettings(ownerCtx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET last_seen=now()-interval '90 seconds' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	admin := WithConfigScope(ctx, "", true)
	task, err := db.CreateTask(admin, core.TaskRequest{AgentID: agent.ID, Action: core.ActionIPQuality})
	if err != nil {
		t.Fatalf("administrator's threshold overrode node policy: %v", err)
	}
	if err := db.CancelTask(admin, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetIPQualitySchedule(admin, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := db.QueueDueIPQualityChecks(ctx, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	tasks, err := db.ListTasks(admin, agent.ID, 100)
	if err != nil || len(tasks) != 2 || tasks[0].Status != core.TaskPending {
		t.Fatalf("scheduled check ignored the node's online threshold: %+v %v", tasks, err)
	}
}

func TestIPQualitySchedulePurgeAndAdministratorDemotion(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	admin := WithConfigScope(ctx, "", true)
	owner, ownerCtx := sharedTestUser(t, db, ctx, "quality-purge-owner")
	agent, _ := enrollTaskTestAgent(t, ownerCtx, db)
	qualityHeartbeat(t, db, ctx, agent.ID)
	if _, err := db.SetIPQualitySchedule(WithConfigScope(ctx, "token_legacy", false), agent.ID, true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("compatibility identity created a durable schedule: %v", err)
	}
	if _, err := db.SetIPQualitySchedule(ownerCtx, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PurgeUser(admin, owner.ID); err != nil {
		t.Fatal(err)
	}
	if schedules, err := db.ListIPQualitySchedules(admin); err != nil || len(schedules) != 0 {
		t.Fatalf("purge left an automatic check policy: %+v %v", schedules, err)
	}
	manager, err := db.CreateUser(ctx, core.UserRequest{Username: "quality-manager", Role: core.RoleAdmin}, "test-only-hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetIPQualitySchedule(WithConfigScope(ctx, manager.ID, true), agent.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE panel_users SET role='user',permissions='["agents.read","agents.manage","tasks.execute"]' WHERE id=$1`, manager.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueueDueIPQualityChecks(ctx, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	schedules, err := db.ListIPQualitySchedules(admin)
	if err != nil || len(schedules) != 1 || schedules[0].Enabled {
		t.Fatalf("scheduler reused the former administrator role: %+v %v", schedules, err)
	}
	if tasks, err := db.ListTasks(admin, agent.ID, 100); err != nil || len(tasks) != 0 {
		t.Fatalf("former administrator scheduled another owner's node: %+v %v", tasks, err)
	}
}

func TestIPQualityOwnerCanSeeAndDisableAdministratorSchedule(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "quality-plan-owner")
	recipient, shared := sharedTestUser(t, db, ctx, "quality-plan-shared")
	_, unrelated := sharedTestUser(t, db, ctx, "quality-plan-unrelated")
	agent, _ := enrollTaskTestAgent(t, owner, db)
	qualityHeartbeat(t, db, ctx, agent.ID)
	sharedTestAllocation(t, db, ctx, recipient.ID, agent.ID, 0, 25000)
	admin := WithConfigScope(ctx, "", true)
	if _, err := db.SetIPQualitySchedule(admin, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	if schedules, err := db.ListIPQualitySchedules(owner); err != nil ||
		len(schedules) != 1 || !schedules[0].Enabled || schedules[0].AgentID != agent.ID {
		t.Fatalf("owner cannot see administrator's host-wide plan: %+v %v", schedules, err)
	}
	for name, scoped := range map[string]context.Context{"share": shared, "unrelated": unrelated} {
		if schedules, err := db.ListIPQualitySchedules(scoped); err != nil || len(schedules) != 0 {
			t.Fatalf("%s can see another host's plan: %+v %v", name, schedules, err)
		}
	}
	if err := db.QueueDueIPQualityChecks(ctx, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	tasks, err := db.ListTasks(admin, agent.ID, 100)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("administrator's plan did not run: %+v %v", tasks, err)
	}
	day := tasks[0].CreatedAt.Format(time.DateOnly)
	if records, err := db.ListIPQualityRecords(owner, day, "UTC"); err != nil || len(records) != 0 {
		t.Fatalf("plan visibility exposed the administrator's private task: %+v %v", records, err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET admin_hidden=true WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	if schedules, err := db.ListIPQualitySchedules(admin); err != nil || len(schedules) != 0 {
		t.Fatalf("administrator saw a hidden node's plan: %+v %v", schedules, err)
	}
	if schedules, err := db.ListIPQualitySchedules(owner); err != nil || len(schedules) != 1 || !schedules[0].Enabled {
		t.Fatalf("hiding a node hid its plan from its owner: %+v %v", schedules, err)
	}
	if schedule, err := db.SetIPQualitySchedule(owner, agent.ID, false); err != nil || schedule.Enabled {
		t.Fatalf("owner cannot disable administrator's plan: %+v %v", schedule, err)
	}
	if err := db.QueueDueIPQualityChecks(ctx, time.Now().Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if tasks, err := db.ListTasks(ctx, agent.ID, 100); err != nil || len(tasks) != 1 {
		t.Fatalf("disabled plan queued more work: %+v %v", tasks, err)
	}
}
