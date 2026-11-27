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
	msg, ok := client.buildMetricsMessage(context.Background(), true, nil)
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
