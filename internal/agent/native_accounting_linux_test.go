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
