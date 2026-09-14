package api

import (
	"context"
	"net/http"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/hostmetrics"
)

const panelMetricsInterval = 2 * time.Second

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
	s.panelMetricsMu.RUnlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, metrics)
}
