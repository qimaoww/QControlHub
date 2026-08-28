package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTrafficManagerInstallsMissingNFTablesAtStartupWithoutPolicies(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	nft := filepath.Join(root, "nft")
	installed := make(chan struct{})
	backend := &nftBackend{
		nftPath: nft, direct: true, missing: true,
		initialization: errors.New("nftables is unavailable"),
		installer: func(_ context.Context, _ *nftBackend) error {
			if err := os.WriteFile(nft, []byte("#!/bin/sh\nprintf '{\"nftables\":[]}'\n"), 0o700); err != nil {
				return err
			}
			close(installed)
			return nil
		},
	}
	manager := &TrafficManager{backend: backend, records: make(map[string]*trafficRecord), now: time.Now}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	select {
	case <-installed:
	case <-time.After(2 * time.Second):
		t.Fatal("nftables installation was not attempted at Agent startup")
	}
	if err := backend.ensureAvailable(context.Background()); err != nil {
		t.Fatalf("installed nftables backend remained unavailable: %v", err)
	}
}

type fakeTrafficBackend struct {
	counters map[string]uint64
	exists   bool
	scripts  []string
	err      error
}

func (backend *fakeTrafficBackend) Counters(context.Context) (map[string]uint64, bool, error) {
	copy := make(map[string]uint64, len(backend.counters))
	for key, value := range backend.counters {
		copy[key] = value
	}
	return copy, backend.exists, backend.err
}

func (backend *fakeTrafficBackend) Replace(_ context.Context, script string) error {
	if backend.err != nil {
		return backend.err
	}
	backend.scripts = append(backend.scripts, script)
	backend.exists = strings.Contains(script, "add table inet "+trafficTableName)
	backend.counters = map[string]uint64{}
	return nil
}

func TestTrafficManagerCountsBlocksAndResetsCalendarPeriod(t *testing.T) {
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	backend := &fakeTrafficBackend{counters: map[string]uint64{}}
	manager := &TrafficManager{
		statePath: t.TempDir() + "/traffic-state.json", backend: backend,
		records: make(map[string]*trafficRecord), now: func() time.Time { return now },
	}
	policy := core.PortTrafficPolicy{
		ID: "trf_0123456789abcdef", AgentID: "agt_0123456789abcdef", Name: "tls inbound",
		Engine: core.EngineXray, Port: 443, Protocol: core.TrafficProtocolBoth,
		Cycle: core.TrafficCycleMonthly, CycleAnchor: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		LimitBytes: 1000, AutoBlock: true, ResetGeneration: 1,
	}
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatal(err)
	}
	if len(backend.scripts) != 1 || strings.Contains(backend.scripts[0], " drop") ||
		!strings.Contains(backend.scripts[0], "tcp dport 443 counter") || !strings.Contains(backend.scripts[0], "udp sport 443 counter") {
		t.Fatalf("initial nftables rules:\n%s", strings.Join(backend.scripts, "\n---\n"))
	}
	backend.counters[trafficRuleComment(policy.ID, "in", "tcp")] = 600
	backend.counters[trafficRuleComment(policy.ID, "out", "tcp")] = 500
	now = now.Add(2 * time.Second)
	manager.collect(context.Background(), false)
	snapshot := manager.Snapshot()
	if len(snapshot) != 1 || snapshot[0].UsedBytes != 1100 || !snapshot[0].Blocked || !snapshot[0].EnforcementAvailable {
		t.Fatalf("blocked snapshot = %+v", snapshot)
	}
	if len(backend.scripts) != 2 || strings.Count(backend.scripts[1], " drop") != 4 {
		t.Fatalf("blocked nftables rules:\n%s", backend.scripts[len(backend.scripts)-1])
	}

	backend.counters[trafficRuleComment(policy.ID, "in", "tcp")] = 200
	now = time.Date(2026, 9, 1, 0, 0, 1, 0, time.UTC)
	manager.collect(context.Background(), false)
	snapshot = manager.Snapshot()
	if snapshot[0].UsedBytes != 0 || snapshot[0].Blocked || snapshot[0].PeriodStart.Format(time.DateOnly) != "2026-09-01" {
		t.Fatalf("reset snapshot = %+v", snapshot[0])
	}
	if strings.Contains(backend.scripts[len(backend.scripts)-1], " drop") {
		t.Fatalf("period reset did not remove blocking rules:\n%s", backend.scripts[len(backend.scripts)-1])
	}
}

func TestTrafficManagerPreservesUsageWhenOnlyLimitChanges(t *testing.T) {
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	backend := &fakeTrafficBackend{counters: map[string]uint64{}}
	manager := &TrafficManager{
		statePath: t.TempDir() + "/traffic-state.json", backend: backend,
		records: make(map[string]*trafficRecord), now: func() time.Time { return now },
	}
	policy := core.PortTrafficPolicy{
		ID: "trf_fedcba9876543210", AgentID: "agt_0123456789abcdef", Name: "ss inbound",
		Engine: core.EngineShadowsocksRust, Port: 8388, Protocol: core.TrafficProtocolTCP,
		Cycle: core.TrafficCycleYearly, CycleAnchor: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		LimitBytes: 2000, AutoBlock: true, ResetGeneration: 1,
	}
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatal(err)
	}
	backend.counters[trafficRuleComment(policy.ID, "in", "tcp")] = 1200
	now = now.Add(2 * time.Second)
	manager.collect(context.Background(), false)
	policy.LimitBytes = 1000
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatal(err)
	}
	snapshot := manager.Snapshot()
	if snapshot[0].UsedBytes != 1200 || !snapshot[0].Blocked {
		t.Fatalf("limit update snapshot = %+v", snapshot[0])
	}
}

func TestTrafficManagerCanMonitorWithoutAutomaticBlocking(t *testing.T) {
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	backend := &fakeTrafficBackend{counters: map[string]uint64{}}
	manager := &TrafficManager{
		statePath: t.TempDir() + "/traffic-state.json", backend: backend,
		records: make(map[string]*trafficRecord), now: func() time.Time { return now },
	}
	policy := core.PortTrafficPolicy{
		ID: "trf_2222222222222222", AgentID: "agt_0123456789abcdef", Name: "observe only",
		Engine: core.EngineXray, Port: 9443, Protocol: core.TrafficProtocolTCP,
		Cycle: core.TrafficCycleMonthly, CycleAnchor: now, LimitBytes: 100, AutoBlock: false, ResetGeneration: 1,
	}
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatal(err)
	}
	backend.counters[trafficRuleComment(policy.ID, "in", "tcp")] = 200
	now = now.Add(2 * time.Second)
	manager.collect(context.Background(), false)
	snapshot := manager.Snapshot()
	if len(snapshot) != 1 || snapshot[0].UsedBytes != 200 || snapshot[0].Blocked {
		t.Fatalf("observe-only snapshot = %+v", snapshot)
	}
	if strings.Contains(backend.scripts[len(backend.scripts)-1], " drop") {
		t.Fatalf("observe-only policy installed a drop rule:\n%s", backend.scripts[len(backend.scripts)-1])
	}
}

func TestParseNFTTrafficCounters(t *testing.T) {
	contents := []byte(`{"nftables":[{"rule":{"comment":"qch:trf_0123456789abcdef:in:tcp","expr":[{"match":{}},{"counter":{"packets":3,"bytes":4096}}]}},{"rule":{"comment":"foreign","expr":[{"counter":{"packets":1,"bytes":99}}]}}]}`)
	counters, err := parseNFTTrafficCounters(contents)
	if err != nil || counters["qch:trf_0123456789abcdef:in:tcp"] != 4096 || len(counters) != 1 {
		t.Fatalf("parsed counters = %+v, %v", counters, err)
	}
}

func TestTrafficManagerReportsUnavailableWithoutRejectingPolicies(t *testing.T) {
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	backend := &fakeTrafficBackend{counters: map[string]uint64{}, err: context.DeadlineExceeded}
	manager := &TrafficManager{
		statePath: t.TempDir() + "/traffic-state.json", backend: backend,
		records: make(map[string]*trafficRecord), now: func() time.Time { return now },
	}
	policy := core.PortTrafficPolicy{
		ID: "trf_1111111111111111", AgentID: "agt_0123456789abcdef", Name: "unavailable",
		Engine: core.EngineSingBox, Port: 24443, Protocol: core.TrafficProtocolUDP,
		Cycle: core.TrafficCycleMonthly, CycleAnchor: now, LimitBytes: 1 << 30, ResetGeneration: 1,
	}
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatalf("operational backend error rejected valid policy: %v", err)
	}
	snapshot := manager.Snapshot()
	if len(snapshot) != 1 || snapshot[0].EnforcementAvailable || snapshot[0].EnforcementError == "" {
		t.Fatalf("unavailable snapshot = %+v", snapshot)
	}
}

func TestTrafficRulesAreEngineIndependentForAllManagedCores(t *testing.T) {
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	backend := &fakeTrafficBackend{counters: map[string]uint64{}}
	manager := &TrafficManager{
		statePath: t.TempDir() + "/traffic-state.json", backend: backend,
		records: make(map[string]*trafficRecord), now: func() time.Time { return now },
	}
	agentID := "agt_0123456789abcdef"
	engines := []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust}
	ids := []string{"trf_0000000000000001", "trf_0000000000000002", "trf_0000000000000003", "trf_0000000000000004"}
	policies := make([]core.PortTrafficPolicy, 0, len(engines))
	for index, engine := range engines {
		policies = append(policies, core.PortTrafficPolicy{
			ID: ids[index], AgentID: agentID, Name: string(engine), Engine: engine,
			Port: 20001 + index, Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly,
			CycleAnchor: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), LimitBytes: 100 << 30, ResetGeneration: 1,
		})
	}
	if err := manager.SetPolicies(context.Background(), policies, agentID); err != nil {
		t.Fatal(err)
	}
	rules := backend.scripts[len(backend.scripts)-1]
	for index, engine := range engines {
		if !strings.Contains(rules, ids[index]) || !strings.Contains(rules, "dport "+strconv.Itoa(20001+index)) {
			t.Errorf("%s policy is missing from engine-independent rules:\n%s", engine, rules)
		}
	}
}
