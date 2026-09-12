package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func sharedTrafficFixture(t *testing.T) (*TrafficManager, *fakeTrafficBackend, []core.PortTrafficPolicy, *time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	backend := &fakeTrafficBackend{counters: map[string]uint64{}}
	manager := &TrafficManager{statePath: filepath.Join(t.TempDir(), "traffic-state.json"), backend: backend,
		records: map[string]*trafficRecord{}, now: func() time.Time { return now }}
	policies := []core.PortTrafficPolicy{
		{ID: "trf_0000000000000001", AgentID: "agt_shared", Name: "one", Engine: core.EngineMihomo, Port: 21001,
			Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly, CycleAnchor: core.UTCDate(now),
			LimitBytes: 0, AutoBlock: true, ResetGeneration: 1,
			SharedQuota: &core.SharedTrafficQuota{ID: "shr_0000000000000001", Engines: []core.Engine{core.EngineMihomo}, LimitBytes: 100}},
		{ID: "trf_0000000000000002", AgentID: "agt_shared", Name: "two", Engine: core.EngineMihomo, Port: 21002,
			Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly, CycleAnchor: core.UTCDate(now),
			LimitBytes: 0, AutoBlock: true, ResetGeneration: 1,
			SharedQuota: &core.SharedTrafficQuota{ID: "shr_0000000000000001", Engines: []core.Engine{core.EngineMihomo}, LimitBytes: 100}},
		{ID: "trf_0000000000000003", AgentID: "agt_shared", Name: "other user", Engine: core.EngineXray, Port: 21003,
			Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly, CycleAnchor: core.UTCDate(now),
			LimitBytes: 0, AutoBlock: true, ResetGeneration: 1,
			SharedQuota: &core.SharedTrafficQuota{ID: "shr_0000000000000002", Engines: []core.Engine{core.EngineXray}, LimitBytes: 100}},
	}
	return manager, backend, policies, &now
}

func TestSharedTrafficCombinesPortsAndSurvivesCalendarRestartAndTopup(t *testing.T) {
	manager, backend, policies, now := sharedTrafficFixture(t)
	ctx := context.Background()
	manager.nativeSource = func(context.Context, core.Engine) (nativeAccountingSnapshot, error) {
		t.Fatal("shared billing attempted user-configurable native counters")
		return nativeAccountingSnapshot{}, nil
	}
	if err := manager.SetPolicies(ctx, policies, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	if policies[0].LimitBytes != 0 || !policies[0].AutoBlock {
		t.Fatal("SetPolicies mutated the wire envelope")
	}
	for _, policy := range policies[:2] {
		record := manager.records[policy.ID]
		backend.counters[trafficCounterName(record, "in", "tcp")] = 60
	}
	*now = now.Add(time.Second)
	manager.collect(ctx, false)
	snapshot := manager.Snapshot()
	if !snapshot[0].Blocked || !snapshot[1].Blocked || snapshot[2].Blocked ||
		snapshot[0].ShareUsedBytes != 60 || snapshot[1].ShareUsedBytes != 60 {
		t.Fatalf("shared aggregate failed: %+v", snapshot)
	}
	*now = now.AddDate(0, 1, 0)
	manager.collect(ctx, false)
	if snapshot = manager.Snapshot(); !snapshot[0].Blocked || snapshot[0].ShareUsedBytes != 60 {
		t.Fatalf("calendar rollover reset the package: %+v", snapshot)
	}
	loaded, err := loadTrafficState(manager.statePath)
	if err != nil || validateLoadedTrafficState(loaded, *now) != nil {
		t.Fatalf("persisted shared state: %+v %v", loaded, err)
	}
	restarted := &TrafficManager{statePath: manager.statePath, records: loaded.Records, backend: backend, now: manager.now}
	for i := range policies[:2] {
		quota := *policies[i].SharedQuota
		quota.LimitBytes, quota.UsedBytes, quota.PortUsedBytes = 200, 120, 60
		policies[i].SharedQuota = &quota
	}
	if err := restarted.SetPolicies(ctx, policies, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	if snapshot = restarted.Snapshot(); snapshot[0].Blocked || snapshot[1].Blocked || snapshot[0].ShareUsedBytes != 60 {
		t.Fatalf("top-up/restart lost usage or failed to unblock: %+v", snapshot)
	}
	// Changing a per-port calendar generation must never mint more shared
	// credit, even if an older server sends such a reset.
	policies[0].ResetGeneration++
	if err := restarted.SetPolicies(ctx, policies, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	if restarted.records[policies[0].ID].ShareUsedBytes != 60 || sharedTrafficUsed(restarted.records, policies[0].SharedQuota.ID) != 120 {
		t.Fatal("port reset changed cumulative allowance")
	}
	for i := range policies[:2] {
		quota := *policies[i].SharedQuota
		quota.Revoked = true
		policies[i].SharedQuota = &quota
	}
	if err := restarted.SetPolicies(ctx, policies, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	if snapshot = restarted.Snapshot(); !snapshot[0].Blocked || !snapshot[1].Blocked || snapshot[2].Blocked {
		t.Fatalf("revocation affected wrong user's ports: %+v", snapshot)
	}
}

func TestSharedTrafficEnforcesEngineRevocationWithoutResettingLedger(t *testing.T) {
	manager, backend, policies, now := sharedTrafficFixture(t)
	ctx := context.Background()
	policies[1].Engine = core.EngineXray
	for index := range policies[:2] {
		policies[index].SharedQuota.Engines = []core.Engine{core.EngineMihomo, core.EngineXray}
		policies[index].SharedQuota.LimitBytes = 1000
	}
	if err := manager.SetPolicies(ctx, policies, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	for _, policy := range policies[:2] {
		record := manager.records[policy.ID]
		backend.counters[trafficCounterName(record, "in", "tcp")] = 40
	}
	*now = now.Add(time.Second)
	manager.collect(ctx, false)
	for index := range policies[:2] {
		policies[index].SharedQuota.Engines = []core.Engine{core.EngineMihomo}
	}
	if err := manager.SetPolicies(ctx, policies, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	got := manager.Snapshot()
	if got[0].Blocked || !got[1].Blocked || got[2].Blocked {
		t.Fatalf("engine revocation affected wrong listeners: %+v", got)
	}
	if sharedTrafficUsed(manager.records, policies[0].SharedQuota.ID) != 80 {
		t.Fatal("engine revocation reset cumulative traffic")
	}
	if err := manager.authorizeSharedDeployment(ctx, policies[0].SharedQuota.ID,
		[]core.PortTrafficEndpoint{{Engine: core.EngineXray, Port: 21002}}); err == nil {
		t.Fatal("revoked engine could redeploy using its retained reservation")
	}
	if err := manager.authorizeSharedDeployment(ctx, policies[0].SharedQuota.ID,
		[]core.PortTrafficEndpoint{{Engine: core.EngineMihomo, Port: 21001}}); err != nil {
		t.Fatalf("authorized engine became unusable: %v", err)
	}
	if err := manager.authorizeSharedDeployment(ctx, policies[0].SharedQuota.ID, nil); err == nil {
		t.Fatal("empty listener set bypassed shared authorization")
	}
}

func TestSharedTrafficRecoversServerTotalAndRejectsUnsafeExecution(t *testing.T) {
	manager, backend, policies, _ := sharedTrafficFixture(t)
	ctx := context.Background()
	for i := range policies[:2] {
		quota := *policies[i].SharedQuota
		quota.UsedBytes, quota.PortUsedBytes = 120, 60
		policies[i].SharedQuota = &quota
	}
	if err := manager.SetPolicies(ctx, policies, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	if !manager.Snapshot()[0].Blocked || sharedTrafficUsed(manager.records, policies[0].SharedQuota.ID) != 120 {
		t.Fatal("missing local checkpoint reset acknowledged usage")
	}
	endpoints := []core.PortTrafficEndpoint{{Engine: core.EngineMihomo, Port: 21001}}
	if err := manager.authorizeSharedDeployment(ctx, policies[0].SharedQuota.ID, endpoints); err == nil {
		t.Fatal("exhausted local allowance permitted deployment")
	}
	if err := manager.authorizeSharedDeployment(ctx, policies[2].SharedQuota.ID, endpoints); err == nil {
		t.Fatal("another allocation's port permitted deployment")
	}
	var halted []core.Engine
	manager.haltShared = func(_ context.Context, engine core.Engine) error {
		halted = append(halted, engine)
		return nil
	}
	backend.err = errors.New("nftables is unavailable")
	if err := manager.authorizeSharedDeployment(ctx, policies[2].SharedQuota.ID, []core.PortTrafficEndpoint{{Engine: core.EngineXray, Port: 21003}}); err == nil {
		t.Fatal("unavailable enforcement permitted deployment")
	}
	if len(halted) != 2 {
		t.Fatalf("failed accounting did not stop each shared core once: %+v", halted)
	}
}

func TestSharedTrafficValidatesAtomicAllocationSnapshots(t *testing.T) {
	manager, _, policies, _ := sharedTrafficFixture(t)
	policies[1].SharedQuota.LimitBytes++
	if err := manager.SetPolicies(context.Background(), policies, "agt_shared"); err == nil || len(manager.records) != 0 {
		t.Fatal("inconsistent group totals partially applied")
	}
	policies[1].SharedQuota.LimitBytes--
	policies[0].Protocol = core.TrafficProtocolTCP
	if err := manager.SetPolicies(context.Background(), policies, "agt_shared"); err == nil {
		t.Fatal("partial transport coverage accepted")
	}
}

func TestSharedTrafficMissingCheckpointStopsGuardedCoresUntilPolicyRecovery(t *testing.T) {
	manager, backend, policies, _ := sharedTrafficFixture(t)
	if err := manager.SetPolicies(context.Background(), policies, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(manager.statePath); err != nil {
		t.Fatal(err)
	}
	recovered := NewTrafficManager(filepath.Join(filepath.Dir(manager.statePath), "agent-state.json"))
	recovered.backend, recovered.now = backend, manager.now
	halted := map[core.Engine]bool{}
	recovered.haltShared = func(_ context.Context, engine core.Engine) error {
		halted[engine] = true
		return nil
	}
	if err := recovered.collectLocked(context.Background(), true); err == nil {
		t.Fatal("lost offline checkpoint silently removed enforcement")
	}
	if !halted[core.EngineMihomo] || !halted[core.EngineXray] || len(halted) != 2 {
		t.Fatalf("activation guard did not stop its shared cores: %+v", halted)
	}
	for index := range policies[:2] {
		quota := *policies[index].SharedQuota
		quota.UsedBytes, quota.PortUsedBytes = 120, 60
		policies[index].SharedQuota = &quota
	}
	if err := recovered.SetPolicies(context.Background(), policies, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	if recovered.awaitingPolicies || !recovered.Snapshot()[0].Blocked || sharedTrafficUsed(recovered.records, policies[0].SharedQuota.ID) != 120 {
		t.Fatal("authenticated recovery lost the acknowledged cumulative quota")
	}
}

func TestSharedTrafficIdentityRevocationMustStopBeforeRemovingRules(t *testing.T) {
	manager, _, policies, _ := sharedTrafficFixture(t)
	ctx := context.Background()
	if err := manager.SetPolicies(ctx, policies, "agt_shared"); err != nil {
		t.Fatal(err)
	}
	manager.haltShared = func(context.Context, core.Engine) error { return errors.New("stop failed") }
	if err := manager.ClearPolicies(ctx); err == nil || len(manager.records) != len(policies) {
		t.Fatal("identity revocation removed rules while shared service was still running")
	}
	var stopped int
	manager.haltShared = func(context.Context, core.Engine) error { stopped++; return nil }
	if err := manager.ClearPolicies(ctx); err != nil || len(manager.records) != 0 || stopped != 2 {
		t.Fatalf("stopped services were not safely cleared: %d %v", stopped, err)
	}
	guard, err := loadTrafficState(manager.sharedGuardPath())
	if err != nil || len(guard.Records) != 0 {
		t.Fatalf("revocation retained a stale activation guard: %+v %v", guard, err)
	}
}

func TestSharedTrafficFirstGuardFailureStopsIncomingCoresAndRetries(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-checkpoint", true: "legacy-checkpoint"}[legacy], func(t *testing.T) {
			manager, backend, policies, _ := sharedTrafficFixture(t)
			ctx := context.Background()
			if legacy {
				previous := append([]core.PortTrafficPolicy(nil), policies...)
				for i := range previous {
					previous[i].SharedQuota = nil
					previous[i].LimitBytes = 1000
				}
				if err := manager.SetPolicies(ctx, previous, "agt_shared"); err != nil {
					t.Fatal(err)
				}
				// Exercise both reused records and a new accounting generation.
				policies[0].ResetGeneration++
			}
			if err := os.Mkdir(manager.sharedGuardPath(), 0o700); err != nil {
				t.Fatal(err)
			}
			stopped := map[core.Engine]int{}
			manager.haltShared = func(_ context.Context, engine core.Engine) error {
				stopped[engine]++
				return errors.New("temporary stop failure")
			}
			if err := manager.SetPolicies(ctx, policies, "agt_shared"); err == nil || !manager.awaitingPolicies {
				t.Fatal("failed first activation did not enter recovery")
			}
			if stopped[core.EngineMihomo] != 1 || stopped[core.EngineXray] != 1 {
				t.Fatalf("incoming shared cores were not stopped: %+v", stopped)
			}
			if hasSharedTraffic(manager.records) {
				t.Fatal("failed activation partially mutated the old policy snapshot")
			}
			replacements := len(backend.scripts)
			manager.haltShared = func(_ context.Context, engine core.Engine) error {
				stopped[engine]++
				return nil
			}
			manager.collect(ctx, false)
			if stopped[core.EngineMihomo] != 2 || stopped[core.EngineXray] != 2 || len(backend.scripts) != replacements {
				t.Fatal("offline recovery failed to retry stopping cores or replaced existing rules")
			}
			if err := os.Remove(manager.sharedGuardPath()); err != nil {
				t.Fatal(err)
			}
			if err := manager.SetPolicies(ctx, policies, "agt_shared"); err != nil || manager.awaitingPolicies {
				t.Fatalf("authenticated recovery failed: %v", err)
			}
		})
	}
}

func TestSharedTrafficActivationGuardRejectsOlderValidCheckpoint(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		t.Run(map[bool]string{true: "before-sharing", false: "before-revocation"}[legacy], func(t *testing.T) {
			manager, backend, policies, _ := sharedTrafficFixture(t)
			ctx := context.Background()
			previous := append([]core.PortTrafficPolicy(nil), policies...)
			if legacy {
				for i := range previous {
					previous[i].SharedQuota = nil
					previous[i].LimitBytes = 1000
				}
			}
			if err := manager.SetPolicies(ctx, previous, "agt_shared"); err != nil {
				t.Fatal(err)
			}
			checkpoint, err := loadTrafficState(manager.statePath)
			if err != nil {
				t.Fatal(err)
			}
			for i := range policies {
				quota := *policies[i].SharedQuota
				quota.Revoked = true
				policies[i].SharedQuota = &quota
			}
			if err := manager.SetPolicies(ctx, policies, "agt_shared"); err != nil {
				t.Fatal(err)
			}
			// Simulate a crash after guard persistence but before the accounting
			// checkpoint was replaced: the previous checkpoint is still valid.
			if err := saveTrafficState(manager.statePath, checkpoint); err != nil {
				t.Fatal(err)
			}
			restarted := NewTrafficManager(filepath.Join(filepath.Dir(manager.statePath), "agent-state.json"))
			restarted.backend, restarted.now = backend, manager.now
			stopped := map[core.Engine]bool{}
			restarted.haltShared = func(_ context.Context, engine core.Engine) error {
				stopped[engine] = true
				return nil
			}
			replacements := len(backend.scripts)
			if err := restarted.collectLocked(ctx, true); err == nil || !restarted.awaitingPolicies {
				t.Fatal("older valid checkpoint overrode the activation guard")
			}
			if !stopped[core.EngineMihomo] || !stopped[core.EngineXray] || len(backend.scripts) != replacements {
				t.Fatal("older checkpoint cleared enforcement without stopping its shared cores")
			}
			if err := restarted.SetPolicies(ctx, policies, "agt_shared"); err != nil || restarted.awaitingPolicies || !restarted.Snapshot()[0].Blocked {
				t.Fatalf("authenticated recovery did not restore revocation: %v", err)
			}
			if err := restarted.SetPolicies(ctx, nil, "agt_shared"); err != nil {
				t.Fatal(err)
			}
			cleared := NewTrafficManager(filepath.Join(filepath.Dir(manager.statePath), "agent-state.json"))
			if cleared.awaitingPolicies {
				t.Fatal("removing the last shared allocation retained a stale guard")
			}
		})
	}
}
