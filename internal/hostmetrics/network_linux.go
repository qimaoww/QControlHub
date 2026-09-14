//go:build linux

package hostmetrics

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

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
			} else if strings.Contains(value, "%") {
				continue
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
	if ip == nil {
		return false
	}
	address, ok := netip.AddrFromSlice(ip)
	return ok && ip.IsGlobalUnicast() && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !netpolicy.IsCloudflareAddress(address)
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
		if err == nil {
			parseNetworkDeviceNames(string(contents), result)
		}
	}
	if len(result) == 0 {
		for _, entry := range fallbackNetworkInterfaces() {
			result[entry] = struct{}{}
		}
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

func fallbackNetworkInterfaces() []string {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && safeNetworkInterfaceName(entry.Name()) && entry.Name() != "lo" {
			result = append(result, entry.Name())
		}
	}
	sort.Strings(result)
	return result
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
