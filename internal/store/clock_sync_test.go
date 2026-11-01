package store

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestEncodeHeartbeatClockSyncBoundsAndNormalizesAgentReport(t *testing.T) {
	receivedAt := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	offset := -1.25
	metrics := core.HostMetrics{ClockSync: &core.ClockSyncStatus{
		Status: "synchronized", OffsetMilliseconds: &offset, CheckedAt: receivedAt.Add(time.Minute),
	}}
	encoded, err := encodeHeartbeatMetrics(&metrics, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	var decoded core.HostMetrics
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ClockSync == nil || decoded.ClockSync.Status != "synchronized" || decoded.ClockSync.OffsetMilliseconds == nil || *decoded.ClockSync.OffsetMilliseconds != offset || !decoded.ClockSync.CheckedAt.Equal(receivedAt) {
		t.Fatalf("clock sync report = %+v", decoded.ClockSync)
	}
}

func TestEncodeHeartbeatSanitizesInvalidClockSyncValues(t *testing.T) {
	badOffset := math.Inf(1)
	metrics := core.HostMetrics{ClockSync: &core.ClockSyncStatus{Status: "untrusted", OffsetMilliseconds: &badOffset}}
	encoded, err := encodeHeartbeatMetrics(&metrics, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var decoded core.HostMetrics
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ClockSync == nil || decoded.ClockSync.Status != "unavailable" || decoded.ClockSync.OffsetMilliseconds != nil {
		t.Fatalf("invalid clock sync report was retained: %+v", decoded.ClockSync)
	}
}
