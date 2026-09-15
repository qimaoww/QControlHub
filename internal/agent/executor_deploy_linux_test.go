//go:build linux

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
