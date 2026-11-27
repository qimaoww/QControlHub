//go:build linux

package hostmetrics

import (
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

func readMetricFileReuse(scratch *[]byte, path string, limit int64) ([]byte, error) {
	if scratch == nil {
		return readMetricFile(path, limit)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	buf := *scratch
	if cap(buf) < 4096 {
		buf = make([]byte, 4096)
	} else {
		buf = buf[:cap(buf)]
	}
	total := 0
	for {
		if total == len(buf) {
			if int64(len(buf)) > limit {
				*scratch = buf
				return nil, errors.New("metric file exceeds size limit")
			}
			nextSize := len(buf) * 2
			if maxLen := int(limit) + 1; nextSize > maxLen {
				nextSize = maxLen
			}
			grown := make([]byte, nextSize)
			copy(grown, buf[:total])
			buf = grown
		}
		n, readErr := file.Read(buf[total:])
		if n > 0 {
			total += n
			if int64(total) > limit {
				*scratch = buf
				return nil, errors.New("metric file exceeds size limit")
			}
		}
		if readErr == io.EOF {
			*scratch = buf
			return buf[:total], nil
		}
		if readErr != nil {
			*scratch = buf
			return nil, readErr
		}
	}
}

func readFirstLineReuse(scratch *[]byte, path string, limit int64) ([]byte, error) {
	if scratch == nil {
		return readMetricFile(path, limit)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	buf := *scratch
	if cap(buf) < 4096 {
		buf = make([]byte, 4096)
	} else {
		buf = buf[:cap(buf)]
	}
	total := 0
	for {
		if total == len(buf) {
			if int64(len(buf)) > limit {
				*scratch = buf
				return nil, errors.New("metric file exceeds size limit")
			}
			nextSize := len(buf) * 2
			if maxLen := int(limit) + 1; nextSize > maxLen {
				nextSize = maxLen
			}
			grown := make([]byte, nextSize)
			copy(grown, buf[:total])
			buf = grown
		}
		n, readErr := file.Read(buf[total:])
		if n > 0 {
			prev := total
			total += n
			for i := prev; i < total; i++ {
				if buf[i] == '\n' {
					*scratch = buf
					return buf[:i], nil
				}
			}
			if int64(total) > limit {
				*scratch = buf
				return nil, errors.New("metric file exceeds size limit")
			}
		}
		if readErr == io.EOF {
			*scratch = buf
			return buf[:total], nil
		}
		if readErr != nil {
			*scratch = buf
			return nil, readErr
		}
	}
}

func isASCIIWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\v' || c == '\f'
}

func equalBytesString[T ~[]byte | ~string](data T, target string) bool {
	if len(data) != len(target) {
		return false
	}
	for i := 0; i < len(target); i++ {
		if data[i] != target[i] {
			return false
		}
	}
	return true
}

func parseUintASCII[T ~[]byte | ~string](data T) (uint64, error) {
	if len(data) == 0 {
		return 0, errors.New("empty integer field")
	}
	var value uint64
	for i := 0; i < len(data); i++ {
		c := data[i]
		if c < '0' || c > '9' {
			return 0, errors.New("invalid integer field")
		}
		digit := uint64(c - '0')
		if value > (math.MaxUint64-digit)/10 {
			return 0, errors.New("integer field overflow")
		}
		value = value*10 + digit
	}
	return value, nil
}

func nextASCIIField[T ~[]byte | ~string](data T, pos int) (T, int, bool) {
	for pos < len(data) && isASCIIWhitespace(data[pos]) {
		pos++
	}
	if pos >= len(data) {
		var zero T
		return zero, pos, false
	}
	start := pos
	for pos < len(data) && !isASCIIWhitespace(data[pos]) {
		pos++
	}
	return data[start:pos], pos, true
}

func nextLineSlice[T ~[]byte | ~string](data T, pos int) (T, int, bool) {
	if pos >= len(data) {
		var zero T
		return zero, pos, false
	}
	start := pos
	for pos < len(data) && data[pos] != '\n' {
		pos++
	}
	end := pos
	if pos < len(data) && data[pos] == '\n' {
		pos++
	}
	if end > start && data[end-1] == '\r' {
		end--
	}
	return data[start:end], pos, true
}

func parseCPUTimesData[T ~[]byte | ~string](contents T) (procCPUTimes, error) {
	line, _, ok := nextLineSlice(contents, 0)
	if !ok {
		return procCPUTimes{}, errors.New("/proc/stat has no aggregate CPU line")
	}
	tag, pos, ok := nextASCIIField(line, 0)
	if !ok || !equalBytesString(tag, "cpu") {
		return procCPUTimes{}, errors.New("/proc/stat has no aggregate CPU line")
	}
	var values procCPUTimes
	count := 0
	for count < len(values) {
		field, nextPos, ok := nextASCIIField(line, pos)
		if !ok {
			break
		}
		pos = nextPos
		value, err := parseUintASCII(field)
		if err != nil {
			return procCPUTimes{}, errors.New("/proc/stat contains an invalid CPU counter")
		}
		values[count] = value
		count++
	}
	if count < 4 {
		return procCPUTimes{}, errors.New("/proc/stat CPU line is incomplete")
	}
	return values, nil
}

// The usage counter belongs to the visible cgroup root. Process affinity
// (including runtime.NumCPU) describes the collector, not that root.
func cgroupCPUReading() (cpuReading, error) {
	const root = "/sys/fs/cgroup"
	if contents, err := readMetricFile(root+"/cpu.stat", 16<<10); err == nil {
		for _, line := range strings.Split(string(contents), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "usage_usec" {
				usage, err := strconv.ParseUint(fields[1], 10, 64)
				if err != nil {
					return cpuReading{}, err
				}
				reading := cpuReading{at: time.Now(), usageUsec: usage}
				reading.quota, err = cgroupCPUCapacity(root, true)
				return reading, err
			}
		}
	}
	nanos, err := readUintMetric(root + "/cpuacct/cpuacct.usage")
	if err != nil {
		return cpuReading{}, err
	}
	reading := cpuReading{at: time.Now(), usageUsec: nanos / 1000, cgroupV1: true}
	reading.quota, err = cgroupCPUCapacity(root, false)
	return reading, err
}

func cgroupCPUCapacity(root string, v2 bool) (float64, error) {
	cpuset := "cpuset.cpus.effective"
	if !v2 {
		cpuset = "cpuset/cpuset.effective_cpus"
	}
	var count int
	for _, path := range []string{filepath.Join(root, cpuset), "/sys/devices/system/cpu/online"} {
		if contents, err := readMetricFile(path, 64<<10); err == nil {
			if count, err = cpuListCount(strings.TrimSpace(string(contents))); err == nil {
				break
			}
		}
	}
	// Without a quota limit, capacity is the group's effective CPU set.
	// On the hierarchy root with no cpuset controller, use online host CPUs.
	var quota, period string
	if v2 {
		if contents, err := readMetricFile(filepath.Join(root, "cpu.max"), 128); err == nil {
			fields := strings.Fields(string(contents))
			if len(fields) != 2 {
				return 0, errors.New("invalid cgroup cpu.max")
			}
			if fields[0] != "max" {
				quota, period = fields[0], fields[1]
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
	} else if contents, err := readMetricFile(filepath.Join(root, "cpu/cpu.cfs_quota_us"), 128); err == nil {
		if value := strings.TrimSpace(string(contents)); value != "-1" {
			quota = value
			contents, err = readMetricFile(filepath.Join(root, "cpu/cpu.cfs_period_us"), 128)
			if err != nil {
				return 0, err
			}
			period = strings.TrimSpace(string(contents))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	if period != "" || quota != "" {
		q, qErr := strconv.ParseUint(quota, 10, 64)
		p, pErr := strconv.ParseUint(period, 10, 64)
		if qErr != nil || pErr != nil || q == 0 || p == 0 {
			return 0, errors.New("invalid cgroup CPU quota")
		}
		limit := float64(q) / float64(p)
		if count == 0 || limit < float64(count) {
			return limit, nil
		}
	}
	if count == 0 {
		return 0, errors.New("cgroup CPU capacity unavailable")
	}
	return float64(count), nil
}

// Linux exports CPU sets as ordered, non-overlapping ranges, e.g. 0-3,8.
func cpuListCount(list string) (int, error) {
	count, previous := 0, -1
	for part := range strings.SplitSeq(list, ",") {
		first, last, ranged := strings.Cut(part, "-")
		lo, err := strconv.Atoi(first)
		if err != nil || lo <= previous {
			return 0, errors.New("invalid CPU list")
		}
		hi := lo
		if ranged {
			hi, err = strconv.Atoi(last)
		}
		if err != nil || hi < lo || hi == math.MaxInt {
			return 0, errors.New("invalid CPU range")
		}
		count += hi - lo + 1
		previous = hi
	}
	return count, nil
}
