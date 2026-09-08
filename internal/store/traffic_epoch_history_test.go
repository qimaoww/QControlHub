package store

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTrafficRestoredEpochAndRetainedHistoryWithPostgreSQL(t *testing.T) {
	for _, native := range []bool{false, true} {
		s := openPerformanceStore(t)
		ctx := context.Background()
		agent, enrollment := enrollTaskTestAgent(t, ctx, s)
		t.Cleanup(func() { cleanupTaskTestAgent(s, agent.ID, enrollment) })
		start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
		p, err := s.CreatePortTrafficPolicy(ctx, core.PortTrafficPolicyRequest{AgentID: agent.ID, Name: "epoch history", Engine: core.EngineMihomo, Port: 443, Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly, CycleAnchor: start, LimitBytes: math.MaxInt64})
		if err != nil {
			t.Fatal(err)
		}
		for i, v := range []struct {
			epoch string
			rx    uint64
		}{
			{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 100}, {"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 50},
			{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 120}, {"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 50},
			{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 110}, // regressed checkpoint must be ignored
		} {
			at := start.Add(time.Duration(i+1) * time.Second)
			u := core.PortTrafficUsage{PolicyID: p.ID, ResetGeneration: p.ResetGeneration, CounterEpoch: v.epoch, CollectedAt: at, PeriodStart: start, PeriodEnd: start.AddDate(0, 1, 0), EnforcementAvailable: true, ReceivedBytes: v.rx, UsedBytes: v.rx, LifetimeReceivedBytes: v.rx}
			if native {
				u.Accounting = &core.TrafficAccounting{Source: "core-api", Inbound: "a", ClientReceived: v.rx}
			}
			if err := s.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{u}, at.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
		}
		policies, err := s.AgentPortTrafficPolicies(ctx, agent.ID)
		if err != nil || len(policies) != 1 || policies[0].ReceivedBytes != 170 {
			t.Fatalf("epoch return rebilled history: %+v %v", policies, err)
		}
		daily, err := s.ListPortTrafficDailyUsage(ctx, agent.ID, p.ID, start)
		if err != nil || len(daily) != 1 || daily[0].UsedBytes != 170 {
			t.Fatalf("daily epoch dedup failed: %+v %v", daily, err)
		}
		if _, err := s.DeletePortTrafficPolicy(ctx, p.ID); err != nil {
			t.Fatal(err)
		}
		var epochs, scopedDays, days int
		if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM port_traffic_accounting_epochs WHERE policy_id=$1),(SELECT count(*) FROM port_traffic_daily_accounting WHERE policy_id=$1),(SELECT count(*) FROM port_traffic_daily_usage WHERE policy_id=$1)`, p.ID).Scan(&epochs, &scopedDays, &days); err != nil {
			t.Fatal(err)
		}
		if epochs != 2 || scopedDays != 1 || days != 1 {
			t.Fatalf("port removal lost history: %d/%d/%d", epochs, scopedDays, days)
		}
		if err := s.DeleteAgent(ctx, agent.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM port_traffic_accounting_epochs WHERE agent_id=$1)+(SELECT count(*) FROM port_traffic_daily_accounting WHERE agent_id=$1)`, agent.ID).Scan(&epochs); err != nil || epochs != 0 {
			t.Fatalf("node deletion left history: %d %v", epochs, err)
		}
	}
}

func TestTrafficHistoryVersion44MigrationWithPostgreSQL(t *testing.T) {
	s := openPerformanceStore(t)
	ctx := context.Background()
	agent, enrollment := enrollTaskTestAgent(t, ctx, s)
	t.Cleanup(func() { cleanupTaskTestAgent(s, agent.ID, enrollment) })
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	p, err := s.CreatePortTrafficPolicy(ctx, core.PortTrafficPolicyRequest{AgentID: agent.ID, Name: "v44", Engine: core.EngineMihomo, Port: 443, Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly, CycleAnchor: start, LimitBytes: math.MaxInt64})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.pool.Exec(ctx, `ALTER TABLE port_traffic_accounting_epochs DROP COLUMN agent_id;
		ALTER TABLE port_traffic_daily_accounting DROP COLUMN agent_id;
		ALTER TABLE port_traffic_accounting_epochs ADD FOREIGN KEY(policy_id) REFERENCES port_traffic_policies(id) ON DELETE CASCADE;
		ALTER TABLE port_traffic_daily_accounting ADD FOREIGN KEY(policy_id) REFERENCES port_traffic_policies(id) ON DELETE CASCADE;
		DELETE FROM qcontrolhub_schema_migrations WHERE version>44;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(44) ON CONFLICT DO NOTHING`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO port_traffic_accounting_epochs(policy_id,reset_generation,counter_epoch,accounting,lifetime_received_bytes,lifetime_sent_bytes,first_collected_at,last_collected_at)
		VALUES($1,$2,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','{"source":"core-api","inbound":"a"}',100,50,$3,$3)`, p.ID, p.ResetGeneration, start)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.pool.Exec(ctx, `UPDATE port_traffic_policies SET counter_epoch='bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
		reported_lifetime_received_bytes=75,last_collected_at=$2 WHERE id=$1`, p.ID, start)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeletePortTrafficPolicy(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	var owner string
	var rx uint64
	if err := s.pool.QueryRow(ctx, `SELECT agent_id,lifetime_received_bytes FROM port_traffic_accounting_epochs WHERE policy_id=$1 AND counter_epoch='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'`, p.ID).Scan(&owner, &rx); err != nil || owner != agent.ID || rx != 100 {
		t.Fatalf("v44 history migration lost data: %s %d %v", owner, rx, err)
	}
	var source string
	if err := s.pool.QueryRow(ctx, `SELECT lifetime_received_bytes,accounting->>'source' FROM port_traffic_accounting_epochs WHERE policy_id=$1 AND counter_epoch='bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'`, p.ID).Scan(&rx, &source); err != nil || rx != 75 || source != "listener" {
		t.Fatalf("v44 listener baseline not seeded: %d %s %v", rx, source, err)
	}
}
