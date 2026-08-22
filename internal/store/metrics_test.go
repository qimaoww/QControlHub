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
		ObservedPublicIP:  "::ffff:198.51.100.9",
	}, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	var decoded core.HostMetrics
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.CollectedAt.Equal(receivedAt) || decoded.CPUPercent != 23.5 || decoded.MemoryUsedBytes != 2<<30 || len(decoded.NetworkInterfaces) != 1 || decoded.ObservedPublicIP != "198.51.100.9" {
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
		{ObservedPublicIP: "not-an-address"},
	}
	for _, metrics := range invalid {
		if _, err := encodeHeartbeatMetrics(&metrics, receivedAt); err == nil {
			t.Errorf("encodeHeartbeatMetrics(%+v) unexpectedly succeeded", metrics)
		}
	}
}
