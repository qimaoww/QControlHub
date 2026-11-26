package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSharedTrafficSettlesOnlyAcceptedDeltas(t *testing.T) {
	for _, scenario := range []string{"failed", "restored-failed", "old-generation", "duplicate", "late", "rollback", "restart"} {
		t.Run(scenario, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			user, actor := sharedTestUser(t, db, ctx, "settlement-user")
			agent := sharedTestAgent(t, db, ctx)
			sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 100000, 21001)
			config := sharedTestConfig(t, db, actor, agent.ID, 21001)
			if _, err := db.CreateTask(actor, core.TaskRequest{AgentID: agent.ID, ConfigID: config.ID, Engine: config.Engine, Action: core.ActionDeploy}); err != nil {
				t.Fatal(err)
			}
			policies, _ := db.AgentPortTrafficPolicies(ctx, agent.ID)
			p := policies[0]
			at := time.Now().UTC().Add(time.Second)
			start, end, _ := core.TrafficPeriodAt(p.CycleAnchor, p.Cycle, at)
			u := core.PortTrafficUsage{PolicyID: p.ID, ShareID: p.SharedQuota.ID, ResetGeneration: p.ResetGeneration,
				CounterEpoch: strings.Repeat("a", 32), CollectedAt: at, PeriodStart: start, PeriodEnd: end,
				ReceivedBytes: 100, UsedBytes: 100, LifetimeReceivedBytes: 100, ShareUsedBytes: 100, EnforcementAvailable: true}
			if err := db.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{u}, at); err != nil {
				t.Fatal(err)
			}
			v := u
			v.CollectedAt = at.Add(time.Second)
			v.ReceivedBytes, v.UsedBytes, v.LifetimeReceivedBytes, v.ShareUsedBytes = 200, 200, 200, 5000
			want := uint64(100)
			switch scenario {
			case "failed":
				v.EnforcementAvailable = false
			case "restored-failed":
				v.EnforcementAvailable = false
				v.CollectedAt = time.Time{}
			case "old-generation":
				v.ResetGeneration++
			case "duplicate":
				v.CollectedAt = at
			case "late":
				v.CollectedAt = at.Add(-time.Second)
			case "rollback":
				v.ReceivedBytes, v.UsedBytes, v.LifetimeReceivedBytes = 50, 50, 50
			case "restart":
				v.CounterEpoch = strings.Repeat("b", 32)
				v.ReceivedBytes, v.UsedBytes, v.LifetimeReceivedBytes, v.ShareUsedBytes = 40, 40, 40, 40
				want = 140
			}
			if err := db.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{v}, at.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			access, _ := db.UserAgentAccess(ctx, user.ID)
			policies, _ = db.AgentPortTrafficPolicies(ctx, agent.ID)
			if access.Shares[0].UsedBytes != want || policies[0].UsedBytes != want {
				t.Fatalf("%s: accepted=%d shared=%d; want both %d", scenario, policies[0].UsedBytes, access.Shares[0].UsedBytes, want)
			}
		})
	}
}

func TestSharedTrafficOwnershipAndAcceptedTotals(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	a, alice := sharedTestUser(t, db, ctx, "accounting-alice")
	b, bob := sharedTestUser(t, db, ctx, "accounting-bob")
	agent := sharedTestAgent(t, db, ctx)
	sharedTestAllocation(t, db, ctx, a.ID, agent.ID, 0, 21001, 21002)
	sharedTestAllocation(t, db, ctx, b.ID, agent.ID, 0, 21003)
	for _, entry := range []struct {
		actor context.Context
		ports []int
	}{{alice, []int{21001, 21002}}, {bob, []int{21003}}} {
		c := sharedTestConfig(t, db, entry.actor, agent.ID, entry.ports...)
		if _, err := db.CreateTask(entry.actor, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, ConfigID: c.ID, Action: core.ActionDeploy}); err != nil {
			t.Fatal(err)
		}
	}
	policies, _ := db.AgentPortTrafficPolicies(ctx, agent.ID)
	at := time.Now().UTC().Truncate(time.Microsecond)
	var reports []core.PortTrafficUsage
	for _, p := range policies {
		start, end, _ := core.TrafficPeriodAt(p.CycleAnchor, p.Cycle, at)
		reports = append(reports, core.PortTrafficUsage{PolicyID: p.ID, ShareID: p.SharedQuota.ID, ResetGeneration: p.ResetGeneration, CounterEpoch: strings.Repeat("a", 32), CollectedAt: at, PeriodStart: start, PeriodEnd: end, EnforcementAvailable: true})
	}
	for i := range reports {
		if policies[i].Port != 21003 {
			reports[i].ReceivedBytes = 100
			reports[i].SentBytes = 50
			reports[i].UsedBytes = 150
			reports[i].LifetimeReceivedBytes = 100
			reports[i].LifetimeSentBytes = 50
		}
	}
	if err := db.UpdatePortTrafficUsage(ctx, agent.ID, reports, at); err != nil {
		t.Fatal(err)
	}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: []string{core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCoreLifecycle, core.AgentFeatureIndependentEgress}, TrafficUsage: reports}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		userID string
		want   uint64
	}{{a.ID, 300}, {b.ID, 0}} {
		access, err := db.UserAgentAccess(ctx, entry.userID)
		if err != nil || len(access.Shares) != 1 || access.Shares[0].UsedBytes != entry.want {
			t.Fatalf("scoped accepted totals: %+v %v", access, err)
		}
	}
	for _, mutation := range []string{"missing-share", "foreign-share", "native-source", "missing-epoch", "missing-time"} {
		bad := reports[0]
		bad.CollectedAt = at.Add(time.Second)
		switch mutation {
		case "missing-share":
			bad.ShareID = ""
		case "foreign-share":
			bad.ShareID = policies[2].SharedQuota.ID
		case "native-source":
			bad.Accounting = &core.TrafficAccounting{Source: "core-api", Inbound: "a"}
		case "missing-epoch":
			bad.CounterEpoch = ""
		case "missing-time":
			bad.CollectedAt = time.Time{}
		}
		if err := db.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{bad}, at.Add(2*time.Second)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s accepted: %v", mutation, err)
		}
	}

}

func TestSharedTrafficCalendarAndEpochReplay(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, actor := sharedTestUser(t, db, ctx, "epoch-user")
	agent := sharedTestAgent(t, db, ctx)
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 0, 21001)
	c := sharedTestConfig(t, db, actor, agent.ID, 21001)
	if _, err := db.CreateTask(actor, core.TaskRequest{AgentID: agent.ID, ConfigID: c.ID, Engine: c.Engine, Action: core.ActionDeploy}); err != nil {
		t.Fatal(err)
	}
	policies, _ := db.AgentPortTrafficPolicies(ctx, agent.ID)
	p := policies[0]
	start, end, _ := core.TrafficPeriodAt(p.CycleAnchor, p.Cycle, time.Now().UTC())
	var used uint64
	for i, entry := range []struct {
		epoch                   string
		period, lifetime, share uint64
		at                      time.Time
		want                    uint64
	}{
		{"a", 100, 100, 100, start.Add(time.Second), 100},
		{"b", 40, 40, 100, start.Add(2 * time.Second), 140},
		{"a", 120, 120, 5000, start.Add(3 * time.Second), 160},
		{"b", 40, 40, 9999, start.Add(4 * time.Second), 160},
		{"b", 10, 50, 1, end.Add(time.Second), 170},
	} {
		periodStart, periodEnd, _ := core.TrafficPeriodAt(p.CycleAnchor, p.Cycle, entry.at)
		u := core.PortTrafficUsage{PolicyID: p.ID, ShareID: p.SharedQuota.ID, ResetGeneration: p.ResetGeneration, CounterEpoch: strings.Repeat(entry.epoch, 32), CollectedAt: entry.at, PeriodStart: periodStart, PeriodEnd: periodEnd, EnforcementAvailable: true, ReceivedBytes: entry.period, UsedBytes: entry.period, LifetimeReceivedBytes: entry.lifetime, ShareUsedBytes: entry.share}
		if err := db.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{u}, entry.at); err != nil {
			t.Fatal(err)
		}
		access, _ := db.UserAgentAccess(ctx, user.ID)
		used = access.Shares[0].UsedBytes
		if used != entry.want {
			t.Fatalf("step %d shared=%d want=%d", i, used, entry.want)
		}
	}
	policies, _ = db.AgentPortTrafficPolicies(ctx, agent.ID)
	if policies[0].UsedBytes != 10 || used != 170 {
		t.Fatalf("calendar/lifetime conflated: %+v shared=%d", policies, used)
	}
}

func TestSharedTrafficDualSettlementPreservesListenerHistory(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, actor := sharedTestUser(t, db, ctx, "dual-settlement")
	agent := sharedTestAgent(t, db, ctx)
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 0, 21001)
	c := sharedTestConfig(t, db, actor, agent.ID, 21001)
	if _, err := db.CreateTask(actor, core.TaskRequest{AgentID: agent.ID, ConfigID: c.ID, Engine: c.Engine, Action: core.ActionDeploy}); err != nil {
		t.Fatal(err)
	}
	policies, _ := db.AgentPortTrafficPolicies(ctx, agent.ID)
	p := policies[0]
	at := time.Now().UTC().Truncate(time.Microsecond)
	start, end, _ := core.TrafficPeriodAt(p.CycleAnchor, p.Cycle, at)
	u := core.PortTrafficUsage{PolicyID: p.ID, ShareID: p.SharedQuota.ID, ResetGeneration: p.ResetGeneration, CounterEpoch: strings.Repeat("a", 32), CollectedAt: at, PeriodStart: start, PeriodEnd: end, ReceivedBytes: 100, UsedBytes: 100, LifetimeReceivedBytes: 100, EnforcementAvailable: true}
	if err := db.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{u}, at); err != nil {
		t.Fatal(err)
	}
	u.CounterEpoch = strings.Repeat("b", 32)
	u.CollectedAt = at.Add(time.Second)
	u.ReceivedBytes, u.LifetimeReceivedBytes = 910, 910
	u.SentBytes, u.LifetimeSentBytes = 1020, 1020
	u.UsedBytes = 1930
	u.ShareUsedBytes = 999999 // Untrusted raw cumulative must never override accepted deltas.
	u.Accounting = &core.TrafficAccounting{Source: "nft-dual", Mark: uint32(0x51430000) | uint32(p.Port), Inbound: "shared", Outbounds: []string{"independent"}, ClientReceived: 10, ClientSent: 1000, TargetReceived: 900, TargetSent: 20}
	if err := db.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{u}, at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := db.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{u}, at.Add(time.Duration(3+i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	access, _ := db.UserAgentAccess(ctx, user.ID)
	policies, _ = db.AgentPortTrafficPolicies(ctx, agent.ID)
	if access.Shares[0].UsedBytes != 2030 || policies[0].UsedBytes != 2030 {
		t.Fatalf("source transition erased/rebilled history: %+v %+v", access.Shares, policies)
	}
	for _, mutation := range []string{"wrong-mark", "bad-legs"} {
		bad := u
		accounting := *u.Accounting
		bad.Accounting = &accounting
		bad.CounterEpoch = strings.Repeat("c", 32)
		bad.CollectedAt = at.Add(10 * time.Second)
		if mutation == "wrong-mark" {
			accounting.Mark++
		} else {
			accounting.TargetReceived++
		}
		if err := db.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{bad}, at.Add(11*time.Second)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s accepted: %v", mutation, err)
		}
	}
}

func TestSharedProcessSettlementAcrossPortsAndRestarts(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	user, actor := sharedTestUser(t, db, ctx, "process-settlement")
	agent := sharedTestAgent(t, db, ctx)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET features=features || '["shared-process-traffic-v1"]' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 120, 21001, 21002)
	source := "routing-mark: 17\nlisteners: [{name: a, type: http, port: 21001}, {name: b, type: http, port: 21002}]\n"
	config, task, err := db.SaveAgentConfigAndTask(actor, core.Config{AgentID: agent.ID, Engine: core.EngineMihomo, Name: "private", Content: source}, 0, ConfigMutationOptions{Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil || !lease.SharedInstance || lease.ConfigContent != source {
		t.Fatalf("shared unmodified lease: %+v %v", lease, err)
	}
	if err = completeSharedTestTask(db, ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 2 {
		t.Fatalf("policies: %v %v", policies, err)
	}
	at := time.Now().UTC()
	reports := []core.PortTrafficUsage{}
	for _, p := range policies {
		if !p.SharedQuota.ProcessMetering {
			t.Fatal("process quota was not negotiated")
		}
		start, end, _ := core.TrafficPeriodAt(p.CycleAnchor, p.Cycle, at)
		reports = append(reports, core.PortTrafficUsage{PolicyID: p.ID, ShareID: p.SharedQuota.ID, ResetGeneration: p.ResetGeneration, PeriodStart: start, PeriodEnd: end, CollectedAt: at, CounterEpoch: strings.Repeat("a", 32), ReceivedBytes: 10, UsedBytes: 10, LifetimeReceivedBytes: 10, EnforcementAvailable: true, ProcessUsage: &core.SharedProcessUsage{Epoch: strings.Repeat("b", 32), Bytes: 70}})
	}
	settle := func(want uint64) {
		t.Helper()
		if err := db.UpdatePortTrafficUsage(ctx, agent.ID, reports, at); err != nil {
			t.Fatal(err)
		}
		read, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range read {
			if p.UsedBytes != 10 || p.SharedQuota.UsedBytes != want || p.SharedQuota.ProcessAck == nil {
				t.Fatalf("port %d, aggregate %d, want 10/%d", p.UsedBytes, p.SharedQuota.UsedBytes, want)
			}
		}
	}
	before := reports[0]
	before.ProcessUsage = nil
	if err := db.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{before}, at); err != nil {
		t.Fatal(err)
	}
	prior, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || prior[0].EnforcementAvailable || prior[0].UsedBytes != 10 {
		t.Fatalf("legacy sample must settle without authorizing recovery: %+v %v", prior, err)
	}
	at = at.Add(time.Second)
	for i := range reports {
		reports[i].CollectedAt = at
	}
	settle(90)
	settle(90)
	at = at.Add(time.Second)
	for i := range reports {
		reports[i].CollectedAt = at
		reports[i].ProcessUsage.Bytes = 100
	}
	settle(120)
	if _, err := db.CreateTask(actor, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy}); err == nil || !strings.Contains(err.Error(), "exhausted") {
		t.Fatalf("exhausted process quota deploy: %v", err)
	}
	at = at.Add(time.Second)
	for i := range reports {
		reports[i].CollectedAt = at
		reports[i].ProcessUsage = &core.SharedProcessUsage{Epoch: strings.Repeat("c", 32), Bytes: 5}
	}
	settle(125)
	at = at.Add(time.Second)
	for i := range reports {
		reports[i].CollectedAt = at
		reports[i].ProcessUsage = &core.SharedProcessUsage{Epoch: strings.Repeat("b", 32), Bytes: 100}
	}
	settle(125)
}
