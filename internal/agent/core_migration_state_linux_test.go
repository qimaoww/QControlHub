//go:build linux

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCompletedCoreMigrationStateDriftBlocksExplicitMappingTasksAfterRestart(t *testing.T) {
	requireAgentRoot(t)
	tests := []struct {
		name            string
		existingStatus  string
		existingEnabled string
		managedStatus   string
		managedEnabled  string
	}{
		{name: "original service reactivated", existingStatus: "active", existingEnabled: "disabled", managedStatus: "inactive", managedEnabled: "enabled"},
		{name: "original service re-enabled", existingStatus: "inactive", existingEnabled: "enabled", managedStatus: "inactive", managedEnabled: "enabled"},
		{name: "original service failed after a start attempt", existingStatus: "failed", existingEnabled: "disabled", managedStatus: "inactive", managedEnabled: "enabled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExistingCoreMigrationFixture(t, false)
			writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", test.existingStatus, test.existingEnabled)
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

// An operator may stop the QAgent-managed core after a migration completed, and
// an Agent upgrade must not reinterpret that runtime state as an unsafe
// migration. Only the retired legacy service coming back invalidates the record.
func TestCompletedCoreMigrationKeepsOwnershipWhenManagedServiceStopped(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	if err := os.WriteFile(fixture.managed.Binary, []byte("migrated managed core bytes\n"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "inactive", "disabled")
	writeMigrationServiceState(t, fixture.stateDirectory, "qagent-xray.service", "inactive", "enabled")
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
		t.Fatalf("load completed migration with a stopped managed service: %v", err)
	}
	if _, pending := restarted.ExistingSpecs[core.EngineXray]; pending {
		t.Fatal("a stopped managed service re-armed the pending import mapping")
	}
	if _, ok := restarted.completedMigrations[core.EngineXray]; !ok {
		t.Fatal("verified migration ownership was dropped when the managed service was stopped")
	}
	if issue := restarted.ExistingDiscoveryIssues[core.EngineXray]; issue != "" {
		t.Fatalf("stopped managed service produced a discovery issue: %q", issue)
	}
	runtime := restarted.Runtime(context.Background())[core.EngineXray]
	if runtime.ExistingConfigUnsupportedReason != "" || runtime.ExistingConfigAvailable {
		t.Fatalf("stopped managed service runtime = %+v", runtime)
	}
	if !runtime.Installed || runtime.ServiceStatus != "inactive" {
		t.Fatalf("stopped managed service was not reported as an installed, stopped core: %+v", runtime)
	}
	if _, err := restarted.Execute(context.Background(), core.Task{Action: core.ActionStart, Engine: core.EngineXray}); err != nil {
		t.Fatalf("start a stopped managed service after Agent restart: %v", err)
	}
	if status, err := serviceStatusWithManager(context.Background(), restarted.serviceManager(), fixture.managed.Service); err != nil || status != "active" {
		t.Fatalf("managed service status after restart task = %q, %v", status, err)
	}
}

// Every state that proves the retired service cannot take the core over again
// is accepted, no matter how the operator left the managed unit. In particular
// a masked unit and a unit that no longer exists must not re-arm the lock: an
// administrator hardening the node, or a package removal, must not force a
// manual recovery on the next Agent upgrade.
func TestCompletedCoreMigrationKeepsOwnershipForRetiredServiceStates(t *testing.T) {
	requireAgentRoot(t)
	tests := []struct {
		name            string
		existingEnabled string
		managedStatus   string
		managedEnabled  string
	}{
		{name: "legacy unit disabled and managed unit stopped", existingEnabled: "disabled", managedStatus: "inactive", managedEnabled: "enabled"},
		{name: "legacy unit disabled and managed unit disabled", existingEnabled: "disabled", managedStatus: "inactive", managedEnabled: "disabled"},
		{name: "legacy unit masked", existingEnabled: "masked", managedStatus: "inactive", managedEnabled: "enabled"},
		{name: "legacy unit removed", existingEnabled: "not-found", managedStatus: "inactive", managedEnabled: "enabled"},
		{name: "legacy unit static", existingEnabled: "static", managedStatus: "inactive", managedEnabled: "enabled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExistingCoreMigrationFixture(t, false)
			writeMigrationServiceState(t, fixture.stateDirectory, "xray.service", "inactive", test.existingEnabled)
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
				t.Fatalf("load completed migration: %v", err)
			}
			if _, pending := restarted.ExistingSpecs[core.EngineXray]; pending {
				t.Fatal("a retired legacy service re-armed the pending import mapping")
			}
			if _, ok := restarted.completedMigrations[core.EngineXray]; !ok {
				t.Fatal("verified migration ownership was dropped for a retired legacy service")
			}
			if issue := restarted.ExistingDiscoveryIssues[core.EngineXray]; issue != "" {
				t.Fatalf("a retired legacy service produced a discovery issue: %q", issue)
			}
		})
	}
}

// The completed-migration restart check is engine independent: sing-box and
// ss-rust imports persist the same marker, so an operator stop must not lock
// those engines either.
func TestCompletedCoreMigrationKeepsOwnershipForEveryMigratableEngine(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	stateDirectory := filepath.Join(root, "systemctl")
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	fakeSystemctl := filepath.Join(root, "fake-systemctl")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
state=%q
command=$1
shift
service=${1:-}
active_file="$state/$service.active"
case "$command" in
  is-active)
    value=$(cat "$active_file")
    printf '%%s\n' "$value"
    [ "$value" = active ]
    ;;
  is-enabled)
    if [ "$(cat "$state/$service.persistent")" = 1 ]; then
      printf 'enabled\n'
      exit 0
    fi
    printf 'disabled\n'
    exit 1
    ;;
  start|restart)
    printf 'active\n' > "$active_file"
    ;;
  stop)
    printf 'inactive\n' > "$active_file"
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

	managedSpecs := make(map[core.Engine]EngineSpec)
	existingSpecs := make(map[core.Engine]EngineSpec)
	markerPrefix := filepath.Join(root, "agent-state.json.core-migration")
	for _, test := range []struct {
		engine          core.Engine
		managedService  string
		existingService string
	}{
		{engine: core.EngineXray, managedService: "qagent-xray.service", existingService: "xray.service"},
		{engine: core.EngineSingBox, managedService: "qagent-sing-box.service", existingService: "sing-box.service"},
		{engine: core.EngineShadowsocksRust, managedService: "qagent-shadowsocks-rust.service", existingService: "shadowsocks-rust.service"},
	} {
		directory := filepath.Join(root, string(test.engine))
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		binary := filepath.Join(directory, "managed-core")
		if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o750); err != nil {
			t.Fatal(err)
		}
		managedConfig := filepath.Join(directory, "managed.json")
		if err := os.WriteFile(managedConfig, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		managedSpecs[test.engine] = EngineSpec{Binary: binary, ConfigPath: managedConfig, Service: test.managedService}
		existingSpecs[test.engine] = EngineSpec{
			Binary:     filepath.Join(directory, "legacy-core"),
			ConfigPath: filepath.Join(directory, "legacy.json"),
			Service:    test.existingService,
		}
		// The managed core was stopped from the console before the upgrade.
		writeMigrationServiceState(t, stateDirectory, test.managedService, "inactive", "enabled")
		writeMigrationServiceState(t, stateDirectory, test.existingService, "inactive", "disabled")
		if err := writeCoreMigrationMarker(markerPrefix, test.engine, coreMigrationComplete, coreMigrationConfigDigest("{}"), coreMigrationSourceDigest(existingSpecs[test.engine]), "enabled", "disabled"); err != nil {
			t.Fatal(err)
		}
	}
	restarted := &Executor{
		Specs:                   managedSpecs,
		ExistingSpecs:           existingSpecs,
		ExistingDiscoveryIssues: make(map[core.Engine]string),
		MigrationMarkerPrefix:   markerPrefix,
	}
	if err := restarted.LoadCoreMigrationState(); err != nil {
		t.Fatalf("load completed migrations with stopped managed services: %v", err)
	}
	for engine := range managedSpecs {
		if _, pending := restarted.ExistingSpecs[engine]; pending {
			t.Fatalf("%s: a stopped managed service re-armed the pending import mapping", engine)
		}
		if _, ok := restarted.completedMigrations[engine]; !ok {
			t.Fatalf("%s: verified migration ownership was dropped when the managed service was stopped", engine)
		}
		if issue := restarted.ExistingDiscoveryIssues[engine]; issue != "" {
			t.Fatalf("%s: stopped managed service produced a discovery issue: %q", engine, issue)
		}
		runtime := restarted.Runtime(context.Background())[engine]
		if runtime.ExistingConfigUnsupportedReason != "" || runtime.ExistingConfigAvailable || !runtime.Installed || runtime.ServiceStatus != "inactive" {
			t.Fatalf("%s: stopped managed service runtime = %+v", engine, runtime)
		}
		if _, err := restarted.Execute(context.Background(), core.Task{Action: core.ActionStart, Engine: engine}); err != nil {
			t.Fatalf("%s: start a stopped managed service after Agent restart: %v", engine, err)
		}
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

func TestCoreMigrationDigestAcceptsInactiveOrphanInstallerBinary(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	allowFixtureOrphanOwnerPath(t, fixture.existing.Binary)
	assignInactiveOrphanOwner(t, fixture.existing.Binary)
	if err := os.Chmod(fixture.existing.Binary, 0o744); err != nil {
		t.Fatal(err)
	}
	record, err := prepareCoreMigrationFileRollback(
		fixture.markerPrefix,
		core.EngineXray,
		fixture.existing,
		fixture.managed,
		coreMigrationRecord{
			State: coreMigrationInProgress, ConfigDigest: coreMigrationConfigDigest(fixture.importedConfig),
			SourceDigest: coreMigrationSourceDigest(fixture.existing), ExistingEnableState: "enabled", ManagedEnableState: "disabled",
		},
	)
	if err != nil {
		t.Fatalf("prepare inactive-orphan migration transaction: %v", err)
	}
	if record.StagedBinaryDigest == "" || record.StagedBinaryDigest == coreMigrationMissingBackup {
		t.Fatalf("inactive-orphan staged digest = %q", record.StagedBinaryDigest)
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
