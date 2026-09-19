package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func qualityReport(ip string) string {
	return `{"Head":{"IP":"` + ip + `","Version":"test"},"Info":{},"Type":{},"Score":{"IPQS":"null","ipapi":"0.47%"},"Factor":{},"Media":{},"Mail":{}}`
}

func TestIPQualityJSONStreams(t *testing.T) {
	for _, content := range []string{
		qualityReport("203.0.113.1"),
		"\r\n" + qualityReport("203.0.113.1") + "\n" + qualityReport("2001:db8::1") + "\n",
	} {
		result, err := ParseIPQualityReports([]byte(content))
		if err != nil {
			t.Fatal(err)
		}
		var report map[string]any
		if err := json.Unmarshal(result.Reports[0], &report); err != nil {
			t.Fatal(err)
		}
		if report["Score"].(map[string]any)["IPQS"] != "null" {
			t.Fatal("missing data became a score")
		}
		if _, err := NormalizeIPQualityResult(&result); err != nil {
			t.Fatal(err)
		}
	}
	for _, content := range []string{
		"", "null", "[]", "{}", qualityReport("10.0.0.1"),
		qualityReport("::1"), qualityReport("203.0.*.*"),
		qualityReport("203.0.113.1") + qualityReport("203.0.113.2"),
		qualityReport("203.0.113.1") + "{",
		qualityReport("203.0.113.1") + "diagnostic output",
		strings.Replace(qualityReport("203.0.113.1"), `"Media":{}`, `"Media":[]`, 1),
		strings.Repeat(" ", MaxIPQualityResultBytes+1),
	} {
		if _, err := ParseIPQualityReports([]byte(content)); err == nil {
			t.Fatalf("accepted malformed report: %.100s", content)
		}
	}
	if _, err := NormalizeIPQualityResult(nil); err == nil {
		t.Fatal("accepted a missing result")
	}
	if !ActionIPQuality.Valid() || !ActionIPQuality.AgentLevel() || !ActionIPQuality.RequiresAgentManagement() {
		t.Fatal("IP quality is not a managed host task")
	}
}

func TestIPQualityDateRange(t *testing.T) {
	for _, test := range []struct {
		day, zone, start string
		hours            int
	}{
		{"2026-09-16", "Asia/Shanghai", "2026-09-15T16:00:00Z", 24},
		{"2026-03-08", "America/New_York", "2026-03-08T05:00:00Z", 23},
		{"2026-11-01", "America/New_York", "2026-11-01T04:00:00Z", 25},
		{"2026-09-16", "", "2026-09-16T00:00:00Z", 24},
	} {
		start, end, err := IPQualityDateRange(test.day, test.zone)
		if err != nil || start.Format(time.RFC3339) != test.start || end.Sub(start) != time.Duration(test.hours)*time.Hour {
			t.Fatalf("%+v: %v %v %v", test, start, end, err)
		}
	}
	for _, day := range []string{"", "2026-2-03", "2026-02-30", "2026-09-16T00:00:00Z"} {
		if _, _, err := IPQualityDateRange(day, "UTC"); err == nil {
			t.Fatalf("accepted date %q", day)
		}
	}
	for _, zone := range []string{"Local", "../../etc/passwd", "Invalid/Zone"} {
		if _, _, err := IPQualityDateRange("2026-09-16", zone); err == nil {
			t.Fatalf("accepted timezone %q", zone)
		}
	}
}
