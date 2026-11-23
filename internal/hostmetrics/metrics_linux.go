//go:build linux

package hostmetrics

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const (
	minimumCPUSampleWindow   = 500 * time.Millisecond
	networkInterfaceCacheTTL = 30 * time.Second
)

// cpuUsagePercent converts two aggregate /proc/stat readings into a busy
// percentage. Identical counters mean a tickless idle host consumed no CPU
// time in the window, which reports as 0% rather than unavailable; counters
// moving backwards cannot produce a trustworthy value.
func cpuUsagePercent(previousTotal, previousIdle, total, idle uint64) (float64, bool) {
	if total < previousTotal || idle < previousIdle {
		return 0, false
	}
	totalDelta := total - previousTotal
	idleDelta := idle - previousIdle
	if idleDelta > totalDelta {
		return 0, false
	}
	if totalDelta == 0 {
		return 0, true
	}
	return float64(totalDelta-idleDelta) * 100 / float64(totalDelta), true
}

// Heartbeats and metrics pushes share this baseline. Only a complete
// window from the same counter source and CPU capacity can produce a rate.
func (sample *cpuSample) record(current cpuReading) {
	previous := sample.reading
	if previous.at.IsZero() || previous.quota != current.quota {
		*sample = cpuSample{reading: current}
		return
	}
	elapsed := current.at.Sub(previous.at)
	if elapsed < minimumCPUSampleWindow {
		return
	}
	sample.reading = current
	if current.quota > 0 {
		if current.total >= previous.total {
			capacity := elapsed.Seconds() * 1_000_000 * current.quota
			sample.percent = math.Min(100, float64(current.total-previous.total)*100/capacity)
			sample.available = true
		}
		return
	}
	if percent, ok := cpuUsagePercent(previous.total, previous.idle, current.total, current.idle); ok {
		sample.percent, sample.available = percent, true
	}
}

func collectHostMetrics(ctx context.Context, previous metricSample) (core.HostMetrics, metricSample, error) {
	if err := ctx.Err(); err != nil {
		return core.HostMetrics{}, previous, err
	}
	now := time.Now()
	metrics := core.HostMetrics{CollectedAt: now.UTC()}
	next := previous
	problems := make([]error, 0, 4)

	cpuRead := false
	if contents, err := readFirstLineReuse(&next.scratch, "/proc/stat", 256<<10); err == nil {
		total, idle, parseErr := parseCPUTimesData(contents)
		if parseErr != nil {
			problems = append(problems, parseErr)
		} else {
			next.cpu.record(cpuReading{at: now, total: total, idle: idle})
			cpuRead = true
		}
	} else {
		problems = append(problems, fmt.Errorf("read CPU metrics: %w", err))
	}
	if !cpuRead {
		usage, err := cgroupCPUUsage()
		if err == nil {
			next.cpu.record(cpuReading{at: now, total: usage, quota: cgroupCPUQuota()})
			cpuRead = true
		} else {
			problems = append(problems, fmt.Errorf("read cgroup CPU metrics: %w", err))
		}
	}
	if cpuRead {
		metrics.CPUPercent, metrics.CPUAvailable = next.cpu.percent, next.cpu.available
	}

	memoryRead := false
	if contents, err := readMetricFileReuse(&next.scratch, "/proc/meminfo", 256<<10); err == nil {
		used, total, parseErr := parseMemoryUsageData(contents)
		if parseErr != nil {
			problems = append(problems, parseErr)
		} else {
			metrics.MemoryAvailable = true
			metrics.MemoryUsedBytes = used
			metrics.MemoryTotalBytes = total
			memoryRead = true
		}
	} else {
		problems = append(problems, fmt.Errorf("read memory metrics: %w", err))
	}
	if !memoryRead {
		used, total, err := fallbackMemoryUsage()
		if err != nil {
			problems = append(problems, fmt.Errorf("read fallback memory metrics: %w", err))
		} else {
			metrics.MemoryAvailable = true
			metrics.MemoryUsedBytes = used
			metrics.MemoryTotalBytes = total
		}
	}

	if used, total, err := rootDiskUsage(); err != nil {
		problems = append(problems, fmt.Errorf("read disk metrics: %w", err))
	} else {
		metrics.DiskAvailable = true
		metrics.DiskUsedBytes = used
		metrics.DiskTotalBytes = total
	}

	interfaces, interfaceDetails, key, cachedInterfaces, interfaceErr := collectNetworkInterfacesCached(&previous, &next, now)
	if interfaceErr != nil {
		problems = append(problems, interfaceErr)
	}
	if len(interfaces) > 0 {
		rx, tx, err := networkCountersWithScratch(interfaces, &next.scratch)
		if err != nil && cachedInterfaces {
			// Cached route interfaces may have disappeared or been renamed; refresh immediately once.
			next.interfacesValid = false
			next.interfaces = nil
			next.interfaceDetails = nil
			next.interfacesKey = ""
			next.interfacesAt = time.Time{}
			interfaces, interfaceDetails, key, _, refreshErr := collectNetworkInterfacesCached(&next, &next, now)
			if refreshErr != nil {
				problems = append(problems, refreshErr)
			}
			if len(interfaces) > 0 {
				metrics.NetworkInterfaces = interfaceDetails
				rx, tx, err = networkCountersWithScratch(interfaces, &next.scratch)
				if err != nil {
					problems = append(problems, err)
				} else {
					recordNetworkSample(&metrics, &next, previous, key, rx, tx, now)
				}
			}
		} else {
			metrics.NetworkInterfaces = interfaceDetails
			if err != nil {
				problems = append(problems, err)
			} else {
				recordNetworkSample(&metrics, &next, previous, key, rx, tx, now)
			}
		}
	}

	if !HasData(metrics) {
		metrics.CollectedAt = time.Time{}
	}
	return metrics, next, errors.Join(problems...)
}

func collectNetworkInterfacesCached(previous *metricSample, next *metricSample, now time.Time) ([]string, []core.HostNetworkInterface, string, bool, error) {
	if previous != nil && previous.interfacesValid && len(previous.interfaces) > 0 && !now.Before(previous.interfacesAt) && now.Sub(previous.interfacesAt) < networkInterfaceCacheTTL {
		return previous.interfaces, previous.interfaceDetails, previous.interfacesKey, true, nil
	}
	interfaces, err := routedNetworkInterfacesWithScratch(&next.scratch)
	if err != nil {
		next.interfacesValid = false
		next.interfaces = nil
		next.interfaceDetails = nil
		next.interfacesKey = ""
		next.interfacesAt = time.Time{}
		return nil, nil, "", false, err
	}
	details, detailsErr := networkInterfaceDetails(interfaces)
	key := strings.Join(interfaces, ",")
	next.interfaces = interfaces
	next.interfaceDetails = details
	next.interfacesKey = key
	next.interfacesAt = now
	next.interfacesValid = detailsErr == nil
	return interfaces, details, key, false, detailsErr
}

func recordNetworkSample(metrics *core.HostMetrics, next *metricSample, previous metricSample, key string, rx, tx uint64, now time.Time) {
	rateAvailable := false
	metrics.NetworkAvailable = true
	metrics.NetworkRateAvailable = &rateAvailable
	metrics.NetworkRXBytes = rx
	metrics.NetworkTXBytes = tx
	if previous.networkValid && previous.networkKey == key && rx >= previous.networkRX && tx >= previous.networkTX {
		seconds := now.Sub(previous.networkAt).Seconds()
		// Windows shorter than half the sampling cadence (for example
		// between the startup warm-up and the first heartbeat) would
		// inflate byte-per-second rates; they report zero instead.
		if seconds >= 0.5 {
			rateAvailable = true
			metrics.NetworkRXBPS = bytesPerSecond(rx-previous.networkRX, seconds)
			metrics.NetworkTXBPS = bytesPerSecond(tx-previous.networkTX, seconds)
		}
	}
	next.networkRX, next.networkTX, next.networkKey, next.networkAt, next.networkValid = rx, tx, key, now, true
}
