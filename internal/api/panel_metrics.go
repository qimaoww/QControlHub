package api

import (
	"context"
	"net/http"
	"runtime"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/hostmetrics"
)

const (
	panelMetricsInterval        = 2 * time.Second
	panelMetricsHistoryCapacity = 60
)

// MonitorPanelMetrics runs once alongside the control-plane lifecycle.
// Requests only read the latest sample: additional viewers do not trigger
// collection, consume CPU sampling windows, or query PostgreSQL.
func (s *Server) MonitorPanelMetrics(ctx context.Context) {
	collector := hostmetrics.NewCollector()
	s.monitorPanelMetrics(ctx, collector.Collect, panelMetricsInterval)
}

func (s *Server) monitorPanelMetrics(ctx context.Context, collect func(context.Context) (core.HostMetrics, error), interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		metrics, _ := collect(ctx)
		if ctx.Err() != nil {
			return
		}
		// Partial collections retain the individual availability flags.
		// The panel needs resource counters, never interface addresses or BBR
		// configuration. Keep these out of the shared in-memory snapshot.
		metrics.BBR = nil
		metrics.NetworkInterfaces = nil
		metrics.ObservedPublicIP = ""
		metrics.PublicIPv4, metrics.PublicIPv6 = "", ""
		metrics.PublicIPv4Source, metrics.PublicIPv6Source = "", ""
		s.panelMetricsMu.Lock()
		s.panelMetrics = metrics
		if !metrics.CollectedAt.IsZero() {
			var rateAvail *bool
			if metrics.NetworkRateAvailable != nil {
				v := *metrics.NetworkRateAvailable
				rateAvail = &v
			}
			sample := core.PanelMetricSample{
				CollectedAt:          metrics.CollectedAt,
				CPUAvailable:         metrics.CPUAvailable,
				CPUPercent:           metrics.CPUPercent,
				MemoryAvailable:      metrics.MemoryAvailable,
				MemoryUsedBytes:      metrics.MemoryUsedBytes,
				MemoryTotalBytes:     metrics.MemoryTotalBytes,
				DiskAvailable:        metrics.DiskAvailable,
				DiskUsedBytes:        metrics.DiskUsedBytes,
				DiskTotalBytes:       metrics.DiskTotalBytes,
				NetworkAvailable:     metrics.NetworkAvailable,
				NetworkRateAvailable: rateAvail,
				NetworkRXBPS:         metrics.NetworkRXBPS,
				NetworkTXBPS:         metrics.NetworkTXBPS,
			}
			s.panelMetricsHistory = append(s.panelMetricsHistory, sample)
			if len(s.panelMetricsHistory) > panelMetricsHistoryCapacity {
				excess := len(s.panelMetricsHistory) - panelMetricsHistoryCapacity
				copy(s.panelMetricsHistory, s.panelMetricsHistory[excess:])
				s.panelMetricsHistory = s.panelMetricsHistory[:panelMetricsHistoryCapacity]
			}
		}
		s.panelMetricsMu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) getPanelMetrics(w http.ResponseWriter, _ *http.Request) {
	s.panelMetricsMu.RLock()
	metrics := s.panelMetrics
	history := append([]core.PanelMetricSample(nil), s.panelMetricsHistory...)
	startedAt := s.panelStartedAt
	s.panelMetricsMu.RUnlock()
	var uptime int64
	if !startedAt.IsZero() {
		uptime = max(0, int64(time.Since(startedAt)/time.Second))
	}
	snapshot := core.PanelMetricsSnapshot{
		HostMetrics: metrics,
		History:     history,
		PanelRuntime: &core.PanelRuntimeStatus{
			SampleIntervalSeconds: int(panelMetricsInterval / time.Second),
			SampleWindowCount:     len(history),
			SampleWindowCapacity:  panelMetricsHistoryCapacity,
			Goroutines:            runtime.NumGoroutine(),
			GoVersion:             runtime.Version(),
			ProcessUptimeSeconds:  uptime,
		},
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, snapshot)
}
