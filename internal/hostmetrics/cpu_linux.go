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

func parseCPUTimes(contents string) (uint64, uint64, error) {
	return parseCPUTimesData(contents)
}

func parseCPUTimesData[T ~[]byte | ~string](contents T) (uint64, uint64, error) {
	line, _, ok := nextLineSlice(contents, 0)
	if !ok {
		return 0, 0, errors.New("/proc/stat has no aggregate CPU line")
	}
	tag, pos, ok := nextASCIIField(line, 0)
	if !ok || !equalBytesString(tag, "cpu") {
		return 0, 0, errors.New("/proc/stat has no aggregate CPU line")
	}
	var values [8]uint64
	count := 0
	for count < len(values) {
		field, nextPos, ok := nextASCIIField(line, pos)
		if !ok {
			break
		}
		pos = nextPos
		value, err := parseUintASCII(field)
		if err != nil {
			return 0, 0, errors.New("/proc/stat contains an invalid CPU counter")
		}
		values[count] = value
		count++
	}
	if count < 4 {
		return 0, 0, errors.New("/proc/stat CPU line is incomplete")
	}
	var total uint64
	for i := 0; i < count; i++ {
		if math.MaxUint64-total < values[i] {
			return 0, 0, errors.New("CPU counter overflow")
		}
		total += values[i]
	}
	idle := values[3]
	if count > 4 {
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
