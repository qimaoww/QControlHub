package core

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTrafficPeriodAtUsesCalendarAnchor(t *testing.T) {
	anchor := time.Date(2024, time.January, 31, 16, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	tests := []struct {
		name  string
		cycle TrafficCycle
		now   time.Time
		start string
		end   string
	}{
		{"monthly clamps February", TrafficCycleMonthly, time.Date(2024, 2, 20, 0, 0, 0, 0, time.UTC), "2024-01-31", "2024-02-29"},
		{"monthly restores anchor day", TrafficCycleMonthly, time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC), "2024-02-29", "2024-03-31"},
		{"yearly follows anniversary", TrafficCycleYearly, time.Date(2025, 2, 20, 0, 0, 0, 0, time.UTC), "2025-01-31", "2026-01-31"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start, end, err := TrafficPeriodAt(anchor, test.cycle, test.now)
			if err != nil {
				t.Fatal(err)
			}
			if start.Format(time.DateOnly) != test.start || end.Format(time.DateOnly) != test.end {
				t.Fatalf("period = %s..%s, want %s..%s", start.Format(time.DateOnly), end.Format(time.DateOnly), test.start, test.end)
			}
		})
	}
	leapAnchor := time.Date(2024, time.February, 29, 0, 0, 0, 0, time.UTC)
	start, end, err := TrafficPeriodAt(leapAnchor, TrafficCycleYearly, time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || start.Format(time.DateOnly) != "2025-02-28" || end.Format(time.DateOnly) != "2026-02-28" {
		t.Fatalf("leap anniversary = %s..%s, %v", start.Format(time.DateOnly), end.Format(time.DateOnly), err)
	}
}

func TestPortTrafficPolicyJSONDefaultsLegacyAutoBlock(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want bool
	}{
		{name: "legacy field omitted", body: `{"id":"trf_0123456789abcdef"}`, want: true},
		{name: "monitor only explicit", body: `{"id":"trf_0123456789abcdef","auto_block":false}`, want: false},
		{name: "blocking explicit", body: `{"id":"trf_0123456789abcdef","auto_block":true}`, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var policy PortTrafficPolicy
			if err := json.Unmarshal([]byte(test.body), &policy); err != nil {
				t.Fatal(err)
			}
			if policy.AutoBlock != test.want {
				t.Fatalf("auto_block = %v, want %v", policy.AutoBlock, test.want)
			}
		})
	}
}

func TestNormalizePortTrafficPolicyRequest(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	request, err := NormalizePortTrafficPolicyRequest(PortTrafficPolicyRequest{
		AgentID: " agt_test ", Name: " edge tls ", Engine: EngineXray,
		Port: 443, Protocol: TrafficProtocolBoth, Cycle: TrafficCycleMonthly,
		CycleAnchor: now.Add(-24 * time.Hour), LimitBytes: 100 << 30,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if request.AgentID != "agt_test" || request.Name != "edge tls" || request.CycleAnchor.Hour() != 0 || request.AutoBlock == nil || !*request.AutoBlock {
		t.Fatalf("normalized request = %+v", request)
	}
	request.CycleAnchor = now.Add(24 * time.Hour)
	if _, err := NormalizePortTrafficPolicyRequest(request, now); err == nil {
		t.Fatal("future anchor was accepted")
	}
}
