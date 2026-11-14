//go:build linux

package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestParseChronyTrackingReportsSignedOffsetAndSyncState(t *testing.T) {
	checkedAt := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name, output, status string
		wantOffset           float64
	}{
		{"positive", "Last offset : +0.000125 seconds\nLeap status : Normal\n", "synchronized", 0.125},
		{"negative", "Last offset : -0.0025 seconds\nLeap status : Normal\n", "synchronized", -2.5},
		{"unsynchronized", "Last offset : +0.004 seconds\nLeap status : Not synchronised\n", "unsynchronized", 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := parseChronyTracking(test.output, checkedAt)
			if got.Status != test.status || got.OffsetMilliseconds == nil || *got.OffsetMilliseconds != test.wantOffset || !got.CheckedAt.Equal(checkedAt) {
				t.Fatalf("clock sync = %+v, want status=%q offset=%g", got, test.status, test.wantOffset)
			}
		})
	}
}

func TestParseChronyTrackingMissingOffsetDoesNotInventOne(t *testing.T) {
	got := parseChronyTracking("Leap status : Not synchronised\n", time.Now())
	if got.Status != "unsynchronized" || got.OffsetMilliseconds != nil {
		t.Fatalf("unexpected missing-offset status: %+v", got)
	}
}

func TestCachedClockSyncSnapshotPopulatesCacheBeforeCancelAndRespectsCanceledContext(t *testing.T) {
	prevCollector := clockSyncCollector
	t.Cleanup(func() { clockSyncCollector = prevCollector })

	checkedAt := time.Now().UTC()
	var calls atomic.Int32
	done := make(chan struct{}, 2)
	clockSyncCollector = func(ctx context.Context) *core.ClockSyncStatus {
		defer func() { done <- struct{}{} }()
		n := calls.Add(1)
		if n == 1 {
			return &core.ClockSyncStatus{Status: "synchronized", CheckedAt: checkedAt}
		}
		<-ctx.Done()
		return &core.ClockSyncStatus{Status: "unavailable", CheckedAt: time.Now().UTC()}
	}

	client := &Client{}
	ctx := context.Background()
	if snap := client.cachedClockSyncSnapshot(ctx); snap != nil {
		t.Fatalf("initial snapshot = %+v, want nil while async refresh starts", snap)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async clock sync refresh")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		snap := client.cachedClockSyncSnapshot(ctx)
		if snap != nil && snap.Status == "synchronized" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cached clock sync was not stored after refresh completed: %+v", snap)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Second read within TTL must reuse cached entry without triggering another collector call.
	if calls.Load() != 1 {
		t.Fatalf("collector call count = %d, want 1", calls.Load())
	}

	// Expire cache and verify canceled/timed-out refresh does not overwrite cached status.
	client.clockSyncMu.Lock()
	client.clockSync.CheckedAt = time.Now().UTC().Add(-time.Minute)
	client.clockSyncMu.Unlock()
	cancelCtx, cancel := context.WithCancel(context.Background())
	_ = client.cachedClockSyncSnapshot(cancelCtx)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for canceled clock sync refresh")
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		client.clockSyncMu.Lock()
		refreshing := client.clockSyncRefreshing
		status := ""
		if client.clockSync != nil {
			status = client.clockSync.Status
		}
		client.clockSyncMu.Unlock()
		if !refreshing {
			if status != "synchronized" {
				t.Fatalf("canceled refresh overwrote cached status with %q", status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("clockSyncRefreshing remained true after canceled refresh")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
