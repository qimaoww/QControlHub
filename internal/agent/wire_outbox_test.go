package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestWireOutboxPriorityAndCoalescing(t *testing.T) {
	outbox := newWireOutbox(8)
	ctx := context.Background()

	// Coalesce multiple pending metrics down to the latest value.
	outbox.EnqueueMetric(core.WireMessage{Type: core.WireMetrics, Metrics: &core.HostMetrics{CPUPercent: 10}})
	outbox.EnqueueMetric(core.WireMessage{Type: core.WireMetrics, Metrics: &core.HostMetrics{CPUPercent: 42}})

	// Coalesce multiple pending heartbeats down to the latest value.
	outbox.EnqueueHeartbeat(core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{Version: "1"}})
	outbox.EnqueueHeartbeat(core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{Version: "2"}})

	// Queue core logs and high-priority control result.
	if !outbox.TryEnqueueLogs(core.WireMessage{Type: core.WireCoreLogs, CoreLogs: &core.CoreLogBatch{ID: "batch-1"}}) {
		t.Fatal("TryEnqueueLogs failed")
	}
	if err := outbox.EnqueueHigh(ctx, core.WireMessage{Type: core.WireResult, Result: &core.TaskResultEnvelope{TaskID: "task-1"}}); err != nil {
		t.Fatal(err)
	}

	got1, ok := outbox.Pop(ctx)
	if !ok || got1.Type != core.WireResult || got1.Result.TaskID != "task-1" {
		t.Fatalf("pop 1 = %+v, ok=%v; want high-priority WireResult", got1, ok)
	}
	got2, ok := outbox.Pop(ctx)
	if !ok || got2.Type != core.WireHeartbeat || got2.Heartbeat.Version != "2" {
		t.Fatalf("pop 2 = %+v, ok=%v; want coalesced latest WireHeartbeat v2", got2, ok)
	}
	got3, ok := outbox.Pop(ctx)
	if !ok || got3.Type != core.WireCoreLogs || got3.CoreLogs.ID != "batch-1" {
		t.Fatalf("pop 3 = %+v, ok=%v; want WireCoreLogs", got3, ok)
	}
	got4, ok := outbox.Pop(ctx)
	if !ok || got4.Type != core.WireMetrics || got4.Metrics.CPUPercent != 42 {
		t.Fatalf("pop 4 = %+v, ok=%v; want coalesced latest WireMetrics CPU=42", got4, ok)
	}

	hbCoalesced, mCoalesced := outbox.stats()
	if hbCoalesced != 1 || mCoalesced != 1 {
		t.Fatalf("coalesced stats = (%d, %d); want (1, 1)", hbCoalesced, mCoalesced)
	}
}

func TestCompactInterfaceClearOnWire(t *testing.T) {
	state := newCompactTelemetryState()
	for _, step := range []struct {
		interfaces []core.HostNetworkInterface
		want       string
	}{
		{[]core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"192.0.2.2"}}}, `[{"name":"eth0","addresses":["192.0.2.2"]}]`},
		{nil, ""}, {[]core.HostNetworkInterface{}, "[]"}, {[]core.HostNetworkInterface{}, ""},
	} {
		frame := state.CompactMetrics(core.WireMessage{Type: core.WireMetrics, Metrics: &core.HostMetrics{NetworkInterfaces: step.interfaces}})
		raw, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Metrics map[string]json.RawMessage `json:"metrics"`
		}
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if got := string(wire.Metrics["network_interfaces"]); got != step.want {
			t.Fatalf("network_interfaces = %q, want %q", got, step.want)
		}
	}
}

func TestCompactTelemetryStateDiffing(t *testing.T) {
	state := newCompactTelemetryState()
	now := time.Now().UTC().Truncate(time.Second)
	offset := 1.25
	interfaces := []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"192.0.2.10", "2001:db8::1"}}}
	bbr := &core.SystemBBRStatus{
		CollectedAt:       now,
		Available:         true,
		CongestionControl: "bbr",
		DefaultQdisc:      "fq",
	}
	clock := &core.ClockSyncStatus{Status: "synchronized", OffsetMilliseconds: &offset, CheckedAt: now}
	usage := []core.PortTrafficUsage{{
		PolicyID:              "ptp_0123456789abcdef",
		ResetGeneration:       1,
		ReceivedBytes:         1024,
		SentBytes:             2048,
		UsedBytes:             3072,
		LifetimeReceivedBytes: 1024,
		LifetimeSentBytes:     2048,
		CollectedAt:           now,
		PeriodStart:           now.Add(-time.Hour),
		PeriodEnd:             now.Add(23 * time.Hour),
		EnforcementAvailable:  true,
		CounterEpoch:          "ep_1",
	}}

	state.RecordHeartbeat(core.WireMessage{
		Type: core.WireHeartbeat,
		Heartbeat: &core.HeartbeatRequest{
			Metrics: &core.HostMetrics{
				CollectedAt:       now,
				CPUAvailable:      true,
				CPUPercent:        12.5,
				NetworkInterfaces: interfaces,
				BBR:               bbr,
				ClockSync:         clock,
			},
			TrafficUsage: usage,
		},
	})

	unchanged := state.CompactMetrics(core.WireMessage{
		Type: core.WireMetrics,
		Metrics: &core.HostMetrics{
			CollectedAt:       now.Add(time.Second),
			CPUAvailable:      true,
			CPUPercent:        14.0,
			NetworkInterfaces: interfaces,
			BBR:               bbr,
			ClockSync:         clock,
		},
		TrafficUsage: usage,
	})
	if unchanged.Metrics.NetworkInterfaces != nil || unchanged.Metrics.BBR != nil || unchanged.Metrics.ClockSync != nil || len(unchanged.TrafficUsage) != 0 {
		t.Fatalf("expected unchanged slow snapshots and identical CollectedAt usage to be omitted, got %+v", unchanged)
	}

	fullRaw, err := json.Marshal(core.WireMessage{
		Type: core.WireMetrics,
		Metrics: &core.HostMetrics{
			CollectedAt:       now.Add(time.Second),
			CPUAvailable:      true,
			CPUPercent:        14.0,
			NetworkInterfaces: interfaces,
			BBR:               bbr,
			ClockSync:         clock,
		},
		TrafficUsage: usage,
	})
	if err != nil {
		t.Fatal(err)
	}
	compactRaw, err := json.Marshal(unchanged)
	if err != nil {
		t.Fatal(err)
	}
	if len(compactRaw) >= len(fullRaw) {
		t.Fatalf("compact payload (%d bytes) did not shrink below full (%d bytes)", len(compactRaw), len(fullRaw))
	}

	// Any changed interface list, clock refresh, or newer CollectedAt sample must be emitted immediately.
	nextNow := now.Add(2 * time.Second)
	changedClock := &core.ClockSyncStatus{Status: "synchronized", OffsetMilliseconds: &offset, CheckedAt: nextNow}
	changedUsage := append([]core.PortTrafficUsage(nil), usage...)
	changedUsage[0].CollectedAt = nextNow
	changedUsage[0].ReceivedBytes = 2048
	changedUsage[0].UsedBytes = 4096
	changedUsage[0].LifetimeReceivedBytes = 2048
	changed := state.CompactMetrics(core.WireMessage{
		Type: core.WireMetrics,
		Metrics: &core.HostMetrics{
			CollectedAt:       nextNow,
			CPUAvailable:      true,
			CPUPercent:        20.0,
			NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"192.0.2.11"}}},
			BBR:               bbr,
			ClockSync:         changedClock,
		},
		TrafficUsage: changedUsage,
	})
	if changed.Metrics.BBR != nil {
		t.Fatal("unchanged BBR should stay omitted while changed sections transmit")
	}
	if len(changed.Metrics.NetworkInterfaces) != 1 || changed.Metrics.ClockSync == nil || len(changed.TrafficUsage) != 1 {
		t.Fatalf("changed snapshots or newer usage missing: %+v", changed)
	}

	// Outbox overwrite of uncompacted frames followed by send-time CompactMetrics preserves B->C transitions.
	outbox := newWireOutbox(8)
	baseIface := []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"10.0.0.1"}}}
	newIface := []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"10.0.0.2"}}}
	newBBR := &core.SystemBBRStatus{CollectedAt: nextNow.Add(time.Second), Available: true, CongestionControl: "cubic", DefaultQdisc: "fq_codel"}
	outboxState := newCompactTelemetryState()
	outboxState.RecordHeartbeat(core.WireMessage{
		Type: core.WireHeartbeat,
		Heartbeat: &core.HeartbeatRequest{
			Metrics: &core.HostMetrics{
				CollectedAt:       now,
				CPUAvailable:      true,
				NetworkInterfaces: baseIface,
				BBR:               bbr,
				ClockSync:         clock,
			},
			TrafficUsage: usage,
		},
	})
	firstUncompacted := core.WireMessage{
		Type: core.WireMetrics,
		Metrics: &core.HostMetrics{
			CollectedAt:       nextNow,
			CPUAvailable:      true,
			CPUPercent:        30,
			NetworkInterfaces: newIface,
			BBR:               newBBR,
			ClockSync:         changedClock,
		},
		TrafficUsage: changedUsage,
	}
	secondUncompacted := core.WireMessage{
		Type: core.WireMetrics,
		Metrics: &core.HostMetrics{
			CollectedAt:       nextNow.Add(time.Second),
			CPUAvailable:      true,
			CPUPercent:        35,
			NetworkInterfaces: newIface,
			BBR:               newBBR,
			ClockSync:         changedClock,
		},
		TrafficUsage: changedUsage,
	}
	outbox.EnqueueMetric(firstUncompacted)
	outbox.EnqueueMetric(secondUncompacted) // Overwrites firstUncompacted in outbox.
	popped, ok := outbox.Pop(context.Background())
	if !ok {
		t.Fatal("expected metric frame from outbox")
	}
	emitted := outboxState.CompactMetrics(popped)
	if len(emitted.Metrics.NetworkInterfaces) != 1 || emitted.Metrics.NetworkInterfaces[0].Addresses[0] != "10.0.0.2" {
		t.Fatalf("emitted frame lost overwritten interface change: %+v", emitted.Metrics.NetworkInterfaces)
	}
	if emitted.Metrics.BBR == nil || emitted.Metrics.BBR.CongestionControl != "cubic" {
		t.Fatalf("emitted frame lost overwritten BBR change: %+v", emitted.Metrics.BBR)
	}
	if emitted.Metrics.ClockSync == nil || len(emitted.TrafficUsage) != 1 || emitted.TrafficUsage[0].ReceivedBytes != 2048 {
		t.Fatalf("emitted frame lost overwritten clock or traffic change: clock=%+v traffic=%+v", emitted.Metrics.ClockSync, emitted.TrafficUsage)
	}

	// Invalidate forces next CompactMetrics frame to carry full state with TelemetryFullSync=true.
	outboxState.Invalidate()
	resynced := outboxState.CompactMetrics(core.WireMessage{
		Type: core.WireMetrics,
		Metrics: &core.HostMetrics{
			CollectedAt:       nextNow.Add(2 * time.Second),
			CPUAvailable:      true,
			CPUPercent:        40,
			NetworkInterfaces: newIface,
			BBR:               newBBR,
			ClockSync:         changedClock,
		},
		TrafficUsage: changedUsage,
	})
	if !resynced.TelemetryFullSync || len(resynced.Metrics.NetworkInterfaces) != 1 || resynced.Metrics.BBR == nil || resynced.Metrics.ClockSync == nil || len(resynced.TrafficUsage) != 1 {
		t.Fatalf("invalidated CompactMetrics did not emit full resync frame: %+v", resynced)
	}
}

func TestCachedPrivateKeyReusesDecodedKeyAndUpdatesOnChange(t *testing.T) {
	_, pk1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, pk2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{}
	raw1, raw2 := authn.EncodePrivateKey(pk1), authn.EncodePrivateKey(pk2)
	got1a, err := c.cachedPrivateKey(raw1)
	if err != nil {
		t.Fatal(err)
	}
	got1b, err := c.cachedPrivateKey(raw1)
	if err != nil {
		t.Fatal(err)
	}
	if !got1a.Equal(pk1) || &got1a[0] != &got1b[0] {
		t.Fatal("expected subsequent cachedPrivateKey call to reuse decoded key slice")
	}
	got2, err := c.cachedPrivateKey(raw2)
	if err != nil {
		t.Fatal(err)
	}
	if !got2.Equal(pk2) {
		t.Fatal("expected cachedPrivateKey to update when credential rotates")
	}
}

func TestRunWebSocketPingTimeoutClosesHalfOpenSession(t *testing.T) {
	prevInterval, prevTimeout := wsPingInterval, wsPingTimeout
	wsPingInterval, wsPingTimeout = 40*time.Millisecond, 40*time.Millisecond
	defer func() {
		wsPingInterval, wsPingTimeout = prevInterval, prevTimeout
	}()

	client := reconnectTestClient(t)
	client.config.HeartbeatEvery, client.config.MetricsEvery = time.Hour, time.Hour
	body := newBlockedWebSocketBody(t) // Reads never return Ping/Pong responses, simulating silent drop.
	client.http.Transport = reconnectTransport(func(request *http.Request) (*http.Response, error) {
		return body.response(request), nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := client.runWebSocket(ctx)
	if err == nil || errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "websocket keepalive ping") {
		t.Fatalf("expected websocket keepalive ping timeout error, got %v", err)
	}
}
