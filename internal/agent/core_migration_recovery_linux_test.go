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

func TestExistingCoreMigrationRestartRestoresManagedFilesAcrossPreparationWindows(t *testing.T) {
	requireAgentRoot(t)
	for _, originalBinaryExists := range []bool{false, true} {
		for _, stageFiles := range []string{"none", "binary", "all"} {
			name := "intent persisted before target staging"
			if stageFiles == "binary" {
				name = "binary staged before configuration staging"
			}
			if stageFiles == "all" {
				name = "managed files staged before original stop"
			}
			if originalBinaryExists {
				name += " with original managed binary"
			} else {
				name += " without original managed binary"
			}
			t.Run(name, func(t *testing.T) {
				fixture := newExistingCoreMigrationFixture(t, false)
				originalBinary := ""
				if originalBinaryExists {
					originalBinary = "original managed core bytes\n"
					if err := os.WriteFile(fixture.managed.Binary, []byte(originalBinary), 0o750); err != nil {
						t.Fatal(err)
					}
				}
				record, err := prepareCoreMigrationFileRollback(
					fixture.markerPrefix,
					core.EngineXray,
					fixture.existing,
					fixture.managed,
					coreMigrationRecord{
						State: coreMigrationInProgress, ConfigDigest: coreMigrationConfigDigest(fixture.importedConfig),
						SourceDigest:        coreMigrationSourceDigest(fixture.existing),
						ExistingEnableState: "enabled", ManagedEnableState: "disabled",
					},
				)
				if err != nil {
					t.Fatalf("persist durable migration intent: %v", err)
				}
				if !record.HasFileRollback {
					t.Fatal("durable migration record has no file rollback metadata")
				}
				if stageFiles != "none" {
					if _, err := copyExistingCoreBinary(fixture.existing.Binary, fixture.managed.Binary); err != nil {
						t.Fatalf("stage managed binary: %v", err)
					}
				}
				if stageFiles == "all" {
					if _, err := atomicDeploy(fixture.managed.ConfigPath, fixture.importedConfig); err != nil {
						t.Fatalf("stage managed config: %v", err)
					}
				}

				restarted := &Executor{
					Specs:                 map[core.Engine]EngineSpec{core.EngineXray: fixture.managed},
					ExistingSpecs:         map[core.Engine]EngineSpec{core.EngineXray: fixture.existing},
					MigrationMarkerPrefix: fixture.markerPrefix,
				}
				if err := restarted.LoadCoreMigrationState(); err != nil {
					t.Fatalf("load interrupted migration: %v", err)
				}
				if err := restarted.ReconcileExistingCoreServices(context.Background()); err != nil {
					t.Fatalf("reconcile interrupted migration: %v", err)
				}
				fixture.assertServiceState(t, "xray.service", "active", "enabled")
				fixture.assertServiceState(t, "qagent-xray.service", "inactive", "disabled")
				assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
				if originalBinaryExists {
					assertFileContentAndMode(t, fixture.managed.Binary, originalBinary, 0o750)
				} else if _, err := os.Lstat(fixture.managed.Binary); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("originally absent managed binary remained after restart recovery: %v", err)
				}
				if record, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineXray); err != nil || record.State != coreMigrationNone {
					t.Fatalf("restart recovery marker = %+v, %v", record, err)
				}
				for _, kind := range []string{"binary", "config"} {
					if _, err := os.Lstat(coreMigrationBackupPath(fixture.markerPrefix, core.EngineXray, kind)); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("%s rollback backup remained after recovery: %v", kind, err)
					}
				}
			})
		}
	}
}

func TestExistingCoreMigrationRestartKeepsMarkerWhenRollbackBackupIsMissingOrUnsafe(t *testing.T) {
	requireAgentRoot(t)
	for _, test := range []struct {
		name   string
		kind   string
		mutate func(string) error
	}{
		{name: "missing binary backup", kind: "binary", mutate: os.Remove},
		{name: "unsafe config backup", kind: "config", mutate: func(path string) error { return os.Chmod(path, 0o666) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExistingCoreMigrationFixture(t, false)
			if err := os.WriteFile(fixture.managed.Binary, []byte("original managed core bytes\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			prepareAndStageMigrationFixture(t, fixture, "enabled", "disabled")
			backupPath := coreMigrationBackupPath(fixture.markerPrefix, core.EngineXray, test.kind)
			if err := test.mutate(backupPath); err != nil {
				t.Fatalf("mutate rollback backup: %v", err)
			}
			restarted := &Executor{
				Specs:                 map[core.Engine]EngineSpec{core.EngineXray: fixture.managed},
				ExistingSpecs:         map[core.Engine]EngineSpec{core.EngineXray: fixture.existing},
				MigrationMarkerPrefix: fixture.markerPrefix,
			}
			if err := restarted.LoadCoreMigrationState(); err != nil {
				t.Fatalf("load interrupted migration: %v", err)
			}
			if err := restarted.ReconcileExistingCoreServices(context.Background()); err == nil || !strings.Contains(err.Error(), "rollback backup") {
				t.Fatalf("unsafe rollback backup error = %v", err)
			}
			record, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineXray)
			if err != nil || record.State != coreMigrationInProgress || !record.HasFileRollback {
				t.Fatalf("unsafe rollback marker = %+v, %v", record, err)
			}
			fixture.assertServiceState(t, "xray.service", "active", "enabled")
			fixture.assertServiceState(t, "qagent-xray.service", "inactive", "disabled")
		})
	}
}

func TestExistingCoreReconcileRequiresPersistedMigrationIntent(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "inactive", "enabled")
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "active", "disabled")
	if err := fixture.executor.ReconcileExistingCoreServices(context.Background()); err != nil {
		t.Fatalf("reconcile without intent: %v", err)
	}
	fixture.assertServiceState(t, "xray.service", "inactive", "enabled")
	fixture.assertServiceState(t, "qagent-xray.service", "active", "disabled")
	if _, pending := fixture.executor.ExistingSpecs[core.EngineXray]; !pending {
		t.Fatal("unmarked service state was treated as a completed migration")
	}
}

func TestExistingCoreReconcileRestoresOriginalAfterInterruptedStop(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "inactive", "enabled")
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "inactive", "disabled")
	prepareAndStageMigrationFixture(t, fixture, "enabled", "disabled")
	if err := fixture.executor.ReconcileExistingCoreServices(context.Background()); err != nil {
		t.Fatalf("reconcile interrupted stop: %v", err)
	}
	fixture.assertServiceState(t, "xray.service", "active", "enabled")
	fixture.assertServiceState(t, "qagent-xray.service", "inactive", "disabled")
	if record, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineXray); err != nil || record.State != coreMigrationNone {
		t.Fatalf("recovered migration marker = %q, %v", record.State, err)
	}
}

func TestExistingCoreReconcileRestoresOriginallyActiveManagedService(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "active", "enabled")
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "active", "enabled")
	prepareAndStageMigrationFixtureWithManagedState(t, fixture, "enabled", "enabled", "active")
	if err := fixture.executor.ReconcileExistingCoreServices(context.Background()); err != nil {
		t.Fatalf("reconcile active managed service: %v", err)
	}
	fixture.assertServiceState(t, "xray.service", "active", "enabled")
	fixture.assertServiceState(t, "qagent-xray.service", "active", "enabled")
	assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
	if _, err := os.Stat(fixture.managed.Binary); !os.IsNotExist(err) {
		t.Fatalf("originally absent managed binary was not removed: %v", err)
	}
	if record, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineXray); err != nil || record.State != coreMigrationNone {
		t.Fatalf("active managed recovery marker = %+v, %v", record, err)
	}
}

func TestExistingCoreReconcileFinalizesStartedManagedService(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "inactive", "enabled")
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "active", "disabled")
	prepareAndStageMigrationFixture(t, fixture, "enabled", "disabled")
	if err := fixture.executor.ReconcileExistingCoreServices(context.Background()); err != nil {
		t.Fatalf("reconcile started managed service: %v", err)
	}
	fixture.assertServiceState(t, "xray.service", "inactive", "disabled")
	fixture.assertServiceState(t, "qagent-xray.service", "active", "enabled")
	if _, pending := fixture.executor.ExistingSpecs[core.EngineXray]; pending {
		t.Fatal("reconciled migration remained pending")
	}
	if marked, err := coreMigrationMarked(fixture.markerPrefix, core.EngineXray); err != nil || !marked {
		t.Fatalf("reconciled migration marker = %t, %v", marked, err)
	}
}

func TestExistingCoreReconcileRollsBackTransientOriginalService(t *testing.T) {
	requireAgentRoot(t)
	for _, status := range []string{"activating", "deactivating", "reloading"} {
		t.Run(status, func(t *testing.T) {
			fixture := newExistingCoreMigrationFixture(t, false)
			writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", status, "enabled")
			writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "active", "disabled")
			prepareAndStageMigrationFixture(t, fixture, "enabled", "disabled")
			if err := fixture.executor.ReconcileExistingCoreServices(context.Background()); err != nil {
				t.Fatalf("reconcile transient original state: %v", err)
			}
			fixture.assertServiceState(t, "xray.service", "active", "enabled")
			fixture.assertServiceState(t, "qagent-xray.service", "inactive", "disabled")
			if record, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineXray); err != nil || record.State != coreMigrationNone {
				t.Fatalf("transient recovery marker = %q, %v", record.State, err)
			}
		})
	}
}

func TestExistingCoreReconcileRechecksOriginalBeforeCompletion(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "inactive", "enabled")
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "active", "disabled")
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, "activate-existing-on-managed-enable"), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prepareAndStageMigrationFixture(t, fixture, "enabled", "disabled")
	if err := fixture.executor.ReconcileExistingCoreServices(context.Background()); err == nil || !strings.Contains(err.Error(), "service state mismatch") {
		t.Fatalf("reconcile original-state drift error = %v", err)
	}
	fixture.assertServiceState(t, "xray.service", "active", "enabled")
	fixture.assertServiceState(t, "qagent-xray.service", "inactive", "disabled")
	if record, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineXray); err != nil || record.State != coreMigrationNone {
		t.Fatalf("drift recovery marker = %q, %v", record.State, err)
	}
}

func TestExistingCoreReconcileCompletionGateRollsBackUnsafeFinalState(t *testing.T) {
	requireAgentRoot(t)
	tests := []struct {
		name                string
		trigger             string
		existingEnableState string
	}{
		{name: "managed exits before completion", trigger: "fail-managed-after-enable", existingEnableState: "enabled"},
		{name: "managed persistent enable is missing", trigger: "ignore-managed-enable", existingEnableState: "enabled"},
		{name: "existing persistent enable remains", trigger: "ignore-existing-disable-persistent", existingEnableState: "enabled"},
		{name: "existing runtime enable remains", trigger: "ignore-existing-disable-runtime", existingEnableState: "enabled-runtime"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExistingCoreMigrationFixture(t, false)
			writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "inactive", test.existingEnableState)
			writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "active", "disabled")
			writeMigrationTrigger(t, fixture.stateDirectory, test.trigger)
			prepareAndStageMigrationFixture(t, fixture, test.existingEnableState, "disabled")
			if err := fixture.executor.ReconcileExistingCoreServices(context.Background()); err == nil {
				t.Fatal("unsafe reconcile completion succeeded")
			}
			fixture.assertServiceState(t, "xray.service", "active", test.existingEnableState)
			fixture.assertServiceState(t, "qagent-xray.service", "inactive", "disabled")
			if record, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineXray); err != nil || record.State != coreMigrationNone {
				t.Fatalf("unsafe reconcile marker = %q, %v", record.State, err)
			}
			if _, pending := fixture.executor.ExistingSpecs[core.EngineXray]; !pending {
				t.Fatal("unsafe reconcile cleared the pending existing service")
			}
		})
	}
}

func TestExistingCoreReconcileRollbackStopsManagedServiceThatActivatesLate(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "active", "enabled")
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "inactive", "disabled")
	writeMigrationTrigger(t, fixture.stateDirectory, "activate-managed-on-existing-start")
	prepareAndStageMigrationFixture(t, fixture, "enabled", "disabled")
	if err := fixture.executor.ReconcileExistingCoreServices(context.Background()); err == nil || !strings.Contains(err.Error(), "final safety check failed") {
		t.Fatalf("late managed activation recovery error = %v", err)
	}
	fixture.assertServiceState(t, "xray.service", "active", "enabled")
	fixture.assertServiceState(t, "qagent-xray.service", "inactive", "disabled")
	record, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineXray)
	if err != nil || record.State != coreMigrationInProgress {
		t.Fatalf("unsafe rollback marker = %q, %v; want migrating marker retained", record.State, err)
	}
}

func TestExistingCoreReconcileDisablesRuntimeOriginalBeforeFinalizing(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "inactive", "enabled-runtime")
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "active", "disabled")
	prepareAndStageMigrationFixture(t, fixture, "enabled-runtime", "disabled")
	if err := fixture.executor.ReconcileExistingCoreServices(context.Background()); err != nil {
		t.Fatalf("reconcile runtime-enabled original: %v", err)
	}
	fixture.assertServiceState(t, "xray.service", "inactive", "disabled")
	fixture.assertServiceState(t, "qagent-xray.service", "active", "enabled")
	_, persistent, runtime, stateErr := readMigrationEnableState(fixture.stateDirectory, "xray.service")
	if stateErr != nil || persistent || runtime {
		t.Fatalf("original enable layers after reconcile: persistent=%t runtime=%t error=%v", persistent, runtime, stateErr)
	}
}

func TestExistingCoreReconcileKeepsLegacyMarkerFailClosedAfterSafeServiceRecovery(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "inactive", "static")
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "active", "enabled")
	if err := writeCoreMigrationMarker(fixture.markerPrefix, core.EngineXray, coreMigrationInProgress, coreMigrationConfigDigest(fixture.importedConfig), coreMigrationSourceDigest(fixture.existing), "static", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.executor.LoadCoreMigrationState(); err != nil {
		t.Fatalf("load legacy migration marker: %v", err)
	}
	if err := fixture.executor.ReconcileExistingCoreServices(context.Background()); err != nil {
		t.Fatalf("reconcile legacy static migration: %v", err)
	}
	fixture.assertServiceState(t, "xray.service", "active", "static")
	fixture.assertServiceState(t, "qagent-xray.service", "inactive", "disabled")
	if _, pending := fixture.executor.ExistingSpecs[core.EngineXray]; !pending {
		t.Fatal("rolled back legacy migration cleared pending existing service")
	}
	if record, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineXray); err != nil || record.State != coreMigrationInProgress || record.HasFileRollback {
		t.Fatalf("legacy migration marker = %+v, %v", record, err)
	}
	if issue := fixture.executor.ExistingDiscoveryIssues[core.EngineXray]; !strings.Contains(issue, "缺少可验证的托管文件回滚信息") {
		t.Fatalf("legacy migration issue = %q", issue)
	}
	if _, err := fixture.executor.Execute(context.Background(), core.Task{Action: core.ActionStart, Engine: core.EngineXray}); err == nil || !strings.Contains(err.Error(), "core tasks are disabled") {
		t.Fatalf("legacy migration task error = %v", err)
	}
}
