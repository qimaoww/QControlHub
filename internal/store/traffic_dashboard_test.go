package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

type dashboardTrafficRow struct {
	policyID                       string
	agentID                        string
	generation                     int
	day                            time.Time
	received, sent, used           uint64
	peakReceive, peakSend, samples uint64
	reportedAt                     time.Time
}

func insertDashboardTrafficRow(t *testing.T, db *Store, ctx context.Context, row dashboardTrafficRow) {
	t.Helper()
	if row.reportedAt.IsZero() {
		row.reportedAt = row.day.Add(2 * time.Hour)
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO port_traffic_daily_usage
		(policy_id,reset_generation,usage_date,agent_id,name,engine,port,protocol,received_bytes,sent_bytes,used_bytes,
		 peak_receive_bps,peak_send_bps,sample_count,first_reported_at,last_reported_at)
		VALUES ($1,$2,$3,$4,'dashboard fixture','mihomo',21001,'both',$5,$6,$7,$8,$9,$10,$11,$11)`,
		row.policyID, row.generation, row.day, row.agentID, row.received, row.sent, row.used,
		row.peakReceive, row.peakSend, row.samples, row.reportedAt); err != nil {
		t.Fatal(err)
	}
}

func TestDashboardPortTrafficUsageCalendarAndAggregationWithPostgreSQL(t *testing.T) {
	db := openPerformanceStore(t)
	ctx := context.Background()
	alphaID, alpha := seedPerformanceAgent(t, db, 2)
	betaID, beta := seedPerformanceAgent(t, db, 1)
	emptyID, _ := seedPerformanceAgent(t, db, 0)
	for id, name := range map[string]string{alphaID: "alpha current name", betaID: "beta current name", emptyID: "no records"} {
		if _, err := db.pool.Exec(ctx, `UPDATE agents SET name=$2 WHERE id=$1`, id, name); err != nil {
			t.Fatal(err)
		}
	}
	day := func(value string) time.Time {
		parsed, err := time.Parse(time.DateOnly, value)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	rows := []dashboardTrafficRow{
		{policyID: alpha[0].PolicyID, agentID: alphaID, generation: 1, day: day("2026-09-15"), received: 10, sent: 20, used: 30, peakReceive: 100, peakSend: 50, samples: 2, reportedAt: day("2026-09-15").Add(time.Hour)},
		{policyID: alpha[0].PolicyID, agentID: alphaID, generation: 2, day: day("2026-09-15"), received: 5, sent: 7, used: 12, peakReceive: 200, peakSend: 80, samples: 3, reportedAt: day("2026-09-15").Add(2 * time.Hour)},
		// A sampled zero-traffic day differs from a day without any daily row.
		{policyID: alpha[1].PolicyID, agentID: alphaID, generation: 1, day: day("2026-09-16"), samples: 1},
		{policyID: beta[0].PolicyID, agentID: betaID, generation: 1, day: day("2026-09-15"), received: 1, sent: 2, used: 3, peakReceive: 40, peakSend: 20, samples: 1, reportedAt: day("2026-09-15").Add(90 * time.Minute)},
		// 2026-11-01 is Sunday: its natural week begins in October.
		{policyID: alpha[0].PolicyID, agentID: alphaID, generation: 1, day: day("2026-10-25"), received: 999, sent: 999, used: 1998, samples: 1},
		{policyID: alpha[0].PolicyID, agentID: alphaID, generation: 1, day: day("2026-10-26"), received: 100, sent: 20, used: 120, samples: 1},
		{policyID: beta[0].PolicyID, agentID: betaID, generation: 1, day: day("2026-10-30"), received: 50, sent: 50, used: 100, samples: 4},
		{policyID: alpha[1].PolicyID, agentID: alphaID, generation: 1, day: day("2026-10-31"), received: 30, sent: 10, used: 40, samples: 2},
		{policyID: alpha[0].PolicyID, agentID: alphaID, generation: 1, day: day("2026-11-01"), received: 7, sent: 3, used: 10, samples: 1, reportedAt: day("2026-11-01").Add(3 * time.Hour)},
		{policyID: beta[0].PolicyID, agentID: betaID, generation: 1, day: day("2026-11-01"), samples: 1, reportedAt: day("2026-11-01").Add(2 * time.Hour)},
	}
	for _, row := range rows {
		insertDashboardTrafficRow(t, db, ctx, row)
	}

	now := day("2026-11-01").Add(3*time.Hour + 47*time.Minute)
	got, err := db.DashboardPortTrafficUsage(ctx, "", "", day("2026-09-01"), now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Month != "2026-09" || got.CurrentDay != "2026-11-01" || !got.GeneratedAt.Equal(now) || got.Timezone != "UTC" || got.Scope != "monitored_ports" {
		t.Fatalf("dashboard envelope = %+v", got)
	}
	if len(got.Days) != 30 || got.Days[0].Day != "2026-09-01" || got.Days[29].Day != "2026-09-30" {
		t.Fatalf("calendar days = %d, first/last = %+v/%+v", len(got.Days), got.Days[0], got.Days[29])
	}
	if empty := got.Days[0]; empty.RecordCount != 0 || empty.SampleCount != 0 || empty.TotalBytes != 0 || empty.LastReportedAt != nil {
		t.Fatalf("day without records = %+v", empty)
	}
	aggregated := got.Days[14]
	if aggregated.ReceivedBytes != 16 || aggregated.SentBytes != 29 || aggregated.TotalBytes != 45 ||
		aggregated.PeakReceiveBPS != 200 || aggregated.PeakSendBPS != 80 || aggregated.RecordCount != 3 || aggregated.SampleCount != 6 ||
		aggregated.LastReportedAt == nil || !aggregated.LastReportedAt.Equal(day("2026-09-15").Add(2*time.Hour)) {
		t.Fatalf("cross-generation/day aggregation = %+v", aggregated)
	}
	zeroSample := got.Days[15]
	if zeroSample.RecordCount != 1 || zeroSample.SampleCount != 1 || zeroSample.TotalBytes != 0 || zeroSample.LastReportedAt == nil {
		t.Fatalf("sampled zero day = %+v", zeroSample)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("nodes = %+v", got.Nodes)
	}
	alphaTotal := got.Nodes[0]
	if alphaTotal.AgentID != alphaID || alphaTotal.Name != "alpha current name" || alphaTotal.ReceivedBytes != 15 || alphaTotal.SentBytes != 27 ||
		alphaTotal.TotalBytes != 42 || alphaTotal.RecordCount != 3 || alphaTotal.SampleCount != 6 || alphaTotal.LastReportedAt == nil ||
		!alphaTotal.LastReportedAt.Equal(day("2026-09-16").Add(2*time.Hour)) {
		t.Fatalf("alpha node aggregation = %+v", alphaTotal)
	}
	if got.Nodes[1].AgentID != betaID || got.Nodes[1].TotalBytes != 3 || got.Nodes[1].RecordCount != 1 {
		t.Fatalf("beta node aggregation = %+v", got.Nodes[1])
	}
	for _, row := range got.Nodes {
		if row.AgentID == emptyID {
			t.Fatalf("node without records was invented: %+v", row)
		}
	}

	today := got.CurrentPeriod.Today
	if today.Start != "2026-11-01" || today.ReceivedBytes != 7 || today.SentBytes != 3 || today.TotalBytes != 10 ||
		today.RecordCount != 2 || today.SampleCount != 2 || today.LastReportedAt == nil || !today.LastReportedAt.Equal(day("2026-11-01").Add(3*time.Hour)) {
		t.Fatalf("today = %+v", today)
	}
	week := got.CurrentPeriod.Week
	if week.Start != "2026-10-26" || week.ReceivedBytes != 187 || week.SentBytes != 83 || week.TotalBytes != 270 ||
		week.RecordCount != 5 || week.SampleCount != 9 || week.LastReportedAt == nil || !week.LastReportedAt.Equal(day("2026-11-01").Add(3*time.Hour)) {
		t.Fatalf("cross-month week = %+v", week)
	}
	month := got.CurrentPeriod.Month
	if month.Start != "2026-11-01" || month.ReceivedBytes != 7 || month.SentBytes != 3 || month.TotalBytes != 10 || month.RecordCount != 2 || month.SampleCount != 2 {
		t.Fatalf("current month independent of selection = %+v", month)
	}

	scoped, err := db.DashboardPortTrafficUsage(ctx, alphaID, "", day("2026-09-01"), now)
	if err != nil || len(scoped.Nodes) != 1 || scoped.Nodes[0].AgentID != alphaID || scoped.CurrentPeriod.Today.RecordCount != 1 || scoped.CurrentPeriod.Today.TotalBytes != 10 {
		t.Fatalf("agent filter = %+v, %v", scoped, err)
	}
	policyScoped, err := db.DashboardPortTrafficUsage(ctx, "", alpha[1].PolicyID, day("2026-09-01"), now)
	if err != nil || len(policyScoped.Nodes) != 1 || policyScoped.Nodes[0].TotalBytes != 0 || policyScoped.Nodes[0].RecordCount != 1 ||
		policyScoped.CurrentPeriod.Today.RecordCount != 0 || policyScoped.CurrentPeriod.Week.TotalBytes != 40 {
		t.Fatalf("policy filter = %+v, %v", policyScoped, err)
	}

	empty, err := db.DashboardPortTrafficUsage(ctx, "agt_missing", "", day("2024-02-01"), now)
	if err != nil || len(empty.Days) != 29 || len(empty.Nodes) != 0 {
		t.Fatalf("empty leap month = %+v, %v", empty, err)
	}
	for _, item := range empty.Days {
		if item.RecordCount != 0 || item.SampleCount != 0 || item.LastReportedAt != nil {
			t.Fatalf("empty month contains a record: %+v", item)
		}
	}
	if empty.CurrentPeriod.Today.Start != "2026-11-01" || empty.CurrentPeriod.Week.Start != "2026-10-26" || empty.CurrentPeriod.Month.Start != "2026-11-01" ||
		empty.CurrentPeriod.Today.RecordCount != 0 || empty.CurrentPeriod.Week.RecordCount != 0 || empty.CurrentPeriod.Month.RecordCount != 0 {
		t.Fatalf("empty current periods = %+v", empty.CurrentPeriod)
	}
}

func TestDashboardPortTrafficUsageQueryBudgetWithPostgreSQL(t *testing.T) {
	base := openPerformanceStore(t)
	ctx := context.Background()
	agentID, usages := seedPerformanceAgent(t, base, 256)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := base.UpdatePortTrafficUsage(ctx, agentID, usages, now); err != nil {
		t.Fatal(err)
	}
	trace := &queryBudget{}
	config := base.pool.Config()
	config.MaxConns, config.MinConns = 1, 0
	config.ConnConfig.Tracer = trace
	config.ShouldPing = func(context.Context, pgxpool.ShouldPingParams) bool { return false }
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	measured := &Store{pool: pool, cryptor: base.cryptor}
	got, err := measured.DashboardPortTrafficUsage(ctx, "", "", now, now)
	if err != nil {
		t.Fatal(err)
	}
	if trace.queries.Load() != 2 || trace.batches.Load() != 0 || trace.statements.Load() != 0 {
		t.Fatalf("dashboard queries/batches/statements = %d/%d/%d, want 2/0/0", trace.queries.Load(), trace.batches.Load(), trace.statements.Load())
	}
	if len(got.Nodes) != 1 || got.Days[now.Day()-1].RecordCount != 256 || got.Days[now.Day()-1].SampleCount != 256 ||
		got.Days[now.Day()-1].TotalBytes != 25600 || got.CurrentPeriod.Today.RecordCount != 256 {
		t.Fatalf("256-policy dashboard = day %+v, nodes %+v, today %+v", got.Days[now.Day()-1], got.Nodes, got.CurrentPeriod.Today)
	}
}

func TestDashboardPortTrafficUsageAuthorizationWithPostgreSQL(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, actor := sharedTestUser(t, db, ctx, "dashboard-share-user")
	other, otherActor := sharedTestUser(t, db, ctx, "dashboard-other-user")
	agent := sharedTestAgent(t, db, ctx)
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 0, 21001)
	sharedTestAllocation(t, db, ctx, other.ID, agent.ID, 0, 21002)
	for _, entry := range []struct {
		actor context.Context
		ports []int
	}{{actor, []int{21001}}, {otherActor, []int{21002}}} {
		config := sharedTestConfig(t, db, entry.actor, agent.ID, entry.ports...)
		if _, err := db.CreateTask(entry.actor, core.TaskRequest{AgentID: agent.ID, ConfigID: config.ID, Engine: config.Engine, Action: core.ActionDeploy}); err != nil {
			t.Fatal(err)
		}
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 2 {
		t.Fatalf("shared policies = %+v, %v", policies, err)
	}
	visible := policies[0]
	// Model reallocation: generation 1 predates the current share generation 2.
	if _, err := db.pool.Exec(ctx, `UPDATE port_traffic_policies SET reset_generation=2,share_generation=2 WHERE id=$1`, visible.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 10, 4, 0, 0, 0, time.UTC)
	day := core.UTCDate(now)
	insertDashboardTrafficRow(t, db, ctx, dashboardTrafficRow{policyID: visible.ID, agentID: agent.ID, generation: 1, day: day, received: 600, sent: 400, used: 1000, samples: 1})
	insertDashboardTrafficRow(t, db, ctx, dashboardTrafficRow{policyID: visible.ID, agentID: agent.ID, generation: 2, day: day, received: 6, sent: 4, used: 10, samples: 2})
	insertDashboardTrafficRow(t, db, ctx, dashboardTrafficRow{policyID: policies[1].ID, agentID: agent.ID, generation: 1, day: day, received: 60, sent: 40, used: 100, samples: 1})

	got, err := db.DashboardPortTrafficUsage(actor, "", "", day, now)
	if err != nil || len(got.Nodes) != 1 || got.Nodes[0].AgentID != agent.ID || got.Nodes[0].TotalBytes != 10 ||
		got.CurrentPeriod.Today.TotalBytes != 10 || got.CurrentPeriod.Today.RecordCount != 1 || got.CurrentPeriod.Today.SampleCount != 2 {
		t.Fatalf("recipient dashboard leaked prior/other shares: %+v, %v", got, err)
	}
	legacy, err := db.ListPortTrafficDailyUsage(actor, "", "", day)
	if err != nil || len(legacy) != 1 || legacy[0].UsedBytes != 10 {
		t.Fatalf("detail/dashboard authorization diverged: %+v, %v", legacy, err)
	}

	admin := WithConfigScope(ctx, "token_dashboard_admin", true)
	adminView, err := db.DashboardPortTrafficUsage(admin, "", "", day, now)
	if err != nil || adminView.CurrentPeriod.Today.TotalBytes != 1110 || adminView.CurrentPeriod.Today.RecordCount != 3 {
		t.Fatalf("admin dashboard = %+v, %v", adminView.CurrentPeriod.Today, err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET admin_hidden=true WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	hidden, err := db.DashboardPortTrafficUsage(admin, "", "", day, now)
	if err != nil || len(hidden.Nodes) != 0 || hidden.CurrentPeriod.Today.RecordCount != 0 {
		t.Fatalf("owner-hidden node leaked to administrator: %+v, %v", hidden, err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET admin_hidden=false WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET enabled=false WHERE id=$1`, visible.ShareID); err != nil {
		t.Fatal(err)
	}
	revoked, err := db.DashboardPortTrafficUsage(actor, "", "", day, now)
	if err != nil || len(revoked.Nodes) != 0 || revoked.CurrentPeriod.Today.RecordCount != 0 ||
		revoked.CurrentPeriod.Today.Start != "2026-11-10" || revoked.CurrentPeriod.Week.Start != "2026-11-09" || revoked.CurrentPeriod.Month.Start != "2026-11-01" ||
		revoked.CurrentPeriod.Today.LastReportedAt != nil || revoked.CurrentPeriod.Week.LastReportedAt != nil || revoked.CurrentPeriod.Month.LastReportedAt != nil {
		t.Fatalf("disabled share remained visible or lost empty periods: %+v, %v", revoked, err)
	}
}
