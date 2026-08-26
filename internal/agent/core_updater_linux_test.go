//go:build linux

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceAndRollbackCoreBinary(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	destination := filepath.Join(directory, "xray")
	if err := os.WriteFile(destination, []byte("old"), 0o750); err != nil {
		t.Fatal(err)
	}
	assignAlternateTestGroup(t, destination)
	want := statFileMetadata(t, destination)
	if err := os.WriteFile(filepath.Join(directory, "candidate"), []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	backup, err := replaceCoreBinary(root, "xray", "candidate")
	if err != nil || backup == "" {
		t.Fatalf("replaceCoreBinary() = %q, %v", backup, err)
	}
	assertFileContentAndMetadata(t, destination, "new", want)
	assertFileContentAndMetadata(t, filepath.Join(directory, backup), "old", want)
	if _, err := rollbackCoreBinary(root, "xray", backup); err != nil {
		t.Fatal(err)
	}
	assertFileContentAndMetadata(t, destination, "old", want)
}

func TestReplaceCoreBinaryFirstInstallUsesExecutableDefault(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "candidate"), []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	backup, err := replaceCoreBinary(root, "sing-box", "candidate")
	if err != nil || backup != "" {
		t.Fatalf("replaceCoreBinary() = %q, %v; want no backup", backup, err)
	}
	assertFileContentAndMode(t, filepath.Join(directory, "sing-box"), "new", 0o755)
	if _, err := rollbackCoreBinary(root, "sing-box", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "sing-box")); !os.IsNotExist(err) {
		t.Fatalf("new installation still exists after rollback: %v", err)
	}
}

func TestFirstInstallRollbackStopsRestartingService(t *testing.T) {
	directory := t.TempDir()
	logPath := filepath.Join(directory, "systemctl.log")
	fakeSystemctl := filepath.Join(directory, "systemctl")
	script := "#!/bin/sh\n" +
		"/usr/bin/printf '%s %s\\n' \"$1\" \"$2\" >> " + logPath + "\n" +
		"if [ \"$1\" = is-active ]; then /usr/bin/printf '%s\\n' inactive; exit 3; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(fakeSystemctl, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	originalSystemctlPath := systemctlPath
	systemctlPath = fakeSystemctl
	t.Cleanup(func() { systemctlPath = originalSystemctlPath })

	output, err := stopServiceAfterFirstInstallRollback("qagent-sing-box.service")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "service status: inactive") {
		t.Fatalf("stop output = %q", output)
	}
	commands, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(commands) != "stop qagent-sing-box.service\nis-active qagent-sing-box.service\nis-active qagent-sing-box.service\n" {
		t.Fatalf("systemctl commands = %q", commands)
	}
}
