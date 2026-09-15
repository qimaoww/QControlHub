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

func TestExistingXrayMigrationStagesAndRollsBackGeoAssets(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	sourceAssets := map[string]string{
		"geoip.dat":   "new geoip data\n",
		"geosite.dat": "new geosite data\n",
	}
	for name, content := range sourceAssets {
		if err := os.WriteFile(filepath.Join(filepath.Dir(fixture.existing.Binary), name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	originalGeoIP := "original managed geoip data\n"
	managedGeoIP := filepath.Join(filepath.Dir(fixture.managed.Binary), "geoip.dat")
	if err := os.WriteFile(managedGeoIP, []byte(originalGeoIP), 0o640); err != nil {
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
		t.Fatalf("prepare Xray asset transaction: %v", err)
	}
	if !record.HasAssetRollback || record.StagedAssetDigests[0] == coreMigrationMissingBackup ||
		record.StagedAssetDigests[1] == coreMigrationMissingBackup || record.AssetBackupDigests[0] == coreMigrationMissingBackup ||
		record.AssetBackupDigests[1] != coreMigrationMissingBackup {
		t.Fatalf("Xray asset transaction record = %+v", record)
	}
	readBack, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineXray)
	if err != nil || readBack.AssetBackupDigests != record.AssetBackupDigests || readBack.StagedAssetDigests != record.StagedAssetDigests {
		t.Fatalf("read Xray asset transaction record = %+v, %v", readBack, err)
	}
	if _, err := copyExistingCoreBinary(fixture.existing.Binary, fixture.managed.Binary); err != nil {
		t.Fatal(err)
	}
	if err := stageExistingXrayMigrationAssets(fixture.existing, fixture.managed, record); err != nil {
		t.Fatal(err)
	}
	if _, err := atomicDeploy(fixture.managed.ConfigPath, fixture.importedConfig); err != nil {
		t.Fatal(err)
	}
	if err := verifyCoreMigrationStagedFiles(fixture.managed, record); err != nil {
		t.Fatalf("verify staged Xray assets: %v", err)
	}
	for name, content := range sourceAssets {
		assertFileContentAndMode(t, filepath.Join(filepath.Dir(fixture.managed.Binary), name), content, map[string]os.FileMode{
			"geoip.dat": 0o640, "geosite.dat": 0o644,
		}[name])
	}
	if err := restoreCoreMigrationFiles(fixture.markerPrefix, core.EngineXray, fixture.managed, record); err != nil {
		t.Fatalf("roll back Xray assets: %v", err)
	}
	assertFileContentAndMode(t, managedGeoIP, originalGeoIP, 0o640)
	if _, err := os.Lstat(filepath.Join(filepath.Dir(fixture.managed.Binary), "geosite.dat")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("originally absent geosite.dat remained after rollback: %v", err)
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
