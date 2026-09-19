package core

import (
	"encoding/json"
	"testing"
)

func TestIPQualityArchiveURLs(t *testing.T) {
	for _, value := range []string{"https://Report.Check.Place/IP/2XEN01YWC.svg", "https://report.check.place/IP/abc-123.svg", "https://Report.Check.Place/ip/current-format.svg"} {
		if !ValidIPQualityReportURL(value) {
			t.Fatalf("rejected upstream URL %q", value)
		}
	}
	for _, value := range []string{"http://report.check.place/IP/a.svg", "https://report.check.place.evil.test/IP/a.svg",
		"https://report.check.place:443/IP/a.svg", "https://report.check.place@127.0.0.1/IP/a.svg",
		"https://report.check.place/IP/../a.svg", "https://report.check.place/IP/%61.svg",
		"https://report.check.place/IP/a.svg?redirect=http://localhost", "https://report.check.place/IP/a.svg#x",
		"https://report.check.place/IP/a.svg\n", "https://report.check.place/IP/a.html"} {
		if ValidIPQualityReportURL(value) {
			t.Fatalf("accepted unsafe URL %q", value)
		}
	}
	result, err := ParseIPQualityReports([]byte(`{"Head":{"IP":"203.0.113.1"},"Info":{},"Type":{},"Score":{},"Factor":{},"Media":{},"Mail":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	result.ReportURLs = []string{"https://Report.Check.Place/IP/fixture.svg"}
	got, err := NormalizeIPQualityResult(&result)
	if err != nil || len(got.ReportURLs) != 1 {
		t.Fatalf("lost report URL: %+v %v", got, err)
	}
	result.ReportURLs = append(result.ReportURLs, result.ReportURLs[0])
	if _, err := NormalizeIPQualityResult(&result); err == nil {
		t.Fatal("accepted extra URLs")
	}
	if IPQualityReportFamily(json.RawMessage(`{"Head":{"IP":"::ffff:203.0.113.1"}}`)) != 4 {
		t.Fatal("mapped IPv4 archive assigned to IPv6")
	}
	if IPQualityReportFamily(json.RawMessage(`{"Head":{"IP":"2001:db8::1"}}`)) != 6 {
		t.Fatal("lost IPv6 mapping")
	}
}
