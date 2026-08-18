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

func TestAtomicDeployCreatesAndBacksUpConfiguration(t *testing.T) {
	t.Parallel()
	destination := filepath.Join(t.TempDir(), "nested", "config.json")
	first := `{"version":1}`
	backup, err := atomicDeploy(destination, first)
	if err != nil {
		t.Fatalf("first atomicDeploy() error = %v", err)
	}
	if backup != "" {
		t.Fatalf("first atomicDeploy() backup = %q, want empty", backup)
	}
	assertFileContentAndMode(t, destination, first, 0o600)
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(destination), ".qcontrolhub-config-*.tmp")); err != nil {
		t.Fatalf("glob temporary files: %v", err)
	} else if len(matches) != 0 {
		t.Fatalf("temporary files left behind after rename: %v", matches)
	}

	second := `{"version":2}`
	backup, err = atomicDeploy(destination, second)
	if err != nil {
		t.Fatalf("replacement atomicDeploy() error = %v", err)
	}
	if backup == "" || filepath.Dir(backup) != filepath.Dir(destination) {
		t.Fatalf("replacement backup path = %q", backup)
	}
	assertFileContentAndMode(t, destination, second, 0o600)
	assertFileContentAndMode(t, backup, first, 0o600)
}

func TestAtomicDeployRejectsUnsafeDestinations(t *testing.T) {
	t.Parallel()
	if _, err := atomicDeploy("relative/config.json", `{}`); err == nil {
		t.Fatal("atomicDeploy() accepted a relative destination")
	}
	if _, err := atomicDeploy("", `{}`); err == nil {
		t.Fatal("atomicDeploy() accepted an empty destination")
	}

	root := t.TempDir()
	realTarget := filepath.Join(root, "real.json")
	if err := os.WriteFile(realTarget, []byte("original"), 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	symlink := filepath.Join(root, "config.json")
	if err := os.Symlink(realTarget, symlink); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if _, err := atomicDeploy(symlink, "replacement"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("atomicDeploy() symlink error = %v", err)
	}
	assertFileContentAndMode(t, realTarget, "original", 0o600)

	directoryDestination := filepath.Join(root, "directory")
	if err := os.Mkdir(directoryDestination, 0o750); err != nil {
		t.Fatalf("create directory destination: %v", err)
	}
	if _, err := atomicDeploy(directoryDestination, "replacement"); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("atomicDeploy() directory error = %v", err)
	}

	writableDestination := filepath.Join(root, "writable.json")
	if err := os.WriteFile(writableDestination, []byte("original"), 0o600); err != nil {
		t.Fatalf("write group-writable destination: %v", err)
	}
	if err := os.Chmod(writableDestination, 0o660); err != nil {
		t.Fatalf("make destination group-writable: %v", err)
	}
	if _, err := atomicDeploy(writableDestination, "replacement"); err == nil || !strings.Contains(err.Error(), "writable by group or others") {
		t.Fatalf("atomicDeploy() writable-file error = %v", err)
	}
	assertFileContentAndMode(t, writableDestination, "original", 0o660)
}

func TestAtomicDeployAndRollbackPreserveConfigurationMetadata(t *testing.T) {
	t.Parallel()
	destination := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(destination, []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	assignAlternateTestGroup(t, destination)
	want := statFileMetadata(t, destination)

	backup, err := atomicDeploy(destination, "replacement")
	if err != nil {
		t.Fatal(err)
	}
	assertFileContentAndMetadata(t, destination, "replacement", want)
	assertFileContentAndMetadata(t, backup, "original", want)

	if _, err := rollbackDeploy(destination, backup); err != nil {
		t.Fatal(err)
	}
	assertFileContentAndMetadata(t, destination, "original", want)
}

func TestRollbackDeployRestoresBackupAndBackupRetentionIsBounded(t *testing.T) {
	t.Parallel()
	destination := filepath.Join(t.TempDir(), "config.json")
	if _, err := atomicDeploy(destination, "version-0"); err != nil {
		t.Fatalf("initial deploy: %v", err)
	}
	var latestBackup string
	for version := 1; version <= 5; version++ {
		backup, err := atomicDeploy(destination, fmt.Sprintf("version-%d", version))
		if err != nil {
			t.Fatalf("deploy version %d: %v", version, err)
		}
		latestBackup = backup
	}
	backups, err := filepath.Glob(destination + ".bak-*")
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	if len(backups) != 3 {
		t.Fatalf("backup count = %d, want 3 (%v)", len(backups), backups)
	}
	message, err := rollbackDeploy(destination, latestBackup)
	if err != nil || !strings.Contains(message, "restored") {
		t.Fatalf("rollbackDeploy() message=%q error=%v", message, err)
	}
	assertFileContentAndMode(t, destination, "version-4", 0o600)
}

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
	executor := &Executor{DryRun: true, Specs: map[core.Engine]EngineSpec{
		core.EngineMihomo: {Binary: binary, ConfigPath: configPath, Service: "mihomo.service"},
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

func assertFileContentAndMode(t *testing.T, path, want string, wantMode os.FileMode) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(content) != want {
		t.Fatalf("content of %s = %q, want %q", path, content, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != wantMode {
		t.Fatalf("mode of %s = %o, want %o", path, got, wantMode)
	}
}

func statFileMetadata(t *testing.T, path string) fileMetadata {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return metadataFromFileInfo(info)
}

func assertFileContentAndMetadata(t *testing.T, path, wantContent string, want fileMetadata) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(contents) != wantContent {
		t.Fatalf("content of %s = %q, want %q", path, contents, wantContent)
	}
	got := statFileMetadata(t, path)
	if got.mode != want.mode {
		t.Fatalf("mode of %s = %o, want %o", path, got.mode, want.mode)
	}
	if want.ownershipKnown && (!got.ownershipKnown || got.uid != want.uid || got.gid != want.gid) {
		t.Fatalf("ownership of %s = %d:%d (known=%t), want %d:%d", path, got.uid, got.gid, got.ownershipKnown, want.uid, want.gid)
	}
}

func assignAlternateTestGroup(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() != 0 {
		return
	}
	gid := 65534
	if gid == os.Getegid() {
		gid = 1
	}
	if err := os.Chown(path, os.Geteuid(), gid); err != nil {
		t.Fatalf("assign alternate test group: %v", err)
	}
}
