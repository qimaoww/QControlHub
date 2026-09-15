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

func TestOpenRCRestoreEnableStateSkipsOnlyVerifiedCurrentState(t *testing.T) {
	runlevels, initRoot := useOpenRCTestRunlevels(t)
	stateRoot := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{"sing-box", "qagent-sing-box"} {
		writeOpenRCTestService(t, initRoot, service)
	}
	if err := os.Symlink(filepath.Join(initRoot, "sing-box"), filepath.Join(runlevels, "default", "sing-box")); err != nil {
		t.Fatal(err)
	}
	manager, commandLog := newOpenRCTestManager(t, stateRoot, runlevels, initRoot)

	if err := restoreServiceEnableState(context.Background(), "qagent-sing-box", "disabled", manager); err != nil {
		t.Fatalf("restore already-disabled managed service: %v", err)
	}
	if err := restoreServiceEnableState(context.Background(), "sing-box", "enabled", manager); err != nil {
		t.Fatalf("restore already-enabled existing service: %v", err)
	}
	if contents, err := os.ReadFile(commandLog); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("idempotent restore invoked rc-update: %q, %v", contents, err)
	}

	if err := os.Symlink(filepath.Join(initRoot, "qagent-sing-box"), filepath.Join(runlevels, "boot", "qagent-sing-box")); err != nil {
		t.Fatal(err)
	}
	if err := restoreServiceEnableState(context.Background(), "qagent-sing-box", "disabled", manager); err == nil ||
		!strings.Contains(err.Error(), "outside the single supported default runlevel") {
		t.Fatalf("unsafe non-default enable state was accepted: %v", err)
	}
	if contents, err := os.ReadFile(commandLog); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe runlevel state invoked rc-update: %q, %v", contents, err)
	}
	if err := os.Remove(filepath.Join(runlevels, "boot", "qagent-sing-box")); err != nil {
		t.Fatal(err)
	}
	if err := restoreServiceEnableState(context.Background(), "qagent-sing-box", "enabled", manager); err != nil {
		t.Fatalf("enable disabled managed service: %v", err)
	}
	if err := restoreServiceEnableState(context.Background(), "sing-box", "disabled", manager); err != nil {
		t.Fatalf("disable enabled existing service: %v", err)
	}
	commands, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	wantCommands := "rc-update add qagent-sing-box default\nrc-update del sing-box default\n"
	if string(commands) != wantCommands {
		t.Fatalf("state-changing rc-update commands = %q; want %q", commands, wantCommands)
	}
}

func TestOpenRCMigrationStopFailureRollbackClosesOnFreshReconcile(t *testing.T) {
	requireAgentRoot(t)
	runlevels, initRoot := useOpenRCTestRunlevels(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	fixture.existing.Service = "sing-box"
	fixture.managed.Service = "qagent-sing-box"
	fixture.executor.Specs = map[core.Engine]EngineSpec{core.EngineSingBox: fixture.managed}
	fixture.executor.ExistingSpecs = map[core.Engine]EngineSpec{core.EngineSingBox: fixture.existing}
	for _, service := range []string{fixture.existing.Service, fixture.managed.Service} {
		writeOpenRCTestService(t, initRoot, service)
	}
	if err := os.Symlink(filepath.Join(initRoot, fixture.existing.Service), filepath.Join(runlevels, "default", fixture.existing.Service)); err != nil {
		t.Fatal(err)
	}
	writeOpenRCTestActiveState(t, fixture.stateDirectory, fixture.existing.Service, "active")
	writeOpenRCTestActiveState(t, fixture.stateDirectory, fixture.managed.Service, "inactive")
	manager, commandLog := newOpenRCTestManager(t, fixture.stateDirectory, runlevels, initRoot)
	fixture.executor.Services = manager

	record, err := prepareCoreMigrationFileRollback(
		fixture.markerPrefix,
		core.EngineSingBox,
		fixture.existing,
		fixture.managed,
		coreMigrationRecord{
			State: coreMigrationInProgress, ConfigDigest: coreMigrationConfigDigest(fixture.importedConfig),
			SourceDigest:        coreMigrationSourceDigest(fixture.existing),
			ExistingEnableState: "enabled", ManagedEnableState: "disabled", ManagedInitialState: "inactive",
		},
	)
	if err != nil {
		t.Fatalf("prepare durable OpenRC migration: %v", err)
	}
	if _, err := copyExistingCoreBinary(fixture.existing.Binary, fixture.managed.Binary); err != nil {
		t.Fatalf("stage managed binary: %v", err)
	}
	if _, err := atomicDeploy(fixture.managed.ConfigPath, fixture.importedConfig); err != nil {
		t.Fatalf("stage managed configuration: %v", err)
	}
	if err := verifyCoreMigrationStagedFiles(fixture.managed, record); err != nil {
		t.Fatalf("verify staged OpenRC migration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, "fail-managed-stop-once"), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restoreInterruptedCoreMigration(context.Background(), fixture.markerPrefix, core.EngineSingBox, fixture.existing, fixture.managed, record, manager); err == nil ||
		!strings.Contains(err.Error(), "stop managed service") {
		t.Fatalf("initial stop failure did not leave an interrupted rollback: %v", err)
	}
	if current, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineSingBox); err != nil || current.State != coreMigrationInProgress {
		t.Fatalf("interrupted OpenRC migration marker = %+v, %v", current, err)
	}

	restarted := &Executor{
		Specs:                   map[core.Engine]EngineSpec{core.EngineSingBox: fixture.managed},
		ExistingSpecs:           map[core.Engine]EngineSpec{core.EngineSingBox: fixture.existing},
		ExistingDiscoveryIssues: make(map[core.Engine]string),
		MigrationMarkerPrefix:   fixture.markerPrefix,
		Services:                manager,
	}
	if err := restarted.LoadCoreMigrationState(); err != nil {
		t.Fatalf("load interrupted OpenRC migration: %v", err)
	}
	if err := restarted.ReconcileExistingCoreServices(context.Background()); err != nil {
		t.Fatalf("fresh Agent reconcile interrupted OpenRC rollback: %v", err)
	}
	if current, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineSingBox); err != nil || current.State != coreMigrationNone {
		t.Fatalf("reconciled OpenRC migration marker = %+v, %v", current, err)
	}
	for service, want := range map[string]string{fixture.existing.Service: "active", fixture.managed.Service: "inactive"} {
		contents, err := os.ReadFile(filepath.Join(fixture.stateDirectory, service+".active"))
		if err != nil || strings.TrimSpace(string(contents)) != want {
			t.Fatalf("%s state = %q, %v; want %s", service, contents, err, want)
		}
	}
	if state, err := openRCServiceEnableState(context.Background(), fixture.existing.Service); err != nil || state != "enabled" {
		t.Fatalf("existing service enable state = %q, %v", state, err)
	}
	if state, err := openRCServiceEnableState(context.Background(), fixture.managed.Service); err != nil || state != "disabled" {
		t.Fatalf("managed service enable state = %q, %v", state, err)
	}
	assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
	if _, err := os.Lstat(fixture.managed.Binary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("originally absent managed binary remained after reconcile: %v", err)
	}
	for _, kind := range []string{"binary", "config"} {
		if _, err := os.Lstat(coreMigrationBackupPath(fixture.markerPrefix, core.EngineSingBox, kind)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s OpenRC rollback backup remained: %v", kind, err)
		}
	}
	commands, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(commands), "rc-update del qagent-sing-box default") {
		t.Fatalf("reconcile invoked non-idempotent runlevel deletion: %s", commands)
	}
	collector := NewCoreLogCollectorForExecutor(restarted)
	if batch := collector.NextBatch(); batch != nil {
		t.Fatalf("rollback-only reconcile produced a core log batch: %+v", batch)
	}
}

// OpenRC completed migrations follow the same ownership rule as systemd: the
// retired service must stay retired, while the QAgent-managed service may be
// stopped by an operator without invalidating the completed record.
func TestOpenRCCompletedMigrationOwnershipOnlyTracksRetiredService(t *testing.T) {
	runlevels, initRoot := useOpenRCTestRunlevels(t)
	stateRoot := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, _ := newOpenRCTestManager(t, stateRoot, runlevels, initRoot)
	useFakeOpenRCTree(t, t.TempDir(), stateRoot, t.TempDir(), "/sbin/supervise-daemon")
	writeOpenRCTestService(t, initRoot, "xray")
	writeOpenRCTestService(t, initRoot, "qagent-xray")
	writeOpenRCTestActiveState(t, stateRoot, "xray", "inactive")
	writeOpenRCTestActiveState(t, stateRoot, "qagent-xray", "inactive")
	// The managed service is enabled but stopped, exactly like an operator stop
	// before an Agent upgrade.
	if err := os.Symlink(filepath.Join(initRoot, "qagent-xray"), filepath.Join(runlevels, "default", "qagent-xray")); err != nil {
		t.Fatal(err)
	}
	existing := EngineSpec{Service: "xray"}
	if err := verifyCompletedCoreMigrationOwnership(context.Background(), existing, manager); err != nil {
		t.Fatalf("stopped managed service invalidated a completed OpenRC migration: %v", err)
	}
	// A retired service that was added back to the boot runlevel fails closed.
	if err := os.Symlink(filepath.Join(initRoot, "xray"), filepath.Join(runlevels, "default", "xray")); err != nil {
		t.Fatal(err)
	}
	if err := verifyCompletedCoreMigrationOwnership(context.Background(), existing, manager); err == nil || !strings.Contains(err.Error(), "after migration completion") {
		t.Fatalf("re-enabled retired OpenRC service error = %v", err)
	}
	// And so does a retired service that runs again.
	if err := os.Remove(filepath.Join(runlevels, "default", "xray")); err != nil {
		t.Fatal(err)
	}
	writeOpenRCTestActiveState(t, stateRoot, "xray", "active")
	if err := verifyCompletedCoreMigrationOwnership(context.Background(), existing, manager); err == nil || !strings.Contains(err.Error(), "after migration completion") {
		t.Fatalf("reactivated retired OpenRC service error = %v", err)
	}
}
