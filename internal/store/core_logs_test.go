package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCoreLogsStoreQueryAndDeduplicateWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dataStore, err := Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	agent, enrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, agent.ID, enrollmentID)

	now := time.Now().UTC().Truncate(time.Microsecond)
	batch := core.CoreLogBatch{ID: "log_0123456789abcdef", Entries: []core.CoreLogEntry{
		{Engine: core.EngineXray, Level: "warn", Message: "reality handshake warning", LoggedAt: now},
		{Engine: core.EngineSingBox, Level: "info", Message: "service started", LoggedAt: now.Add(time.Second)},
	}}
	if err := dataStore.StoreCoreLogs(ctx, agent.ID, batch); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.StoreCoreLogs(ctx, agent.ID, batch); err != nil {
		t.Fatalf("duplicate batch: %v", err)
	}
	otherAgent, otherEnrollmentID := enrollTaskTestAgent(t, ctx, dataStore)
	defer cleanupTaskTestAgent(dataStore, otherAgent.ID, otherEnrollmentID)
	if err := dataStore.StoreCoreLogs(ctx, otherAgent.ID, batch); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-agent duplicate batch error = %v", err)
	}
	entries, err := dataStore.ListCoreLogs(ctx, CoreLogQuery{AgentID: agent.ID, Limit: 20})
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	if entries[0].Engine != core.EngineSingBox || entries[1].Level != "warning" {
		t.Fatalf("ordered entries = %+v", entries)
	}
	filtered, err := dataStore.ListCoreLogs(ctx, CoreLogQuery{AgentID: agent.ID, Engine: core.EngineXray, Level: "warning", Search: "handshake", Limit: 20})
	if err != nil || len(filtered) != 1 || filtered[0].Message != batch.Entries[0].Message {
		t.Fatalf("filtered entries = %+v, %v", filtered, err)
	}
	older, err := dataStore.ListCoreLogs(ctx, CoreLogQuery{AgentID: agent.ID, Before: entries[0].ID, Limit: 20})
	if err != nil || len(older) != 1 || older[0].ID != entries[1].ID {
		t.Fatalf("older entries = %+v, %v", older, err)
	}

	invalid := core.CoreLogBatch{ID: "log_fedcba9876543210", Entries: []core.CoreLogEntry{{
		Engine: core.EngineXray, Level: "info", Message: strings.Repeat("x", core.MaxCoreLogMessageBytes+1), LoggedAt: now,
	}}}
	if err := dataStore.StoreCoreLogs(ctx, agent.ID, invalid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized log error = %v", err)
	}
	if _, err := dataStore.pool.Exec(ctx, `UPDATE core_log_batches SET received_at=$2 WHERE id=$1`, batch.ID, time.Now().UTC().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	pruned, err := dataStore.PruneCoreLogs(ctx, time.Now().UTC().Add(-time.Hour))
	if err != nil || pruned != 1 {
		t.Fatalf("pruned batches = %d, %v", pruned, err)
	}
	remaining, err := dataStore.ListCoreLogs(ctx, CoreLogQuery{AgentID: agent.ID, Limit: 20})
	if err != nil || len(remaining) != 0 {
		t.Fatalf("logs after prune = %+v, %v", remaining, err)
	}
}

func TestStoreSanitizeCoreLogMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"ansi color", "\x1b[32mINFO\x1b[0m inbound started", "INFO inbound started"},
		{"ansi multi attrs", "\x1b[1;33mWARN\x1b[0m dial timeout", "WARN dial timeout"},
		{"lone esc", "up\x1bstream", "upstream"},
		{"nul", "before\x00after", "before�after"},
		{"invalid utf8", "bad\xffbyte", "bad�byte"},
		{"plain unchanged", "plain text", "plain text"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sanitizeCoreLogMessage(test.in); got != test.want {
				t.Fatalf("sanitizeCoreLogMessage(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
	if got := sanitizeCoreLogMessage("\x1b[31m"); got != "" {
		t.Fatalf("sanitizeCoreLogMessage(only ansi) = %q", got)
	}
}
