//go:build linux

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestExistingCoreMigrationSwitchesServicesAndPersistsCompletion(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	if err := os.WriteFile(fixture.managed.Binary, []byte("previous managed core bytes\n"), 0o750); err != nil {
		t.Fatal(err)
	}
	output, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
	})
	if err != nil {
		t.Fatalf("import existing config: %v\n%s", err, output)
	}
	if !strings.Contains(output, "stopped and disabled xray.service") {
		t.Fatalf("migration output = %q", output)
	}
	fixture.assertServiceState(t, "xray.service", "inactive", "disabled")
	fixture.assertServiceState(t, "qagent-xray.service", "active", "enabled")
	assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.importedConfig, 0o600)
	existingBinary, err := os.ReadFile(fixture.existing.Binary)
	if err != nil {
		t.Fatal(err)
	}
	assertFileContentAndMode(t, fixture.managed.Binary, string(existingBinary), 0o750)
	for _, kind := range []string{"binary", "config"} {
		if _, err := os.Lstat(coreMigrationBackupPath(fixture.markerPrefix, core.EngineXray, kind)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("completed migration left %s rollback backup: %v", kind, err)
		}
	}
	if _, pending := fixture.executor.ExistingSpecs[core.EngineXray]; pending {
		t.Fatal("completed migration remained pending in memory")
	}
	if ownership, ok := fixture.executor.completedMigrations[core.EngineXray]; !ok || ownership.Existing != fixture.existing || ownership.Managed != fixture.managed {
		t.Fatalf("completed migration ownership = %+v, present=%v", ownership, ok)
	}
	output, err = fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
	})
	if err != nil || !strings.Contains(output, "already completed") {
		t.Fatalf("idempotent migration retry = %q, %v", output, err)
	}
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: `{"inbounds":[],"outbounds":[],"tag":"different"}`,
	}); err == nil || !strings.Contains(err.Error(), "no existing service") {
		t.Fatalf("different migration retry error = %v", err)
	}

	restarted := &Executor{
		Specs:                 map[core.Engine]EngineSpec{core.EngineXray: fixture.managed},
		ExistingSpecs:         map[core.Engine]EngineSpec{core.EngineXray: fixture.existing},
		MigrationMarkerPrefix: fixture.markerPrefix,
	}
	if err := restarted.LoadCoreMigrationState(); err != nil {
		t.Fatalf("reload migration marker: %v", err)
	}
	if _, pending := restarted.ExistingSpecs[core.EngineXray]; pending {
		t.Fatal("completed migration returned after Agent restart")
	}
	if _, ok := restarted.completedMigrations[core.EngineXray]; !ok {
		t.Fatal("verified migration ownership was not restored after Agent restart")
	}
	changedSource := fixture.existing
	changedSource.ConfigPath = filepath.Join(filepath.Dir(changedSource.ConfigPath), "replacement.json")
	remapped := &Executor{
		ExistingSpecs:         map[core.Engine]EngineSpec{core.EngineXray: changedSource},
		MigrationMarkerPrefix: fixture.markerPrefix,
	}
	if err := remapped.LoadCoreMigrationState(); err != nil {
		t.Fatalf("load stale migration marker: %v", err)
	}
	if _, pending := remapped.ExistingSpecs[core.EngineXray]; !pending {
		t.Fatal("a completed marker for another source suppressed a new mapping")
	}
}
