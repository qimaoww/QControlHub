package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestAccountingSystemdEpochWithoutProcAccess(t *testing.T) {
	for _, test := range []struct {
		name, output string
		valid        bool
	}{
		{"active", "MainPID=123\nExecMainStartTimestampMonotonic=456\nActiveState=active", true},
		{"stopped", "MainPID=0\nExecMainStartTimestampMonotonic=456\nActiveState=inactive", false},
		{"missing", "MainPID=123\nActiveState=active", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "systemctl")
			writeExecutable(t, binary, "#!/bin/sh\nprintf '%s\\n' '"+test.output+"'\n")
			executor := &Executor{Services: &ServiceManager{kind: ServiceManagerSystemd, executable: binary}}
			epoch, err := executor.accountingProcessEpoch(context.Background(), DefaultSpecs()[core.EngineXray])
			if test.valid {
				if err != nil || !strings.HasSuffix(epoch, ":123:456") {
					t.Fatalf("epoch %q %v", epoch, err)
				}
			} else if err == nil {
				t.Fatal("invalid service epoch accepted")
			}
		})
	}
}

func TestNativeAccountingLifecycle(t *testing.T) {
	manager, _, now, policy := newAccuracyTrafficManager(t)
	policy.Engine = core.EngineXray
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatal(err)
	}
	port := serverconfig.AccountingPort{Port: policy.Port, Inbound: "a", Outbounds: []string{"a-direct", "a-alternate"}}
	counters := map[string]uint64{"inbound>>>a>>>traffic>>>uplink": 100, "inbound>>>a>>>traffic>>>downlink": 200, "outbound>>>a-direct>>>traffic>>>uplink": 100, "outbound>>>a-direct>>>traffic>>>downlink": 200}
	snapshot := nativeAccountingSnapshot{Plan: serverconfig.AccountingPlan{Source: "core-api", Ports: []serverconfig.AccountingPort{port}}, ProcessEpoch: "first", Counters: counters}
	var sourceErr error
	manager.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) { return snapshot, sourceErr }
	manager.collect(context.Background(), false)
	first := manager.Snapshot()[0]
	if first.Accounting == nil || first.Accounting.Source != "core-api" || first.UsedBytes != 0 {
		t.Fatalf("migration rebilled prior process lifetime: %+v", first)
	}
	for key := range counters {
		counters[key] += 100
	}
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	usage := manager.Snapshot()[0]
	if usage.ReceivedBytes != 200 || usage.SentBytes != 200 || usage.Accounting.ClientReceived != 100 || usage.Accounting.TargetReceived != 100 {
		t.Fatalf("four-leg sample: %+v", usage)
	}
	if first.Accounting.ClientReceived != 0 {
		t.Fatal("published snapshot mutated")
	}
	// The primary outbound resets while another grows: subtract each raw
	// counter independently, never subtract pre-aggregated outbound totals.
	counters["outbound>>>a-direct>>>traffic>>>downlink"] = 5
	counters["outbound>>>a-alternate>>>traffic>>>downlink"] = 1000
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	if got := manager.Snapshot()[0]; got.ReceivedBytes != 1205 {
		t.Fatalf("independent reset lost bytes: %+v", got)
	}
	sourceErr = errors.New("API unavailable")
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	if got := manager.Snapshot()[0]; got.EnforcementAvailable || got.ReceivedBytes != 1205 {
		t.Fatalf("failure substituted listener bytes: %+v", got)
	}
	sourceErr = nil
	snapshot.ProcessEpoch = "restarted"
	snapshot.Counters = map[string]uint64{"inbound>>>a>>>traffic>>>uplink": 5000}
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	if got := manager.Snapshot()[0]; got.ReceivedBytes != 6205 || !got.EnforcementAvailable {
		t.Fatalf("process restart with larger counter: %+v", got)
	}
	state, err := loadTrafficState(manager.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLoadedTrafficState(state, *now); err != nil {
		t.Fatal(err)
	}
	if state.Records[policy.ID].Accounting.ProcessEpoch != "restarted" {
		t.Fatal("process epoch not checkpointed")
	}
}

func TestMarkedAccountingFailurePreservesAllLegs(t *testing.T) {
	m, backend, now, policy := newAccuracyTrafficManager(t)
	var sourceErr error
	m.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) {
		return nativeAccountingSnapshot{Plan: serverconfig.AccountingPlan{Source: "nft-dual", Ports: []serverconfig.AccountingPort{{Port: policy.Port, Mark: 0x51430000 | uint32(policy.Port)}}}}, sourceErr
	}
	m.collect(context.Background(), false)
	for _, direction := range []string{"in", "out", "targetin", "targetout"} {
		backend.counters[trafficCounterName(m.records[policy.ID], direction, "tcp")] = 100
	}
	sourceErr = errors.New("temporary process lookup failure")
	for i := 0; i < 3; i++ {
		*now = now.Add(time.Second)
		m.collect(context.Background(), false)
	}
	sourceErr = nil
	*now = now.Add(time.Second)
	m.collect(context.Background(), false)
	got := m.Snapshot()[0]
	if got.ReceivedBytes != 200 || got.SentBytes != 200 || got.Accounting.ClientReceived != 100 || got.Accounting.ClientSent != 100 {
		t.Fatalf("recovery lost a leg: %+v / %+v", got, got.Accounting)
	}
	m.collect(context.Background(), false)
	if got := m.Snapshot()[0]; got.UsedBytes != 400 {
		t.Fatalf("recovery rebilled bytes: %+v", got)
	}
}

func TestQuotaRelaxationDuringAccountingFailure(t *testing.T) {
	for _, change := range []string{"disable", "raise", "rollover"} {
		t.Run(change, func(t *testing.T) {
			m, _, now, policy := newAccuracyTrafficManager(t)
			counters := map[string]uint64{}
			var sourceErr error
			m.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) {
				return nativeAccountingSnapshot{Plan: serverconfig.AccountingPlan{Source: "core-api", Ports: []serverconfig.AccountingPort{{Port: policy.Port, Inbound: "a"}}}, Counters: counters, ProcessEpoch: "same"}, sourceErr
			}
			policy.AutoBlock, policy.LimitBytes = true, 100
			if err := m.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
				t.Fatal(err)
			}
			counters["inbound>>>a>>>traffic>>>uplink"] = 110
			*now = now.Add(time.Second)
			m.collect(context.Background(), false)
			if !m.Snapshot()[0].Blocked {
				t.Fatal("setup not blocked")
			}
			sourceErr = errors.New("stats API unavailable")
			switch change {
			case "disable":
				policy.AutoBlock = false
			case "raise":
				policy.LimitBytes = 1000
			case "rollover":
				*now = time.Date(2026, 9, 1, 0, 0, 1, 0, time.UTC)
			}
			if err := m.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
				t.Fatal(err)
			}
			m.collect(context.Background(), false)
			if m.Snapshot()[0].Blocked {
				t.Fatal("quota relaxation retained drop rules")
			}
			sourceErr = nil
			counters["inbound>>>a>>>traffic>>>uplink"] = 120
			*now = now.Add(time.Second)
			m.collect(context.Background(), false)
			got := m.Snapshot()[0]
			if got.LifetimeReceivedBytes != 120 || got.Blocked {
				t.Fatalf("invalid recovery: %+v", got)
			}
			if change == "rollover" && got.ReceivedBytes != 10 {
				t.Fatalf("old usage billed to new period: %+v", got)
			}
		})
	}
}

func TestSingleProtocolAccountingDoesNotBillOtherTransport(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo, core.EngineShadowsocksRust} {
		for _, protocol := range []core.TrafficProtocol{core.TrafficProtocolTCP, core.TrafficProtocolUDP} {
			m, b, now, p := newAccuracyTrafficManager(t)
			p.Engine, p.Protocol = engine, protocol
			if err := m.SetPolicies(context.Background(), []core.PortTrafficPolicy{p}, p.AgentID); err != nil {
				t.Fatal(err)
			}
			m.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) {
				t.Error("queried protocol-ambiguous source")
				return nativeAccountingSnapshot{}, nil
			}
			r := m.records[p.ID]
			b.counters[trafficCounterName(r, "targetout", "udp")] = 100
			b.counters[trafficCounterName(r, "targetout", "tcp")] = 100
			b.counters[trafficCounterName(r, "in", string(protocol))] = 25
			*now = now.Add(time.Second)
			m.collect(context.Background(), false)
			got := m.Snapshot()[0]
			if got.UsedBytes != 25 || !got.EnforcementAvailable || !strings.Contains(got.EnforcementError, "listener-only") {
				t.Fatalf("invalid filtered sample: %+v", got)
			}
		}
	}
}

func TestSingleProtocolUpgradePreservesQuotaBaseline(t *testing.T) {
	m, backend, now, policy := newAccuracyTrafficManager(t)
	policy.Protocol = core.TrafficProtocolTCP
	if err := m.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatal(err)
	}
	record := m.records[policy.ID]
	oldEpoch := record.CounterEpoch
	record.Accounting = &core.TrafficAccounting{Source: "core-api", Inbound: "a"}
	record.ReceivedBytes, record.LifetimeReceivedBytes = 100, 100
	record.QuotaBaselineBytes = 50
	m.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) {
		t.Fatal("single protocol must not query shared native counters")
		return nativeAccountingSnapshot{}, nil
	}
	*now = now.Add(time.Second)
	m.collect(context.Background(), false)
	if record.CounterEpoch == oldEpoch || record.QuotaBaselineBytes != 150 || record.Accounting != nil || record.LifetimeReceivedBytes != 0 {
		t.Fatalf("upgrade lost quota or reused mixed-scope epoch: %+v", record)
	}
	backend.counters[trafficCounterName(record, "in", "tcp")] = 25
	*now = now.Add(time.Second)
	m.collect(context.Background(), false)
	if got := m.Snapshot()[0]; got.ReceivedBytes != 25 || record.QuotaBaselineBytes != 150 {
		t.Fatalf("upgrade rebilled old scope: %+v", got)
	}
}
