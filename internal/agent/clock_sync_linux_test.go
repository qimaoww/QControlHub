//go:build linux

package agent

import (
	"testing"
	"time"
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
