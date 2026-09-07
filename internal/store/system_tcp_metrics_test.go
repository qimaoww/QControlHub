package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestSystemTCPMetricsKeepSampleTimeAndBoundPayload(t *testing.T) {
	now := time.Now().UTC()
	for _, sampledAt := range []time.Time{now.Add(-time.Hour), now.Add(time.Hour)} {
		status := &core.SystemBBRStatus{CollectedAt: sampledAt, Available: true, CongestionControl: "bbr3", Persistence: "unmanaged"}
		encoded, err := encodeHeartbeatMetrics(&core.HostMetrics{BBR: status}, now)
		if err != nil {
			t.Fatal(err)
		}
		var decoded core.HostMetrics
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		want := sampledAt
		if want.After(now) {
			want = now
		}
		if !decoded.BBR.CollectedAt.Equal(want) || !status.CollectedAt.Equal(sampledAt) || decoded.BBR.CongestionControl != "bbr3" || decoded.BBR.Persistence != "unmanaged" {
			t.Fatalf("stale sample made fresh or caller mutated: %+v", decoded)
		}
	}
	for _, status := range []*core.SystemBBRStatus{
		{Error: strings.Repeat("x", 33<<10)},
		{Qdiscs: make([]core.SystemQdisc, 257)},
		{AvailableAlgorithms: make([]string, 65)},
	} {
		if _, err := encodeHeartbeatMetrics(&core.HostMetrics{BBR: status}, now); err == nil {
			t.Fatal("accepted unbounded TCP telemetry")
		}
	}
}
