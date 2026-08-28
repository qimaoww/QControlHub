package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestPortTrafficPolicyLifecycleWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)

	request := core.PortTrafficPolicyRequest{
		AgentID: agent.ID, Name: "edge 443", Engine: core.EngineMihomo, Port: 443,
		Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly,
		CycleAnchor: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), LimitBytes: 100 << 30,
	}
	created, err := dataStore.CreatePortTrafficPolicy(ctx, request)
	if err != nil || created.ResetGeneration != 1 || created.EnforcementAvailable || !created.AutoBlock || !created.MonitoringEnabled {
		t.Fatalf("created policy = %+v, %v", created, err)
	}
	if _, err := dataStore.CreatePortTrafficPolicy(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate port error = %v", err)
	}

	periodStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	usage := core.PortTrafficUsage{
		PolicyID: created.ID, ResetGeneration: created.ResetGeneration,
		ReceivedBytes: 3 << 30, SentBytes: 2 << 30, UsedBytes: 5 << 30,
		ReceiveBPS: 1200, SendBPS: 800, PeriodStart: periodStart, PeriodEnd: periodStart.AddDate(0, 1, 0),
		EnforcementAvailable: true,
	}
	reportedAt := time.Now().UTC().Truncate(time.Second)
	if err := dataStore.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{usage}, reportedAt); err != nil {
		t.Fatal(err)
	}
	usage.ReceivedBytes = 4 << 30
	usage.SentBytes = 3 << 30
	usage.UsedBytes = 7 << 30
	usage.ReceiveBPS = 2400
	usage.SendBPS = 1600
	if err := dataStore.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{usage}, reportedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	staleUsage := usage
	staleUsage.ReceivedBytes = 8 << 30
	staleUsage.SentBytes = 7 << 30
	staleUsage.UsedBytes = 15 << 30
	if err := dataStore.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{staleUsage}, reportedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	policies, err := dataStore.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].UsedBytes != 7<<30 || !policies[0].EnforcementAvailable {
		t.Fatalf("reported policies = %+v, %v", policies, err)
	}
	daily, err := dataStore.ListPortTrafficDailyUsage(ctx, agent.ID, created.ID, reportedAt)
	wantAverageRate := uint64(math.Round(float64(1<<30) / time.Minute.Seconds()))
	if err != nil || len(daily) != 1 || daily[0].UsedBytes != 7<<30 || daily[0].ReceivedBytes != 4<<30 || daily[0].SentBytes != 3<<30 || daily[0].PeakReceiveBPS != wantAverageRate || daily[0].PeakSendBPS != wantAverageRate || daily[0].SampleCount != 2 {
		t.Fatalf("daily usage = %+v, %v", daily, err)
	}

	request.LimitBytes = 4 << 30
	autoBlock := false
	request.AutoBlock = &autoBlock
	updated, err := dataStore.UpdatePortTrafficPolicy(ctx, created.ID, request)
	if err != nil || updated.ResetGeneration != 1 || updated.UsedBytes != 7<<30 || updated.AutoBlock {
		t.Fatalf("limit-only update = %+v, %v", updated, err)
	}
	request.Port = 8443
	updated, err = dataStore.UpdatePortTrafficPolicy(ctx, created.ID, request)
	if err != nil || updated.ResetGeneration != 2 || updated.UsedBytes != 0 {
		t.Fatalf("port update = %+v, %v", updated, err)
	}
	if err := dataStore.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{usage}, time.Now()); err != nil {
		t.Fatal(err)
	}
	updatedPolicies, err := dataStore.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || updatedPolicies[0].UsedBytes != 0 {
		t.Fatalf("stale generation was accepted: %+v, %v", updatedPolicies, err)
	}
	reset, err := dataStore.ResetPortTrafficPolicy(ctx, created.ID)
	if err != nil || reset.ResetGeneration != 3 {
		t.Fatalf("reset policy = %+v, %v", reset, err)
	}
	agentID, err := dataStore.DeletePortTrafficPolicy(ctx, created.ID)
	if err != nil || agentID != agent.ID {
		t.Fatalf("delete policy agent = %q, %v", agentID, err)
	}
	daily, err = dataStore.ListPortTrafficDailyUsage(ctx, agent.ID, created.ID, reportedAt)
	if err != nil || len(daily) != 1 || daily[0].Port != 443 || daily[0].UsedBytes != 7<<30 {
		t.Fatalf("deleted policy history was not preserved: %+v, %v", daily, err)
	}
}

func TestDeletePortTrafficMonitoringHidesConfiguredPortUntilReenabledWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	request := core.PortTrafficPolicyRequest{
		AgentID: agent.ID, Name: "deletable 443", Engine: core.EngineMihomo, Port: 443,
		Protocol: core.TrafficProtocolTCP, Cycle: core.TrafficCycleMonthly,
		CycleAnchor: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), LimitBytes: 10 << 30,
	}
	created, err := dataStore.CreatePortTrafficPolicy(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.DeletePortTrafficMonitoring(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	active, err := dataStore.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(active) != 0 {
		t.Fatalf("deleted monitor was still sent to Agent: %+v, %v", active, err)
	}
	all, err := dataStore.ListPortTrafficPolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundHidden := false
	for _, policy := range all {
		if policy.ID == created.ID {
			foundHidden = !policy.MonitoringEnabled
		}
	}
	if !foundHidden {
		t.Fatal("deleted monitor did not retain a suppression marker")
	}
	reenabled, err := dataStore.CreatePortTrafficPolicy(ctx, request)
	if err != nil || reenabled.ID != created.ID || !reenabled.MonitoringEnabled {
		t.Fatalf("reenabled monitor = %+v, %v", reenabled, err)
	}
}

func TestPortTrafficTotalsSurviveAgentCounterRestartWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)

	policy, err := dataStore.CreatePortTrafficPolicy(ctx, core.PortTrafficPolicyRequest{
		AgentID: agent.ID, Name: "restart-safe totals", Engine: core.EngineMihomo, Port: 443,
		Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly,
		CycleAnchor: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), LimitBytes: 100 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	periodStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	reportedAt := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	report := func(received, sent uint64, at time.Time) {
		t.Helper()
		if err := dataStore.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{{
			PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration,
			ReceivedBytes: received, SentBytes: sent, UsedBytes: received + sent,
			PeriodStart: periodStart, PeriodEnd: periodStart.AddDate(0, 1, 0), EnforcementAvailable: true,
		}}, at); err != nil {
			t.Fatalf("report traffic usage: %v", err)
		}
	}
	report(1<<30, 2<<30, reportedAt)
	report(2<<30, 3<<30, reportedAt.Add(15*time.Second))
	// Simulate a lost Agent state file or rebuilt local counter. The new raw
	// counters are smaller, but the control-plane totals must keep increasing.
	report(256<<10, 512<<10, reportedAt.Add(30*time.Second))

	policies, err := dataStore.AgentPortTrafficPolicies(ctx, agent.ID)
	wantReceived, wantSent := uint64(2<<30)+uint64(256<<10), uint64(3<<30)+uint64(512<<10)
	if err != nil || len(policies) != 1 || policies[0].ReceivedBytes != wantReceived || policies[0].SentBytes != wantSent || policies[0].UsedBytes != wantReceived+wantSent {
		t.Fatalf("restart-safe totals = %+v, %v; want received=%d sent=%d", policies, err, wantReceived, wantSent)
	}
	if policies[0].ReceiveBPS != uint64(math.Round(float64(256<<10)/15)) ||
		policies[0].SendBPS != uint64(math.Round(float64(512<<10)/15)) {
		t.Fatalf("restart interval rates = %d/%d", policies[0].ReceiveBPS, policies[0].SendBPS)
	}
	var reportedReceived, reportedSent uint64
	if err := dataStore.pool.QueryRow(ctx, `
		SELECT reported_received_bytes,reported_sent_bytes FROM port_traffic_policies WHERE id=$1`, policy.ID,
	).Scan(&reportedReceived, &reportedSent); err != nil || reportedReceived != 256<<10 || reportedSent != 512<<10 {
		t.Fatalf("raw Agent baseline = %d/%d, %v", reportedReceived, reportedSent, err)
	}
}

func TestDiscoveredPortIsMonitoredWithoutQuota(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)

	changed, err := dataStore.ReconcilePortTrafficEndpoints(ctx, []core.PortTrafficEndpoint{{
		AgentID: agent.ID, Name: "auto 8443", Engine: core.EngineMihomo,
		Port: 8443, Protocol: core.TrafficProtocolBoth,
	}})
	if err != nil || len(changed) != 1 || changed[0] != agent.ID {
		t.Fatalf("reconcile changed agents = %v, %v", changed, err)
	}
	policies, err := dataStore.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].QuotaEnabled || !policies[0].Discovered || policies[0].AutoBlock || policies[0].LimitBytes != math.MaxInt64 {
		t.Fatalf("automatic monitor = %+v, %v", policies, err)
	}
	policy := policies[0]
	periodStart := time.Date(time.Now().UTC().Year(), time.Now().UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	reportedAt := time.Now().UTC().Truncate(time.Second)
	usage := core.PortTrafficUsage{
		PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration,
		ReceivedBytes: 1024, SentBytes: 2048, UsedBytes: 3072,
		PeriodStart: periodStart, PeriodEnd: periodStart.AddDate(0, 1, 0), EnforcementAvailable: true,
	}
	if err := dataStore.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{usage}, reportedAt); err != nil {
		t.Fatal(err)
	}
	usage.ReceivedBytes, usage.SentBytes, usage.UsedBytes = 4096, 8192, 12288
	if err := dataStore.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{usage}, reportedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	policies, err = dataStore.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || policies[0].UsedBytes != 12288 || policies[0].QuotaEnabled {
		t.Fatalf("monitor-only usage = %+v, %v", policies, err)
	}
	request := core.PortTrafficPolicyRequest{
		AgentID: agent.ID, Name: policy.Name, Engine: policy.Engine, Port: policy.Port,
		Protocol: policy.Protocol, Cycle: policy.Cycle, CycleAnchor: policy.CycleAnchor,
		LimitBytes: 1 << 30,
	}
	updated, err := dataStore.CreatePortTrafficPolicy(ctx, request)
	if err != nil || updated.ID != policy.ID || !updated.QuotaEnabled || updated.UsedBytes != 12288 {
		t.Fatalf("enable quota = %+v, %v", updated, err)
	}
	if _, err := dataStore.DeletePortTrafficPolicy(ctx, policy.ID); err != nil {
		t.Fatal(err)
	}
	policies, err = dataStore.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].QuotaEnabled || !policies[0].Discovered || policies[0].UsedBytes != 12288 {
		t.Fatalf("quota removal stopped monitoring: %+v, %v", policies, err)
	}
	changed, err = dataStore.ReconcilePortTrafficEndpoints(ctx, nil)
	if err != nil || len(changed) != 1 || changed[0] != agent.ID {
		t.Fatalf("remove stale monitor = %v, %v", changed, err)
	}
	policies, err = dataStore.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 0 {
		t.Fatalf("stale automatic monitor remains: %+v, %v", policies, err)
	}
}

func TestDiscoveredPortsRespectAgentPolicyLimit(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	if _, err := dataStore.CreatePortTrafficPolicy(ctx, core.PortTrafficPolicyRequest{
		AgentID: agent.ID, Name: "manual", Engine: core.EngineMihomo, Port: 1,
		Protocol: core.TrafficProtocolTCP, Cycle: core.TrafficCycleMonthly,
		CycleAnchor: core.UTCDate(time.Now().UTC()), LimitBytes: 1 << 30,
	}); err != nil {
		t.Fatal(err)
	}
	endpoints := make([]core.PortTrafficEndpoint, 0, 256)
	for port := 2; port <= 257; port++ {
		endpoints = append(endpoints, core.PortTrafficEndpoint{
			AgentID: agent.ID, Name: fmt.Sprintf("auto %d", port), Engine: core.EngineMihomo,
			Port: port, Protocol: core.TrafficProtocolTCP,
		})
	}
	if _, err := dataStore.ReconcilePortTrafficEndpoints(ctx, endpoints); !errors.Is(err, ErrConflict) {
		t.Fatalf("257 final monitored ports error = %v, want conflict", err)
	}
	policies, err := dataStore.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].Port != 1 {
		t.Fatalf("failed reconciliation changed policies: %+v, %v", policies, err)
	}
}

func TestTrafficHistoryUpgradeBaseline(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)
	policy, err := dataStore.CreatePortTrafficPolicy(ctx, core.PortTrafficPolicyRequest{
		AgentID: agent.ID, Name: "upgrade baseline", Engine: core.EngineMihomo, Port: 8080,
		Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly,
		CycleAnchor: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), LimitBytes: 100 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	reportedAt := time.Now().UTC().Truncate(time.Second)
	if _, err := dataStore.pool.Exec(ctx, `
		UPDATE port_traffic_policies SET received_bytes=$2,sent_bytes=$3,used_bytes=$4,
			reported_received_bytes=$2,reported_sent_bytes=$3,
			period_start='2026-08-01',period_end='2026-09-01',
			last_reported_at=$5,traffic_history_initialized=false
		WHERE id=$1`, policy.ID, 6<<30, 3<<30, 9<<30, reportedAt); err != nil {
		t.Fatal(err)
	}
	usage := core.PortTrafficUsage{
		PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration,
		ReceivedBytes: 7 << 30, SentBytes: 3 << 30, UsedBytes: 10 << 30,
		PeriodStart: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), PeriodEnd: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		EnforcementAvailable: true,
	}
	if err := dataStore.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{usage}, reportedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	usage.ReceivedBytes = 8 << 30
	usage.SentBytes = 4 << 30
	usage.UsedBytes = 12 << 30
	if err := dataStore.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{usage}, reportedAt.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	daily, err := dataStore.ListPortTrafficDailyUsage(ctx, agent.ID, policy.ID, reportedAt)
	if err != nil || len(daily) != 1 || daily[0].ReceivedBytes != 2<<30 || daily[0].SentBytes != 1<<30 || daily[0].UsedBytes != 3<<30 || daily[0].SampleCount != 2 {
		t.Fatalf("upgrade baseline daily usage = %+v, %v", daily, err)
	}
	policies, err := dataStore.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].ReceivedBytes != 8<<30 || policies[0].SentBytes != 4<<30 || policies[0].UsedBytes != 12<<30 {
		t.Fatalf("upgrade baseline cumulative totals = %+v, %v", policies, err)
	}
}

func TestTrafficCounterDelta(t *testing.T) {
	for _, test := range []struct {
		name              string
		current, previous uint64
		want              uint64
	}{
		{name: "increment", current: 12, previous: 7, want: 5},
		{name: "unchanged", current: 7, previous: 7, want: 0},
		{name: "counter restart", current: 3, previous: 9, want: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := trafficCounterDelta(test.current, test.previous); got != test.want {
				t.Fatalf("trafficCounterDelta(%d,%d) = %d, want %d", test.current, test.previous, got, test.want)
			}
		})
	}
}

func TestTrafficAverageRate(t *testing.T) {
	base := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		delta   uint64
		current time.Time
		want    uint64
	}{
		{name: "regular report", delta: 1500, current: base.Add(15 * time.Second), want: 100},
		{name: "round sub-byte rate", delta: 8, current: base.Add(15 * time.Second), want: 1},
		{name: "no traffic", current: base.Add(15 * time.Second), want: 0},
		{name: "stale reconnect", delta: 1 << 30, current: base.Add(3 * time.Minute), want: 0},
		{name: "non increasing time", delta: 100, current: base, want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := trafficAverageRate(test.delta, base, test.current); got != test.want {
				t.Fatalf("trafficAverageRate() = %d; want %d", got, test.want)
			}
		})
	}
}
