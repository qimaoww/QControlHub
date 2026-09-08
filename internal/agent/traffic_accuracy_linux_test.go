package agent

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func newAccuracyTrafficManager(t *testing.T) (*TrafficManager, *fakeTrafficBackend, *time.Time, core.PortTrafficPolicy) {
	t.Helper()
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	backend := &fakeTrafficBackend{}
	manager := &TrafficManager{statePath: t.TempDir() + "/traffic-state.json", backend: backend,
		records: make(map[string]*trafficRecord), now: func() time.Time { return now }}
	policy := core.PortTrafficPolicy{ID: "trf_0123456789abcdef", AgentID: "agt_0123456789abcdef", Name: "accuracy",
		Engine: core.EngineXray, Port: 443, Protocol: core.TrafficProtocolBoth, Cycle: core.TrafficCycleMonthly,
		CycleAnchor: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), LimitBytes: math.MaxInt64, ResetGeneration: 1}
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatal(err)
	}
	return manager, backend, &now, policy
}

func TestTrafficNamedCountersSurviveRuleTransaction(t *testing.T) {
	manager, backend, now, policy := newAccuracyTrafficManager(t)
	key := trafficCounterName(manager.records[policy.ID], "in", "tcp")
	backend.counters[key] = 100
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	backend.onReplace = func() { backend.counters[key] += 70 }
	policy.Name = "renamed"
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatal(err)
	}
	backend.onReplace = nil
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	if got := manager.Snapshot()[0].ReceivedBytes; got != 170 {
		t.Fatalf("bytes arriving during replacement lost or duplicated: %d", got)
	}
	if strings.Contains(backend.scripts[len(backend.scripts)-1], "delete table") {
		t.Fatal("rule replacement destroyed counter objects")
	}
	if len(backend.scripts) != 2 {
		t.Fatalf("healthy periodic collection rewrote rules: %d", len(backend.scripts))
	}
}

func TestTrafficCountersResetIndependently(t *testing.T) {
	manager, backend, now, policy := newAccuracyTrafficManager(t)
	record := manager.records[policy.ID]
	tcp, udp := trafficCounterName(record, "in", "tcp"), trafficCounterName(record, "in", "udp")
	backend.counters[tcp], backend.counters[udp] = 100, 1000
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	// The combined sum still grows; subtracting combined baselines loses 100.
	backend.counters[tcp], backend.counters[udp] = 20, 1200
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	if got := manager.Snapshot()[0].ReceivedBytes; got != 1320 {
		t.Fatalf("independent TCP/UDP reset = %d, want 1320", got)
	}
	// Recreated objects can already exceed their old value when read.
	backend.counters["handle:"+tcp]++
	backend.counters[tcp] = 200
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	if got := manager.Snapshot()[0].ReceivedBytes; got != 1520 {
		t.Fatalf("counter recreation = %d, want 1520", got)
	}
}

func TestTrafficFailedRuleCommitRetriesWithoutPretendingBlocked(t *testing.T) {
	manager, backend, now, policy := newAccuracyTrafficManager(t)
	key := trafficCounterName(manager.records[policy.ID], "in", "tcp")
	policy.AutoBlock, policy.LimitBytes = true, 100
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatal(err)
	}
	backend.counters[key] = 110
	backend.replaceErr = errors.New("transaction failed")
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	if usage := manager.Snapshot()[0]; usage.Blocked || usage.EnforcementAvailable || usage.ReceiveBPS != 0 {
		t.Fatalf("failed enforcement was reported as active: %+v", usage)
	}
	backend.counters[key] = 150
	backend.replaceErr = nil
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	if usage := manager.Snapshot()[0]; !usage.Blocked || !usage.EnforcementAvailable || usage.ReceivedBytes != 150 {
		t.Fatalf("failed commit was not retried or lost still-allowed bytes: %+v", usage)
	}
	// The final allowed packet can arrive between the counter read and drop.
	backend.counters[key] = 160
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	if got := manager.Snapshot()[0].ReceivedBytes; got != 160 {
		t.Fatalf("final pre-drop bytes were lost: %d", got)
	}
}

func TestTrafficCheckpointRecoveryAndCalendarLifetime(t *testing.T) {
	manager, backend, now, policy := newAccuracyTrafficManager(t)
	key := trafficCounterName(manager.records[policy.ID], "in", "tcp")
	backend.counters[key] = 100
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	state, err := loadTrafficState(manager.statePath)
	if err != nil || state.Records[policy.ID].ReceivedBytes != 100 {
		t.Fatalf("published sample was not checkpointed: %+v, %v", state, err)
	}
	// Restore from disk while nftables continues counting during Agent downtime.
	manager.records = state.Records
	backend.counters[key] = 140
	*now = now.Add(time.Second)
	manager.collect(context.Background(), true)
	if got := manager.Snapshot()[0].ReceivedBytes; got != 140 {
		t.Fatalf("restart recovery double counted or lost usage: %d", got)
	}
	backend.counters[key] = 155
	*now = time.Date(2026, 9, 1, 0, 0, 1, 0, time.UTC)
	manager.collect(context.Background(), false)
	if usage := manager.Snapshot()[0]; usage.ReceivedBytes != 15 || usage.LifetimeReceivedBytes != 155 {
		t.Fatalf("calendar rollover discarded crossing bytes or lifetime: %+v", usage)
	}
}

func TestTrafficMissingRulesRepairWithoutCounterReset(t *testing.T) {
	manager, backend, now, policy := newAccuracyTrafficManager(t)
	key := trafficCounterName(manager.records[policy.ID], "in", "tcp")
	backend.counters[key] = 50
	delete(backend.counters, "rule:"+key)
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	if backend.counters[key] != 50 || backend.counters["rule:"+key] != 1 || len(backend.scripts) != 2 {
		t.Fatalf("missing rule repair reset accounting: %+v", backend.counters)
	}
	backend.counters[key] = 75
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	if got := manager.Snapshot()[0].ReceivedBytes; got != 75 {
		t.Fatalf("repair double counted: %d", got)
	}
}

func TestTrafficCheckpointFailureRetriesWhileIdle(t *testing.T) {
	manager, backend, now, policy := newAccuracyTrafficManager(t)
	path := manager.statePath
	manager.statePath = t.TempDir() // Renaming a file onto a directory fails.
	backend.counters[trafficCounterName(manager.records[policy.ID], "in", "tcp")] = 42
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	if manager.Snapshot()[0].EnforcementAvailable || !manager.dirty {
		t.Fatal("failed checkpoint was acknowledged or forgotten")
	}
	manager.statePath = path
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	state, err := loadTrafficState(path)
	if err != nil || state.Records[policy.ID].ReceivedBytes != 42 || manager.dirty || !manager.Snapshot()[0].EnforcementAvailable {
		t.Fatalf("idle checkpoint retry failed: %+v, %v", state, err)
	}
}

func TestTrafficRecoversQuotaFromDatabaseWithoutRebilling(t *testing.T) {
	manager, backend, now, policy := newAccuracyTrafficManager(t)
	start, end, _ := core.TrafficPeriodAt(policy.CycleAnchor, policy.Cycle, *now)
	policy.PeriodStart, policy.PeriodEnd = &start, &end
	policy.ReceivedBytes, policy.SentBytes, policy.UsedBytes = 600, 300, 900
	policy.LimitBytes, policy.AutoBlock = 1000, true
	// A fresh Agent has no local baseline but receives the last database total.
	manager.records = make(map[string]*trafficRecord)
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatal(err)
	}
	if usage := manager.Snapshot()[0]; usage.UsedBytes != 0 || usage.LifetimeReceivedBytes != 0 || usage.Blocked {
		t.Fatalf("database baseline was re-billed: %+v", usage)
	}
	backend.counters[trafficCounterName(manager.records[policy.ID], "in", "tcp")] = 150
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	if usage := manager.Snapshot()[0]; usage.UsedBytes != 150 || !usage.Blocked {
		t.Fatalf("recovered quota ignored already billed 900 bytes: %+v", usage)
	}
	*now = end.Add(time.Second)
	manager.collect(context.Background(), false)
	if usage := manager.Snapshot()[0]; usage.Blocked || usage.UsedBytes != 0 || manager.records[policy.ID].QuotaBaselineBytes != 0 {
		t.Fatalf("old quota baseline survived calendar reset: %+v", usage)
	}
}

func TestTrafficIdenticalPoliciesDoNotRewriteRules(t *testing.T) {
	manager, backend, _, policy := newAccuracyTrafficManager(t)
	if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
		t.Fatal(err)
	}
	if len(backend.scripts) != 1 {
		t.Fatalf("identical policy refresh rebuilt rules: %d", len(backend.scripts))
	}
}

func TestTrafficShutdownWaitsForFinalCheckpoint(t *testing.T) {
	manager, backend, _, policy := newAccuracyTrafficManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := manager.Start(ctx)
	manager.mu.Lock()
	backend.counters[trafficCounterName(manager.records[policy.ID], "in", "tcp")] = 123
	manager.mu.Unlock()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("traffic shutdown did not complete")
	}
	state, err := loadTrafficState(manager.statePath)
	if err != nil || state.Records[policy.ID].ReceivedBytes != 123 {
		t.Fatalf("shutdown did not persist final sample: %+v, %v", state, err)
	}
}

func TestTrafficMigratesAnonymousCountersOnce(t *testing.T) {
	manager, backend, now, policy := newAccuracyTrafficManager(t)
	record := manager.records[policy.ID]
	record.CounterEpoch, record.KernelCounters = "", nil
	record.ReceivedBytes, record.LastKernelReceived = 100, 400
	backend.counters = map[string]uint64{trafficRuleComment(policy.ID, "in", "tcp"): 600}
	*now = now.Add(time.Second)
	manager.collect(context.Background(), true)
	if usage := manager.Snapshot()[0]; usage.ReceivedBytes != 300 || usage.LifetimeReceivedBytes != 300 {
		t.Fatalf("legacy baseline migration: %+v", usage)
	}
	backend.counters[trafficCounterName(record, "in", "tcp")] = 50
	*now = now.Add(time.Second)
	manager.collect(context.Background(), false)
	if usage := manager.Snapshot()[0]; usage.ReceivedBytes != 350 || usage.LifetimeReceivedBytes != 350 {
		t.Fatalf("first named sample repeated legacy bytes: %+v", usage)
	}
}

// Opt-in because the child requires CAP_SYS_ADMIN/CAP_NET_ADMIN. All nft and
// link changes are confined to a fresh network namespace, never the host.
func TestTrafficNFTablesNetworkNamespace(t *testing.T) {
	if os.Getenv("QCH_TEST_NFTABLES") != "1" {
		t.Skip("QCH_TEST_NFTABLES is not enabled")
	}
	if os.Getenv("QCH_NFT_TEST_CHILD") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "unshare", "--net", os.Args[0], "-test.run=^TestTrafficNFTablesNetworkNamespace$", "-test.v")
		command.Env = append(os.Environ(), "QCH_NFT_TEST_CHILD=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated nftables test: %v\n%s", err, output)
		}
		return
	}
	if output, err := exec.Command("ip", "link", "set", "lo", "up").CombinedOutput(); err != nil {
		t.Fatalf("enable isolated loopback: %v: %s", err, output)
	}
	manager, _, now, policy := newAccuracyTrafficManager(t)
	manager.backend = &nftBackend{nftPath: "/usr/sbin/nft", direct: true}
	for _, network := range []string{"udp4", "udp6"} {
		t.Run(network, func(t *testing.T) {
			address, overhead := "127.0.0.1:0", uint64(28)
			if network == "udp6" {
				address, overhead = "[::1]:0", 48
			}
			listener, err := net.ListenPacket(network, address)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			policy.Port = listener.LocalAddr().(*net.UDPAddr).Port
			policy.ResetGeneration++
			if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
				t.Fatal(err)
			}
			connection, err := net.Dial(network, listener.LocalAddr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			packet := []byte("qcontrolhub-traffic-accuracy")
			transfer := func() {
				t.Helper()
				_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
				_ = listener.SetDeadline(time.Now().Add(2 * time.Second))
				if _, err := connection.Write(packet); err != nil {
					t.Fatal(err)
				}
				buffer := make([]byte, 1024)
				n, remote, err := listener.ReadFrom(buffer)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := listener.WriteTo(buffer[:n], remote); err != nil {
					t.Fatal(err)
				}
				if _, err := connection.Read(buffer); err != nil {
					t.Fatal(err)
				}
			}
			transfer()
			*now = now.Add(time.Second)
			manager.collect(context.Background(), false)
			want := uint64(len(packet)) + overhead
			if usage := manager.Snapshot()[0]; !usage.EnforcementAvailable || usage.ReceivedBytes != want || usage.SentBytes != want {
				t.Fatalf("kernel %s byte accounting: %+v, want %d/%d", network, usage, want, want)
			}
			manager.collect(context.Background(), true)
			transfer()
			*now = now.Add(time.Second)
			manager.collect(context.Background(), false)
			if usage := manager.Snapshot()[0]; usage.ReceivedBytes != 2*want || usage.SentBytes != 2*want {
				t.Fatalf("kernel counters did not survive reload: %+v", usage)
			}
			policy.LimitBytes, policy.AutoBlock = 1, true
			if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
				t.Fatal(err)
			}
			if !manager.Snapshot()[0].Blocked {
				t.Fatal("quota did not block")
			}
			if _, err := connection.Write(packet); err != nil {
				t.Fatal(err)
			}
			_ = listener.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			if _, _, err := listener.ReadFrom(make([]byte, 1024)); err == nil {
				t.Fatal("blocked packet was delivered")
			}
			*now = now.Add(time.Second)
			manager.collect(context.Background(), false)
			if usage := manager.Snapshot()[0]; usage.ReceivedBytes != 2*want {
				t.Fatalf("drop was billed: %+v", usage)
			}
			policy.AutoBlock, policy.LimitBytes = false, math.MaxInt64
		})
	}
	for _, network := range []string{"tcp4", "tcp6"} {
		t.Run(network, func(t *testing.T) {
			address := "127.0.0.1:0"
			if network == "tcp6" {
				address = "[::1]:0"
			}
			listener, err := net.Listen(network, address)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			policy.Port = listener.Addr().(*net.TCPAddr).Port
			policy.ResetGeneration++
			if err := manager.SetPolicies(context.Background(), []core.PortTrafficPolicy{policy}, policy.AgentID); err != nil {
				t.Fatal(err)
			}
			connection, err := net.Dial(network, listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			peer, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
			_ = peer.SetDeadline(time.Now().Add(2 * time.Second))
			packet := []byte("qcontrolhub-tcp-accounting")
			if _, err := connection.Write(packet); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(peer, make([]byte, len(packet))); err != nil {
				t.Fatal(err)
			}
			if _, err := peer.Write(packet); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(connection, make([]byte, len(packet))); err != nil {
				t.Fatal(err)
			}
			*now = now.Add(time.Second)
			manager.collect(context.Background(), false)
			before := manager.Snapshot()[0]
			if !before.EnforcementAvailable || before.ReceivedBytes <= uint64(len(packet)) || before.SentBytes <= uint64(len(packet)) {
				t.Fatalf("TCP payload/headers were not counted: %+v", before)
			}
			manager.collect(context.Background(), true)
			after := manager.Snapshot()[0]
			if !after.EnforcementAvailable || after.ReceivedBytes < before.ReceivedBytes || after.SentBytes < before.SentBytes {
				t.Fatalf("TCP reload lost bytes: before=%+v after=%+v", before, after)
			}
		})
	}
}
