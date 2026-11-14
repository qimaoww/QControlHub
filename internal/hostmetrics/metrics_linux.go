//go:build linux

package hostmetrics

import (
	"context"
	"errors"
	"fmt"
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

// sampledCPUPercent rejects sub-window deltas. The one-second metrics ticker
// and the full-heartbeat ticker occasionally fire back-to-back; a single
// scheduler tick in that tiny interval otherwise appears as a false 100% host
// spike. The last complete sample remains visible until a stable window is
// available.
func sampledCPUPercent(previous metricSample, total, idle uint64, now time.Time) (float64, bool, bool) {
	if !previous.cpuValid {
		return 0, false, true
	}
	if !previous.cpuAt.IsZero() && now.Sub(previous.cpuAt) < minimumCPUSampleWindow {
		return previous.cpuPercent, previous.cpuPercentValid, false
	}
	percent, ok := cpuUsagePercent(previous.cpuTotal, previous.cpuIdle, total, idle)
	if !ok {
		return previous.cpuPercent, previous.cpuPercentValid, true
	}
	return percent, true, true
}

func collectHostMetrics(ctx context.Context, previous metricSample) (core.HostMetrics, metricSample, error) {
	if err := ctx.Err(); err != nil {
		return core.HostMetrics{}, previous, err
	}
	now := time.Now().UTC()
	metrics := core.HostMetrics{CollectedAt: now}
	next := previous
	problems := make([]error, 0, 4)

	cpuRead := false
	if contents, err := readFirstLineReuse(&next.scratch, "/proc/stat", 256<<10); err == nil {
		total, idle, parseErr := parseCPUTimesData(contents)
		if parseErr != nil {
			problems = append(problems, parseErr)
		} else {
			percent, available, advance := sampledCPUPercent(previous, total, idle, now)
			if advance {
				next.cpuTotal, next.cpuIdle, next.cpuAt, next.cpuValid = total, idle, now, true
			}
			if available {
				metrics.CPUAvailable = true
				metrics.CPUPercent = percent
				next.cpuPercent, next.cpuPercentValid = percent, true
			}
			cpuRead = true
		}
	} else {
		problems = append(problems, fmt.Errorf("read CPU metrics: %w", err))
	}
	if !cpuRead {
		usage, err := cgroupCPUUsage()
		if err == nil {
			advance := !previous.cgroupCPUValid || now.Sub(previous.cgroupCPUAt) >= minimumCPUSampleWindow
			if advance {
				next.cgroupCPU, next.cgroupCPUAt, next.cgroupCPUValid = usage, now, true
			}
			if !advance && previous.cpuPercentValid {
				metrics.CPUAvailable = true
				metrics.CPUPercent = previous.cpuPercent
			} else if previous.cgroupCPUValid && usage >= previous.cgroupCPU && now.After(previous.cgroupCPUAt) {
				quota := cgroupCPUQuota()
				seconds := now.Sub(previous.cgroupCPUAt).Seconds()
				if seconds > 0 && quota > 0 {
					metrics.CPUAvailable = true
					metrics.CPUPercent = float64(usage-previous.cgroupCPU) / (seconds * 1_000_000 * quota) * 100
					if metrics.CPUPercent > 100 {
						metrics.CPUPercent = 100
					}
					next.cpuPercent, next.cpuPercentValid = metrics.CPUPercent, true
				}
			} else if previous.cpuPercentValid {
				metrics.CPUAvailable = true
				metrics.CPUPercent = previous.cpuPercent
			}
		} else {
			problems = append(problems, fmt.Errorf("read cgroup CPU metrics: %w", err))
		}
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
	next.interfacesValid = true
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
