//go:build linux

package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCoreLogCollectorKeepsBatchUntilAcknowledged(t *testing.T) {
	t.Parallel()
	collector := NewCoreLogCollector()
	collector.append(core.CoreLogEntry{Engine: core.EngineXray, Level: "info", Message: "first", LoggedAt: time.Now()})
	collector.append(core.CoreLogEntry{Engine: core.EngineMihomo, Level: "debug", Message: "second", LoggedAt: time.Now()})
	first := collector.NextBatch()
	if first == nil || len(first.Entries) != 2 || !strings.HasPrefix(first.ID, "log_") {
		t.Fatalf("first batch = %+v", first)
	}
	retry := collector.NextBatch()
	if retry == nil || retry.ID != first.ID || len(retry.Entries) != 2 {
		t.Fatalf("retry batch = %+v", retry)
	}
	if collector.Acknowledge("log_0000000000000000") {
		t.Fatal("collector accepted an unrelated acknowledgment")
	}
	if !collector.Acknowledge(first.ID) || collector.NextBatch() != nil {
		t.Fatal("acknowledged batch remained queued")
	}
}
