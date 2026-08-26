//go:build linux

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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

func TestCompletedCoreMigrationStateDriftBlocksExplicitMappingTasksAfterRestart(t *testing.T) {
	requireAgentRoot(t)
	tests := []struct {
		name           string
		existingStatus string
		managedStatus  string
		managedEnabled string
	}{
		{name: "original service reactivated", existingStatus: "active", managedStatus: "inactive", managedEnabled: "enabled"},
		{name: "managed service stopped", existingStatus: "inactive", managedStatus: "inactive", managedEnabled: "enabled"},
		{name: "managed persistent enable missing", existingStatus: "inactive", managedStatus: "active", managedEnabled: "disabled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExistingCoreMigrationFixture(t, false)
			writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", test.existingStatus, "disabled")
			writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", test.managedStatus, test.managedEnabled)
			if err := writeCoreMigrationMarker(fixture.markerPrefix, core.EngineXray, coreMigrationComplete, coreMigrationConfigDigest(fixture.importedConfig), coreMigrationSourceDigest(fixture.existing), "enabled", "disabled"); err != nil {
				t.Fatal(err)
			}
			restarted := &Executor{
				Specs:                   map[core.Engine]EngineSpec{core.EngineXray: fixture.managed},
				ExistingSpecs:           map[core.Engine]EngineSpec{core.EngineXray: fixture.existing},
				ExistingDiscoveryIssues: make(map[core.Engine]string),
				MigrationMarkerPrefix:   fixture.markerPrefix,
			}
			if err := restarted.LoadCoreMigrationState(); err != nil {
				t.Fatalf("load unsafe completed migration: %v", err)
			}
			if _, pending := restarted.ExistingSpecs[core.EngineXray]; !pending {
				t.Fatal("unsafe completed migration suppressed the existing mapping")
			}
			if _, ok := restarted.completedMigrations[core.EngineXray]; ok {
				t.Fatal("unsafe completion state was recorded as verified ownership")
			}
			issue := restarted.ExistingDiscoveryIssues[core.EngineXray]
			if !strings.Contains(issue, "迁移状态不再安全") {
				t.Fatalf("unsafe completed migration issue = %q", issue)
			}
			runtime := restarted.Runtime(context.Background())[core.EngineXray]
			if runtime.ExistingConfigUnsupportedReason != issue || runtime.ExistingConfigAvailable {
				t.Fatalf("unsafe completed migration runtime = %+v", runtime)
			}
			for _, action := range []core.Action{core.ActionStart, core.ActionInstall, core.ActionImportExisting} {
				if _, err := restarted.Execute(context.Background(), core.Task{
					Action: action, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
				}); err == nil || !strings.Contains(err.Error(), "core tasks are disabled") {
					t.Fatalf("unsafe completed migration %s error = %v", action, err)
				}
			}
		})
	}
}

func TestExistingSingBoxConfigDirectoryMigrationSucceeds(t *testing.T) {
	requireAgentRoot(t)
	fixture, content, _ := configureSingBoxDirectoryFixture(t, newExistingCoreMigrationFixture(t, false))
	forwarder := filepath.Join(filepath.Dir(fixture.existing.ConfigPath), "sing-box-forwarder")
	serviceBinary := filepath.Join(filepath.Dir(fixture.existing.ConfigPath), "sing-box-service")
	if err := os.WriteFile(forwarder, []byte("#!/bin/sh\nexec /usr/bin/true \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(forwarder, serviceBinary); err != nil {
		t.Fatal(err)
	}
	fixture.existing.Binary = "/usr/bin/true"
	fixture.existing.ServiceBinary = serviceBinary
	fixture.executor.ExistingSpecs[core.EngineSingBox] = fixture.existing
	writeMigrationExecStart(t, fixture.stateDirectory, fixture.existing.Service, systemdExecStart(
		serviceBinary, serviceBinary+" run -c "+fixture.existing.ConfigPath+" -C "+fixture.existing.ConfigDirectory,
	))
	output, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineSingBox, ConfigContent: content,
	})
	if err != nil {
		t.Fatalf("migrate sing-box config directory: %v\n%s", err, output)
	}
	if !strings.Contains(output, "stopped and disabled sing-box.service") {
		t.Fatalf("migration output = %q", output)
	}
	fixture.assertServiceState(t, "sing-box.service", "inactive", "disabled")
	fixture.assertServiceState(t, "qagent-sing-box.service", "active", "enabled")
	assertFileContentAndMode(t, fixture.managed.ConfigPath, content, 0o600)
	if _, pending := fixture.executor.ExistingSpecs[core.EngineSingBox]; pending {
		t.Fatal("completed sing-box directory migration remained pending")
	}
}

func TestExistingSingBoxOfficialRelativeLogOutputMigrationSucceeds(t *testing.T) {
	requireAgentRoot(t)
	logRoot := t.TempDir()
	previousLogRoot := importedSingBoxLogRoot
	importedSingBoxLogRoot = logRoot
	t.Cleanup(func() { importedSingBoxLogRoot = previousLogRoot })
	fixture, _ := configureSingBoxOfficialFixture(t, newExistingCoreMigrationFixture(t, false))
	if err := os.WriteFile(filepath.Join(fixture.existing.ConfigDirectory, "20-log.json"),
		[]byte(`{"log":{"level":"info","timestamp":true,"output":"runtime.log"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	content, _, err := readExistingConfigurationSources(fixture.existing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineSingBox, ConfigContent: content,
	}); err != nil {
		t.Fatalf("official -D/-C relative log.output migration failed: %v", err)
	}
	fixture.assertServiceState(t, "sing-box.service", "inactive", "disabled")
	fixture.assertServiceState(t, "qagent-sing-box.service", "active", "enabled")
	restarted := &Executor{
		Specs:                   map[core.Engine]EngineSpec{core.EngineSingBox: fixture.managed},
		ExistingSpecs:           map[core.Engine]EngineSpec{core.EngineSingBox: fixture.existing},
		ExistingDiscoveryIssues: make(map[core.Engine]string),
		MigrationMarkerPrefix:   fixture.markerPrefix, Services: fixture.executor.Services,
	}
	if err := restarted.LoadCoreMigrationState(); err != nil {
		t.Fatalf("reload official sing-box migration: %v", err)
	}
	collector := NewCoreLogCollectorForExecutor(restarted)
	collector.mu.Lock()
	fileSources := 0
	for _, source := range collector.fileSources {
		if source.engine == core.EngineSingBox && source.kind == "file" {
			fileSources++
		}
	}
	collector.mu.Unlock()
	if fileSources != 1 {
		t.Fatalf("restarted imported sing-box file sources = %d", fileSources)
	}
	if _, err := restarted.Execute(context.Background(), core.Task{
		Action: core.ActionValidate, Engine: core.EngineSingBox, ConfigContent: content,
	}); err != nil {
		t.Fatalf("validate unchanged imported file log configuration: %v", err)
	}
	replacementConfig := `{"log":{"level":"info","output":"replacement.log"},"inbounds":[],"outbounds":[]}`
	if _, err := restarted.Execute(context.Background(), core.Task{
		Action: core.ActionDeploy, Engine: core.EngineSingBox, ConfigContent: replacementConfig,
	}); err != nil {
		t.Fatalf("deploy imported file-to-file log configuration: %v", err)
	}
	if err := collector.RefreshImportedSingBoxSource(restarted); err != nil {
		t.Fatalf("refresh imported file-to-file source: %v", err)
	}
	collector.mu.Lock()
	filePath := ""
	for _, source := range collector.fileSources {
		if source.engine == core.EngineSingBox && source.kind == "file" {
			filePath = source.path
		}
	}
	collector.mu.Unlock()
	if filePath != filepath.Join(logRoot, "replacement.log") {
		t.Fatalf("file-to-file source path = %q", filePath)
	}
	failedConfig := `{"log":{"level":"info","output":"failed.log"},"inbounds":[],"outbounds":[]}`
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, "fail-managed-restart"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Execute(context.Background(), core.Task{
		Action: core.ActionDeploy, Engine: core.EngineSingBox, ConfigContent: failedConfig,
	}); err == nil {
		t.Fatal("failed imported file deploy unexpectedly succeeded")
	}
	if err := os.Remove(filepath.Join(fixture.stateDirectory, "fail-managed-restart")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := collector.RefreshImportedSingBoxSource(restarted); err != nil {
		t.Fatalf("refresh rolled-back imported file source: %v", err)
	}
	collector.mu.Lock()
	filePath = ""
	for _, source := range collector.fileSources {
		if source.engine == core.EngineSingBox && source.kind == "file" {
			filePath = source.path
		}
	}
	collector.mu.Unlock()
	if filePath != filepath.Join(logRoot, "replacement.log") {
		t.Fatalf("rollback source path = %q", filePath)
	}
	consoleConfig := `{"log":{"level":"info","timestamp":true},"inbounds":[],"outbounds":[]}`
	if _, err := restarted.Execute(context.Background(), core.Task{
		Action: core.ActionDeploy, Engine: core.EngineSingBox, ConfigContent: consoleConfig,
	}); err != nil {
		t.Fatalf("deploy imported file-to-console log configuration: %v", err)
	}
	if err := collector.RefreshImportedSingBoxSource(restarted); err != nil {
		t.Fatalf("switch restarted import to console source: %v", err)
	}
	collector.mu.Lock()
	preferred := collector.preferredKind[core.EngineSingBox]
	fileSources = 0
	for _, source := range collector.fileSources {
		if source.engine == core.EngineSingBox && source.kind == "file" {
			fileSources++
		}
	}
	collector.mu.Unlock()
	if preferred != "journal" || fileSources != 0 {
		t.Fatalf("restarted source switch preferred=%q file count=%d", preferred, fileSources)
	}
}

func TestExistingSingBoxConfigDirectoryDriftRollsBackPreparation(t *testing.T) {
	requireAgentRoot(t)
	fixture, content, overlay := configureSingBoxDirectoryFixture(t, newExistingCoreMigrationFixture(t, false))
	writeExistingCoreHelperMutations(t, fixture.existing.Binary, 4, map[string]string{
		overlay: "{\"outbounds\":[{\"tag\":\"changed\"}]}\n",
	})
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineSingBox, ConfigContent: content,
	}); err == nil || !strings.Contains(err.Error(), "configuration sources changed") {
		t.Fatalf("config-directory preparation drift error = %v", err)
	}
	fixture.assertServiceState(t, "sing-box.service", "active", "enabled")
	fixture.assertServiceState(t, "qagent-sing-box.service", "inactive", "disabled")
	assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
	if _, err := os.Stat(fixture.managed.Binary); !os.IsNotExist(err) {
		t.Fatalf("drifted migration left managed binary: %v", err)
	}
	if _, err := os.Stat(coreMigrationMarkerPath(fixture.markerPrefix, core.EngineSingBox)); !os.IsNotExist(err) {
		t.Fatalf("drifted migration left marker: %v", err)
	}
}

func TestExistingSingBoxConfigDirectoryArgvDriftRollsBackPreparation(t *testing.T) {
	requireAgentRoot(t)
	fixture, content, _ := configureSingBoxDirectoryFixture(t, newExistingCoreMigrationFixture(t, false))
	replacementDirectory := fixture.existing.ConfigDirectory + "-replacement"
	drifted := systemdExecStart(fixture.existing.Binary,
		fixture.existing.Binary+" run -c "+fixture.existing.ConfigPath+" -C "+replacementDirectory)
	writeExistingCoreHelperMutations(t, fixture.existing.Binary, 1, map[string]string{
		filepath.Join(fixture.stateDirectory, fixture.existing.Service+".exec-start"): drifted + "\n",
	})
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineSingBox, ConfigContent: content,
	}); err == nil || !strings.Contains(err.Error(), "ExecStart no longer matches") {
		t.Fatalf("config-directory argv drift error = %v", err)
	}
	fixture.assertServiceState(t, "sing-box.service", "active", "enabled")
	fixture.assertServiceState(t, "qagent-sing-box.service", "inactive", "disabled")
	assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
	if _, err := os.Stat(fixture.managed.Binary); !os.IsNotExist(err) {
		t.Fatalf("argv drift left managed binary: %v", err)
	}
}

func TestExistingSingBoxOfficialWorkDirectoryDriftRejectsBeforeChanges(t *testing.T) {
	requireAgentRoot(t)
	fixture, content := configureSingBoxOfficialFixture(t, newExistingCoreMigrationFixture(t, false))
	replacementWork := fixture.existing.WorkingDirectory + "-replacement"
	drifted := systemdExecStart(fixture.existing.Binary,
		fixture.existing.Binary+" -D "+replacementWork+" -C "+fixture.existing.ConfigDirectory+" run")
	writeExistingCoreHelperMutations(t, fixture.existing.Binary, 1, map[string]string{
		filepath.Join(fixture.stateDirectory, fixture.existing.Service+".exec-start"): drifted + "\n",
	})
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineSingBox, ConfigContent: content,
	}); err == nil || !strings.Contains(err.Error(), "ExecStart no longer matches") {
		t.Fatalf("official working-directory drift error = %v", err)
	}
	fixture.assertServiceState(t, "sing-box.service", "active", "enabled")
	fixture.assertServiceState(t, "qagent-sing-box.service", "inactive", "disabled")
	assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
	if _, err := os.Stat(fixture.managed.Binary); !os.IsNotExist(err) {
		t.Fatalf("working-directory drift left managed binary: %v", err)
	}
	if _, err := os.Stat(coreMigrationMarkerPath(fixture.markerPrefix, core.EngineSingBox)); !os.IsNotExist(err) {
		t.Fatalf("working-directory drift left marker: %v", err)
	}
}

func TestExistingCoreMigrationRestoresOriginalServiceWhenNewServiceFails(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, true)
	output, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
	})
	if err == nil || !strings.Contains(output, "original configuration, binary, and service were restored") {
		t.Fatalf("failed migration = %q, %v", output, err)
	}
	fixture.assertServiceState(t, "xray.service", "active", "enabled")
	fixture.assertServiceState(t, "qagent-xray.service", "inactive", "disabled")
	assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
	if _, err := os.Stat(fixture.managed.Binary); !os.IsNotExist(err) {
		t.Fatalf("failed migration left managed binary: %v", err)
	}
	if _, pending := fixture.executor.ExistingSpecs[core.EngineXray]; !pending {
		t.Fatal("failed migration cleared pending existing service")
	}
	if marked, err := coreMigrationMarked(fixture.markerPrefix, core.EngineXray); err != nil || marked {
		t.Fatalf("failed migration marker = %t, %v", marked, err)
	}
}

func TestExistingCoreMigrationCoordinatesActiveManagedService(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "active", "enabled-runtime")
	writeMigrationTrigger(t, fixture.stateDirectory, "respawn-managed-on-runtime-stop")
	output, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
	})
	if err != nil {
		t.Fatalf("active managed service migration = %q, %v", output, err)
	}
	if !strings.Contains(output, "stopped and disabled") {
		t.Fatalf("migration output did not describe coordinated stop: %q", output)
	}
	fixture.assertServiceState(t, "xray.service", "inactive", "disabled")
	fixture.assertServiceState(t, "qagent-xray.service", "active", "enabled")
	commands, err := os.ReadFile(filepath.Join(fixture.stateDirectory, "commands.log"))
	if err != nil {
		t.Fatalf("read service command log: %v", err)
	}
	log := string(commands)
	runtimeDisable := strings.Index(log, "disable --runtime qagent-xray.service")
	stop := strings.Index(log, "stop qagent-xray.service")
	if runtimeDisable < 0 || stop < 0 || runtimeDisable > stop {
		t.Fatalf("runtime enablement was not cleared before managed stop: %q", log)
	}
}

func TestExistingCoreMigrationRejectsManagedTransientStatesBeforeChanges(t *testing.T) {
	requireAgentRoot(t)
	for _, status := range []string{"activating", "reloading", "deactivating"} {
		t.Run(status, func(t *testing.T) {
			fixture := newExistingCoreMigrationFixture(t, false)
			writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", status, "disabled")
			if _, err := fixture.executor.Execute(context.Background(), core.Task{
				Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
			}); err == nil {
				t.Fatalf("managed %s state was accepted", status)
			}
			fixture.assertServiceState(t, "xray.service", "active", "enabled")
			fixture.assertServiceState(t, "qagent-xray.service", status, "disabled")
			assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
			if _, err := os.Stat(fixture.managed.Binary); !os.IsNotExist(err) {
				t.Fatalf("managed %s rejection installed binary: %v", status, err)
			}
			if _, err := os.Stat(coreMigrationMarkerPath(fixture.markerPrefix, core.EngineXray)); !os.IsNotExist(err) {
				t.Fatalf("managed %s rejection left marker: %v", status, err)
			}
		})
	}
}

func TestExistingCoreMigrationRejectsManagedServiceActivationDuringPreparation(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	writeExistingCoreHelperMutations(t, fixture.existing.Binary, 3, map[string]string{
		filepath.Join(fixture.stateDirectory, "qagent-xray.service.active"): "active\n",
	})
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
	}); err == nil || !strings.Contains(err.Error(), `must remain inactive or failed`) || !strings.Contains(err.Error(), `status "active"`) {
		t.Fatalf("managed preparation activation error = %v", err)
	}
	fixture.assertServiceState(t, "xray.service", "active", "enabled")
	fixture.assertServiceState(t, "qagent-xray.service", "inactive", "disabled")
	assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
	if _, err := os.Stat(fixture.managed.Binary); !os.IsNotExist(err) {
		t.Fatalf("managed activation left prepared binary: %v", err)
	}
	if _, err := os.Stat(coreMigrationMarkerPath(fixture.markerPrefix, core.EngineXray)); !os.IsNotExist(err) {
		t.Fatalf("managed activation left marker: %v", err)
	}
}

func TestExistingCoreMigrationAcceptsFailedManagedService(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "failed", "disabled")
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
	}); err != nil {
		t.Fatalf("migrate with failed managed service: %v", err)
	}
	fixture.assertServiceState(t, "xray.service", "inactive", "disabled")
	fixture.assertServiceState(t, "qagent-xray.service", "active", "enabled")
}

func TestExistingCoreMigrationRestoresDisabledEnableState(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, true)
	writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "active", "disabled")
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "inactive", "enabled")
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
	}); err == nil {
		t.Fatal("migration unexpectedly succeeded")
	}
	fixture.assertServiceState(t, "xray.service", "active", "disabled")
	fixture.assertServiceState(t, "qagent-xray.service", "inactive", "enabled")
}

func TestExistingCoreMigrationRestoresRuntimeEnableState(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, true)
	writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "active", "enabled-runtime")
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
	}); err == nil {
		t.Fatal("migration unexpectedly succeeded")
	}
	fixture.assertServiceState(t, "xray.service", "active", "enabled-runtime")
}

func TestExistingCoreMigrationDisablesRuntimeEnabledOriginalOnSuccess(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "active", "enabled-runtime")
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
	}); err != nil {
		t.Fatalf("migrate runtime-enabled original: %v", err)
	}
	fixture.assertServiceState(t, "xray.service", "inactive", "disabled")
	fixture.assertServiceState(t, "qagent-xray.service", "active", "enabled")
	_, persistent, runtime, stateErr := readMigrationEnableState(fixture.stateDirectory, "xray.service")
	if stateErr != nil || persistent || runtime {
		t.Fatalf("original enable layers after migration: persistent=%t runtime=%t error=%v", persistent, runtime, stateErr)
	}
}

func TestExistingCoreMigrationCompletionGateRollsBackUnsafeFinalState(t *testing.T) {
	requireAgentRoot(t)
	tests := []struct {
		name                string
		trigger             string
		existingEnableState string
	}{
		{name: "managed exits after initial start verification", trigger: "fail-managed-after-enable", existingEnableState: "enabled"},
		{name: "managed enable reports success without persistent link", trigger: "ignore-managed-enable", existingEnableState: "enabled"},
		{name: "existing persistent enable link remains", trigger: "ignore-existing-disable-persistent", existingEnableState: "enabled"},
		{name: "existing runtime enable link remains", trigger: "ignore-existing-disable-runtime", existingEnableState: "enabled-runtime"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExistingCoreMigrationFixture(t, false)
			writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "active", test.existingEnableState)
			writeMigrationTrigger(t, fixture.stateDirectory, test.trigger)
			output, err := fixture.executor.Execute(context.Background(), core.Task{
				Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
			})
			if err == nil || !strings.Contains(output, "original configuration, binary, and service were restored") {
				t.Fatalf("unsafe completion = %q, %v", output, err)
			}
			fixture.assertServiceState(t, "xray.service", "active", test.existingEnableState)
			fixture.assertServiceState(t, "qagent-xray.service", "inactive", "disabled")
			assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
			if _, err := os.Stat(fixture.managed.Binary); !os.IsNotExist(err) {
				t.Fatalf("unsafe completion left managed binary: %v", err)
			}
			if record, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineXray); err != nil || record.State != coreMigrationNone {
				t.Fatalf("unsafe completion marker = %q, %v", record.State, err)
			}
			if _, pending := fixture.executor.ExistingSpecs[core.EngineXray]; !pending {
				t.Fatal("unsafe completion cleared the pending existing service")
			}
		})
	}
}

func TestExistingCoreMigrationRestoresManagedRuntimeOnlyAfterPersistentEnable(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "inactive", "enabled-runtime")
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, "fail-existing-disable"), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
	})
	if err == nil || !strings.Contains(output, "original configuration, binary, and service were restored") {
		t.Fatalf("migration after persistent managed enable = %q, %v", output, err)
	}
	fixture.assertServiceState(t, "xray.service", "active", "enabled")
	fixture.assertServiceState(t, "qagent-xray.service", "inactive", "enabled-runtime")
	_, persistent, runtime, stateErr := readMigrationEnableState(fixture.stateDirectory, "qagent-xray.service")
	if stateErr != nil || persistent || !runtime {
		t.Fatalf("managed runtime-only layers after rollback: persistent=%t runtime=%t error=%v", persistent, runtime, stateErr)
	}
	assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
	if _, err := os.Stat(fixture.managed.Binary); !os.IsNotExist(err) {
		t.Fatalf("failed migration left managed binary: %v", err)
	}
	if _, err := os.Stat(coreMigrationMarkerPath(fixture.markerPrefix, core.EngineXray)); !os.IsNotExist(err) {
		t.Fatalf("failed migration left marker: %v", err)
	}
}

func TestExistingCoreMigrationRejectsDriftedExecStartBeforeChanges(t *testing.T) {
	requireAgentRoot(t)
	tests := []struct {
		name      string
		execStart func(existing EngineSpec) string
	}{
		{
			name: "executable",
			execStart: func(existing EngineSpec) string {
				replacement := existing.Binary + "-replacement"
				return systemdExecStart(replacement, replacement+" run -config "+existing.ConfigPath)
			},
		},
		{
			name: "configuration argv",
			execStart: func(existing EngineSpec) string {
				return systemdExecStart(existing.Binary, existing.Binary+" run -config "+existing.ConfigPath+"-replacement")
			},
		},
		{
			name: "multiple commands",
			execStart: func(existing EngineSpec) string {
				exact := systemdExecStart(existing.Binary, existing.Binary+" run -config "+existing.ConfigPath)
				return exact + "\n" + exact
			},
		},
		{
			name: "wrapper",
			execStart: func(existing EngineSpec) string {
				wrapper := existing.Binary + "-wrapper"
				return systemdExecStart(wrapper, wrapper+" "+existing.Binary+" run -config "+existing.ConfigPath)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExistingCoreMigrationFixture(t, false)
			writeMigrationExecStart(t, fixture.stateDirectory, fixture.existing.Service, test.execStart(fixture.existing))
			if _, err := fixture.executor.Execute(context.Background(), core.Task{
				Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
			}); err == nil || !strings.Contains(err.Error(), "ExecStart no longer matches") {
				t.Fatalf("drifted ExecStart error = %v", err)
			}
			fixture.assertServiceState(t, "xray.service", "active", "enabled")
			fixture.assertServiceState(t, "qagent-xray.service", "inactive", "disabled")
			assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
			if _, err := os.Stat(fixture.managed.Binary); !os.IsNotExist(err) {
				t.Fatalf("rejected migration installed managed binary: %v", err)
			}
			if _, err := os.Stat(coreMigrationMarkerPath(fixture.markerPrefix, core.EngineXray)); !os.IsNotExist(err) {
				t.Fatalf("rejected migration left marker: %v", err)
			}
			if _, pending := fixture.executor.ExistingSpecs[core.EngineXray]; !pending {
				t.Fatal("rejected migration cleared pending existing service")
			}
		})
	}
}

func TestExistingCoreMigrationRejectsExecStartDriftDuringPreparation(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	wrapper := fixture.existing.Binary + "-wrapper"
	drifted := systemdExecStart(wrapper, wrapper+" "+fixture.existing.Binary+" run -config "+fixture.existing.ConfigPath)
	writeExistingCoreHelperMutations(t, fixture.existing.Binary, 1, map[string]string{
		filepath.Join(fixture.stateDirectory, fixture.existing.Service+".exec-start"): drifted + "\n",
	})
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
	}); err == nil || !strings.Contains(err.Error(), "ExecStart no longer matches") {
		t.Fatalf("preparation ExecStart drift error = %v", err)
	}
	fixture.assertServiceState(t, "xray.service", "active", "enabled")
	fixture.assertServiceState(t, "qagent-xray.service", "inactive", "disabled")
	assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
	if _, err := os.Stat(fixture.managed.Binary); !os.IsNotExist(err) {
		t.Fatalf("drifted migration left managed binary: %v", err)
	}
	if _, err := os.Stat(coreMigrationMarkerPath(fixture.markerPrefix, core.EngineXray)); !os.IsNotExist(err) {
		t.Fatalf("drifted migration left marker: %v", err)
	}
	if _, pending := fixture.executor.ExistingSpecs[core.EngineXray]; !pending {
		t.Fatal("drifted migration cleared pending existing service")
	}
}

func TestExistingCoreMigrationAcceptsExactSupportedExecStartForms(t *testing.T) {
	tests := []struct {
		name             string
		engine           core.Engine
		binary           string
		config           string
		configDirectory  string
		workingDirectory string
		serviceBinary    string
		argv             string
	}{
		{name: "xray config", engine: core.EngineXray, binary: "/usr/bin/xray", config: "/etc/xray/config.json", argv: "/usr/bin/xray run -config /etc/xray/config.json"},
		{name: "xray short config", engine: core.EngineXray, binary: "/usr/bin/xray", config: "/etc/xray/config.json", argv: "/usr/bin/xray run -c /etc/xray/config.json"},
		{name: "sing-box config", engine: core.EngineSingBox, binary: "/usr/bin/sing-box", config: "/etc/sing-box/config.json", argv: "/usr/bin/sing-box run --config /etc/sing-box/config.json"},
		{name: "sing-box short config", engine: core.EngineSingBox, binary: "/usr/bin/sing-box", config: "/etc/sing-box/config.json", argv: "/usr/bin/sing-box run -c /etc/sing-box/config.json"},
		{name: "sing-box config directory", engine: core.EngineSingBox, binary: "/usr/lib/sing-box/sing-box", serviceBinary: "/usr/local/bin/sing-box", config: "/etc/sing-box/config.json", configDirectory: "/etc/sing-box/conf.d", argv: "/usr/local/bin/sing-box run -c /etc/sing-box/config.json -C /etc/sing-box/conf.d"},
		{name: "sing-box official directory", engine: core.EngineSingBox, binary: "/usr/bin/sing-box", config: "/etc/sing-box/config.json", configDirectory: "/etc/sing-box", workingDirectory: "/var/lib/sing-box", argv: "/usr/bin/sing-box -D /var/lib/sing-box -C /etc/sing-box run"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceBinary := test.serviceBinary
			if serviceBinary == "" {
				serviceBinary = test.binary
			}
			executable, argv, err := parseSingleSystemdExecStart(systemdExecStart(serviceBinary, test.argv) + "\n")
			if err != nil {
				t.Fatalf("parse exact ExecStart: %v", err)
			}
			existing := EngineSpec{Binary: test.binary, ServiceBinary: test.serviceBinary, ConfigPath: test.config, ConfigDirectory: test.configDirectory, WorkingDirectory: test.workingDirectory}
			if executable != serviceBinary || !supportedExistingExecStart(test.engine, existing, argv) {
				t.Fatalf("exact ExecStart rejected: executable=%q argv=%q", executable, argv)
			}
		})
	}
}

func TestExistingCoreSourceDigestKeepsDirectSingleFileCompatibility(t *testing.T) {
	legacy := EngineSpec{Binary: "/usr/bin/sing-box", ConfigPath: "/etc/sing-box/config.json", Service: "sing-box.service"}
	explicit := legacy
	explicit.ServiceBinary = explicit.Binary
	if coreMigrationSourceDigest(legacy) != coreMigrationSourceDigest(explicit) {
		t.Fatal("explicit direct service executable changed the existing single-file marker digest")
	}
	explicit.ConfigDirectory = "/etc/sing-box/conf.d"
	if coreMigrationSourceDigest(legacy) == coreMigrationSourceDigest(explicit) {
		t.Fatal("config-directory source was not included in the migration marker digest")
	}
}

func TestCoreMigrationSourceDigestKeepsLegacyConfigDirectoryMarker(t *testing.T) {
	legacy := EngineSpec{
		Binary: "/usr/lib/sing-box/sing-box", ConfigPath: "/etc/sing-box/config.json",
		ConfigDirectory: "/etc/sing-box/conf.d", ServiceBinary: "/usr/local/bin/sing-box",
		Service: "sing-box.service",
	}
	// The pre-WorkingDirectory marker digest for a run -c FILE -C DIR mapping had
	// exactly five NUL-separated fields and must remain byte-for-byte stable so
	// an existing migrating/completed marker still matches after upgrade.
	oldSource := legacy.Binary + "\x00" + legacy.ConfigPath + "\x00" + legacy.ConfigDirectory +
		"\x00" + legacy.ServiceBinary + "\x00" + legacy.Service
	oldDigest := sha256.Sum256([]byte(oldSource))
	if got := coreMigrationSourceDigest(legacy); got != hex.EncodeToString(oldDigest[:]) {
		t.Fatalf("legacy config-directory digest changed: got %s want %s", got, hex.EncodeToString(oldDigest[:]))
	}

	official := legacy
	official.WorkingDirectory = "/var/lib/sing-box"
	if coreMigrationSourceDigest(official) == coreMigrationSourceDigest(legacy) {
		t.Fatal("official working-directory mapping must be bound in the source digest")
	}
	drifted := official
	drifted.WorkingDirectory = "/var/lib/sing-box-drifted"
	if coreMigrationSourceDigest(drifted) == coreMigrationSourceDigest(official) {
		t.Fatal("working-directory drift must change the source digest")
	}
}

func TestCoreMigrationPreparedMarkerTracksManagedInitialStateAndReadsLegacy(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "active", "enabled")
	record, err := prepareCoreMigrationFileRollback(
		fixture.markerPrefix,
		core.EngineXray,
		fixture.existing,
		fixture.managed,
		coreMigrationRecord{
			State: coreMigrationInProgress, ConfigDigest: coreMigrationConfigDigest(fixture.importedConfig),
			SourceDigest: coreMigrationSourceDigest(fixture.existing), ExistingEnableState: "enabled",
			ManagedEnableState: "enabled", ManagedInitialState: "active",
		},
	)
	if err != nil {
		t.Fatalf("write active managed prepared marker: %v", err)
	}
	if record.ManagedInitialState != "active" {
		t.Fatalf("prepared record initial managed state = %q", record.ManagedInitialState)
	}
	readBack, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineXray)
	if err != nil || readBack.ManagedInitialState != "active" {
		t.Fatalf("read active managed prepared marker = %+v, %v", readBack, err)
	}
	if err := removeCoreMigrationMarker(fixture.markerPrefix, core.EngineXray); err != nil {
		t.Fatal(err)
	}
	if err := cleanupCoreMigrationBackups(fixture.markerPrefix, core.EngineXray); err != nil {
		t.Fatal(err)
	}

	stagedDigest, exists, err := protectedCoreMigrationFileDigest(fixture.existing.Binary, maxReleaseAssetSize)
	if err != nil || !exists {
		t.Fatalf("digest legacy staged binary: %q, %v, exists=%v", stagedDigest, err, exists)
	}
	legacyContents := fmt.Sprintf(
		"migrating-v2 %s %s enabled disabled - - %s\n",
		coreMigrationConfigDigest(fixture.importedConfig), coreMigrationSourceDigest(fixture.existing), stagedDigest,
	)
	if err := writeCoreMigrationMarkerContents(fixture.markerPrefix, core.EngineXray, legacyContents); err != nil {
		t.Fatalf("write legacy prepared marker: %v", err)
	}
	legacy, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineXray)
	if err != nil || legacy.ManagedInitialState != "inactive" || !legacy.HasFileRollback {
		t.Fatalf("legacy prepared marker compatibility = %+v, %v", legacy, err)
	}
}

func TestRefreshExistingCoreDiscoveryAcceptsLegacyCompletedMarker(t *testing.T) {
	fixture := newExistingCoreMigrationFixture(t, false)
	discoveryStatePath := filepath.Join(filepath.Dir(fixture.markerPrefix), "agent-state.json.existing-cores")
	legacy := EngineSpec{
		Binary: "/usr/lib/sing-box/sing-box", ConfigPath: "/etc/sing-box/config.json",
		ConfigDirectory: "/etc/sing-box/conf.d", ServiceBinary: "/usr/local/bin/sing-box",
		Service: "sing-box.service",
	}
	legacySource := legacy.Binary + "\x00" + legacy.ConfigPath + "\x00" + legacy.ConfigDirectory +
		"\x00" + legacy.ServiceBinary + "\x00" + legacy.Service
	legacyDigest := sha256.Sum256([]byte(legacySource))
	configDigest := sha256.Sum256([]byte(`{"inbounds":[],"outbounds":[]}`))
	if err := saveExistingCoreDiscoveryState(discoveryStatePath, existingCoreDiscoveryState{
		Version: existingCoreDiscoveryStateVersion,
		Specs: map[core.Engine]existingDiscoverySpec{
			core.EngineSingBox: discoverySpecFromEngineSpec(legacy),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(coreMigrationMarkerPath(fixture.markerPrefix, core.EngineSingBox), []byte(
		"migrated "+hex.EncodeToString(configDigest[:])+" "+hex.EncodeToString(legacyDigest[:])+" enabled disabled\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), discoveryStatePath, fixture.markerPrefix,
		map[core.Engine]EngineSpec{core.EngineSingBox: fixture.managed},
		nil,
	)
	if err != nil {
		t.Fatalf("refresh discovery with legacy completed marker: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("legacy completed marker discovery issues = %+v", issues)
	}
	if got, ok := specs[core.EngineSingBox]; !ok || got != legacy {
		t.Fatalf("legacy completed marker mapping = %+v, want %+v", specs[core.EngineSingBox], legacy)
	}
}

func TestExistingCoreMigrationRejectsUnsupportedSingBoxConfigDirectoryArgv(t *testing.T) {
	existing := EngineSpec{
		Binary: "/usr/bin/sing-box", ConfigPath: "/etc/sing-box/config.json",
		ConfigDirectory: "/etc/sing-box/conf.d",
	}
	for name, argv := range map[string]string{
		"unknown extra":      "/usr/bin/sing-box run -c /etc/sing-box/config.json -C /etc/sing-box/conf.d --unknown",
		"reordered":          "/usr/bin/sing-box run -C /etc/sing-box/conf.d -c /etc/sing-box/config.json",
		"long directory":     "/usr/bin/sing-box run -c /etc/sing-box/config.json --config-directory /etc/sing-box/conf.d",
		"multiple directory": "/usr/bin/sing-box run -c /etc/sing-box/config.json -C /etc/sing-box/conf.d -C /etc/sing-box/other",
	} {
		t.Run(name, func(t *testing.T) {
			if supportedExistingExecStart(core.EngineSingBox, existing, argv) {
				t.Fatalf("unsupported sing-box argv was accepted: %s", argv)
			}
		})
	}
}

func TestExistingCoreMigrationRejectsUnsupportedSingBoxOfficialArgv(t *testing.T) {
	existing := EngineSpec{
		Binary: "/usr/bin/sing-box", ConfigPath: "/etc/sing-box/config.json",
		ConfigDirectory: "/etc/sing-box", WorkingDirectory: "/var/lib/sing-box",
	}
	for name, argv := range map[string]string{
		"missing run":         "/usr/bin/sing-box -D /var/lib/sing-box -C /etc/sing-box",
		"missing directory":   "/usr/bin/sing-box -D /var/lib/sing-box run",
		"missing config flag": "/usr/bin/sing-box -D /var/lib/sing-box -C run",
		"duplicate directory": "/usr/bin/sing-box -D /var/lib/sing-box -C /etc/sing-box -C /etc/sing-box run",
		"repeated working":    "/usr/bin/sing-box -D /var/lib/sing-box -D /var/lib/sing-box -C /etc/sing-box run",
		"relative working":    "/usr/bin/sing-box -D var/lib/sing-box -C /etc/sing-box run",
		"relative config":     "/usr/bin/sing-box -D /var/lib/sing-box -C etc/sing-box run",
		"unknown flag":        "/usr/bin/sing-box -D /var/lib/sing-box -C /etc/sing-box run --verbose",
		"extra argument":      "/usr/bin/sing-box -D /var/lib/sing-box -C /etc/sing-box run stop",
		"workdir drift":       "/usr/bin/sing-box -D /var/lib/sing-box-drifted -C /etc/sing-box run",
	} {
		existing := existing
		t.Run(name, func(t *testing.T) {
			if supportedExistingExecStart(core.EngineSingBox, existing, argv) {
				t.Fatalf("unsupported sing-box official argv was accepted: %s", argv)
			}
		})
	}
}

func TestExistingCoreMigrationRejectsUnrestorableEnableStatesBeforeChanges(t *testing.T) {
	requireAgentRoot(t)
	tests := []struct {
		name            string
		existingEnabled string
		managedEnabled  string
	}{
		{name: "static original", existingEnabled: "static", managedEnabled: "disabled"},
		{name: "indirect original", existingEnabled: "indirect", managedEnabled: "disabled"},
		{name: "static managed", existingEnabled: "enabled", managedEnabled: "static"},
		{name: "indirect managed", existingEnabled: "enabled", managedEnabled: "indirect"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExistingCoreMigrationFixture(t, false)
			writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "active", test.existingEnabled)
			writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "inactive", test.managedEnabled)
			if _, err := fixture.executor.Execute(context.Background(), core.Task{
				Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
			}); err == nil || !strings.Contains(err.Error(), "enable states cannot be migrated safely") {
				t.Fatalf("unrestorable enable state error = %v", err)
			}
			fixture.assertServiceState(t, "xray.service", "active", test.existingEnabled)
			fixture.assertServiceState(t, "qagent-xray.service", "inactive", test.managedEnabled)
			assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
			if _, err := os.Stat(fixture.managed.Binary); !os.IsNotExist(err) {
				t.Fatalf("rejected migration installed a managed binary: %v", err)
			}
			if _, pending := fixture.executor.ExistingSpecs[core.EngineXray]; !pending {
				t.Fatal("rejected migration cleared pending existing service")
			}
			if _, err := os.Stat(coreMigrationMarkerPath(fixture.markerPrefix, core.EngineXray)); !os.IsNotExist(err) {
				t.Fatalf("rejected migration left a marker: %v", err)
			}
		})
	}
}

func TestExistingCoreMigrationNormalizesPersistentXrayLogsIntoManagedStream(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	content := `{"log":{"access":"/var/log/xray/access.log","error":"/var/log/xray/error.log","loglevel":"info"},"inbounds":[],"outbounds":[]}`
	if err := os.WriteFile(fixture.existing.ConfigPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	normalized, err := normalizeImportedXrayLogDestinations(content)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: normalized,
	}); err != nil {
		t.Fatalf("normalized persistent log migration: %v", err)
	}
	fixture.assertServiceState(t, "xray.service", "inactive", "disabled")
	fixture.assertServiceState(t, "qagent-xray.service", "active", "enabled")
	managedContent, err := os.ReadFile(fixture.managed.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateNoPersistentCoreLogs(core.EngineXray, string(managedContent)); err != nil {
		t.Fatalf("managed Xray snapshot retained persistent logs: %v", err)
	}
}

func TestExistingCoreMigrationRejectsEnableStateChangeBeforeStoppingOriginalService(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	writeExistingCoreHelperMutations(t, fixture.existing.Binary, 2, map[string]string{
		filepath.Join(fixture.stateDirectory, "xray.service.persistent"): "0\n",
		filepath.Join(fixture.stateDirectory, "xray.service.runtime"):    "0\n",
		filepath.Join(fixture.stateDirectory, "xray.service.fixed"):      "static\n",
	})
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionImportExisting, Engine: core.EngineXray, ConfigContent: fixture.importedConfig,
	}); err == nil || !strings.Contains(err.Error(), "enable states changed during migration preparation") {
		t.Fatalf("changed enable state error = %v", err)
	}
	fixture.assertServiceState(t, "xray.service", "active", "static")
	fixture.assertServiceState(t, "qagent-xray.service", "inactive", "disabled")
	assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
	if _, err := os.Stat(fixture.managed.Binary); !os.IsNotExist(err) {
		t.Fatalf("changed enable state left managed binary: %v", err)
	}
	if record, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineXray); err != nil || record.State != coreMigrationInProgress || !record.HasFileRollback {
		t.Fatalf("changed enable state recovery marker = %+v, %v", record, err)
	}
}

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

type existingCoreMigrationFixture struct {
	executor              *Executor
	existing              EngineSpec
	managed               EngineSpec
	importedConfig        string
	originalManagedConfig string
	markerPrefix          string
	stateDirectory        string
}

func prepareAndStageMigrationFixture(t *testing.T, fixture existingCoreMigrationFixture, existingEnableState, managedEnableState string) coreMigrationRecord {
	return prepareAndStageMigrationFixtureWithManagedState(t, fixture, existingEnableState, managedEnableState, "inactive")
}

func prepareAndStageMigrationFixtureWithManagedState(t *testing.T, fixture existingCoreMigrationFixture, existingEnableState, managedEnableState, managedInitialState string) coreMigrationRecord {
	t.Helper()
	record, err := prepareCoreMigrationFileRollback(
		fixture.markerPrefix,
		core.EngineXray,
		fixture.existing,
		fixture.managed,
		coreMigrationRecord{
			State: coreMigrationInProgress, ConfigDigest: coreMigrationConfigDigest(fixture.importedConfig),
			SourceDigest:        coreMigrationSourceDigest(fixture.existing),
			ExistingEnableState: existingEnableState, ManagedEnableState: managedEnableState,
			ManagedInitialState: managedInitialState,
		},
	)
	if err != nil {
		t.Fatalf("prepare durable migration fixture: %v", err)
	}
	if _, err := copyExistingCoreBinary(fixture.existing.Binary, fixture.managed.Binary); err != nil {
		t.Fatalf("stage managed binary: %v", err)
	}
	if _, err := atomicDeploy(fixture.managed.ConfigPath, fixture.importedConfig); err != nil {
		t.Fatalf("stage managed config: %v", err)
	}
	if err := verifyCoreMigrationStagedFiles(fixture.managed, record); err != nil {
		t.Fatalf("verify staged migration fixture: %v", err)
	}
	return record
}

func configureSingBoxDirectoryFixture(t *testing.T, fixture existingCoreMigrationFixture) (existingCoreMigrationFixture, string, string) {
	t.Helper()
	configDirectory := filepath.Join(filepath.Dir(fixture.existing.ConfigPath), "conf.d")
	if err := os.Mkdir(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.existing.ConfigPath, []byte(`{"log":{"output":"runtime.log"},"inbounds":[{"tag":"primary"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(configDirectory, "10-outbounds.json")
	if err := os.WriteFile(overlay, []byte(`{"outbounds":[{"tag":"direct"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeMigrationServiceState(t, fixture.stateDirectory, "sing-box.service", "active", "enabled")
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-sing-box.service", "inactive", "disabled")
	fixture.existing.ConfigDirectory = configDirectory
	fixture.existing.Service = "sing-box.service"
	fixture.managed.Service = "qagent-sing-box.service"
	writeMigrationExecStart(t, fixture.stateDirectory, fixture.existing.Service, systemdExecStart(
		fixture.existing.Binary,
		fixture.existing.Binary+" run -c "+fixture.existing.ConfigPath+" -C "+configDirectory,
	))
	content, _, err := readExistingConfigurationSources(fixture.existing)
	if err != nil {
		t.Fatalf("build merged sing-box fixture: %v", err)
	}
	fixture.importedConfig = content
	fixture.executor.Specs = map[core.Engine]EngineSpec{core.EngineSingBox: fixture.managed}
	fixture.executor.ExistingSpecs = map[core.Engine]EngineSpec{core.EngineSingBox: fixture.existing}
	return fixture, content, overlay
}

func configureSingBoxOfficialFixture(t *testing.T, fixture existingCoreMigrationFixture) (existingCoreMigrationFixture, string) {
	t.Helper()
	baseDirectory := filepath.Dir(fixture.existing.ConfigPath)
	workDirectory := filepath.Join(baseDirectory, "work")
	configDirectory := filepath.Join(baseDirectory, "etc-sing-box")
	for _, directory := range []string{workDirectory, configDirectory} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	officialConfig := filepath.Join(configDirectory, "config.json")
	if err := os.WriteFile(officialConfig, []byte(`{"inbounds":[{"tag":"primary"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "10-outbounds.json"), []byte(`{"outbounds":[{"tag":"direct"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writeMigrationServiceState(t, fixture.stateDirectory, "sing-box.service", "active", "enabled")
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-sing-box.service", "inactive", "disabled")
	fixture.existing.ConfigPath = officialConfig
	fixture.existing.ConfigDirectory = configDirectory
	fixture.existing.WorkingDirectory = workDirectory
	fixture.existing.Service = "sing-box.service"
	fixture.managed.Service = "qagent-sing-box.service"
	writeMigrationExecStart(t, fixture.stateDirectory, fixture.existing.Service, systemdExecStart(
		fixture.existing.Binary,
		fixture.existing.Binary+" -D "+workDirectory+" -C "+configDirectory+" run",
	))
	content, _, err := readExistingConfigurationSources(fixture.existing)
	if err != nil {
		t.Fatalf("build merged official sing-box fixture: %v", err)
	}
	fixture.importedConfig = content
	fixture.executor.Specs = map[core.Engine]EngineSpec{core.EngineSingBox: fixture.managed}
	fixture.executor.ExistingSpecs = map[core.Engine]EngineSpec{core.EngineSingBox: fixture.existing}
	return fixture, content
}

func newExistingCoreMigrationFixture(t *testing.T, failManagedStart bool) existingCoreMigrationFixture {
	t.Helper()
	root := t.TempDir()
	existingDirectory := filepath.Join(root, "existing")
	managedBinaryDirectory := filepath.Join(root, "managed-bin")
	managedConfigDirectory := filepath.Join(root, "managed-config")
	stateDirectory := filepath.Join(root, "systemctl")
	for _, directory := range []string{existingDirectory, managedBinaryDirectory, managedConfigDirectory, stateDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	existingBinary := filepath.Join(existingDirectory, "xray")
	if err := os.WriteFile(existingBinary, existingDiscoveryCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	existingConfig := filepath.Join(existingDirectory, "config.json")
	importedConfig := `{"inbounds":[],"outbounds":[],"tag":"imported"}`
	if err := os.WriteFile(existingConfig, []byte(importedConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	originalManagedConfig := `{"inbounds":[],"outbounds":[],"tag":"original-managed"}`
	managedConfig := filepath.Join(managedConfigDirectory, "config.json")
	if err := os.WriteFile(managedConfig, []byte(originalManagedConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	writeMigrationServiceState(t, stateDirectory, "xray.service", "active", "enabled")
	writeMigrationServiceState(t, stateDirectory, "qagent-xray.service", "inactive", "disabled")
	writeMigrationExecStart(t, stateDirectory, "xray.service", systemdExecStart(existingBinary, existingBinary+" run -config "+existingConfig))
	if failManagedStart {
		if err := os.WriteFile(filepath.Join(stateDirectory, "fail-managed-start"), []byte("1"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fakeSystemctl := filepath.Join(root, "fake-systemctl")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
state=%q
command=$1
shift
service=${1:-}
printf '%%s %%s\n' "$command" "$*" >> "$state/commands.log"
active_file="$state/$service.active"
case "$command" in
  is-active)
    value=$(cat "$active_file")
    printf '%%s\n' "$value"
    [ "$value" = active ]
    ;;
  is-enabled)
    fixed=$(cat "$state/$service.fixed")
    if [ -n "$fixed" ]; then
      printf '%%s\n' "$fixed"
      exit 1
    fi
    if [ "$(cat "$state/$service.persistent")" = 1 ]; then
      printf 'enabled\n'
      exit 0
    fi
    if [ "$(cat "$state/$service.runtime")" = 1 ]; then
      printf 'enabled-runtime\n'
      exit 0
    fi
    printf 'disabled\n'
    exit 1
    ;;
  show)
    cat "$state/$service.exec-start"
    ;;
  stop)
    printf 'inactive\n' > "$active_file"
    if [ "$service" = qagent-xray.service ] && [ "$(cat "$state/$service.runtime")" = 1 ] && [ -f "$state/respawn-managed-on-runtime-stop" ] && [ ! -f "$state/managed-respawned" ]; then
      printf 'active\n' > "$active_file"
      : > "$state/managed-respawned"
    fi
    ;;
  start|restart)
	if [ "$command" = restart ] && [ "$service" = qagent-sing-box.service ] && [ -f "$state/fail-managed-restart" ]; then
	  rm -f "$state/fail-managed-restart"
	  printf 'failed\n' > "$active_file"
	  exit 1
	fi
    if [ "$service" = qagent-xray.service ] && [ -f "$state/fail-managed-start" ]; then
      printf 'failed\n' > "$active_file"
      exit 1
    fi
    printf 'active\n' > "$active_file"
	if [ "$service" = xray.service ] && [ -f "$state/activate-managed-on-existing-start" ]; then
	  printf 'active\n' > "$state/qagent-xray.service.active"
	fi
    ;;
  enable)
	if [ "$service" = --runtime ]; then
	  service=$2
	  printf '1\n' > "$state/$service.runtime"
	  exit 0
	fi
	if [ "$service" != qagent-xray.service ] || [ ! -f "$state/ignore-managed-enable" ]; then
      printf '1\n' > "$state/$service.persistent"
	fi
	if [ "$service" = qagent-xray.service ] && [ -f "$state/activate-existing-on-managed-enable" ]; then
	  printf 'active\n' > "$state/xray.service.active"
	fi
	if [ "$service" = qagent-xray.service ] && [ -f "$state/fail-managed-after-enable" ]; then
	  printf 'failed\n' > "$state/qagent-xray.service.active"
	fi
    ;;
  disable)
    if [ "$service" = --runtime ]; then
      service=$2
	  if [ "$service" != xray.service ] || [ ! -f "$state/ignore-existing-disable-runtime" ]; then
	    printf '0\n' > "$state/$service.runtime"
	  fi
      exit 0
    fi
    if [ "$service" = xray.service ] && [ -f "$state/fail-existing-disable" ]; then
      exit 1
    fi
	if [ "$service" != xray.service ] || [ ! -f "$state/ignore-existing-disable-persistent" ]; then
      printf '0\n' > "$state/$service.persistent"
	fi
    ;;
  *) exit 1 ;;
esac
`, stateDirectory)
	if err := os.WriteFile(fakeSystemctl, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	previousSystemctl := systemctlPath
	systemctlPath = fakeSystemctl
	t.Cleanup(func() { systemctlPath = previousSystemctl })

	existing := EngineSpec{Binary: existingBinary, ConfigPath: existingConfig, Service: "xray.service"}
	managed := EngineSpec{
		Binary: filepath.Join(managedBinaryDirectory, "xray"), ConfigPath: managedConfig, Service: "qagent-xray.service",
	}
	markerPrefix := filepath.Join(root, "agent-state.json.core-migration")
	return existingCoreMigrationFixture{
		executor: &Executor{
			Specs:                 map[core.Engine]EngineSpec{core.EngineXray: managed},
			ExistingSpecs:         map[core.Engine]EngineSpec{core.EngineXray: existing},
			MigrationMarkerPrefix: markerPrefix,
		},
		existing: existing, managed: managed, importedConfig: importedConfig,
		originalManagedConfig: originalManagedConfig, markerPrefix: markerPrefix, stateDirectory: stateDirectory,
	}
}

func writeMigrationServiceState(t *testing.T, directory, service, active, enabled string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, service+".active"), []byte(active+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	persistent, runtime, fixed := "0", "0", ""
	switch enabled {
	case "enabled":
		persistent = "1"
	case "enabled-runtime":
		runtime = "1"
	case "disabled":
	case "static", "indirect":
		fixed = enabled
	default:
		t.Fatalf("unsupported test enable state %q", enabled)
	}
	for suffix, value := range map[string]string{
		".persistent": persistent,
		".runtime":    runtime,
		".fixed":      fixed,
	} {
		if err := os.WriteFile(filepath.Join(directory, service+suffix), []byte(value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func writeMigrationExecStart(t *testing.T, directory, service, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, service+".exec-start"), []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeMigrationTrigger(t *testing.T, directory, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func systemdExecStart(executable, argv string) string {
	return fmt.Sprintf("{ path=%s ; argv[]=%s ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=1 ; code=(null) ; status=0/0 }", executable, argv)
}

func (fixture existingCoreMigrationFixture) assertServiceState(t *testing.T, service, active, enabled string) {
	t.Helper()
	activeValue, err := os.ReadFile(filepath.Join(fixture.stateDirectory, service+".active"))
	if err != nil || strings.TrimSpace(string(activeValue)) != active {
		t.Fatalf("%s active state = %q, %v", service, activeValue, err)
	}
	actualEnabled, persistent, runtime, err := readMigrationEnableState(fixture.stateDirectory, service)
	if err != nil || actualEnabled != enabled {
		t.Fatalf("%s enabled state = %q (persistent=%t runtime=%t), %v", service, actualEnabled, persistent, runtime, err)
	}
}

func readMigrationEnableState(directory, service string) (string, bool, bool, error) {
	read := func(suffix string) (string, error) {
		value, err := os.ReadFile(filepath.Join(directory, service+suffix))
		return strings.TrimSpace(string(value)), err
	}
	fixed, err := read(".fixed")
	if err != nil {
		return "", false, false, err
	}
	persistentValue, err := read(".persistent")
	if err != nil {
		return "", false, false, err
	}
	runtimeValue, err := read(".runtime")
	if err != nil {
		return "", false, false, err
	}
	persistent := persistentValue == "1"
	runtime := runtimeValue == "1"
	switch {
	case fixed != "":
		return fixed, persistent, runtime, nil
	case persistent:
		return "enabled", persistent, runtime, nil
	case runtime:
		return "enabled-runtime", persistent, runtime, nil
	default:
		return "disabled", persistent, runtime, nil
	}
}
