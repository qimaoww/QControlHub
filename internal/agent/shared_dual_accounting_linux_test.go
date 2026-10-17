package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// A and B run the same core. A's target download is the previously missing RX
// leg; both legs must settle once, across all ports of the same allocation.
func TestSharedDualAccountingScopesBothLegsByInstance(t *testing.T) {
	m, b, policies, now := sharedTrafficFixture(t)
	for i := range policies {
		policies[i].Engine = core.EngineSingBox
		policies[i].SharedQuota.Engines = []core.Engine{core.EngineSingBox}
		policies[i].SharedQuota.LimitBytes = 100000
	}
	calls := map[string]int{}
	var unavailable bool
	m.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) {
		t.Fatal("shared instance read the owner's core")
		return nativeAccountingSnapshot{}, nil
	}
	m.sharedSource = func(_ context.Context, e core.Engine, share string) (nativeAccountingSnapshot, error) {
		calls[share]++
		if unavailable && share == policies[0].SharedQuota.ID {
			return nativeAccountingSnapshot{}, errors.New("invalid instance configuration")
		}
		plan := serverconfig.AccountingPlan{Source: "nft-dual"}
		for _, p := range policies {
			if p.SharedQuota.ID == share {
				plan.Ports = append(plan.Ports, serverconfig.AccountingPort{Port: p.Port, Inbound: p.ID, Outbounds: []string{p.ID + "-exit"}, Mark: uint32(0x51430000) | uint32(p.Port)})
			}
		}
		return nativeAccountingSnapshot{Plan: plan, ProcessEpoch: share + "-process"}, nil
	}
	if err := m.SetPolicies(context.Background(), policies, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	for k := range calls {
		calls[k] = 0
	}
	a := m.records[policies[0].ID]
	b.counters[trafficCounterName(a, "in", "tcp")] = 10
	b.counters[trafficCounterName(a, "out", "tcp")] = 1000
	b.counters[trafficCounterName(a, "targetin", "tcp")] = 900
	b.counters[trafficCounterName(a, "targetout", "tcp")] = 20
	*now = now.Add(time.Second)
	m.collect(context.Background(), false)
	got := m.Snapshot()
	if got[0].ReceivedBytes != 910 || got[0].SentBytes != 1020 || got[0].ShareUsedBytes != 1930 || got[1].UsedBytes != 0 || got[2].UsedBytes != 0 {
		t.Fatalf("dual leg attribution: %+v", got)
	}
	if calls[policies[0].SharedQuota.ID] != 1 || calls[policies[2].SharedQuota.ID] != 1 {
		t.Fatalf("source cache merged independent instances: %v", calls)
	}
	*now = now.Add(time.Second)
	m.collect(context.Background(), false)
	if m.Snapshot()[0].UsedBytes != 1930 {
		t.Fatal("repeated counters double billed")
	}
	var stopped []string
	m.haltShare = func(_ context.Context, _ core.Engine, id string) error { stopped = append(stopped, id); return nil }
	unavailable = true
	b.counters[trafficCounterName(a, "targetin", "tcp")] = 950
	*now = now.Add(time.Second)
	m.collect(context.Background(), false)
	if s := m.Snapshot(); s[0].EnforcementAvailable || s[0].UsedBytes != 1930 || !s[2].EnforcementAvailable {
		t.Fatalf("failed sample modified totals or other user: %+v", s)
	}
	if len(stopped) == 0 {
		t.Fatal("invalid attribution left shared instance running")
	}
	if _, settled := m.settledSnapshot(context.Background(), core.Task{Engine: core.EngineSingBox, SharedTrafficID: policies[0].SharedQuota.ID}); settled {
		t.Fatal("failed source authorized final port release")
	}
	if _, settled := m.settledSnapshot(context.Background(), core.Task{Engine: core.EngineSingBox, SharedTrafficID: policies[2].SharedQuota.ID}); !settled {
		t.Fatal("A's source failure blocked B's final settlement")
	}
	unavailable = false
	*now = now.Add(time.Second)
	m.collect(context.Background(), false)
	if s := m.Snapshot()[0]; s.UsedBytes != 1980 || !s.EnforcementAvailable {
		t.Fatalf("recovery lost/rebilled target bytes: %+v", s)
	}
}

func TestSharedListenerMigrationCarriesOnlyUnacknowledgedBytes(t *testing.T) {
	m, b, policies, now := sharedTrafficFixture(t)
	if err := m.SetPolicies(context.Background(), policies, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	r := m.records[policies[0].ID]
	b.counters[trafficCounterName(r, "in", "tcp")] = 200
	*now = now.Add(time.Second)
	m.collect(context.Background(), false)
	q := *r.Policy.SharedQuota
	q.CounterEpoch = r.CounterEpoch
	q.LifetimeReceivedBytes = 100
	q.PortUsedBytes = 100
	q.UsedBytes = 100
	r.Policy.SharedQuota = &q
	b.counters[trafficCounterName(r, "in", "tcp")] = 225
	r.LastCollectedAt = time.Time{} // Restored checkpoints deliberately omit this field.
	m.sharedSource = func(_ context.Context, _ core.Engine, share string) (nativeAccountingSnapshot, error) {
		plan := serverconfig.AccountingPlan{Source: "nft-dual"}
		for _, p := range policies {
			if p.SharedQuota.ID == share {
				plan.Ports = append(plan.Ports, serverconfig.AccountingPort{Port: p.Port, Mark: uint32(0x51430000) | uint32(p.Port)})
			}
		}
		return nativeAccountingSnapshot{Plan: plan, ProcessEpoch: "live"}, nil
	}
	*now = now.Add(time.Second)
	m.collect(context.Background(), false)
	if r.LifetimeReceivedBytes != 125 || r.Accounting.ClientReceived != 125 || r.ShareUsedBytes != 225 {
		t.Fatalf("migration lost or rebilled pending interval: %+v", r)
	}
}

func TestSharedConnectionMarkChangesOnPortReassignment(t *testing.T) {
	m, _, p, _ := sharedTrafficFixture(t)
	if err := m.SetPolicies(context.Background(), p, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	a := m.records[p[0].ID]
	old := trafficConnectionMark(a)
	next := *a
	next.Policy = p[2]
	next.Policy.Port = a.Policy.Port
	if trafficConnectionMark(&next) == old {
		t.Fatal("reassigned port retained old flow identity")
	}
	next = *a
	next.CounterEpoch = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if trafficConnectionMark(&next) == old {
		t.Fatal("new counter scope retained old flow identity")
	}
}

func TestSharedUndeployedSourceReportsValidUnhealthyCalendar(t *testing.T) {
	m, _, p, _ := sharedTrafficFixture(t)
	m.sharedSource = func(context.Context, core.Engine, string) (nativeAccountingSnapshot, error) {
		return nativeAccountingSnapshot{}, errors.New("configuration not deployed")
	}
	if err := m.SetPolicies(context.Background(), p, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	for _, u := range m.Snapshot() {
		if u.EnforcementAvailable || u.PeriodStart.IsZero() || !u.PeriodEnd.After(u.PeriodStart) {
			t.Fatalf("undeployed policy prevents heartbeat recovery: %+v", u)
		}
	}
}

func TestSharedSourceMigrationDoesNotRebillAcknowledgedCheckpointOverlap(t *testing.T) {
	m, b, p, now := sharedTrafficFixture(t)
	if err := m.SetPolicies(context.Background(), p, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	r := m.records[p[0].ID]
	b.counters[trafficCounterName(r, "in", "tcp")] = 100
	*now = now.Add(time.Second)
	m.collect(context.Background(), false)
	q := *r.Policy.SharedQuota
	q.CounterEpoch = r.CounterEpoch
	q.LifetimeReceivedBytes = 150
	q.PortUsedBytes = 150
	q.UsedBytes = 150
	r.Policy.SharedQuota = &q
	r.ShareUsedBytes = 150
	r.LastCollectedAt = time.Time{}
	b.counters[trafficCounterName(r, "in", "tcp")] = 175
	m.sharedSource = func(_ context.Context, _ core.Engine, share string) (nativeAccountingSnapshot, error) {
		plan := serverconfig.AccountingPlan{Source: "nft-dual"}
		for _, v := range p {
			if v.SharedQuota.ID == share {
				plan.Ports = append(plan.Ports, serverconfig.AccountingPort{Port: v.Port, Mark: uint32(0x51430000) | uint32(v.Port)})
			}
		}
		return nativeAccountingSnapshot{Plan: plan, ProcessEpoch: "current"}, nil
	}
	*now = now.Add(time.Second)
	m.collect(context.Background(), false)
	if r.LifetimeReceivedBytes != 25 || r.Accounting.ClientReceived != 25 || r.ShareUsedBytes != 175 {
		t.Fatalf("rebilled checkpoint overlap: %+v", r)
	}
}

// A port reserved before deployment is not an attribution failure. After a
// replacement, old nft counters remain trustworthy for the final settlement.
func TestSharedPortReplacementSettlesOldCountersWithoutStoppingInstance(t *testing.T) {
	m, b, policies, now := sharedTrafficFixture(t)
	ctx := context.Background()
	active := policies[0].Port
	m.sharedSource = func(_ context.Context, e core.Engine, share string) (nativeAccountingSnapshot, error) {
		plan := serverconfig.AccountingPlan{Source: "nft-dual"}
		if share == policies[0].SharedQuota.ID {
			plan.Ports = []serverconfig.AccountingPort{{Port: active, Inbound: "stable-protocol", Outbounds: []string{"exit"}, Mark: 0x51430000 | uint32(active)}}
		} else {
			plan.Ports = []serverconfig.AccountingPort{{Port: policies[2].Port, Mark: 0x51430000 | uint32(policies[2].Port)}}
		}
		return nativeAccountingSnapshot{Plan: plan, ProcessEpoch: "verified"}, nil
	}
	stopped := 0
	m.haltShare = func(context.Context, core.Engine, string) error { stopped++; return nil }
	if err := m.SetPolicies(ctx, policies, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	if stopped != 0 {
		t.Fatal("reserved future listener stopped the running instance")
	}
	future := m.records[policies[1].ID]
	b.counters[trafficCounterName(future, "in", "tcp")] = 999
	*now = now.Add(time.Second)
	m.collect(ctx, false)
	if m.Snapshot()[1].UsedBytes != 0 || m.Snapshot()[1].ShareUsedBytes != 0 {
		t.Fatal("undeployed port charged unrelated traffic")
	}
	old := m.records[policies[0].ID]
	epoch := old.CounterEpoch
	b.counters[trafficCounterName(old, "in", "tcp")] = 10
	b.counters[trafficCounterName(old, "targetin", "tcp")] = 20
	*now = now.Add(time.Second)
	m.collect(ctx, false)
	active = policies[1].Port
	b.counters[trafficCounterName(old, "in", "tcp")] = 15
	b.counters[trafficCounterName(old, "targetin", "tcp")] = 27
	*now = now.Add(time.Second)
	snapshot, settled := m.settledSnapshot(ctx, core.Task{Engine: policies[0].Engine, SharedTrafficID: policies[0].SharedQuota.ID})
	if !settled || stopped != 0 || old.CounterEpoch != epoch || snapshot[0].UsedBytes != 42 || !snapshot[0].EnforcementAvailable {
		t.Fatalf("replacement lost final traffic or stopped service: stopped=%d settled=%v snapshot=%+v", stopped, settled, snapshot)
	}
	*now = now.Add(time.Second)
	m.collect(ctx, false)
	if m.Snapshot()[0].UsedBytes != 42 {
		t.Fatal("old tail billed twice")
	}
	if err := m.SetPolicies(ctx, policies[1:], "agt_shared"); err != nil {
		t.Fatal(err)
	}
	if stopped != 0 || m.records[policies[0].ID] != nil {
		t.Fatal("retiring a verified old port stopped the replacement instance")
	}
	// Removing a listener still present in the protected config is a real
	// revocation, and must retain the established protective stop behavior.
	if err := m.SetPolicies(ctx, policies[2:], "agt_shared"); err != nil {
		t.Fatal(err)
	}
	if stopped == 0 {
		t.Fatal("live listener revocation did not stop its allocation")
	}
}

func TestSharedAccountingDoesNotReadAnUncommittedDeployment(t *testing.T) {
	e := &Executor{Specs: DefaultSpecs()}
	share := "shr_0000000000000001"
	lock := e.sharedAccountingLock(core.EngineSingBox, share)
	lock.Lock()
	_, err := e.sharedAccounting(context.Background(), core.EngineSingBox, share)
	if !errors.Is(err, errSharedAccountingTransition) {
		lock.Unlock()
		t.Fatalf("uncommitted source was read: %v", err)
	}
	if !e.sharedAccountingLock(core.EngineSingBox, "shr_0000000000000002").TryLock() {
		lock.Unlock()
		t.Fatal("another user's deployment shares the lock")
	}
	e.sharedAccountingLock(core.EngineSingBox, "shr_0000000000000002").Unlock()
	lock.Unlock()
	_, err = e.sharedAccounting(context.Background(), core.EngineSingBox, share)
	if errors.Is(err, errSharedAccountingTransition) {
		t.Fatal("sampling did not resume after deployment")
	}
}
