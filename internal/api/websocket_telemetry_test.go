package api

import (
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestWSRateBucketRefillBurstAndLimit(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	bucket := newWSRateBucket(2.0, 3.0, start)

	for i := 0; i < 3; i++ {
		if !bucket.allow(start) {
			t.Fatalf("burst frame %d rejected unexpectedly", i)
		}
	}
	if bucket.allow(start) {
		t.Fatal("frame beyond initial burst was allowed")
	}
	if !bucket.allow(start.Add(500 * time.Millisecond)) {
		t.Fatal("token should have refilled after 500ms at 2/s")
	}
	if bucket.allow(start.Add(500 * time.Millisecond)) {
		t.Fatal("second frame after 500ms refill should be rate limited")
	}
	// Huge elapsed interval clamps at burst ceiling.
	afterIdle := start.Add(10 * time.Minute)
	for i := 0; i < 3; i++ {
		if !bucket.allow(afterIdle) {
			t.Fatalf("burst frame %d after idle rejected", i)
		}
	}
	if bucket.allow(afterIdle) {
		t.Fatal("refill exceeded burst cap")
	}
}

func TestSameHTTPChallenges(t *testing.T) {
	t.Parallel()
	exp := time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC)
	a := []core.HTTPChallenge{{OrderID: "ord1", Domain: "a.example", Token: "t1", Value: "v1", ExpiresAt: exp}}
	b := []core.HTTPChallenge{{OrderID: "ord1", Domain: "a.example", Token: "t1", Value: "v1", ExpiresAt: exp}}

	if !sameHTTPChallenges(nil, []core.HTTPChallenge{}) {
		t.Fatal("nil and empty slices should match to suppress empty broadcasts")
	}
	if !sameHTTPChallenges(a, b) {
		t.Fatal("equal challenge slices reported different")
	}
	b[0].Value = "v2"
	if sameHTTPChallenges(a, b) {
		t.Fatal("different challenge value reported equal")
	}
}

func TestSameHostMetricsSnapshotAndMergeCompact(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC)
	rateAvail := true
	prev := core.HostMetrics{
		CollectedAt:          ts,
		CPUAvailable:         true,
		CPUPercent:           12.5,
		NetworkRateAvailable: &rateAvail,
		NetworkInterfaces:    []core.HostNetworkInterface{{Name: "eth0", Addresses: []string{"10.0.0.2/24"}}},
		BBR:                  &core.SystemBBRStatus{CollectedAt: ts, Available: true, CongestionControl: "bbr"},
		ClockSync:            &core.ClockSyncStatus{Status: "synchronized", CheckedAt: ts},
	}

	// Advancing CollectedAt with omitted nested snapshots should be recognized as unchanged.
	compactUnchanged := core.HostMetrics{
		CollectedAt:          ts.Add(time.Second),
		CPUAvailable:         true,
		CPUPercent:           12.5,
		NetworkRateAvailable: &rateAvail,
	}
	if !sameHostMetricsSnapshot(prev, compactUnchanged) {
		t.Fatal("compact snapshot with identical scalars treated as changed")
	}

	compactChanged := compactUnchanged
	compactChanged.CPUPercent = 25.0
	if sameHostMetricsSnapshot(prev, compactChanged) {
		t.Fatal("changed CPUPercent treated as identical")
	}

	merged := mergeCompactHostMetrics(&prev, compactChanged)
	if merged.CPUPercent != 25.0 || len(merged.NetworkInterfaces) != 1 || merged.BBR == nil || merged.ClockSync == nil {
		t.Fatalf("compact merge dropped prior nested snapshots: %+v", merged)
	}
	// Mutating source slice must not affect merged copy.
	prev.NetworkInterfaces[0].Addresses[0] = "192.0.2.99/24"
	if merged.NetworkInterfaces[0].Addresses[0] != "10.0.0.2/24" {
		t.Fatalf("merged NetworkInterfaces aliases previous slice: %+v", merged.NetworkInterfaces)
	}

	// Present empty interface slice explicitly replaces prior interfaces.
	clearedIfaces := compactUnchanged
	clearedIfaces.NetworkInterfaces = []core.HostNetworkInterface{}
	if sameHostMetricsSnapshot(prev, clearedIfaces) {
		t.Fatal("explicit empty NetworkInterfaces should differ from non-empty snapshot")
	}
	if sameHostMetricsSnapshot(compactUnchanged, clearedIfaces) || !sameHostMetricsSnapshot(clearedIfaces, compactUnchanged) {
		t.Fatal("first explicit clear after reconnect must be written; a later omission must not rewrite it")
	}
}

func TestSamePortTrafficUsageSliceAndClone(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC)
	items := []core.PortTrafficUsage{{
		PolicyID:              "pol1",
		ShareID:               "shr1",
		CollectedAt:           ts,
		CounterEpoch:          "boot-1",
		LifetimeReceivedBytes: 1000,
		ReceivedBytes:         500,
		Accounting: &core.TrafficAccounting{
			Source:         "listener",
			Inbound:        "mixed-1",
			Outbounds:      []string{"direct"},
			ProcessEpoch:   "proc-1",
			ClientReceived: 500,
			Counters:       map[string]uint64{"tcp": 500},
		},
	}}
	cloned := clonePortTrafficUsageSlice(items)
	if !samePortTrafficUsageSlice(items, cloned) {
		t.Fatal("cloned slice does not compare equal")
	}
	// Advancing CollectedAt invalidates equality so live rate calculations still persist.
	items[0].CollectedAt = ts.Add(time.Second)
	if samePortTrafficUsageSlice(items, cloned) {
		t.Fatal("CollectedAt change was incorrectly ignored")
	}
	items[0].CollectedAt = ts
	items[0].Accounting.ClientReceived = 999
	if samePortTrafficUsageSlice(items, cloned) {
		t.Fatal("Accounting scalar mutation leaked into clone")
	}
	items[0].Accounting.ClientReceived = 500
	items[0].Accounting.Counters["tcp"] = 999
	if samePortTrafficUsageSlice(items, cloned) {
		t.Fatal("Accounting map mutation leaked into clone")
	}
}
