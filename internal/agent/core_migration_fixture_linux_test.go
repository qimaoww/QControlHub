//go:build linux

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
	content, err = normalizeImportedSingBoxLogDestination(content)
	if err != nil {
		t.Fatalf("normalize merged sing-box fixture: %v", err)
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
    if [ "$service" = qagent-shadowsocks-rust.service ] && [ -f "$state/fail-ssrust-rollback-stop" ]; then
      exit 1
    fi
    printf 'inactive\n' > "$active_file"
    if [ "$service" = qagent-xray.service ] && [ "$(cat "$state/$service.runtime")" = 1 ] && [ -f "$state/respawn-managed-on-runtime-stop" ] && [ ! -f "$state/managed-respawned" ]; then
      printf 'active\n' > "$active_file"
      : > "$state/managed-respawned"
    fi
    ;;
  start|restart)
	if [ "$service" = qagent-shadowsocks-rust.service ] && [ -f "$state/fail-ssrust-start-once" ]; then
	  rm -f "$state/fail-ssrust-start-once"
	  if [ -f "$state/ssrust-incomplete-rollback" ]; then : > "$state/fail-ssrust-rollback-stop"; fi
	  printf 'failed\n' > "$active_file"
	  exit 1
	fi
	if [ "$command" = restart ] && [ "$service" = qagent-sing-box.service ] && [ -f "$state/fail-managed-restart" ]; then
	  rm -f "$state/fail-managed-restart"
	  printf 'failed\n' > "$active_file"
	  exit 1
	fi
    if { [ "$service" = qagent-xray.service ] || [ "$service" = qagent-shadowsocks-rust.service ]; } && [ -f "$state/fail-managed-start" ]; then
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
	case "static", "indirect", "masked", "not-found":
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
