//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func newImportedSingBoxLogExecutor(t *testing.T, content string) *Executor {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	markerPrefix := filepath.Join(t.TempDir(), "migration")
	sourceDigest := strings.Repeat("b", 64)
	if err := writeCoreMigrationMarker(markerPrefix, core.EngineSingBox, coreMigrationComplete,
		coreMigrationConfigDigest(content), sourceDigest, "enabled", "disabled"); err != nil {
		t.Fatal(err)
	}
	managed := EngineSpec{ConfigPath: configPath, Service: "qagent-sing-box.service"}
	return &Executor{
		Specs: map[core.Engine]EngineSpec{core.EngineSingBox: managed}, MigrationMarkerPrefix: markerPrefix,
		completedMigrations: map[core.Engine]completedCoreMigration{core.EngineSingBox: {
			Managed: managed, SourceDigest: sourceDigest,
		}},
		verifyCompletedMigration: func(context.Context, EngineSpec, EngineSpec, *ServiceManager) error { return nil },
	}
}

func containsArgument(arguments []string, expected string) bool {
	for _, argument := range arguments {
		if argument == expected {
			return true
		}
	}
	return false
}

func waitForLine(t *testing.T, collector *CoreLogCollector, want string) (core.CoreLogEntry, bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, batch := range []*core.CoreLogBatch{collector.NextBatch()} {
			if batch == nil {
				break
			}
			var matched *core.CoreLogEntry
			for i, entry := range batch.Entries {
				if want == "" {
					t.Fatalf("unexpected entry streamed before tail start: %+v", entry)
				}
				if entry.Message == want {
					candidate := batch.Entries[i]
					matched = &candidate
				}
			}
			if matched != nil {
				return *matched, true
			}
			collector.Acknowledge(batch.ID)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if want == "" {
		return core.CoreLogEntry{}, true
	}
	return core.CoreLogEntry{}, false
}
