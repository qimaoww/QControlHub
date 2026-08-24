package store

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestEncodeHeartbeatMetricsValidatesAndServerStamps(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, 7, 31, 5, 0, 0, 0, time.UTC)
	encoded, err := encodeHeartbeatMetrics(&core.HostMetrics{
		CollectedAt: time.Unix(1, 0), CPUAvailable: true, CPUPercent: 23.5,
		MemoryAvailable: true, MemoryUsedBytes: 2 << 30, MemoryTotalBytes: 4 << 30,
		DiskAvailable: true, DiskUsedBytes: 10 << 30, DiskTotalBytes: 20 << 30,
		NetworkAvailable: true, NetworkRXBytes: 1000, NetworkTXBytes: 500, NetworkRXBPS: 20, NetworkTXBPS: 10,
		NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"192.0.2.20"}}},
		ObservedPublicIP:  "::ffff:93.184.216.34",
	}, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	var decoded core.HostMetrics
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.CollectedAt.Equal(receivedAt) || decoded.CPUPercent != 23.5 || decoded.MemoryUsedBytes != 2<<30 || len(decoded.NetworkInterfaces) != 1 || decoded.ObservedPublicIP != "93.184.216.34" {
		t.Fatalf("encoded metrics = %+v", decoded)
	}

	invalid := []core.HostMetrics{
		{CPUAvailable: true, CPUPercent: math.NaN()},
		{CPUAvailable: true, CPUPercent: 101},
		{MemoryAvailable: true, MemoryUsedBytes: 2, MemoryTotalBytes: 1},
		{DiskAvailable: true, DiskUsedBytes: 1, DiskTotalBytes: 0},
		{NetworkAvailable: true, NetworkRXBPS: maxReportedMetricBytes + 1},
		{NetworkInterfaces: []core.HostNetworkInterface{{Name: "../eth0", Addresses: []string{"192.0.2.20"}}}},
		{NetworkInterfaces: []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"0.0.0.0"}}}},
		{ObservedPublicIP: "10.0.0.8"},
		{ObservedPublicIP: "100.64.0.8"},
		{ObservedPublicIP: "192.0.2.8"},
		{ObservedPublicIP: "198.18.0.8"},
		{ObservedPublicIP: "198.51.100.8"},
		{ObservedPublicIP: "203.0.113.8"},
		{ObservedPublicIP: "2001:db8::8"},
		{ObservedPublicIP: "not-an-address"},
		{PublicIPv4: "2606:4700:4700::1111"},
		{PublicIPv4: "10.0.0.8"},
		{PublicIPv4: "203.0.113.8"},
		{PublicIPv4: "not-an-address"},
		{PublicIPv6: "93.184.216.34"},
		{PublicIPv6: "::ffff:93.184.216.34"},
		{PublicIPv6: "fc00::8"},
		{PublicIPv6: "2001:db8::8"},
		{PublicIPv6: "not-an-address"},
	}
	for _, metrics := range invalid {
		if _, err := encodeHeartbeatMetrics(&metrics, receivedAt); err == nil {
			t.Errorf("encodeHeartbeatMetrics(%+v) unexpectedly succeeded", metrics)
		}
	}
}

func TestEncodeHeartbeatMetricsAcceptsDualStackProbedAddresses(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, 7, 31, 5, 0, 0, 0, time.UTC)
	encoded, err := encodeHeartbeatMetrics(&core.HostMetrics{
		ObservedPublicIP: "93.184.216.34",
		PublicIPv4:       "198.35.26.96",
	}, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" {
		t.Fatal("expected encoded payload")
	}
	encoded, err = encodeHeartbeatMetrics(&core.HostMetrics{
		PublicIPv6: "2001:4860:4860::8888",
	}, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	var decoded core.HostMetrics
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.PublicIPv6 != "2001:4860:4860::8888" || decoded.PublicIPv4 != "" {
		t.Fatalf("encoded probed addresses = %+v", decoded)
	}
	// Older Agents never probe; empty fields must keep encoding successfully.
	if _, err := encodeHeartbeatMetrics(&core.HostMetrics{}, receivedAt); err != nil {
		t.Fatal(err)
	}
}

func TestEncodeHeartbeatMetricsClearsCloudflareProbes(t *testing.T) {
	t.Parallel()
	receivedAt := time.Date(2026, 8, 24, 5, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		metrics core.HostMetrics
	}{
		{name: "IPv4", metrics: core.HostMetrics{PublicIPv4: "172.69.135.152"}},
		{name: "IPv6", metrics: core.HostMetrics{PublicIPv6: "2400:cb00::1"}},
		{name: "mapped IPv4", metrics: core.HostMetrics{PublicIPv4: "::ffff:172.69.135.152"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := encodeHeartbeatMetrics(&test.metrics, receivedAt)
			if err != nil {
				t.Fatalf("encode relay probe: %v", err)
			}
			var decoded core.HostMetrics
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("decode cleared relay probe: %v", err)
			}
			if decoded.PublicIPv4 != "" || decoded.PublicIPv6 != "" {
				t.Fatalf("relay probe was persisted: %+v", decoded)
			}
		})
	}

	encoded, err := encodeHeartbeatMetrics(&core.HostMetrics{PublicIPv4: "93.184.216.34", PublicIPv6: "2001:4860:4860::8888"}, receivedAt)
	if err != nil {
		t.Fatalf("encode genuine probes: %v", err)
	}
	var decoded core.HostMetrics
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode genuine probes: %v", err)
	}
	if decoded.PublicIPv4 != "93.184.216.34" || decoded.PublicIPv6 != "2001:4860:4860::8888" {
		t.Fatalf("genuine probes were not retained: %+v", decoded)
	}
}
