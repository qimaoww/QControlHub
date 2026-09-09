package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTrafficSyncCandidates(t *testing.T) {
	endpoints := []core.PortTrafficEndpoint{
		{AgentID: "a", Name: "new", Engine: core.EngineMihomo, Port: 443, Protocol: core.TrafficProtocolTCP},
		{AgentID: "a", Name: "new udp", Engine: core.EngineMihomo, Port: 443, Protocol: core.TrafficProtocolUDP},
		{AgentID: "a", Name: "active", Engine: core.EngineMihomo, Port: 80, Protocol: core.TrafficProtocolTCP},
	}
	candidates, err := TrafficSyncCandidates(endpoints, []core.PortTrafficPolicy{
		{AgentID: "a", Name: "deleted", Engine: core.EngineMihomo, Port: 8443, Protocol: core.TrafficProtocolBoth},
		{AgentID: "a", Port: 80, MonitoringEnabled: true},
	})
	if err != nil || len(candidates) != 2 || candidates[0].Kind != "new" || candidates[0].Protocol != core.TrafficProtocolBoth || candidates[1].Kind != "deleted" {
		t.Fatalf("candidates: %+v %v", candidates, err)
	}
}

func TestTrafficSyncSelectedWithPostgreSQL(t *testing.T) {
	ctx, db := optionalTrafficTestStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	endpoint := func(port int) core.PortTrafficEndpoint {
		return core.PortTrafficEndpoint{AgentID: agent.ID, Engine: core.EngineMihomo, Name: "listener", Port: port, Protocol: core.TrafficProtocolBoth}
	}
	if _, err := db.ReconcilePortTrafficEndpoints(ctx, []core.PortTrafficEndpoint{endpoint(80), endpoint(443), endpoint(8443)}, false); err != nil {
		t.Fatal(err)
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreatePortTrafficPolicy(ctx, core.PortTrafficPolicyRequest{AgentID: agent.ID, Name: "retained quota", Engine: core.EngineMihomo, Port: 80, Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly, CycleAnchor: policies[0].CycleAnchor, LimitBytes: 1000000}); err != nil {
		t.Fatal(err)
	}
	policies, _ = db.AgentPortTrafficPolicies(ctx, agent.ID)
	now := time.Now().UTC()
	for _, policy := range policies {
		if err := db.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{{PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration, ReceivedBytes: 100, SentBytes: 200, UsedBytes: 300, PeriodStart: policy.CycleAnchor, PeriodEnd: policy.CycleAnchor.AddDate(0, 1, 0)}}, now); err != nil {
			t.Fatal(err)
		}
	}
	policies, _ = db.AgentPortTrafficPolicies(ctx, agent.ID)
	active := policies[0]
	deleted := policies[1]
	for _, p := range policies[1:] {
		if _, err := db.DeletePortTrafficMonitoring(ctx, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	before, err := db.ListPortTrafficPolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Deleted 443 is no longer in saved configuration, but is still recoverable.
	raw := []core.PortTrafficEndpoint{endpoint(80), endpoint(8443), endpoint(9443), endpoint(10443)}
	choose := func(port int) core.TrafficSyncSelection {
		return core.TrafficSyncSelection{AgentID: agent.ID, Port: port}
	}
	for _, bad := range [][]core.TrafficSyncSelection{nil, {}, {choose(443), choose(443)}, {choose(443), choose(5555)}} {
		if _, err := db.SyncSelectedPortTrafficEndpoints(ctx, raw, bad); err == nil {
			t.Fatalf("accepted invalid selection: %+v", bad)
		}
		after, _ := db.ListPortTrafficPolicies(ctx)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("failed selection partially wrote")
		}
	}
	selected := []core.TrafficSyncSelection{choose(443), choose(9443)}
	changed, err := db.SyncSelectedPortTrafficEndpoints(ctx, raw, selected)
	if err != nil || len(changed) != 1 || changed[0] != agent.ID {
		t.Fatalf("sync: %v %v", changed, err)
	}
	after, err := db.ListPortTrafficPolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 4 {
		t.Fatalf("unselected new port inserted: %+v", after)
	}
	for _, p := range after {
		switch p.Port {
		case 80:
			if !reflect.DeepEqual(p, active) {
				t.Fatal("active policy changed")
			}
		case 443:
			if p.ID != deleted.ID || !p.MonitoringEnabled || p.QuotaEnabled || p.AutoBlock || p.UsedBytes != 0 || p.Name != deleted.Name {
				t.Fatalf("bad restoration: %+v", p)
			}
			if p.ResetGeneration != before[1].ResetGeneration {
				t.Fatal("restoration changed deletion epoch")
			}
		case 8443:
			if p.MonitoringEnabled {
				t.Fatal("unselected tombstone restored")
			}
		case 9443:
			if !p.MonitoringEnabled || p.QuotaEnabled {
				t.Fatal("new port not monitor-only")
			}
		default:
			t.Fatalf("unexpected port: %d", p.Port)
		}
	}
	changed, err = db.SyncSelectedPortTrafficEndpoints(ctx, raw, selected)
	if err != nil || len(changed) != 0 {
		t.Fatalf("not idempotent: %v %v", changed, err)
	}
	repeated, _ := db.ListPortTrafficPolicies(ctx)
	if !reflect.DeepEqual(after, repeated) {
		t.Fatal("retry changed policies")
	}
	history, err := db.ListPortTrafficDailyUsage(ctx, agent.ID, active.ID, now)
	if err != nil || len(history) != 1 || history[0].UsedBytes != 300 {
		t.Fatalf("active history changed: %+v %v", history, err)
	}
	history, err = db.ListPortTrafficDailyUsage(ctx, agent.ID, deleted.ID, now)
	if err != nil || len(history) != 0 {
		t.Fatalf("deleted history resurrected: %+v %v", history, err)
	}
}

func TestTrafficSyncSelectedLimitAtomicWithPostgreSQL(t *testing.T) {
	ctx, db := optionalTrafficTestStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	endpoints := make([]core.PortTrafficEndpoint, 257)
	for i := range endpoints {
		endpoints[i] = core.PortTrafficEndpoint{AgentID: agent.ID, Engine: core.EngineMihomo, Name: "port", Port: 10000 + i, Protocol: core.TrafficProtocolTCP}
	}
	if candidates, err := TrafficSyncCandidates(endpoints, nil); err != nil || len(candidates) != 257 {
		t.Fatalf("preview capped available ports: %d %v", len(candidates), err)
	}
	if _, err := db.SyncSelectedPortTrafficEndpoints(ctx, endpoints, []core.TrafficSyncSelection{{AgentID: agent.ID, Port: 10000}}); err != nil {
		t.Fatalf("unselected candidates consumed monitoring budget: %v", err)
	}
	if _, err := db.ReconcilePortTrafficEndpoints(ctx, endpoints[:256], false); err != nil {
		t.Fatal(err)
	}
	policies, _ := db.ListPortTrafficPolicies(ctx)
	if _, err := db.DeletePortTrafficMonitoring(ctx, policies[0].ID); err != nil {
		t.Fatal(err)
	}
	before, _ := db.ListPortTrafficPolicies(ctx)
	_, err := db.SyncSelectedPortTrafficEndpoints(ctx, endpoints[256:], []core.TrafficSyncSelection{{AgentID: agent.ID, Port: 10000}, {AgentID: agent.ID, Port: 10256}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("over limit: %v", err)
	}
	after, _ := db.ListPortTrafficPolicies(context.Background())
	if !reflect.DeepEqual(before, after) {
		t.Fatal("limit failure partially wrote")
	}
}
