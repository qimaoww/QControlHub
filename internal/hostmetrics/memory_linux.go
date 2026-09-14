//go:build linux

package hostmetrics

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"syscall"
)

func parseMemoryUsage(contents string) (uint64, uint64, error) {
	values := make(map[string]uint64)
	present := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		switch key {
		case "MemTotal", "MemFree", "Buffers", "Cached", "SReclaimable", "Shmem":
			value, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil || value > math.MaxUint64/1024 {
				return 0, 0, errors.New("/proc/meminfo contains an invalid counter")
			}
			values[key] = value * 1024
			present[key] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	total := values["MemTotal"]
	if total == 0 {
		return 0, 0, errors.New("/proc/meminfo has no MemTotal")
	}
	for _, key := range []string{"MemFree", "Buffers", "Cached"} {
		if !present[key] {
			return 0, 0, fmt.Errorf("/proc/meminfo has no %s", key)
		}
	}
	// Cached includes tmpfs/shared-memory pages. Keep Shmem in used memory and
	// exclude only the file-backed portion that can act as reclaimable cache.
	cached := values["Cached"]
	if shmem := values["Shmem"]; shmem < cached {
		cached -= shmem
	} else {
		cached = 0
	}
	reclaimable := values["MemFree"]
	for _, value := range []uint64{values["Buffers"], cached, values["SReclaimable"]} {
		if math.MaxUint64-reclaimable < value {
			return 0, 0, errors.New("/proc/meminfo reclaimable memory overflow")
		}
		reclaimable += value
	}
	if reclaimable > total {
		reclaimable = total
	}
	return total - reclaimable, total, nil
}

func fallbackMemoryUsage() (uint64, uint64, error) {
	current, currentErr := readUintMetric("/sys/fs/cgroup/memory.current")
	limitContents, limitErr := readMetricFile("/sys/fs/cgroup/memory.max", 128)
	if currentErr == nil && limitErr == nil {
		limitText := strings.TrimSpace(string(limitContents))
		if limitText != "max" {
			limit, parseErr := strconv.ParseUint(limitText, 10, 64)
			if parseErr == nil && limit > 0 {
				if current > limit {
					current = limit
				}
				return current, limit, nil
			}
		}
	}
	var info syscall.Sysinfo_t
	if err := syscall.Sysinfo(&info); err != nil {
		return 0, 0, err
	}
	unit := uint64(info.Unit)
	total := uint64(info.Totalram) * unit
	free := uint64(info.Freeram) * unit
	if total == 0 || free > total {
		return 0, 0, errors.New("system memory counters are invalid")
	}
	return total - free, total, nil
}
