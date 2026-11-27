//go:build linux

package hostmetrics

import (
	"context"
	"math"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseCPUTimes(t *testing.T) {
	t.Parallel()
	samples, err := parseCPUTimesData("cpu  100 2 30 400 5 6 7 8 90 1\ncpu0 1 2 3 4\n")
	if err != nil || samples != (procCPUTimes{100, 2, 30, 400, 5, 6, 7, 8}) {
		t.Fatalf("parseCPUTimesData() = %v, %v", samples, err)
	}
	if _, err := parseCPUTimesData("cpu invalid 1 2 3\n"); err == nil {
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

func TestProcCPUUsageWindows(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after procCPUTimes
		want          float64
		valid         bool
	}{
		{"tickless idle", procCPUTimes{100}, procCPUTimes{100}, 0, true},
		{"idle plus IO wait", procCPUTimes{}, procCPUTimes{50, 0, 0, 25, 25}, 50, true},
		{"revised IO wait", procCPUTimes{100, 0, 0, 300, 100}, procCPUTimes{110, 0, 0, 390, 10}, 10, true},
		{"hypervisor took whole window", procCPUTimes{}, procCPUTimes{7: 100}, 0, true},
		{"partly stolen", procCPUTimes{}, procCPUTimes{25, 0, 0, 0, 0, 0, 0, 75}, 25, true},
		{"busy includes interrupts", procCPUTimes{}, procCPUTimes{10, 20, 30, 0, 0, 20, 20, 0}, 100, true},
		{"one counter reset", procCPUTimes{100}, procCPUTimes{0, 0, 200}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, valid := tc.after.usageSince(tc.before); valid != tc.valid || got != tc.want {
				t.Fatalf("usageSince() = %v, %t; want %v, %t", got, valid, tc.want, tc.valid)
			}
		})
	}
}

func TestCPUWindowIgnoresBackToBackSamples(t *testing.T) {
	start := time.Unix(100, 0)
	for _, tc := range []struct {
		name                              string
		quota                             float64
		baseline, short, half, full, idle uint64
	}{
		{"proc", 0, 1000, 1001, 1100, 1200, 50},
		{"cgroup", 0.5, 1000, 8000, 251000, 751000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sample cpuSample
			check := func(after time.Duration, total, idle uint64, percent float64, available bool, baseline time.Duration) {
				t.Helper()
				sample.record(cpuReading{at: start.Add(after), proc: procCPUTimes{0: total - idle, 3: idle}, usageUsec: total, quota: tc.quota})
				if sample.percent != percent || sample.available != available || !sample.reading.at.Equal(start.Add(baseline)) {
					t.Fatalf("after %s, got %+v; want %.1f%%, available=%t, baseline=%s", after, sample, percent, available, baseline)
				}
			}
			check(0, tc.baseline, 0, 0, false, 0)
			check(5*time.Millisecond, tc.short, 0, 0, false, 0)
			check(time.Second, tc.half, tc.idle, 50, true, time.Second)
			check(time.Second+time.Millisecond, tc.half+1, tc.idle, 50, true, time.Second)
			check(2*time.Second, tc.full, tc.idle, 100, true, 2*time.Second)
			check(3*time.Second, 10, 0, 0, false, 3*time.Second)
			check(4*time.Second, 10, 0, 0, true, 4*time.Second)
		})
	}
}

func TestCgroupCPUCapacity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		v2    bool
		quota string
		want  float64
	}{
		{"v2 unlimited", true, "max", 5},
		{"v2 fractional", true, "25000", 0.25},
		{"v2 cpuset below quota", true, "800000", 5},
		{"v2 invalid quota", true, "-1", 0},
		{"v1 unlimited", false, "-1", 5},
		{"v1 fractional", false, "50000", 0.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{"cpuset.cpus.effective": "1-2,6,8-9", "cpu.max": tc.quota + " 100000"}
			if !tc.v2 {
				files = map[string]string{"cpuset/cpuset.effective_cpus": "1-2,6,8-9", "cpu/cpu.cfs_quota_us": tc.quota, "cpu/cpu.cfs_period_us": "100000"}
			}
			for name, value := range files {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(value+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := cgroupCPUCapacity(root, tc.v2)
			if got != tc.want || (err != nil) != (tc.want == 0) {
				t.Fatalf("capacity = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestCPUWindowRestartsWhenSourceOrQuotaChanges(t *testing.T) {
	var sample cpuSample
	start := time.Unix(100, 0)
	for i, quota := range []float64{0, 1, 0.5, 0} {
		at := start.Add(time.Duration(2*i) * time.Second)
		sample.record(cpuReading{at: at, proc: procCPUTimes{1000}, usageUsec: 1000, quota: quota})
		if sample.available {
			t.Fatalf("quota %v reused a different CPU baseline", quota)
		}
		sample.record(cpuReading{at: at.Add(time.Second), proc: procCPUTimes{1000}, usageUsec: 1000, quota: quota})
		if !sample.available || sample.percent != 0 {
			t.Fatalf("quota %v, complete idle window: %+v", quota, sample)
		}
	}
}

func TestMetricsCollectorReadsLiveLinuxHost(t *testing.T) {
	collector := NewCollector()
	first, err := collector.Collect(context.Background())
	if err != nil && !HasData(first) {
		t.Fatalf("first metrics collection: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	second, err := collector.Collect(context.Background())
	if err != nil && !HasData(second) {
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

func TestInterfaceDiscoveryPublishesOnlyCompleteSnapshots(t *testing.T) {
	names, err := routedNetworkInterfaces()
	if err != nil {
		t.Fatal(err)
	}
	good, err := networkInterfaceDetails(names)
	if err != nil || len(good) == 0 {
		t.Fatalf("real routed interface control: count=%d err=%v", len(good), err)
	}
	// A disappeared route interface must not erase previously known addresses.
	// This name exceeds Linux IFNAMSIZ, so it cannot exist on the test host.
	partial, err := networkInterfaceDetails(append(names, "qch-missing-interface"))
	if err == nil || partial != nil {
		t.Fatalf("incomplete discovery: got %d entries, omitted=%t, error=%v", len(partial), partial == nil, err)
	}
	empty, err := networkInterfaceDetails(nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("authoritative empty discovery: %v, %v", empty, err)
	}
}

func TestSysfsFallbackFindsKernelNetworkInterfaces(t *testing.T) {
	devices, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	names := fallbackNetworkInterfaces()
	if slices.Contains(names, "lo") {
		t.Fatal("loopback included in fallback")
	}
	for _, device := range devices {
		if safeNetworkInterfaceName(device.Name) && !slices.Contains(names, device.Name) {
			t.Fatalf("sysfs fallback omitted kernel interface %q", device.Name)
		}
	}
}

func TestParseProcNetDevCounters(t *testing.T) {
	t.Parallel()
	sample := "Inter-|   Receive                                                |  Transmit\n" +
		" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n" +
		"    lo: 1000       10    0    0    0     0          0         0     2000       10    0    0    0     0       0          0\n" +
		"  eth0: 1234567890   100 0 0 0 0 0 0 9876543210 200 0 0 0 0 0 0\n" +
		" ens18:9001       50 0 0 0 0 0 0 42        10 0 0 0 0 0 0\n"
	rx, tx, ok := parseProcNetDevCounters(sample, []string{"eth0", "ens18"})
	if !ok || rx != 1234576891 || tx != 9876543252 {
		t.Fatalf("parseProcNetDevCounters() = (%d, %d, %v)", rx, tx, ok)
	}
	if _, _, ok := parseProcNetDevCounters(sample, []string{"eth0", "missing0"}); ok {
		t.Fatal("parseProcNetDevCounters accepted missing interface")
	}
	if _, _, ok := parseProcNetDevCounters(sample+"\neth0: 1 0 0 0 0 0 0 0 1 0 0 0 0 0 0 0\n", []string{"eth0"}); ok {
		t.Fatal("parseProcNetDevCounters accepted duplicate interface row")
	}
}

func TestCollectorCachesAndInvalidatesInterfaces(t *testing.T) {
	collector := NewCollector()
	first, err := collector.Collect(context.Background())
	if err != nil && !HasData(first) {
		t.Fatalf("first collect: %v", err)
	}
	if !collector.previous.interfacesValid || len(collector.previous.interfaces) == 0 {
		t.Fatalf("expected routed interfaces cache to be populated: %+v", collector.previous.interfaces)
	}
	cachedAt := collector.previous.interfacesAt
	second, err := collector.Collect(context.Background())
	if err != nil && !HasData(second) {
		t.Fatalf("second collect: %v", err)
	}
	if !collector.previous.interfacesAt.Equal(cachedAt) {
		t.Fatalf("expected cached interface timestamp %v to be reused, got %v", cachedAt, collector.previous.interfacesAt)
	}
	collector.InvalidateInterfaces()
	if collector.previous.interfacesValid {
		t.Fatal("InvalidateInterfaces did not clear interfacesValid")
	}
}

func BenchmarkCollectorCollect(b *testing.B) {
	collector := NewCollector()
	ctx := context.Background()
	if _, err := collector.Collect(ctx); err != nil {
		b.Fatalf("warmup Collect: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := collector.Collect(ctx); err != nil {
			b.Fatalf("Collect: %v", err)
		}
	}
}
