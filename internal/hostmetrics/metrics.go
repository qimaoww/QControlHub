// Package hostmetrics collects read-only resource counters from the local
// operating environment. Both the Agent and the control plane use the same
// sampling windows and capacity calculations.
package hostmetrics

import (
	"context"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type metricSample struct {
	cpuTotal         uint64
	cpuIdle          uint64
	cpuAt            time.Time
	cpuValid         bool
	cpuPercent       float64
	cpuPercentValid  bool
	cgroupCPU        uint64
	cgroupCPUAt      time.Time
	cgroupCPUValid   bool
	networkRX        uint64
	networkTX        uint64
	networkKey       string
	networkAt        time.Time
	networkValid     bool
	interfaces       []string
	interfaceDetails []core.HostNetworkInterface
	interfacesKey    string
	interfacesAt     time.Time
	interfacesValid  bool
	scratch          []byte
}

type Collector struct {
	mu       sync.Mutex
	previous metricSample
}

func NewCollector() *Collector {
	return &Collector{}
}

// InvalidateInterfaces drops cached routed network interface discovery so the
// next Collect call refreshes interface names and assigned IP addresses.
func (collector *Collector) InvalidateInterfaces() {
	if collector == nil {
		return
	}
	collector.mu.Lock()
	collector.previous.interfacesValid = false
	collector.previous.interfaces = nil
	collector.previous.interfaceDetails = nil
	collector.previous.interfacesKey = ""
	collector.previous.interfacesAt = time.Time{}
	collector.mu.Unlock()
}

func (collector *Collector) Collect(ctx context.Context) (core.HostMetrics, error) {
	if collector == nil {
		collector = NewCollector()
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	metrics, next, err := collectHostMetrics(ctx, collector.previous)
	collector.previous = next
	return metrics, err
}

func HasData(metrics core.HostMetrics) bool {
	return metrics.CPUAvailable || metrics.MemoryAvailable || metrics.DiskAvailable || metrics.NetworkAvailable || len(metrics.NetworkInterfaces) > 0 || metrics.ClockSync != nil
}
