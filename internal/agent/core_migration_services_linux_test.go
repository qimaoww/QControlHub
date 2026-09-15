//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

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
	if _, err := copyExistingCoreBinary(fixture.existing.Binary, fixture.managed.Binary); err != nil {
		t.Fatalf("install managed core before migration: %v", err)
	}
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "active", "enabled-runtime")
	writeMigrationTrigger(t, fixture.stateDirectory, "respawn-managed-on-runtime-stop")
	runtime := fixture.executor.Runtime(context.Background())[core.EngineXray]
	if !runtime.Installed || !runtime.ExistingConfigAvailable || runtime.ServiceStatus != "active" {
		t.Fatalf("installed managed core with optional import = %+v", runtime)
	}
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

func TestPendingExistingServiceDoesNotBlockManagedCoreOperations(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)

	runtime := fixture.executor.Runtime(context.Background())[core.EngineXray]
	if runtime.Installed || !runtime.ExistingConfigAvailable || runtime.ServiceStatus != "inactive" {
		t.Fatalf("uninstalled managed core with optional import = %+v", runtime)
	}
	status, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionStatus, Engine: core.EngineXray,
	})
	if err != nil || strings.TrimSpace(status) != "inactive" {
		t.Fatalf("managed status while import is available = %q, %v", status, err)
	}
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionInstall, Engine: core.EngineXray, CoreVersion: "not/a/version",
	}); err == nil || strings.Contains(err.Error(), "import") || strings.Contains(err.Error(), "导入") || !strings.Contains(err.Error(), "内核版本") {
		t.Fatalf("managed install was still gated by optional import: %v", err)
	}
	if _, err := copyExistingCoreBinary(fixture.existing.Binary, fixture.managed.Binary); err != nil {
		t.Fatalf("stage independently installed managed core: %v", err)
	}
	managedConfig := `{"inbounds":[{"tag":"managed","protocol":"http","port":21001}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`
	accounted, err := serverconfig.PrepareAccounting(core.EngineXray, managedConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionDeploy, Engine: core.EngineXray, ConfigContent: managedConfig,
	}); err != nil {
		t.Fatalf("deploy managed configuration while optional import remains: %v", err)
	}
	externalSnapshot, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionReadConfig, Engine: core.EngineXray,
	})
	if err != nil || externalSnapshot != fixture.importedConfig {
		t.Fatalf("external import snapshot = %q, %v", externalSnapshot, err)
	}
	managedSnapshot, err := fixture.executor.Execute(context.Background(), core.Task{
		Action: core.ActionReadManagedConfig, Engine: core.EngineXray,
	})
	if err != nil || managedSnapshot != accounted.Content {
		t.Fatalf("managed configuration snapshot = %q, %v", managedSnapshot, err)
	}
	fixture.assertServiceState(t, "xray.service", "active", "enabled")
	fixture.assertServiceState(t, "qagent-xray.service", "active", "disabled")
	if _, pending := fixture.executor.ExistingSpecs[core.EngineXray]; !pending {
		t.Fatal("ordinary managed operation discarded the optional import")
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
