package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestPanelMetricsRequireSeparatePermission(t *testing.T) {
	t.Parallel()
	server := New(nil, Config{
		AdminToken: "panel-admin", OperatorTokens: []string{"panel-operator"},
		AuditorTokens: []string{"panel-auditor"}, ReadonlyTokens: []string{"panel-readonly"},
	})
	server.roleTokens[sha256.Sum256([]byte("panel-granted"))] = tokenPrincipal{
		Role: core.RoleUser, ConfigOwnerID: "panel-reader",
		Permissions: []core.Permission{core.PermissionPanelMetricsRead},
	}
	server.panelMetrics = core.HostMetrics{
		CollectedAt: time.Now().UTC(), CPUAvailable: true, CPUPercent: 12.5,
		MemoryAvailable: true, MemoryUsedBytes: 1024, MemoryTotalBytes: 4096,
	}
	handler := server.Handler()
	for _, test := range []struct {
		token  string
		status int
	}{
		{"", http.StatusUnauthorized}, {"invalid", http.StatusUnauthorized},
		{"panel-operator", http.StatusForbidden}, {"panel-auditor", http.StatusForbidden},
		{"panel-readonly", http.StatusForbidden}, {"panel-admin", http.StatusOK},
		{"panel-granted", http.StatusOK},
	} {
		t.Run(test.token, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/panel-metrics", nil)
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
			if test.status != http.StatusOK {
				return
			}
			var metrics core.HostMetrics
			if err := json.Unmarshal(response.Body.Bytes(), &metrics); err != nil {
				t.Fatal(err)
			}
			if metrics.CPUPercent != 12.5 || metrics.MemoryTotalBytes != 4096 {
				t.Fatalf("cached metrics = %+v", metrics)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("panel-host responses must not be stored in browser/proxy caches")
			}
		})
	}
}

func TestPanelMetricsWarmupReportsUnavailableWithoutDatabase(t *testing.T) {
	t.Parallel()
	server := New(nil, Config{AdminToken: "panel-admin"})
	response := httptest.NewRecorder()
	server.getPanelMetrics(response, httptest.NewRequest(http.MethodGet, "/api/v1/panel-metrics", nil))
	var metrics core.HostMetrics
	if err := json.Unmarshal(response.Body.Bytes(), &metrics); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !metrics.CollectedAt.IsZero() || metrics.CPUAvailable || metrics.MemoryAvailable || metrics.DiskAvailable || metrics.NetworkAvailable {
		t.Fatalf("warm-up must not claim zero resource utilization: %+v", metrics)
	}
}

func TestPanelMetricsMonitorCachesPartialSamplesAndStops(t *testing.T) {
	t.Parallel()
	server := New(nil, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var calls atomic.Int64
	go func() {
		defer close(done)
		server.monitorPanelMetrics(ctx, func(context.Context) (core.HostMetrics, error) {
			calls.Add(1)
			return core.HostMetrics{
				CollectedAt:     time.Now().UTC(),
				MemoryAvailable: true, MemoryUsedBytes: 1024, MemoryTotalBytes: 4096,
				NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"192.0.2.1"}}},
				PublicIPv4:        "192.0.2.1", ObservedPublicIP: "192.0.2.2",
				BBR: &core.SystemBBRStatus{Available: true},
			}, errors.New("CPU counters unavailable")
		}, 2*time.Millisecond)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("panel metrics monitor did not stop with the control plane")
		}
	})
	deadline := time.Now().Add(time.Second)
	for {
		server.panelMetricsMu.RLock()
		ready := server.panelMetrics.MemoryAvailable
		server.panelMetricsMu.RUnlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("monitor did not publish its initial sample")
		}
		time.Sleep(time.Millisecond)
	}
	var readers sync.WaitGroup
	for range 16 {
		readers.Go(func() {
			for range 10 {
				response := httptest.NewRecorder()
				server.getPanelMetrics(response, nil)
				var metrics core.HostMetrics
				if err := json.Unmarshal(response.Body.Bytes(), &metrics); err != nil {
					t.Error(err)
					return
				}
				if !metrics.MemoryAvailable || metrics.CPUAvailable || metrics.MemoryUsedBytes != 1024 {
					t.Errorf("partial sample was lost: %+v", metrics)
				}
				if metrics.BBR != nil || len(metrics.NetworkInterfaces) != 0 || metrics.PublicIPv4 != "" || metrics.ObservedPublicIP != "" {
					t.Error("panel metrics disclosed unrelated host configuration")
				}
			}
		})
	}
	readers.Wait()
	cancel()
	<-done
	before := calls.Load()
	server.getPanelMetrics(httptest.NewRecorder(), nil)
	if calls.Load() != before {
		t.Fatal("reading the snapshot triggered a new collection")
	}
}

func TestPanelMetricsCancellationDoesNotReplaceSnapshot(t *testing.T) {
	t.Parallel()
	server := New(nil, Config{})
	server.panelMetrics.CPUPercent = 25
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		server.monitorPanelMetrics(ctx, func(ctx context.Context) (core.HostMetrics, error) {
			close(started)
			<-ctx.Done()
			return core.HostMetrics{}, ctx.Err()
		}, time.Second)
	}()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled collection did not stop")
	}
	if server.panelMetrics.CPUPercent != 25 {
		t.Fatal("canceled collection replaced the previous sample")
	}
}

func TestPanelMetricsMaintainsBoundedHistoryAndRuntimeStatus(t *testing.T) {
	t.Parallel()
	server := New(nil, Config{AdminToken: "panel-admin"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	var calls atomic.Int64
	go func() {
		defer close(done)
		server.monitorPanelMetrics(ctx, func(context.Context) (core.HostMetrics, error) {
			step := calls.Add(1)
			return core.HostMetrics{
				CollectedAt:       time.Now().UTC(),
				CPUAvailable:      true,
				CPUPercent:        float64(step % 100),
				MemoryAvailable:   true,
				MemoryUsedBytes:   uint64(1024 + step),
				MemoryTotalBytes:  4096,
				DiskAvailable:     true,
				DiskUsedBytes:     2048,
				DiskTotalBytes:    8192,
				NetworkAvailable:  true,
				NetworkRXBPS:      uint64(step * 10),
				NetworkTXBPS:      uint64(step * 5),
				NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"192.0.2.10"}}},
				PublicIPv4:        "192.0.2.10",
			}, nil
		}, time.Millisecond)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < panelMetricsHistoryCapacity+8 {
		if time.Now().After(deadline) {
			t.Fatalf("collected only %d samples before deadline", calls.Load())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done

	response := httptest.NewRecorder()
	server.getPanelMetrics(response, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var snapshot core.PanelMetricsSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.History) != panelMetricsHistoryCapacity {
		t.Fatalf("history length = %d, want capped %d", len(snapshot.History), panelMetricsHistoryCapacity)
	}
	if snapshot.History[0].MemoryUsedBytes >= snapshot.History[len(snapshot.History)-1].MemoryUsedBytes {
		t.Fatalf("history did not keep oldest-to-newest chronological order: %+v", snapshot.History)
	}
	if snapshot.PanelRuntime == nil || snapshot.PanelRuntime.SampleWindowCapacity != panelMetricsHistoryCapacity || snapshot.PanelRuntime.SampleIntervalSeconds != 2 {
		t.Fatalf("unexpected runtime status: %+v", snapshot.PanelRuntime)
	}
	if snapshot.BBR != nil || len(snapshot.NetworkInterfaces) != 0 || snapshot.PublicIPv4 != "" {
		t.Fatal("panel metrics history/snapshot leaked host config fields")
	}
}
