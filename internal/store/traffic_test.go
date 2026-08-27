package store

import (
	"context"
	"errors"
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
	if err != nil || created.ResetGeneration != 1 || created.EnforcementAvailable || !created.AutoBlock {
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
	if err != nil || len(daily) != 1 || daily[0].UsedBytes != 7<<30 || daily[0].ReceivedBytes != 4<<30 || daily[0].SentBytes != 3<<30 || daily[0].PeakReceiveBPS != 2400 || daily[0].SampleCount != 2 {
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
		UPDATE port_traffic_policies SET received_bytes=$2,sent_bytes=$3,used_bytes=$4,last_reported_at=$5,traffic_history_initialized=false
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
	if err != nil || len(daily) != 1 || daily[0].ReceivedBytes != 1<<30 || daily[0].SentBytes != 1<<30 || daily[0].UsedBytes != 2<<30 || daily[0].SampleCount != 2 {
		t.Fatalf("upgrade baseline daily usage = %+v, %v", daily, err)
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
