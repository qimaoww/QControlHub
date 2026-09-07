package store

import (
	"context"
	"math"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestTrafficSampleIdentityAndLifetimeWithPostgreSQL(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	agent, enrollment := enrollTaskTestAgent(t, ctx, s)
	t.Cleanup(func() { cleanupTaskTestAgent(s, agent.ID, enrollment) })
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	policy, err := s.CreatePortTrafficPolicy(ctx, core.PortTrafficPolicyRequest{
		AgentID: agent.ID, Name: "sample identity", Engine: core.EngineMihomo, Port: 443,
		Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly, CycleAnchor: start, LimitBytes: math.MaxInt64,
	})
	if err != nil {
		t.Fatal(err)
	}
	usage := core.PortTrafficUsage{
		PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration, CounterEpoch: "0123456789abcdef0123456789abcdef",
		CollectedAt: start.AddDate(0, 0, 30).Add(23*time.Hour + 59*time.Minute + 40*time.Second),
		PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), EnforcementAvailable: true,
		ReceivedBytes: 100, SentBytes: 200, UsedBytes: 300, LifetimeReceivedBytes: 100, LifetimeSentBytes: 200,
	}
	arrival := usage.CollectedAt.Add(10 * time.Second)
	report := func(value core.PortTrafficUsage, at time.Time) {
		t.Helper()
		if err := s.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{value}, at); err != nil {
			t.Fatal(err)
		}
	}
	read := func() core.PortTrafficPolicy {
		t.Helper()
		values, err := s.AgentPortTrafficPolicies(ctx, agent.ID)
		if err != nil || len(values) != 1 {
			t.Fatalf("policies = %+v, %v", values, err)
		}
		return values[0]
	}
	report(usage, arrival)
	first := usage
	usage.CollectedAt = usage.CollectedAt.Add(5 * time.Second)
	usage.ReceivedBytes, usage.SentBytes, usage.UsedBytes = 200, 400, 600
	usage.LifetimeReceivedBytes, usage.LifetimeSentBytes = 200, 400
	// The network batches reports only 100ms apart; the sample spans 5s.
	report(usage, arrival.Add(100*time.Millisecond))
	if got := read(); got.ReceiveBPS != 20 || got.SendBPS != 40 || got.UsedBytes != 600 || got.LastCollectedAt == nil || !got.LastCollectedAt.Equal(usage.CollectedAt) {
		t.Fatalf("sample-time rate/SQL timestamp = %+v", got)
	}
	// Repeated heartbeat, delayed old sample and rollback with newer clock.
	report(usage, arrival.Add(time.Second))
	report(first, arrival.Add(2*time.Second))
	rollback := first
	rollback.CollectedAt = usage.CollectedAt.Add(time.Second)
	report(rollback, arrival.Add(3*time.Second))
	if got := read(); got.UsedBytes != 600 || got.ReceiveBPS != 20 {
		t.Fatalf("replay changed totals/rate: %+v", got)
	}
	daily, err := s.ListPortTrafficDailyUsage(ctx, agent.ID, policy.ID, start)
	if err != nil || len(daily) != 1 || daily[0].SampleCount != 2 || daily[0].UsedBytes != 600 {
		t.Fatalf("duplicate daily usage: %+v, %v", daily, err)
	}
	// A failed sample changes health only; recovery computes from last success.
	unavailable := usage
	unavailable.EnforcementAvailable, unavailable.EnforcementError = false, "nft unavailable"
	report(unavailable, arrival.Add(4*time.Second))
	if got := read(); got.EnforcementAvailable || got.ReceiveBPS != 0 || got.UsedBytes != 600 {
		t.Fatalf("failed sample: %+v", got)
	}
	// Rollover must retain the tail not reported before the period boundary.
	usage.CollectedAt = start.AddDate(0, 1, 0).Add(time.Second)
	usage.PeriodStart, usage.PeriodEnd = start.AddDate(0, 1, 0), start.AddDate(0, 2, 0)
	usage.ReceivedBytes, usage.SentBytes, usage.UsedBytes = 10, 20, 30
	usage.LifetimeReceivedBytes, usage.LifetimeSentBytes = 260, 520
	report(usage, usage.CollectedAt.Add(time.Second))
	if got := read(); got.UsedBytes != 30 || got.ReceiveBPS != 4 || got.SendBPS != 8 {
		t.Fatalf("new period/live rate: %+v", got)
	}
	daily, err = s.ListPortTrafficDailyUsage(ctx, agent.ID, policy.ID, usage.PeriodStart)
	if err != nil || len(daily) != 1 || daily[0].UsedBytes != 180 {
		t.Fatalf("unreported period tail lost: %+v, %v", daily, err)
	}
	// A real local state loss has a different epoch. Its first total can
	// already be larger than the previous total (not detectable by < alone).
	usage.CounterEpoch = "fedcba9876543210fedcba9876543210"
	usage.CollectedAt = usage.CollectedAt.Add(time.Second)
	usage.ReceivedBytes, usage.SentBytes, usage.UsedBytes = 1000, 2000, 3000
	usage.LifetimeReceivedBytes, usage.LifetimeSentBytes = 1000, 2000
	report(usage, usage.CollectedAt.Add(time.Second))
	if got := read(); got.UsedBytes != 3030 {
		t.Fatalf("epoch restart was undercounted: %+v", got)
	}
	var epoch string
	var lifetimeRX, lifetimeTX uint64
	if err := s.pool.QueryRow(ctx, `SELECT counter_epoch,reported_lifetime_received_bytes,reported_lifetime_sent_bytes FROM port_traffic_policies WHERE id=$1`, policy.ID).Scan(&epoch, &lifetimeRX, &lifetimeTX); err != nil || epoch != usage.CounterEpoch || lifetimeRX != 1000 || lifetimeTX != 2000 {
		t.Fatalf("durable epoch/lifetime = %s %d/%d, %v", epoch, lifetimeRX, lifetimeTX, err)
	}
	reset, err := s.ResetPortTrafficPolicy(ctx, policy.ID)
	if err != nil || reset.LastCollectedAt != nil || reset.UsedBytes != 0 {
		t.Fatalf("reset sample state: %+v, %v", reset, err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT counter_epoch,reported_lifetime_received_bytes FROM port_traffic_policies WHERE id=$1`, policy.ID).Scan(&epoch, &lifetimeRX); err != nil || epoch != "" || lifetimeRX != 0 {
		t.Fatalf("reset retained old baseline: %s %d, %v", epoch, lifetimeRX, err)
	}
}

func TestTrafficAccuracyMigrationPreservesVersion42Data(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema, err := testdb.IsolatePostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(context.Background())
	s, err := Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	agent, _ := enrollTaskTestAgent(t, ctx, s)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	policy, err := s.CreatePortTrafficPolicy(ctx, core.PortTrafficPolicyRequest{AgentID: agent.ID,
		Name: "v42 upgrade", Engine: core.EngineMihomo, Port: 443, Protocol: core.TrafficProtocolTCP,
		Cycle: core.TrafficCycleMonthly, CycleAnchor: start, LimitBytes: math.MaxInt64})
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	usage := core.PortTrafficUsage{PolicyID: policy.ID, ResetGeneration: 1, ReceivedBytes: 100, UsedBytes: 100,
		PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), EnforcementAvailable: true}
	if err := s.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{usage}, start.Add(time.Second)); err != nil {
		s.Close()
		t.Fatal(err)
	}
	_, err = s.pool.Exec(ctx, `
		ALTER TABLE port_traffic_policies DROP COLUMN last_collected_at;
		ALTER TABLE port_traffic_policies DROP COLUMN counter_epoch;
		ALTER TABLE port_traffic_policies DROP COLUMN reported_lifetime_received_bytes;
		ALTER TABLE port_traffic_policies DROP COLUMN reported_lifetime_sent_bytes;
		DELETE FROM qcontrolhub_schema_migrations;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(42);`)
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	policies, err := s.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].UsedBytes != 100 || policies[0].LastCollectedAt != nil {
		t.Fatalf("migration lost legacy policy: %+v, %v", policies, err)
	}
	usage.CounterEpoch = "0123456789abcdef0123456789abcdef"
	usage.CollectedAt = start.Add(2 * time.Second)
	usage.ReceivedBytes, usage.UsedBytes, usage.LifetimeReceivedBytes = 150, 150, 150
	if err := s.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{usage}, start.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	daily, err := s.ListPortTrafficDailyUsage(ctx, agent.ID, policy.ID, start)
	if err != nil || len(daily) != 1 || daily[0].UsedBytes != 150 {
		t.Fatalf("first modern report re-billed legacy usage: %+v, %v", daily, err)
	}
	// Reapplying the idempotent migration must retain modern baselines too.
	if _, err := s.pool.Exec(ctx, `DELETE FROM qcontrolhub_schema_migrations WHERE version=$1`, currentSchemaVersion); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var epoch string
	var lifetime uint64
	if err := s.pool.QueryRow(ctx, `SELECT counter_epoch,reported_lifetime_received_bytes FROM port_traffic_policies WHERE id=$1`, policy.ID).Scan(&epoch, &lifetime); err != nil || epoch != usage.CounterEpoch || lifetime != 150 {
		t.Fatalf("migration rewrote new baselines: %s %d, %v", epoch, lifetime, err)
	}
}

func TestTrafficCollectionDayAndValidationWithPostgreSQL(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	agent, enrollment := enrollTaskTestAgent(t, ctx, s)
	t.Cleanup(func() { cleanupTaskTestAgent(s, agent.ID, enrollment) })
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	policy, err := s.CreatePortTrafficPolicy(ctx, core.PortTrafficPolicyRequest{AgentID: agent.ID,
		Name: "collection day", Engine: core.EngineMihomo, Port: 8443, Protocol: core.TrafficProtocolTCP,
		Cycle: core.TrafficCycleMonthly, CycleAnchor: start, LimitBytes: math.MaxInt64})
	if err != nil {
		t.Fatal(err)
	}
	usage := core.PortTrafficUsage{PolicyID: policy.ID, ResetGeneration: 1,
		CounterEpoch: "0123456789abcdef0123456789abcdef", CollectedAt: start.Add(24*time.Hour - time.Second),
		PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), EnforcementAvailable: true,
		ReceivedBytes: 5, UsedBytes: 5, LifetimeReceivedBytes: 5}
	arrival := usage.CollectedAt.Add(5 * time.Second)
	if err := s.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{usage}, arrival); err != nil {
		t.Fatal(err)
	}
	daily, err := s.ListPortTrafficDailyUsage(ctx, agent.ID, policy.ID, start)
	if err != nil || len(daily) != 1 || daily[0].Day != "2026-08-01" {
		t.Fatalf("arrival date replaced collection date: %+v, %v", daily, err)
	}
	for _, edit := range []func(*core.PortTrafficUsage){
		func(u *core.PortTrafficUsage) { u.CounterEpoch = "bad" },
		func(u *core.PortTrafficUsage) { u.CollectedAt = arrival.Add(time.Hour) },
		func(u *core.PortTrafficUsage) { u.CollectedAt = time.Time{} },
		func(u *core.PortTrafficUsage) { u.LifetimeReceivedBytes = 1 },
		func(u *core.PortTrafficUsage) {
			u.PeriodEnd = u.PeriodEnd.Add(time.Hour)
			u.CollectedAt = u.CollectedAt.Add(time.Second)
		},
	} {
		invalid := usage
		edit(&invalid)
		if err := s.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{invalid}, arrival.Add(time.Second)); err == nil {
			t.Fatalf("accepted invalid sample: %+v", invalid)
		}
	}
	// Both directions and the total use the same saturating int64 contract.
	usage.CollectedAt = usage.CollectedAt.Add(time.Second)
	usage.ReceivedBytes, usage.SentBytes, usage.UsedBytes = math.MaxInt64, math.MaxInt64, math.MaxInt64
	usage.LifetimeReceivedBytes, usage.LifetimeSentBytes = math.MaxInt64, math.MaxInt64
	if err := s.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{usage}, arrival.Add(time.Second)); err != nil {
		t.Fatalf("saturated traffic rejected: %v", err)
	}
}
