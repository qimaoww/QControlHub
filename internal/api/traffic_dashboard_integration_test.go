package api

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTrafficUsageDashboardAPIAndLegacyCompatibilityWithPostgreSQL(t *testing.T) {
	db, ctx, admin, _, _ := newConfigScopeAPIFixture(t)
	agent := enrollConfigScopeAPIAgent(t, ctx, db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	policy, err := db.CreatePortTrafficPolicy(ctx, core.PortTrafficPolicyRequest{
		AgentID: agent.ID, Name: "dashboard API", Engine: core.EngineMihomo, Port: 21443,
		Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly, CycleAnchor: periodStart, LimitBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{{
		PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration, ReceivedBytes: 100, SentBytes: 50, UsedBytes: 150,
		PeriodStart: periodStart, PeriodEnd: periodStart.AddDate(0, 1, 0), EnforcementAvailable: true,
	}}, now); err != nil {
		t.Fatal(err)
	}

	var dashboard core.PortTrafficDashboard
	body := admin.call("GET", "/traffic-usage?view=dashboard&month="+now.Format("2006-01"), nil, http.StatusOK, &dashboard)
	if dashboard.Month != now.Format("2006-01") || dashboard.CurrentDay != now.Format(time.DateOnly) || dashboard.Timezone != "UTC" || dashboard.Scope != "monitored_ports" {
		t.Fatalf("dashboard envelope = %+v", dashboard)
	}
	if len(dashboard.Days) != time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day() || len(dashboard.Nodes) != 1 {
		t.Fatalf("dashboard sizes = %d days, %+v nodes", len(dashboard.Days), dashboard.Nodes)
	}
	day := dashboard.Days[now.Day()-1]
	if day.TotalBytes != 150 || day.RecordCount != 1 || day.SampleCount != 1 || day.LastReportedAt == nil {
		t.Fatalf("dashboard current day = %+v", day)
	}
	if dashboard.Nodes[0].AgentID != agent.ID || dashboard.Nodes[0].Name != agent.Name || dashboard.Nodes[0].TotalBytes != 150 {
		t.Fatalf("dashboard node = %+v", dashboard.Nodes[0])
	}
	if dashboard.CurrentPeriod.Today.TotalBytes != 150 || dashboard.CurrentPeriod.Week.TotalBytes != 150 || dashboard.CurrentPeriod.Month.TotalBytes != 150 {
		t.Fatalf("dashboard current periods = %+v", dashboard.CurrentPeriod)
	}
	for _, fragment := range [][]byte{[]byte(`"record_count":1`), []byte(`"sample_count":1`), []byte(`"total_bytes":150`)} {
		if !bytes.Contains(body, fragment) {
			t.Fatalf("numeric dashboard field %s missing from %s", fragment, body)
		}
	}

	var legacy struct {
		Month    string                       `json:"month"`
		Timezone string                       `json:"timezone"`
		Days     []core.PortTrafficDailyUsage `json:"days"`
	}
	admin.call("GET", "/traffic-usage?month="+now.Format("2006-01"), nil, http.StatusOK, &legacy)
	if legacy.Month != now.Format("2006-01") || legacy.Timezone != "UTC" || len(legacy.Days) != 1 || legacy.Days[0].UsedBytes != 150 {
		t.Fatalf("legacy response changed: %+v", legacy)
	}
}
