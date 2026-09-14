//go:build linux

package hostmetrics

import (
	"errors"
	"io"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
)

func readMetricFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > limit {
		return nil, errors.New("metric file exceeds size limit")
	}
	return contents, nil
}

func parseCPUTimes(contents string) (uint64, uint64, error) {
	line, _, _ := strings.Cut(contents, "\n")
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, errors.New("/proc/stat has no aggregate CPU line")
	}
	values := make([]uint64, 0, 8)
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0, 0, errors.New("/proc/stat contains an invalid CPU counter")
		}
		values = append(values, value)
		if len(values) == 8 {
			break
		}
	}
	if len(values) < 4 {
		return 0, 0, errors.New("/proc/stat CPU line is incomplete")
	}
	var total uint64
	for _, value := range values {
		if math.MaxUint64-total < value {
			return 0, 0, errors.New("CPU counter overflow")
		}
		total += value
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	return total, idle, nil
}

func cgroupCPUUsage() (uint64, error) {
	if contents, err := readMetricFile("/sys/fs/cgroup/cpu.stat", 16<<10); err == nil {
		for _, line := range strings.Split(string(contents), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "usage_usec" {
				return strconv.ParseUint(fields[1], 10, 64)
			}
		}
	}
	contents, err := readMetricFile("/sys/fs/cgroup/cpuacct/cpuacct.usage", 128)
	if err != nil {
		return 0, err
	}
	nanoseconds, err := strconv.ParseUint(strings.TrimSpace(string(contents)), 10, 64)
	if err != nil {
		return 0, err
	}
	return nanoseconds / 1000, nil
}

func cgroupCPUQuota() float64 {
	if contents, err := readMetricFile("/sys/fs/cgroup/cpu.max", 128); err == nil {
		fields := strings.Fields(string(contents))
		if len(fields) == 2 && fields[0] != "max" {
			quota, quotaErr := strconv.ParseFloat(fields[0], 64)
			period, periodErr := strconv.ParseFloat(fields[1], 64)
			if quotaErr == nil && periodErr == nil && quota > 0 && period > 0 {
				return quota / period
			}
		}
		return float64(runtime.NumCPU())
	}
	return float64(runtime.NumCPU())
}
