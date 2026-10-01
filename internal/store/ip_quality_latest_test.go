package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestIPQualityLatestAcrossDatesAndAttempts(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	owner := WithConfigScope(ctx, "token_quality_latest", false)
	other := WithConfigScope(ctx, "token_quality_other", false)
	first, _ := enrollTaskTestAgent(t, owner, db)
	second, _ := enrollTaskTestAgent(t, owner, db)
	qualityHeartbeat(t, db, ctx, first.ID)
	qualityHeartbeat(t, db, ctx, second.ID)
	complete := func(principal context.Context, agentID string, daysAgo int) core.Task {
		t.Helper()
		task, err := db.CreateTask(principal, core.TaskRequest{AgentID: agentID, Action: core.ActionIPQuality})
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := db.ClaimTask(ctx, agentID)
		if err != nil || claimed == nil || claimed.ID != task.ID {
			t.Fatalf("claim = %+v %v", claimed, err)
		}
		result := storeQualityResult(t)
		result.ReportsText = []string{"archived printed report"}
		if err := db.CompleteTask(ctx, agentID, task.ID, core.TaskResultRequest{
			LeaseID: claimed.LeaseID, Success: true, IPQuality: result,
		}, storeQualityArchives(t, result)...); err != nil {
			t.Fatal(err)
		}
		stamp := time.Now().UTC().AddDate(0, 0, -daysAgo).Truncate(time.Microsecond)
		if _, err := db.pool.Exec(ctx, `UPDATE tasks SET created_at=$2,started_at=$2,finished_at=$2 WHERE id=$1`, task.ID, stamp); err != nil {
			t.Fatal(err)
		}
		return task
	}
	complete(owner, first.ID, 8)
	latestFirst := complete(owner, first.ID, 7)
	latestSecond := complete(owner, second.ID, 3)
	read := func(principal context.Context) map[string]core.IPQualityRecord {
		t.Helper()
		records, err := db.ListIPQualityRecords(principal, "", "Asia/Shanghai")
		if err != nil {
			t.Fatal(err)
		}
		byAgent := make(map[string]core.IPQualityRecord)
		for _, record := range records {
			if _, exists := byAgent[record.AgentID]; exists {
				t.Fatal("latest returned multiple checks for one node")
			}
			byAgent[record.AgentID] = record
		}
		return byAgent
	}
	latest := read(owner)
	if len(latest) != 2 || latest[first.ID].TaskID != latestFirst.ID || latest[second.ID].TaskID != latestSecond.ID {
		t.Fatalf("per-node latest across different dates = %+v", latest)
	}
	if dates, err := db.ListIPQualityDates(owner, "UTC"); err != nil || !slices.Equal(dates, []string{
		latest[second.ID].CreatedAt.Format(time.DateOnly), latest[first.ID].CreatedAt.Format(time.DateOnly),
		latest[first.ID].CreatedAt.AddDate(0, 0, -1).Format(time.DateOnly),
	}) {
		t.Fatalf("dates omitted earlier attempts or were not newest first: %+v %v", dates, err)
	}
	if latest[first.ID].Result == nil || len(latest[first.ID].Result.ReportsText) != 0 || latest[first.ID].LastSuccessful != nil {
		t.Fatalf("successful latest duplicated or lost report data: %+v", latest[first.ID])
	}
	if records, err := db.ListIPQualityRecords(owner, time.Now().UTC().Format(time.DateOnly), "UTC"); err != nil || len(records) != 0 {
		t.Fatalf("date filter included older results: %+v %v", records, err)
	}
	// A more recent result from a different account must not become the owner's
	// fallback report, even though compatibility tokens can manage this node.
	otherResult := complete(other, first.ID, 6)
	pending, err := db.CreateTask(owner, core.TaskRequest{AgentID: first.ID, Action: core.ActionIPQuality})
	if err != nil {
		t.Fatal(err)
	}
	assertRetained := func(status core.TaskStatus) {
		t.Helper()
		record := read(owner)[first.ID]
		previous := record.LastSuccessful
		if record.TaskID != pending.ID || record.Status != status || record.Result != nil || previous == nil ||
			previous.TaskID != latestFirst.ID || previous.Result == nil || len(previous.Result.ReportsText) != 0 ||
			len(previous.Archives) != 1 || previous.FinishedAt == nil || previous.LastSuccessful != nil {
			t.Fatalf("%s did not retain the correct scoped report: %+v", status, record)
		}
	}
	assertRetained(core.TaskPending)
	if dates, err := db.ListIPQualityDates(other, "UTC"); err != nil || len(dates) != 1 || dates[0] != time.Now().UTC().AddDate(0, 0, -6).Format(time.DateOnly) {
		t.Fatalf("date metadata crossed account scope: %+v %v", dates, err)
	}
	if records := read(other); len(records) != 1 || records[first.ID].TaskID != otherResult.ID || records[first.ID].LastSuccessful != nil {
		t.Fatalf("account scope leaked another attempt: %+v", records)
	}
	admin := read(WithConfigScope(ctx, "", true))[first.ID]
	if admin.LastSuccessful == nil || admin.LastSuccessful.TaskID != otherResult.ID {
		t.Fatalf("admin fallback did not follow authorized records: %+v", admin)
	}
	claimed, err := db.ClaimTask(ctx, first.ID)
	if err != nil || claimed == nil {
		t.Fatalf("claim = %+v %v", claimed, err)
	}
	assertRetained(core.TaskRunning)
	if err := db.CompleteTask(ctx, first.ID, pending.ID, core.TaskResultRequest{
		LeaseID: claimed.LeaseID, Success: false, Error: "network failure",
	}); err != nil {
		t.Fatal(err)
	}
	assertRetained(core.TaskFailed)
	if records, err := db.ListIPQualityRecords(owner, pending.CreatedAt.UTC().Format(time.DateOnly), "UTC"); err != nil ||
		len(records) != 1 || records[0].LastSuccessful != nil || records[0].Result != nil {
		t.Fatalf("historical day inherited a report from another day: %+v %v", records, err)
	}
	pending, err = db.CreateTask(owner, core.TaskRequest{AgentID: first.ID, Action: core.ActionIPQuality})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CancelTask(owner, pending.ID); err != nil {
		t.Fatal(err)
	}
	assertRetained(core.TaskCanceled)
	newResult := complete(owner, first.ID, 0)
	if record := read(owner)[first.ID]; record.TaskID != newResult.ID || record.LastSuccessful != nil || record.Result == nil {
		t.Fatalf("new success did not replace the retained report: %+v", record)
	}
	if dates, err := db.ListIPQualityDates(owner, "UTC"); err != nil || len(dates) != 4 {
		t.Fatalf("multiple attempts duplicated a calendar date: %+v %v", dates, err)
	}
	for _, input := range []struct{ date, timezone string }{
		{"2026-02-30", "UTC"}, {"", "Invalid/Zone"}, {"", "Local"},
	} {
		if _, err := db.ListIPQualityRecords(owner, input.date, input.timezone); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid latest/history filter accepted: %+v %v", input, err)
		}
		if input.date == "" {
			if _, err := db.ListIPQualityDates(owner, input.timezone); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid calendar timezone accepted: %+v %v", input, err)
			}
		}
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET revoked_at=now() WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if dates, err := db.ListIPQualityDates(owner, "UTC"); err != nil || len(dates) != 1 || dates[0] != latest[second.ID].CreatedAt.Format(time.DateOnly) {
		t.Fatalf("revoked node left selectable dates: %+v %v", dates, err)
	}
}
