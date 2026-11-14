//go:build linux

package agent

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func collectTrafficLockedForTest(t *testing.T, manager *TrafficManager, forceRules bool) {
	t.Helper()
	manager.mu.Lock()
	err := manager.collectLocked(context.Background(), forceRules)
	manager.mu.Unlock()
	if err != nil {
		t.Fatalf("collectLocked(forceRules=%v): %v", forceRules, err)
	}
}

func TestTrafficManagerSIGKILLChildHelper(t *testing.T) {
	stateDir := os.Getenv("QCH_TEST_TRAFFIC_SIGKILL_STATE_DIR")
	if stateDir == "" {
		return
	}
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	prevBootReader := trafficBootIDReader
	trafficBootIDReader = func() (string, error) { return "boot-sigkill-1", nil }
	defer func() { trafficBootIDReader = prevBootReader }()

	agentStatePath := filepath.Join(stateDir, "state.json")
	backend := &fakeTrafficBackend{counters: map[string]uint64{}}
	manager := NewTrafficManagerForServiceManager(agentStatePath, defaultSystemdServiceManager())
	manager.backend = backend
	manager.now = func() time.Time { return now }
	manager.checkpointEvery = time.Hour

	policy := core.PortTrafficPolicy{
		ID: "trf_0123456789abcdef", AgentID: "agt_0123456789abcdef", Name: "sigkill-test",
		Engine: core.EngineXray, Port: 443, Protocol: core.TrafficProtocolTCP,
		Cycle: core.TrafficCycleMonthly, CycleAnchor: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		LimitBytes: 1000, AutoBlock: true, ResetGeneration: 1,
	}
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatalf("child SetPolicies: %v", err)
	}
	record := manager.records[policy.ID]
	inKey := trafficCounterName(record, "in", "tcp")
	backend.counters[inKey] = 500
	now = now.Add(time.Second)
	collectTrafficLockedForTest(t, manager, true)
	manager.mu.Lock()
	flushErr := manager.saveWithForceLocked(true)
	manager.mu.Unlock()
	if flushErr != nil {
		t.Fatalf("child initial saveWithForceLocked: %v", flushErr)
	}

	// Simulate +400 uncheckpointed bytes observed in memory & sent to panel before SIGKILL.
	backend.counters[inKey] = 900
	now = now.Add(time.Second)
	collectTrafficLockedForTest(t, manager, false)
	snap := manager.Snapshot()
	if len(snap) != 1 || snap[0].ReceivedBytes != 900 || snap[0].LifetimeReceivedBytes != 900 {
		t.Fatalf("child snapshot = %+v, want rx=900", snap)
	}

	fmt.Printf("READY:%s\n", record.CounterEpoch)
	select {}
}

func TestTrafficManagerSIGKILLBeforeCheckpointRecoversFromKernelCounters(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestTrafficManagerSIGKILLChildHelper$")
	cmd.Env = append(os.Environ(), "QCH_TEST_TRAFFIC_SIGKILL_STATE_DIR="+stateDir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	reader := bufio.NewScanner(stdout)
	var readyLine string
	doneRead := make(chan bool, 1)
	go func() {
		for reader.Scan() {
			line := reader.Text()
			if strings.HasPrefix(line, "READY:") {
				readyLine = line
				doneRead <- true
				return
			}
		}
		doneRead <- false
	}()
	select {
	case ok := <-doneRead:
		if !ok {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("child exited before reporting READY: %v", reader.Err())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("timed out waiting for SIGKILL helper child")
	}
	epoch := strings.TrimPrefix(readyLine, "READY:")
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("SIGKILL child: %v", err)
	}
	_ = cmd.Wait()

	// Confirm the 15s checkpoint had NOT written the +400 uncheckpointed delta.
	agentStatePath := filepath.Join(stateDir, "state.json")
	persisted, err := loadTrafficState(filepath.Join(stateDir, "traffic-state.json"))
	if err != nil {
		t.Fatalf("loadTrafficState after SIGKILL: %v", err)
	}
	rec := persisted.Records["trf_0123456789abcdef"]
	if rec == nil || rec.ReceivedBytes != 500 || rec.CounterEpoch != epoch {
		t.Fatalf("persisted record after SIGKILL = %+v; want rx=500 epoch=%s", rec, epoch)
	}

	// Reboot does NOT happen; same bootID retains kernel counters (900 pre-kill + 200 post-kill = 1100).
	prevBootReader := trafficBootIDReader
	trafficBootIDReader = func() (string, error) { return "boot-sigkill-1", nil }
	defer func() { trafficBootIDReader = prevBootReader }()

	now := time.Date(2026, 8, 21, 0, 1, 0, 0, time.UTC)
	manager := NewTrafficManagerForServiceManager(agentStatePath, defaultSystemdServiceManager())
	recoveredRec := manager.records["trf_0123456789abcdef"]
	if recoveredRec == nil || recoveredRec.CounterEpoch != epoch {
		t.Fatalf("restarted epoch = %+v, want %s", recoveredRec, epoch)
	}
	inKey := trafficCounterName(recoveredRec, "in", "tcp")
	backend := &fakeTrafficBackend{
		exists: true,
		counters: map[string]uint64{
			inKey:             1100,
			"handle:" + inKey: recoveredRec.KernelCounters["handle:"+inKey],
			trafficCounterName(recoveredRec, "out", "tcp"): 0,
		},
	}
	manager.backend = backend
	manager.now = func() time.Time { return now }

	policy := core.PortTrafficPolicy{
		ID: "trf_0123456789abcdef", AgentID: "agt_0123456789abcdef", Name: "sigkill-test",
		Engine: core.EngineXray, Port: 443, Protocol: core.TrafficProtocolTCP,
		Cycle: core.TrafficCycleMonthly, CycleAnchor: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		LimitBytes: 1000, AutoBlock: true, ResetGeneration: 1,
		PeriodStart: &rec.PeriodStart, PeriodEnd: &rec.PeriodEnd, ReceivedBytes: 900, UsedBytes: 900,
	}
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatalf("restart SetPolicies: %v", err)
	}
	collectTrafficLockedForTest(t, manager, true)
	snap := manager.Snapshot()
	if len(snap) != 1 || snap[0].CounterEpoch != epoch || snap[0].ReceivedBytes != 1100 || snap[0].LifetimeReceivedBytes != 1100 || !snap[0].Blocked {
		t.Fatalf("post-SIGKILL snapshot = %+v; want epoch=%s rx=1100 lifetime=1100 blocked=true", snap, epoch)
	}
}

func TestTrafficManagerRebootSyncsServerAheadQuotaAndTriggersAutoBlock(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	agentStatePath := filepath.Join(stateDir, "state.json")
	periodStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0)
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)

	policy := core.PortTrafficPolicy{
		ID: "trf_0123456789abcdef", AgentID: "agt_0123456789abcdef", Name: "reboot-sync",
		Engine: core.EngineXray, Port: 443, Protocol: core.TrafficProtocolTCP,
		Cycle: core.TrafficCycleMonthly, CycleAnchor: periodStart,
		LimitBytes: 1000, AutoBlock: true, ResetGeneration: 1,
	}
	oldEpoch := "0123456789abcdef0123456789abcdef"
	if err := saveTrafficState(filepath.Join(stateDir, "traffic-state.json"), trafficState{
		BootID: "boot-old",
		Records: map[string]*trafficRecord{
			policy.ID: {
				Policy:                policy,
				ReceivedBytes:         500,
				LifetimeReceivedBytes: 500,
				LastKernelReceived:    500,
				PeriodStart:           periodStart,
				PeriodEnd:             periodEnd,
				CounterEpoch:          oldEpoch,
				KernelCounters: map[string]uint64{
					"qch_" + policy.ID + "_" + oldEpoch + "_in_tcp":  500,
					"qch_" + policy.ID + "_" + oldEpoch + "_out_tcp": 0,
				},
			},
		},
	}); err != nil {
		t.Fatalf("seed stale pre-reboot checkpoint: %v", err)
	}

	prevBootReader := trafficBootIDReader
	trafficBootIDReader = func() (string, error) { return "boot-new", nil }
	defer func() { trafficBootIDReader = prevBootReader }()

	manager := NewTrafficManagerForServiceManager(agentStatePath, defaultSystemdServiceManager())
	backend := &fakeTrafficBackend{exists: false, counters: map[string]uint64{}}
	manager.backend = backend
	manager.now = func() time.Time { return now }

	// Reinstalled epoch differs immediately on boot change and marks server sync pending.
	bootRec := manager.records[policy.ID]
	if bootRec == nil || bootRec.CounterEpoch == oldEpoch || !bootRec.NeedsServerQuotaSync || bootRec.QuotaBaselineBytes != 500 {
		t.Fatalf("post-reboot record = %+v; want new epoch != %s, NeedsServerQuotaSync=true, QuotaBaselineBytes=500", bootRec, oldEpoch)
	}

	// Simulate 50 offline bytes counted on new epoch BEFORE SetPolicies syncs server total (900).
	collectTrafficLockedForTest(t, manager, false)
	inKey := trafficCounterName(manager.records[policy.ID], "in", "tcp")
	backend.counters[inKey] = 50
	now = now.Add(time.Second)
	collectTrafficLockedForTest(t, manager, false)
	if manager.records[policy.ID].Blocked {
		t.Fatal("should not be blocked at 500+50=550 < 1000 yet")
	}

	// Server reconnects and sends policy showing 900 bytes billed before reboot in the same period.
	serverPolicy := policy
	serverPolicy.PeriodStart, serverPolicy.PeriodEnd = &periodStart, &periodEnd
	serverPolicy.ReceivedBytes, serverPolicy.UsedBytes = 900, 900
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{serverPolicy}, serverPolicy.AgentID); err != nil {
		t.Fatalf("SetPolicies after reboot: %v", err)
	}
	if got := manager.records[policy.ID]; got.QuotaBaselineBytes != 900 || got.NeedsServerQuotaSync || got.ReceivedBytes != 50 || got.Blocked {
		t.Fatalf("after server sync = %+v; want QuotaBaseline=900, Received=50, Blocked=false", got)
	}

	// +60 bytes pushes total past 1000 (server 900 + post-reboot 110 = 1010) and AutoBlock fires.
	backend.counters[inKey] = 110
	now = now.Add(time.Second)
	collectTrafficLockedForTest(t, manager, false)
	snap := manager.Snapshot()
	if len(snap) != 1 || snap[0].CounterEpoch == oldEpoch || snap[0].ReceivedBytes != 110 || snap[0].LifetimeReceivedBytes != 110 || !snap[0].Blocked {
		t.Fatalf("snapshot after hitting server-synced limit = %+v; want rx=110 blocked=true", snap)
	}
}

func TestTrafficManagerKernelTableWipeAndCrossPeriodRolloverRecovery(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	periodStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	backend := &fakeTrafficBackend{counters: map[string]uint64{}}
	manager := &TrafficManager{
		statePath: filepath.Join(stateDir, "traffic-state.json"),
		backend:   backend,
		records:   make(map[string]*trafficRecord),
		now:       func() time.Time { return now },
		bootID:    "same-boot",
	}
	policy := core.PortTrafficPolicy{
		ID: "trf_0123456789abcdef", AgentID: "agt_0123456789abcdef", Name: "table-wipe",
		Engine: core.EngineXray, Port: 443, Protocol: core.TrafficProtocolTCP,
		Cycle: core.TrafficCycleMonthly, CycleAnchor: periodStart,
		LimitBytes: 1000, AutoBlock: true, ResetGeneration: 1,
	}
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatal(err)
	}
	firstEpoch := manager.records[policy.ID].CounterEpoch
	backend.counters[trafficCounterName(manager.records[policy.ID], "in", "tcp")] = 400
	now = now.Add(time.Second)
	collectTrafficLockedForTest(t, manager, false)

	// Simulate external firewall reload deleting table while daemon stays alive.
	backend.exists = false
	backend.counters = map[string]uint64{}
	now = now.Add(time.Second)
	collectTrafficLockedForTest(t, manager, false)
	wiped := manager.records[policy.ID]
	if wiped.CounterEpoch == firstEpoch || wiped.QuotaBaselineBytes != 400 || wiped.ReceivedBytes != 0 || !wiped.NeedsServerQuotaSync {
		t.Fatalf("after table wipe = %+v; want rotated epoch != %s, baseline=400", wiped, firstEpoch)
	}
	backend.counters[trafficCounterName(wiped, "in", "tcp")] = 600
	now = now.Add(time.Second)
	collectTrafficLockedForTest(t, manager, false)
	if !manager.records[policy.ID].Blocked {
		t.Fatal("expected AutoBlock after 400 baseline + 600 post-wipe >= 1000")
	}

	// Now jump across calendar period boundary (2026-09-01) with stale server P1 bytes:
	// P1 baseline must reset to 0 and un-block for P2 without carrying P1 quota over.
	p1End := periodStart.AddDate(0, 1, 0)
	now = time.Date(2026, 9, 1, 0, 5, 0, 0, time.UTC)
	staleP1Policy := policy
	staleP1Policy.PeriodStart, staleP1Policy.PeriodEnd = &periodStart, &p1End
	staleP1Policy.ReceivedBytes, staleP1Policy.UsedBytes = 900, 900
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{staleP1Policy}, policy.AgentID); err != nil {
		t.Fatalf("SetPolicies across period rollover: %v", err)
	}
	rolled := manager.records[policy.ID]
	if rolled.QuotaBaselineBytes != 0 || rolled.ReceivedBytes != 0 || rolled.Blocked || !rolled.PeriodStart.Equal(p1End) {
		t.Fatalf("after period rollover across reboot/wipe = %+v; want baseline=0, rx=0, blocked=false, periodStart=%v", rolled, p1End)
	}
}
