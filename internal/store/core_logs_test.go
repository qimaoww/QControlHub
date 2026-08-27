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
	originalSettings, err := dataStore.PanelSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	testSettings := originalSettings
	testSettings.CoreLogMinimumLevel = "debug"
	if _, err := dataStore.SavePanelSettings(ctx, testSettings); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, restoreErr := dataStore.SavePanelSettings(cleanupCtx, originalSettings); restoreErr != nil {
			t.Errorf("restore panel settings: %v", restoreErr)
		}
	}()
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

	testSettings.CoreLogMinimumLevel = "warning"
	if _, err := dataStore.SavePanelSettings(ctx, testSettings); err != nil {
		t.Fatal(err)
	}
	thresholdBatch := core.CoreLogBatch{ID: "log_1111111111111111", Entries: []core.CoreLogEntry{
		{Engine: core.EngineMihomo, Level: "debug", Message: "threshold debug", LoggedAt: now},
		{Engine: core.EngineXray, Level: "info", Message: "threshold info", LoggedAt: now},
		{Engine: core.EngineSingBox, Level: "warn", Message: "threshold warning", LoggedAt: now},
		{Engine: core.EngineShadowsocksRust, Level: "critical", Message: "threshold critical", LoggedAt: now},
	}}
	if err := dataStore.StoreCoreLogs(ctx, agent.ID, thresholdBatch); err != nil {
		t.Fatal(err)
	}
	thresholdEntries, err := dataStore.ListCoreLogs(ctx, CoreLogQuery{AgentID: agent.ID, Search: "threshold", Limit: 20})
	if err != nil || len(thresholdEntries) != 2 || thresholdEntries[0].Level != "critical" || thresholdEntries[1].Level != "warning" {
		t.Fatalf("threshold entries = %+v, %v", thresholdEntries, err)
	}

	testSettings.CoreLogMinimumLevel = "off"
	if _, err := dataStore.SavePanelSettings(ctx, testSettings); err != nil {
		t.Fatal(err)
	}
	offBatch := core.CoreLogBatch{ID: "log_2222222222222222", Entries: []core.CoreLogEntry{{
		Engine: core.EngineXray, Level: "critical", Message: "disabled persistence", LoggedAt: now,
	}}}
	if err := dataStore.StoreCoreLogs(ctx, agent.ID, offBatch); err != nil {
		t.Fatal(err)
	}
	disabledEntries, err := dataStore.ListCoreLogs(ctx, CoreLogQuery{AgentID: agent.ID, Search: "disabled persistence", Limit: 20})
	if err != nil || len(disabledEntries) != 0 {
		t.Fatalf("disabled persistence entries = %+v, %v", disabledEntries, err)
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
	if err != nil || len(remaining) != 2 {
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

func TestCoreLogLevelAtLeast(t *testing.T) {
	t.Parallel()
	tests := []struct {
		level   string
		minimum string
		want    bool
	}{
		{"debug", "debug", true},
		{"info", "debug", true},
		{"info", "warning", false},
		{"warning", "warning", true},
		{"critical", "error", true},
		{"critical", "off", false},
		{"invalid", "debug", false},
		{"error", "invalid", false},
	}
	for _, test := range tests {
		if got := coreLogLevelAtLeast(test.level, test.minimum); got != test.want {
			t.Errorf("coreLogLevelAtLeast(%q, %q) = %t, want %t", test.level, test.minimum, got, test.want)
		}
	}
}
