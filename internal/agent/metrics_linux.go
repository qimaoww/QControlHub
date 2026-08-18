//go:build linux

package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func collectHostMetrics(ctx context.Context, previous metricSample) (core.HostMetrics, metricSample, error) {
	if err := ctx.Err(); err != nil {
		return core.HostMetrics{}, previous, err
	}
	now := time.Now().UTC()
	metrics := core.HostMetrics{CollectedAt: now}
	next := previous
	problems := make([]error, 0, 4)

	if contents, err := readMetricFile("/proc/stat", 256<<10); err == nil {
		total, idle, parseErr := parseCPUTimes(string(contents))
		if parseErr != nil {
			problems = append(problems, parseErr)
		} else {
			next.cpuTotal, next.cpuIdle, next.cpuValid = total, idle, true
			if previous.cpuValid && total >= previous.cpuTotal && idle >= previous.cpuIdle {
				totalDelta := total - previous.cpuTotal
				idleDelta := idle - previous.cpuIdle
				if totalDelta > 0 && idleDelta <= totalDelta {
					metrics.CPUAvailable = true
					metrics.CPUPercent = float64(totalDelta-idleDelta) * 100 / float64(totalDelta)
				}
			}
		}
	} else {
		problems = append(problems, fmt.Errorf("read CPU metrics: %w", err))
	}

	if contents, err := readMetricFile("/proc/meminfo", 256<<10); err == nil {
		used, total, parseErr := parseMemoryUsage(string(contents))
		if parseErr != nil {
			problems = append(problems, parseErr)
		} else {
			metrics.MemoryAvailable = true
			metrics.MemoryUsedBytes = used
			metrics.MemoryTotalBytes = total
		}
	} else {
		problems = append(problems, fmt.Errorf("read memory metrics: %w", err))
	}

	if used, total, err := rootDiskUsage(); err != nil {
		problems = append(problems, fmt.Errorf("read disk metrics: %w", err))
	} else {
		metrics.DiskAvailable = true
		metrics.DiskUsedBytes = used
		metrics.DiskTotalBytes = total
	}

	interfaces, interfaceErr := routedNetworkInterfaces()
	if interfaceErr != nil {
		problems = append(problems, interfaceErr)
	} else {
		metrics.NetworkInterfaces, interfaceErr = networkInterfaceDetails(interfaces)
		if interfaceErr != nil {
			problems = append(problems, interfaceErr)
		}
		rx, tx, err := networkCounters(interfaces)
		if err != nil {
			problems = append(problems, err)
		} else {
			key := strings.Join(interfaces, ",")
			metrics.NetworkAvailable = true
			metrics.NetworkRXBytes = rx
			metrics.NetworkTXBytes = tx
			if previous.networkValid && previous.networkKey == key && rx >= previous.networkRX && tx >= previous.networkTX {
				seconds := now.Sub(previous.networkAt).Seconds()
				if seconds > 0 {
					metrics.NetworkRXBPS = bytesPerSecond(rx-previous.networkRX, seconds)
					metrics.NetworkTXBPS = bytesPerSecond(tx-previous.networkTX, seconds)
				}
			}
			next.networkRX, next.networkTX, next.networkKey, next.networkAt, next.networkValid = rx, tx, key, now, true
		}
	}

	if !metricsHaveData(metrics) {
		metrics.CollectedAt = time.Time{}
	}
	return metrics, next, errors.Join(problems...)
}

func networkInterfaceDetails(names []string) ([]core.HostNetworkInterface, error) {
	details := make([]core.HostNetworkInterface, 0, len(names))
	problems := make([]error, 0)
	for _, name := range names {
		if !safeNetworkInterfaceName(name) {
			problems = append(problems, errors.New("unsafe network interface name"))
			continue
		}
		device, err := net.InterfaceByName(name)
		if err != nil {
			problems = append(problems, fmt.Errorf("read %s addresses: %w", name, err))
			continue
		}
		assigned, err := device.Addrs()
		if err != nil {
			problems = append(problems, fmt.Errorf("read %s addresses: %w", name, err))
			continue
		}
		addresses := make([]string, 0, len(assigned))
		seen := make(map[string]struct{})
		for _, address := range assigned {
			value := address.String()
			if host, _, parseErr := net.ParseCIDR(value); parseErr == nil {
				value = host.String()
			} else if zone := strings.LastIndexByte(value, '%'); zone >= 0 {
				value = value[:zone]
			}
			ip := net.ParseIP(value)
			if !usableNetworkAddress(ip) {
				continue
			}
			value = ip.String()
			if _, duplicate := seen[value]; duplicate {
				continue
			}
			seen[value] = struct{}{}
			addresses = append(addresses, value)
		}
		sort.SliceStable(addresses, func(i, j int) bool {
			left, right := networkAddressPriority(net.ParseIP(addresses[i])), networkAddressPriority(net.ParseIP(addresses[j]))
			if left != right {
				return left < right
			}
			return addresses[i] < addresses[j]
		})
		if len(addresses) > 0 {
			details = append(details, core.HostNetworkInterface{Name: name, Addresses: addresses})
		}
	}
	return details, errors.Join(problems...)
}

func usableNetworkAddress(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}

func networkAddressPriority(ip net.IP) int {
	if ip == nil {
		return 4
	}
	if ip.To4() != nil {
		if ip.IsPrivate() {
			return 1
		}
		return 0
	}
	if ip.IsPrivate() {
		return 3
	}
	return 2
}

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

func parseMemoryUsage(contents string) (uint64, uint64, error) {
	values := make(map[string]uint64)
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		switch key {
		case "MemTotal", "MemAvailable", "MemFree", "Buffers", "Cached", "SReclaimable":
			value, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil || value > math.MaxUint64/1024 {
				return 0, 0, errors.New("/proc/meminfo contains an invalid counter")
			}
			values[key] = value * 1024
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	total := values["MemTotal"]
	if total == 0 {
		return 0, 0, errors.New("/proc/meminfo has no MemTotal")
	}
	available := values["MemAvailable"]
	if available == 0 {
		available = values["MemFree"] + values["Buffers"] + values["Cached"] + values["SReclaimable"]
	}
	if available > total {
		available = total
	}
	return total - available, total, nil
}

func rootDiskUsage() (uint64, uint64, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs("/", &stats); err != nil {
		return 0, 0, err
	}
	blockSize := uint64(stats.Bsize)
	if blockSize == 0 || stats.Blocks > math.MaxUint64/blockSize || stats.Bfree > stats.Blocks {
		return 0, 0, errors.New("root filesystem returned invalid counters")
	}
	total := stats.Blocks * blockSize
	used := (stats.Blocks - stats.Bfree) * blockSize
	if total == 0 {
		return 0, 0, errors.New("root filesystem size is zero")
	}
	return used, total, nil
}

func routedNetworkInterfaces() ([]string, error) {
	result := make(map[string]struct{})
	if contents, err := readMetricFile("/proc/net/route", 1<<20); err == nil {
		parseIPv4DefaultRoutes(string(contents), result)
	}
	if contents, err := readMetricFile("/proc/net/ipv6_route", 1<<20); err == nil {
		parseIPv6DefaultRoutes(string(contents), result)
	}
	if len(result) == 0 {
		contents, err := readMetricFile("/proc/net/dev", 1<<20)
		if err != nil {
			return nil, fmt.Errorf("find network interfaces: %w", err)
		}
		parseNetworkDeviceNames(string(contents), result)
	}
	interfaces := make([]string, 0, len(result))
	for name := range result {
		if safeNetworkInterfaceName(name) {
			interfaces = append(interfaces, name)
		}
	}
	sort.Strings(interfaces)
	if len(interfaces) == 0 || len(interfaces) > 16 {
		return nil, errors.New("no safe default-route network interface found")
	}
	return interfaces, nil
}

func parseIPv4DefaultRoutes(contents string, result map[string]struct{}) {
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 64)
		if err == nil && flags&1 != 0 && fields[0] != "lo" {
			result[fields[0]] = struct{}{}
		}
	}
}

func parseIPv6DefaultRoutes(contents string, result map[string]struct{}) {
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 10 && fields[0] == strings.Repeat("0", 32) && fields[1] == "00" && fields[len(fields)-1] != "lo" {
			result[fields[len(fields)-1]] = struct{}{}
		}
	}
}

func parseNetworkDeviceNames(contents string, result map[string]struct{}) {
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		name, _, found := strings.Cut(scanner.Text(), ":")
		name = strings.TrimSpace(name)
		if found && name != "lo" {
			result[name] = struct{}{}
		}
	}
}

func safeNetworkInterfaceName(name string) bool {
	if name == "" || name == "lo" || len(name) > 64 || strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	for _, character := range name {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("_.:-", character) {
			continue
		}
		return false
	}
	return true
}

func networkCounters(interfaces []string) (uint64, uint64, error) {
	var rxTotal, txTotal uint64
	for _, name := range interfaces {
		if !safeNetworkInterfaceName(name) {
			return 0, 0, errors.New("unsafe network interface name")
		}
		rx, err := readUintMetric("/sys/class/net/" + name + "/statistics/rx_bytes")
		if err != nil {
			return 0, 0, fmt.Errorf("read %s receive counter: %w", name, err)
		}
		tx, err := readUintMetric("/sys/class/net/" + name + "/statistics/tx_bytes")
		if err != nil {
			return 0, 0, fmt.Errorf("read %s transmit counter: %w", name, err)
		}
		if math.MaxUint64-rxTotal < rx || math.MaxUint64-txTotal < tx {
			return 0, 0, errors.New("network counter overflow")
		}
		rxTotal += rx
		txTotal += tx
	}
	return rxTotal, txTotal, nil
}

func readUintMetric(path string) (uint64, error) {
	contents, err := readMetricFile(path, 128)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(contents)), 10, 64)
}

func bytesPerSecond(delta uint64, seconds float64) uint64 {
	if delta == 0 || seconds <= 0 {
		return 0
	}
	value := float64(delta) / seconds
	if value >= float64(math.MaxUint64) {
		return math.MaxUint64
	}
	return uint64(value)
}
