//go:build linux

package hostmetrics

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"syscall"
)

func parseMemoryUsage(contents string) (uint64, uint64, error) {
	return parseMemoryUsageData(contents)
}

func parseMemoryUsageData[T ~[]byte | ~string](contents T) (uint64, uint64, error) {
	var memTotal, memFree, buffers, cached, sReclaimable, shmem uint64
	var hasTotal, hasFree, hasBuffers, hasCached, hasSReclaimable, hasShmem bool
	seenTargets := 0

	pos := 0
	for {
		line, nextPos, ok := nextLineSlice(contents, pos)
		if !ok {
			break
		}
		pos = nextPos
		key, fieldPos, hasKey := nextASCIIField(line, 0)
		if !hasKey {
			continue
		}
		if len(key) > 0 && key[len(key)-1] == ':' {
			key = key[:len(key)-1]
		}
		var slot *uint64
		var present *bool
		switch len(key) {
		case 5:
			if equalBytesString(key, "Shmem") {
				slot, present = &shmem, &hasShmem
			}
		case 6:
			if equalBytesString(key, "Cached") {
				slot, present = &cached, &hasCached
			}
		case 7:
			if equalBytesString(key, "MemFree") {
				slot, present = &memFree, &hasFree
			} else if equalBytesString(key, "Buffers") {
				slot, present = &buffers, &hasBuffers
			}
		case 8:
			if equalBytesString(key, "MemTotal") {
				slot, present = &memTotal, &hasTotal
			}
		case 12:
			if equalBytesString(key, "SReclaimable") {
				slot, present = &sReclaimable, &hasSReclaimable
			}
		}
		if slot == nil {
			continue
		}
		rawValue, _, hasVal := nextASCIIField(line, fieldPos)
		if !hasVal {
			continue
		}
		value, err := parseUintASCII(rawValue)
		if err != nil || value > math.MaxUint64/1024 {
			return 0, 0, errors.New("/proc/meminfo contains an invalid counter")
		}
		*slot = value * 1024
		if !*present {
			*present = true
			seenTargets++
			if seenTargets == 6 {
				break
			}
		}
	}
	if memTotal == 0 {
		return 0, 0, errors.New("/proc/meminfo has no MemTotal")
	}
	if !hasFree {
		return 0, 0, errors.New("/proc/meminfo has no MemFree")
	}
	if !hasBuffers {
		return 0, 0, errors.New("/proc/meminfo has no Buffers")
	}
	if !hasCached {
		return 0, 0, errors.New("/proc/meminfo has no Cached")
	}
	// Cached includes tmpfs/shared-memory pages. Keep Shmem in used memory and
	// exclude only the file-backed portion that can act as reclaimable cache.
	if shmem < cached {
		cached -= shmem
	} else {
		cached = 0
	}
	reclaimable := memFree
	for _, value := range [3]uint64{buffers, cached, sReclaimable} {
		if math.MaxUint64-reclaimable < value {
			return 0, 0, errors.New("/proc/meminfo reclaimable memory overflow")
		}
		reclaimable += value
	}
	if reclaimable > memTotal {
		reclaimable = memTotal
	}
	return memTotal - reclaimable, memTotal, nil
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
