//go:build linux

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestExecutorReadsAndValidatesCurrentConfigurationWithoutWriting(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	binary := filepath.Join(root, "mihomo")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.yaml")
	content := "mixed-port: 7890\nmode: rule\nproxies: []\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{
		core.EngineMihomo: {Binary: binary, ConfigPath: configPath, Service: "qagent-mihomo.service"},
	}}
	output, err := executor.Execute(context.Background(), core.Task{Action: core.ActionReadConfig, Engine: core.EngineMihomo})
	if err != nil {
		t.Fatalf("read current configuration: %v", err)
	}
	if output != content {
		t.Fatalf("read configuration output = %q, want exact current file", output)
	}
	assertFileContentAndMode(t, configPath, content, 0o600)

	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(), core.Task{Action: core.ActionReadConfig, Engine: core.EngineMihomo}); err == nil || !strings.Contains(err.Error(), "real core validation") {
		t.Fatalf("read configuration with rejecting core error = %v", err)
	}
}

func TestReadCurrentConfigValidatesTheReturnedSnapshot(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	original := `{"inbounds":[],"outbounds":[],"tag":"original"}`
	changed := `{"inbounds":[],"outbounds":[],"tag":"changed"}`
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "xray")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' %q > %q\ngrep -q '\"tag\":\"original\"' \"$4\"\n", changed, configPath)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{
		core.EngineXray: {Binary: binary, ConfigPath: configPath, Service: "xray.service"},
	}}

	content, err := executor.ReadCurrentConfig(context.Background(), core.EngineXray)
	if err != nil {
		t.Fatalf("ReadCurrentConfig() error = %v", err)
	}
	if content != original {
		t.Fatalf("returned snapshot = %q, want original %q", content, original)
	}
	if live, err := os.ReadFile(configPath); err != nil || string(live) != changed {
		t.Fatalf("live file after concurrent change = %q, %v", live, err)
	}
}

func TestManualReadAllowsViewingConfigThatManagedDeployWouldReject(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "xray")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	content := `{"log":{"access":"/var/log/xray/access.log"},"inbounds":[{"tag":"a","protocol":"http","port":21001}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{
		core.EngineXray: {Binary: binary, ConfigPath: configPath, Service: "xray.service"},
	}}

	read, err := executor.Execute(context.Background(), core.Task{Action: core.ActionReadConfig, Engine: core.EngineXray})
	if err != nil || read != content {
		t.Fatalf("manual read = %q, %v", read, err)
	}
	if _, err := executor.Execute(context.Background(), core.Task{
		Action: core.ActionValidate, Engine: core.EngineXray, ConfigContent: read,
	}); err == nil || !strings.Contains(err.Error(), "persistent") {
		t.Fatalf("managed validation accepted persistent logs: %v", err)
	}
}

func TestReadConfigurationFileRejectsUnsafeOrOversizedSources(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	regular := filepath.Join(root, "regular.yaml")
	if err := os.WriteFile(regular, []byte("mixed-port: 7890\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "symlink.yaml")
	if err := os.Symlink(regular, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfigurationFile(symlink); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlink configuration error = %v", err)
	}
	unsafe := filepath.Join(root, "unsafe.yaml")
	if err := os.WriteFile(unsafe, []byte("mixed-port: 7890\n"), 0o620); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unsafe, 0o620); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfigurationFile(unsafe); err == nil || !strings.Contains(err.Error(), "writable by group") {
		t.Fatalf("group-writable configuration error = %v", err)
	}
	oversized := filepath.Join(root, "oversized.yaml")
	if err := os.WriteFile(oversized, []byte(strings.Repeat("x", core.MaxConfigBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfigurationFile(oversized); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized configuration error = %v", err)
	}
}
