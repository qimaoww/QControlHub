//go:build linux

package agent

import (
	"context"
	"math"
	"net"
	"strings"
	"testing"
	"time"
)

func TestParseCPUTimes(t *testing.T) {
	t.Parallel()
	total, idle, err := parseCPUTimes("cpu  100 2 30 400 5 6 7 8 0 0\ncpu0 1 2 3 4\n")
	if err != nil || total != 558 || idle != 405 {
		t.Fatalf("parseCPUTimes() = total %d idle %d error %v", total, idle, err)
	}
	if _, _, err := parseCPUTimes("cpu invalid 1 2 3\n"); err == nil {
		t.Fatal("parseCPUTimes accepted an invalid counter")
	}
}

func TestParseMemoryUsageExcludesReclaimableCaches(t *testing.T) {
	t.Parallel()
	contents := "MemTotal: 8192 kB\nMemFree: 1024 kB\nMemAvailable: 3072 kB\nBuffers: 512 kB\nCached: 2048 kB\nSReclaimable: 256 kB\nShmem: 128 kB\n"
	used, total, err := parseMemoryUsage(contents)
	if err != nil || total != 8192*1024 || used != 4480*1024 {
		t.Fatalf("parseMemoryUsage() = used %d total %d error %v", used, total, err)
	}
}

func TestParseMemoryUsageKeepsSharedMemoryUsed(t *testing.T) {
	t.Parallel()
	contents := "MemTotal: 4096 kB\nMemFree: 512 kB\nBuffers: 128 kB\nCached: 1024 kB\nSReclaimable: 128 kB\nShmem: 256 kB\n"
	used, total, err := parseMemoryUsage(contents)
	if err != nil || total != 4096*1024 || used != 2560*1024 {
		t.Fatalf("parseMemoryUsage() = used %d total %d error %v", used, total, err)
	}
}

func TestParseMemoryUsageRequiresCoreCounters(t *testing.T) {
	t.Parallel()
	for _, contents := range []string{
		"MemTotal: 4096 kB\nBuffers: 128 kB\nCached: 1024 kB\n",
		"MemTotal: 4096 kB\nMemFree: 512 kB\nCached: 1024 kB\n",
		"MemTotal: 4096 kB\nMemFree: 512 kB\nBuffers: 128 kB\n",
	} {
		if _, _, err := parseMemoryUsage(contents); err == nil {
			t.Fatalf("parseMemoryUsage accepted incomplete meminfo %q", contents)
		}
	}
}

func TestDefaultRouteParsersAndInterfaceSafety(t *testing.T) {
	t.Parallel()
	interfaces := make(map[string]struct{})
	parseIPv4DefaultRoutes("Iface Destination Gateway Flags RefCnt Use Metric Mask\neth0 00000000 0100000A 0003 0 0 100 00000000\nlo 00000000 00000000 0001 0 0 0 00000000\n", interfaces)
	parseIPv6DefaultRoutes(strings.Repeat("0", 32)+" 00 "+strings.Repeat("0", 32)+" 00 "+strings.Repeat("0", 32)+" 00000064 00000000 00000000 00000001 eth1\n", interfaces)
	if _, ok := interfaces["eth0"]; !ok {
		t.Fatal("IPv4 default route interface was not detected")
	}
	if _, ok := interfaces["eth1"]; !ok {
		t.Fatal("IPv6 default route interface was not detected")
	}
	for _, name := range []string{"eth0", "ens18.20", "bond0:1"} {
		if !safeNetworkInterfaceName(name) {
			t.Errorf("safeNetworkInterfaceName(%q) = false", name)
		}
	}
	for _, name := range []string{"", "lo", "../eth0", "eth 0", strings.Repeat("a", 65)} {
		if safeNetworkInterfaceName(name) {
			t.Errorf("safeNetworkInterfaceName(%q) = true", name)
		}
	}
}

func TestUsableNetworkAddressAndPreference(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"192.168.31.205", "203.0.113.20", "2001:db8::20", "fd00::20"} {
		if !usableNetworkAddress(net.ParseIP(value)) {
			t.Errorf("usableNetworkAddress(%q) = false", value)
		}
	}
	for _, value := range []string{"0.0.0.0", "127.0.0.1", "169.254.1.2", "::", "::1", "fe80::1", "ff02::1"} {
		if usableNetworkAddress(net.ParseIP(value)) {
			t.Errorf("usableNetworkAddress(%q) = true", value)
		}
	}
	if networkAddressPriority(net.ParseIP("203.0.113.20")) >= networkAddressPriority(net.ParseIP("192.168.31.205")) {
		t.Fatal("public IPv4 should be preferred over private IPv4")
	}
	if networkAddressPriority(net.ParseIP("192.168.31.205")) >= networkAddressPriority(net.ParseIP("2001:db8::20")) {
		t.Fatal("private IPv4 should be preferred over IPv6")
	}
}

func TestBytesPerSecond(t *testing.T) {
	t.Parallel()
	if got := bytesPerSecond(1500, 1.5); got != 1000 {
		t.Fatalf("bytesPerSecond() = %d", got)
	}
	if got := bytesPerSecond(math.MaxUint64, 0.1); got != math.MaxUint64 {
		t.Fatalf("bytesPerSecond overflow clamp = %d", got)
	}
}

func TestMetricsCollectorReadsLiveLinuxHost(t *testing.T) {
	collector := NewMetricsCollector()
	first, err := collector.Collect(context.Background())
	if err != nil && !metricsHaveData(first) {
		t.Fatalf("first metrics collection: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	second, err := collector.Collect(context.Background())
	if err != nil && !metricsHaveData(second) {
		t.Fatalf("second metrics collection: %v", err)
	}
	if second.CollectedAt.IsZero() || !second.MemoryAvailable || !second.DiskAvailable || !second.NetworkAvailable {
		t.Fatalf("live metrics are incomplete: %+v", second)
	}
	if second.MemoryTotalBytes == 0 || second.MemoryUsedBytes > second.MemoryTotalBytes || second.DiskTotalBytes == 0 || second.DiskUsedBytes > second.DiskTotalBytes {
		t.Fatalf("live capacity metrics are invalid: %+v", second)
	}
	if len(second.NetworkInterfaces) == 0 || len(second.NetworkInterfaces[0].Addresses) == 0 {
		t.Fatalf("live default-route interface addresses are missing: %+v", second.NetworkInterfaces)
	}
}
