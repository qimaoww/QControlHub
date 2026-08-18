package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestAuditLogsRecordListAndPrune(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()

	if err := dataStore.RecordAudit(ctx, core.AuditLogEntry{
		Action: "config.created", Target: "cfg_0123456789abcdef", Detail: "demo v1", RemoteIP: "127.0.0.1",
	}); err != nil {
		t.Fatalf("record audit: %v", err)
	}
	oversized := core.AuditLogEntry{
		Action: "task.created", Target: strings.Repeat("t", 600), Detail: strings.Repeat("d", 3000),
	}
	if err := dataStore.RecordAudit(ctx, oversized); err != nil {
		t.Fatalf("record oversized audit: %v", err)
	}
	if err := dataStore.RecordAudit(ctx, core.AuditLogEntry{}); err == nil {
		t.Fatal("empty audit entry was accepted")
	}

	entries, err := dataStore.ListAuditLogs(ctx, 10)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("audit entries = %d, want >= 2", len(entries))
	}
	if entries[0].Action != "task.created" || len(entries[0].Target) != 503 || len(entries[0].Detail) != 2003 {
		t.Fatalf("newest audit entry = %+v (oversized fields not truncated)", entries[0])
	}
	if entries[1].Action != "config.created" || entries[1].Target != "cfg_0123456789abcdef" {
		t.Fatalf("second audit entry = %+v", entries[1])
	}
	if entries[0].ActedAt.Before(entries[1].ActedAt) {
		t.Fatal("audit entries are not ordered newest first")
	}

	pruned, err := dataStore.PruneAuditLogs(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil || pruned != int64(len(entries)) {
		t.Fatalf("prune audit logs = %d, %v; want %d", pruned, err, len(entries))
	}
	remaining, err := dataStore.ListAuditLogs(ctx, 10)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("audit entries after prune = %d, %v; want none", len(remaining), err)
	}
}
