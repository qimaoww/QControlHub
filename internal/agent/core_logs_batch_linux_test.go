//go:build linux

package agent

import (
	"fmt"
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

func TestCoreLogBatchOwnershipAcrossAppendRetryAndACK(t *testing.T) {
	t.Parallel()
	for _, count := range []int{1, core.MaxCoreLogBatchEntries, core.MaxCoreLogBatchEntries + 1, coreLogQueueLimit} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			collector := NewCoreLogCollector(map[core.Engine]EngineSpec{})
			for i := range count {
				collector.append(core.CoreLogEntry{Engine: core.EngineXray, Message: fmt.Sprint(i)})
			}
			storage := collector.queued
			first := collector.NextBatch()
			for _, entry := range storage[:min(count, core.MaxCoreLogBatchEntries)] {
				if entry != (core.CoreLogEntry{}) {
					t.Fatal("consumed queue prefix retained an entry")
				}
			}
			first.Entries[0].Message = "caller changed its copy"
			collector.append(core.CoreLogEntry{Engine: core.EngineMihomo, InstanceID: "shr_0123456789abcdef", Message: "arrived while pending"})
			retry := collector.NextBatch()
			if retry.ID != first.ID || retry.Entries[0].Message != "0" {
				t.Fatal("append or caller mutation changed the pending batch")
			}
			if collector.Acknowledge("unrelated") || collector.NextBatch().Entries[0].Message != "0" {
				t.Fatal("unrelated ACK released the pending batch")
			}
			if !collector.Acknowledge(first.ID) || retry.Entries[0].Message != "0" {
				t.Fatal("ACK changed a batch already handed to a sender")
			}
			next := min(count, core.MaxCoreLogBatchEntries)
			// The existing full-queue policy evicts the oldest queued line,
			// while preserving all entries already awaiting ACK.
			if count == coreLogQueueLimit {
				next++
			}
			for batch := collector.NextBatch(); batch != nil; batch = collector.NextBatch() {
				for _, entry := range batch.Entries {
					if next < count {
						if entry.Message != fmt.Sprint(next) || entry.Engine != core.EngineXray {
							t.Fatalf("queued entry %d changed: %+v", next, entry)
						}
					} else if entry.Message != "arrived while pending" || entry.InstanceID != "shr_0123456789abcdef" {
						t.Fatalf("new instance entry changed: %+v", entry)
					}
					next++
				}
				collector.Acknowledge(batch.ID)
			}
			if next != count+1 {
				t.Fatalf("delivered through %d, want %d", next, count)
			}
			for _, entry := range collector.queueStorage[:cap(collector.queueStorage)] {
				if entry != (core.CoreLogEntry{}) {
					t.Fatal("drained queue retained an acknowledged entry")
				}
			}
			collector.append(core.CoreLogEntry{Message: "after recovery"})
			if batch := collector.NextBatch(); len(batch.Entries) != 1 || batch.Entries[0].Message != "after recovery" {
				t.Fatal("queue did not recover after draining")
			}
		})
	}
}
