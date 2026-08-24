//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/agent"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestInspectExistingValidatesACopyOutsideTheSourceDirectory(t *testing.T) {
	root := t.TempDir()
	sourceDirectory := filepath.Join(root, "existing")
	if err := os.Mkdir(sourceDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(sourceDirectory, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[],"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(sourceDirectory, "xray")
	script := fmt.Sprintf("#!/bin/sh\n[ \"$1 $2 $3\" = 'run -test -config' ]\n[ \"$4\" != %q ]\ngrep -q '\"inbounds\"' \"$4\"\n", configPath)
	if err := os.WriteFile(binaryPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(sourceDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if err := runUtilityCommand(map[core.Engine]agent.EngineSpec{
		core.EngineXray: {Binary: binaryPath, ConfigPath: configPath, Service: "xray.service"},
	}, []string{"inspect-existing", "xray"}); err != nil {
		t.Fatalf("inspect existing: %v", err)
	}
	after, err := os.ReadDir(sourceDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("inspection changed source directory: before=%d after=%d", len(before), len(after))
	}
}

func TestExistingSingBoxSpecCarriesConfigDirectoryAndServiceExecutable(t *testing.T) {
	t.Setenv("QCH_EXISTING_SING_BOX_BINARY", "/usr/lib/sing-box/sing-box")
	t.Setenv("QCH_EXISTING_SING_BOX_CONFIG", "/etc/sing-box/config.json")
	t.Setenv("QCH_EXISTING_SING_BOX_CONFIG_DIRECTORY", "/etc/sing-box/conf.d")
	t.Setenv("QCH_EXISTING_SING_BOX_WORK_DIRECTORY", "/var/lib/sing-box")
	t.Setenv("QCH_EXISTING_SING_BOX_SERVICE_BINARY", "/usr/local/bin/sing-box")
	t.Setenv("QCH_EXISTING_SING_BOX_SERVICE", "sing-box.service")
	spec, ok := existingSpec("SING_BOX")
	if !ok {
		t.Fatal("complete sing-box directory mapping was not loaded")
	}
	want := agent.EngineSpec{
		Binary: "/usr/lib/sing-box/sing-box", ConfigPath: "/etc/sing-box/config.json",
		ConfigDirectory: "/etc/sing-box/conf.d", WorkingDirectory: "/var/lib/sing-box",
		ServiceBinary: "/usr/local/bin/sing-box",
		Service:       "sing-box.service",
	}
	if spec != want {
		t.Fatalf("existing sing-box spec = %+v, want %+v", spec, want)
	}
}

func TestExistingSingBoxSpecCapturesRelativeWorkingDirectory(t *testing.T) {
	t.Setenv("QCH_EXISTING_SING_BOX_BINARY", "/usr/bin/sing-box")
	t.Setenv("QCH_EXISTING_SING_BOX_CONFIG", "/etc/sing-box/config.json")
	t.Setenv("QCH_EXISTING_SING_BOX_CONFIG_DIRECTORY", "/etc/sing-box")
	t.Setenv("QCH_EXISTING_SING_BOX_WORK_DIRECTORY", "var/lib/sing-box")
	t.Setenv("QCH_EXISTING_SING_BOX_SERVICE", "sing-box.service")
	spec, ok := existingSpec("SING_BOX")
	if !ok {
		t.Fatal("sing-box mapping was not loaded")
	}
	if spec.WorkingDirectory != "var/lib/sing-box" {
		t.Fatalf("relative sing-box working directory was not captured: %+v", spec)
	}
}

func TestInspectExistingOfficialSingBoxUsesWorkingDirectoryAndRejectsRelativeResources(t *testing.T) {
	root := t.TempDir()
	workingDirectory := filepath.Join(root, "work")
	configDirectory := filepath.Join(root, "config")
	if err := os.MkdirAll(workingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDirectory, "config.json")
	fragmentPath := filepath.Join(configDirectory, "10-outbounds.json")
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[],"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragmentPath, []byte(`{"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(root, "checks.log")
	binaryPath := filepath.Join(root, "sing-box")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"cwd=$(pwd) args=$*\" >> %q\n[ \"$1\" = check ] || exit 1\ncase \"$2\" in -D|-c) exit 0 ;; *) exit 1 ;; esac\n", recordPath)
	if err := os.WriteFile(binaryPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QCH_SING_BOX_BINARY", binaryPath)
	t.Setenv("QCH_SING_BOX_CONFIG", configPath)
	t.Setenv("QCH_SING_BOX_CONFIG_DIRECTORY", configDirectory)
	t.Setenv("QCH_SING_BOX_WORK_DIRECTORY", workingDirectory)
	t.Setenv("QCH_SING_BOX_SERVICE_BINARY", binaryPath)
	t.Setenv("QCH_SING_BOX_SERVICE", "sing-box.service")
	spec := overrideSpec(agent.DefaultSpecs()[core.EngineSingBox], "SING_BOX")
	if spec.WorkingDirectory != workingDirectory {
		t.Fatalf("utility sing-box spec lost working directory: %+v", spec)
	}
	if err := runUtilityCommand(map[core.Engine]agent.EngineSpec{core.EngineSingBox: spec}, []string{"inspect-existing", "sing-box"}); err != nil {
		t.Fatalf("inspect official sing-box: %v", err)
	}
	checks, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "cwd=" + workingDirectory + " args=check -D " + workingDirectory + " -C " + configDirectory
	if !strings.Contains(string(checks), want) {
		t.Fatalf("official sing-box check did not preserve -D context: %q", checks)
	}
	if err := os.WriteFile(fragmentPath, []byte(`{"log":{"output":"relative.log"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runUtilityCommand(map[core.Engine]agent.EngineSpec{core.EngineSingBox: spec}, []string{"inspect-existing", "sing-box"}); err == nil || !strings.Contains(err.Error(), "cannot be migrated safely") {
		t.Fatalf("relative official sing-box resource was accepted: %v", err)
	}
}

func TestAgentStartupRefreshesAutomaticExistingCoreDiscovery(t *testing.T) {
	contents, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	mainSource := string(contents)
	for _, required := range []string{
		"manualExistingSpecs", "RefreshExistingCoreDiscovery", `statePath+".existing-cores"`,
		"ExistingDiscoveryIssues: discoveryIssues", "MigrationMarkerPrefix:",
	} {
		if !strings.Contains(mainSource, required) {
			t.Errorf("Agent startup discovery is missing %q", required)
		}
	}
}
