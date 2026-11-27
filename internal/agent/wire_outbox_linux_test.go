//go:build linux

package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestMetricsPushPublishesUnavailableCPUDuringWarmup(t *testing.T) {
	client := reconnectTestClient(t)
	client.metrics = NewMetricsCollector()
	client.clockSync = &core.ClockSyncStatus{CheckedAt: time.Now(), Status: "synchronized"}
	msg := core.WireMessage{Type: core.WireMetrics}
	ok := client.prepareTelemetry(&msg, client.collectHostMetrics(context.Background()))
	if !ok || msg.Metrics == nil || msg.Metrics.CPUAvailable || !msg.Metrics.MemoryAvailable {
		t.Fatalf("first metrics push = %+v, sent=%t", msg.Metrics, ok)
	}
}

func TestRunWebSocketCompactTelemetryWriterResync(t *testing.T) {
	client := reconnectTestClient(t)
	client.bbr = NewSystemBBRManager(client.executor.serviceManager())
	_, _ = client.metrics.Collect(context.Background())
	client.config.HeartbeatEvery = time.Hour
	client.config.MetricsEvery = 30 * time.Millisecond

	resyncVerified := make(chan struct{})
	errCh := make(chan error, 1)
	reportErr := func(format string, args ...any) {
		select {
		case errCh <- fmt.Errorf(format, args...):
		default:
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"qcontrolhub.agent.v1"}})
		if err != nil {
			reportErr("websocket.Accept: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		ctx := r.Context()
		if err := wsjson.Write(ctx, conn, core.WireMessage{Type: core.WireHello, CompactTelemetry: true, TrafficPoliciesPending: true}); err != nil {
			reportErr("write WireHello: %v", err)
			return
		}

		sawHeartbeat := false
		sawCompactMetrics := false
		sentResync := false
		for {
			var msg core.WireMessage
			if err := wsjson.Read(ctx, conn, &msg); err != nil {
				return
			}
			switch msg.Type {
			case core.WireHeartbeat:
				if msg.Heartbeat == nil || !slices.Contains(msg.Heartbeat.Features, core.AgentFeatureCompactTelemetry) {
					reportErr("heartbeat missing AgentFeatureCompactTelemetry: %+v", msg.Heartbeat)
					return
				}
				sawHeartbeat = true
			case core.WireMetrics:
				if msg.Metrics == nil {
					continue
				}
				if sawHeartbeat && !sawCompactMetrics && msg.Metrics.NetworkInterfaces == nil && msg.Metrics.BBR == nil {
					sawCompactMetrics = true
					if err := wsjson.Write(ctx, conn, core.WireMessage{Type: core.WireTelemetryResync}); err != nil {
						reportErr("write WireTelemetryResync: %v", err)
						return
					}
					sentResync = true
					continue
				}
				if sentResync && msg.TelemetryFullSync {
					if msg.Metrics.NetworkInterfaces == nil || msg.Metrics.BBR == nil {
						reportErr("TelemetryFullSync frame omitted snapshot fields: %+v", msg.Metrics)
						return
					}
					close(resyncVerified)
					return
				}
			}
		}
	}))
	defer server.Close()

	client.websocketURL = "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		_ = client.runWebSocket(ctx)
	}()

	select {
	case <-resyncVerified:
		cancel()
	case err := <-errCh:
		t.Fatalf("server error: %v", err)
	case <-ctx.Done():
		t.Fatal("timed out waiting for runWebSocket WireTelemetryResync -> TelemetryFullSync")
	}
}

// Runtime refreshes can arrive between any two metrics ticks. A burst must
// leave the CPU measurement window intact, including its original timestamp.
func TestWebSocketHeartbeatsShareOneHostSampleBetweenMetricsTicks(t *testing.T) {
	client := reconnectTestClient(t)
	client.config.HeartbeatEvery, client.config.MetricsEvery = time.Hour, time.Hour
	client.runtimeRefresh = make(chan struct{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	result := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"qcontrolhub.agent.v1"}})
		if err != nil {
			result <- err
			return
		}
		defer conn.CloseNow()
		var initial time.Time
		for i := 0; i < 3; i++ {
			var msg core.WireMessage
			if err := wsjson.Read(ctx, conn, &msg); err != nil {
				result <- err
				return
			}
			if msg.Heartbeat == nil || msg.Heartbeat.Metrics == nil || msg.Heartbeat.Metrics.CollectedAt.IsZero() {
				result <- fmt.Errorf("missing host sample in heartbeat %d", i)
				return
			}
			at := msg.Heartbeat.Metrics.CollectedAt
			if i == 0 {
				initial = at
			} else if !at.Equal(initial) {
				result <- fmt.Errorf("runtime refresh advanced the sample from %s to %s before the metrics tick", initial, at)
				return
			}
			if i < 2 {
				client.runtimeRefresh <- struct{}{}
			}
		}
		result <- nil
	}))
	defer server.Close()
	client.websocketURL = "ws" + strings.TrimPrefix(server.URL, "http")
	_ = client.runWebSocket(ctx)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

type scriptedHostSamples chan core.HostMetrics

func (samples scriptedHostSamples) Collect(ctx context.Context) (core.HostMetrics, error) {
	select {
	case sample := <-samples:
		return sample, nil
	case <-ctx.Done():
		return core.HostMetrics{}, ctx.Err()
	}
}

func TestWebSocketPublishesCPURecoveryWhileTrafficProbeIsBlocked(t *testing.T) {
	client := reconnectTestClient(t)
	client.traffic = &TrafficManager{}
	client.config.HeartbeatEvery, client.config.MetricsEvery = time.Hour, 20*time.Millisecond
	client.runtimeRefresh = make(chan struct{}, 1)
	samples := make(scriptedHostSamples, 1)
	client.metrics = samples
	peak := core.HostMetrics{CPUAvailable: true, CPUPercent: 100, CollectedAt: time.Unix(200, 0)}
	// The node clock steps backwards during recovery.
	low := core.HostMetrics{CPUAvailable: true, CPUPercent: 7, CollectedAt: time.Unix(190, 0)}
	missing := core.HostMetrics{MemoryAvailable: true, CollectedAt: time.Unix(191, 0)}
	samples <- peak

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"qcontrolhub.agent.v1"}})
		if err != nil {
			result <- err
			return
		}
		defer conn.CloseNow()
		read := func(kind string, want core.HostMetrics) error {
			var msg core.WireMessage
			if err := wsjson.Read(ctx, conn, &msg); err != nil {
				return err
			}
			got := msg.Metrics
			if msg.Heartbeat != nil {
				got = msg.Heartbeat.Metrics
			}
			if msg.Type != kind || got == nil || got.CPUAvailable != want.CPUAvailable ||
				got.CPUPercent != want.CPUPercent || !got.CollectedAt.Equal(want.CollectedAt) {
				return fmt.Errorf("received %s %+v, want %s %+v", msg.Type, got, kind, want)
			}
			return nil
		}
		if err := read(core.WireHeartbeat, peak); err != nil {
			result <- err
			return
		}
		client.traffic.mu.Lock()
		defer client.traffic.mu.Unlock()
		samples <- low
		if err := read(core.WireMetrics, low); err != nil {
			result <- err
			return
		}
		client.runtimeRefresh <- struct{}{}
		if err := read(core.WireHeartbeat, low); err != nil {
			result <- err
			return
		}
		samples <- missing
		result <- read(core.WireMetrics, missing)
	}))
	defer server.Close()
	client.websocketURL = "ws" + strings.TrimPrefix(server.URL, "http")
	_ = client.runWebSocket(ctx)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestWriterReplacesQueuedPeakInBothFrameTypes(t *testing.T) {
	client := &Client{}
	peak := &core.HostMetrics{CPUAvailable: true, CPUPercent: 100, CollectedAt: time.Unix(200, 0)}
	low := &core.HostMetrics{CPUAvailable: true, CPUPercent: 7, CollectedAt: time.Unix(190, 0)}
	outbox := newWireOutbox(1)
	outbox.EnqueueMetric(core.WireMessage{Type: core.WireMetrics, Metrics: peak})
	outbox.EnqueueHeartbeat(core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{Metrics: peak}})
	for range 2 {
		msg, ok := outbox.Pop(context.Background())
		if !ok || !client.prepareTelemetry(&msg, low) {
			t.Fatal("queued telemetry lost")
		}
		got := msg.Metrics
		if msg.Heartbeat != nil {
			got = msg.Heartbeat.Metrics
		}
		if got.CPUPercent != low.CPUPercent || !got.CollectedAt.Equal(low.CollectedAt) {
			t.Fatalf("%s replayed old sample %+v", msg.Type, got)
		}
	}
}
