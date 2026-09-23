package core

import (
	"encoding/json"
	"testing"
)

func TestIPQualityArchiveFamily(t *testing.T) {
	if IPQualityReportFamily(json.RawMessage(`{"Head":{"IP":"::ffff:203.0.113.1"}}`)) != 4 {
		t.Fatal("mapped IPv4 archive assigned to IPv6")
	}
	if IPQualityReportFamily(json.RawMessage(`{"Head":{"IP":"2001:db8::1"}}`)) != 6 {
		t.Fatal("lost IPv6 mapping")
	}
}
