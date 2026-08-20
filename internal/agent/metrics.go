package agent

import (
	"context"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type metricSample struct {
	cpuTotal       uint64
	cpuIdle        uint64
	cpuValid       bool
	cgroupCPU      uint64
	cgroupCPUAt    time.Time
	cgroupCPUValid bool
	networkRX      uint64
	networkTX      uint64
	networkKey     string
	networkAt      time.Time
	networkValid   bool
}

type MetricsCollector struct {
	mu       sync.Mutex
	previous metricSample
}

func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{}
}

func (collector *MetricsCollector) Collect(ctx context.Context) (core.HostMetrics, error) {
	if collector == nil {
		collector = NewMetricsCollector()
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	metrics, next, err := collectHostMetrics(ctx, collector.previous)
	collector.previous = next
	return metrics, err
}

func metricsHaveData(metrics core.HostMetrics) bool {
	return metrics.CPUAvailable || metrics.MemoryAvailable || metrics.DiskAvailable || metrics.NetworkAvailable || len(metrics.NetworkInterfaces) > 0
}
