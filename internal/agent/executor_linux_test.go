//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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

func TestManagedConfigurationAccessRepairsExistingAndNewFiles(t *testing.T) {
	requireAgentRoot(t)
	root, err := os.MkdirTemp("/tmp", "qcontrolhub-managed-config-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	managedRoot := filepath.Join(root, "qagent")
	directory := filepath.Join(managedRoot, "xray")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "config.json")
	if err := os.WriteFile(configPath, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	const serviceGID = 65534
	if err := os.Chown(managedRoot, 0, serviceGID); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(managedRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	rootMetadata := statFileMetadata(t, managedRoot)

	metadata, err := prepareManagedConfigurationAccessWithGID(managedRoot, configPath, serviceGID)
	if err != nil {
		t.Fatal(err)
	}
	if got := statFileMetadata(t, managedRoot); got != rootMetadata {
		t.Fatalf("managed root metadata changed from %+v to %+v", rootMetadata, got)
	}
	if got := statFileMetadata(t, directory); got.uid != 0 || got.gid != serviceGID || got.mode != 0o750 {
		t.Fatalf("managed engine directory metadata = %+v", got)
	}
	got := statFileMetadata(t, configPath)
	if got.uid != 0 || got.gid != serviceGID || got.mode != 0o640 {
		t.Fatalf("existing managed configuration metadata = %+v", got)
	}

	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	metadata, err = prepareManagedConfigurationAccessWithGID(managedRoot, configPath, serviceGID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := atomicDeployWithDefaultMetadata(configPath, "new", metadata); err != nil {
		t.Fatal(err)
	}
	got = statFileMetadata(t, configPath)
	if got.uid != 0 || got.gid != serviceGID || got.mode != 0o640 {
		t.Fatalf("new managed configuration metadata = %+v", got)
	}
	command := exec.Command("/usr/bin/test", "-r", configPath)
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: serviceGID, Gid: serviceGID}}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("managed service group cannot read new configuration: %v: %s", err, output)
	}
}

func TestManagedConfigurationAccessRejectsUnsafePaths(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	managedRoot := filepath.Join(root, "qagent")
	directory := filepath.Join(managedRoot, "xray")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareManagedConfigurationAccessWithGID(managedRoot, filepath.Join(root, "outside.json"), 65534); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("outside managed configuration error = %v", err)
	}
	if err := os.Chmod(directory, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareManagedConfigurationAccessWithGID(managedRoot, filepath.Join(directory, "config.json"), 65534); err == nil || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("writable managed directory error = %v", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "config.json")
	if err := os.Symlink(target, configPath); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareManagedConfigurationAccessWithGID(managedRoot, configPath, 65534); err == nil || !strings.Contains(err.Error(), "protected regular file") {
		t.Fatalf("symlinked managed configuration error = %v", err)
	}
	assertFileContentAndMode(t, target, "unchanged", 0o600)
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
	content := `{"log":{"access":"/var/log/xray/access.log"},"inbounds":[],"outbounds":[]}`
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

func TestExistingSingBoxSnapshotMergesExactConfigDirectory(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	validationDirectory := filepath.Join(root, "managed")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(validationDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(root, "config.json")
	if err := os.WriteFile(primary, []byte(`{"log":{"level":"info","timestamp":true,"output":"/var/log/sing-box/box.log"},"inbounds":[{"tag":"primary"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "10-outbounds.json"), []byte(`{"outbounds":[{"tag":"direct","type":"direct"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "20-log.json"), []byte(`{"log":{"level":"debug"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "sing-box")
	if err := os.WriteFile(binary, existingDiscoveryCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := EngineSpec{Binary: binary, ConfigPath: primary, ConfigDirectory: configDirectory, Service: "sing-box.service"}
	managed := EngineSpec{Binary: binary, ConfigPath: filepath.Join(validationDirectory, "config.json"), Service: "qagent-sing-box.service"}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineSingBox: managed}, ExistingSpecs: map[core.Engine]EngineSpec{core.EngineSingBox: existing}}
	content, err := executor.readExistingConfig(context.Background(), core.EngineSingBox, managed, existing)
	if err != nil {
		t.Fatalf("read merged sing-box snapshot: %v", err)
	}
	for _, required := range []string{`"tag": "primary"`, `"tag": "direct"`, `"level": "debug"`} {
		if !strings.Contains(content, required) {
			t.Errorf("merged snapshot is missing %s: %s", required, content)
		}
	}
	if !strings.Contains(content, `"output": "stdout"`) || !strings.Contains(content, `"timestamp": true`) {
		t.Fatalf("unsafe existing sing-box file log was not normalized to managed console output: %s", content)
	}
	if strings.Contains(content, `"level": "info"`) {
		t.Fatalf("later path unexpectedly replaced the earlier sorted sing-box value: %s", content)
	}

	if err := os.Symlink(filepath.Join(configDirectory, "10-outbounds.json"), filepath.Join(configDirectory, "30-linked.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.readExistingConfig(context.Background(), core.EngineSingBox, managed, existing); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlinked config-directory entry error = %v", err)
	}
	if err := os.Remove(filepath.Join(configDirectory, "30-linked.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configDirectory, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.readExistingConfig(context.Background(), core.EngineSingBox, managed, existing); err == nil || !strings.Contains(err.Error(), "writable by group") {
		t.Fatalf("group-writable config-directory error = %v", err)
	}
	if err := os.Chmod(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	realDirectory := configDirectory + "-real"
	if err := os.Rename(configDirectory, realDirectory); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDirectory, configDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.readExistingConfig(context.Background(), core.EngineSingBox, managed, existing); err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("symlinked config-directory error = %v", err)
	}
}

func TestDecodeExtendedJSONPreservesCommentShapedStrings(t *testing.T) {
	content := `{
		// leading comment
		"url": "http://example.com#frag", # trailing hash comment
		"path": "a//b/*c*/d", /* block comment */
		"escaped": "a\"b\\c"
	}`
	decoded, err := decodeExtendedJSON(content)
	if err != nil {
		t.Fatalf("decodeExtendedJSON() error = %v", err)
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		t.Fatalf("decoded value = %T, want object", decoded)
	}
	if object["url"] != "http://example.com#frag" {
		t.Fatalf("url was corrupted: %v", object["url"])
	}
	if object["path"] != "a//b/*c*/d" {
		t.Fatalf("comment-shaped string was corrupted: %v", object["path"])
	}
	if object["escaped"] != "a\"b\\c" {
		t.Fatalf("escaped string was corrupted: %v", object["escaped"])
	}
}

func TestExistingSingBoxExtendedJSONConfigDirectorySnapshot(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(root, "config.json")
	if err := os.WriteFile(primary, []byte(`{"log": { "level": "info" } // primary
, "inbounds": [{ "tag": "primary" }]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "10-outbounds.json"), []byte(`{
  // outbound fragment
  "outbounds": [{ "tag": "direct", "url": "http://example.com#frag" }]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "20-log.json"), []byte(`{"log": { "level": "debug" } /* override */}`), 0o600); err != nil {
		t.Fatal(err)
	}
	existing := EngineSpec{ConfigPath: primary, ConfigDirectory: configDirectory}
	content, _, err := readExistingConfigurationSources(existing)
	if err != nil {
		t.Fatalf("readExistingConfigurationSources() error = %v", err)
	}
	for _, required := range []string{`"tag": "primary"`, `"tag": "direct"`, `"level": "debug"`, `"http://example.com#frag"`} {
		if !strings.Contains(content, required) {
			t.Errorf("extended-JSON snapshot is missing %s: %s", required, content)
		}
	}
	if strings.Contains(content, `"level": "info"`) {
		t.Fatalf("later sorted source unexpectedly replaced the win-by-destination value: %s", content)
	}
	// The comment-shaped URL text must remain inside the string, undecorated.
	if strings.Contains(content, "example.com#frag ") || strings.Contains(content, "// outbound fragment") {
		t.Fatalf("comment stripping altered string content or left comments in the snapshot: %s", content)
	}
}

func TestReadExistingXrayConfigurationUsesSourceDump(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	if err := os.Mkdir(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(root, "config.json")
	if err := os.WriteFile(primary, []byte(`{"log":{"loglevel":"info"},"inbounds":[],"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "20-log.json"), []byte(`{"log":{"loglevel":"debug"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dumped := `{"log":{"loglevel":"error","access":"/var/log/xray/access.log","error":"/var/log/xray/error.log"},"inbounds":[],"outbounds":[]}`
	binary := filepath.Join(root, "xray")
	if err := os.WriteFile(binary, existingDiscoveryCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "xray-dump.json"), []byte(dumped), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary+".control.json", []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	managed := EngineSpec{ConfigPath: filepath.Join(root, "managed", "config.json")}
	existing := EngineSpec{Binary: binary, ConfigPath: primary, ConfigDirectory: configDirectory}
	content, err := (&Executor{}).readExistingConfig(context.Background(), core.EngineXray, managed, existing)
	if err != nil {
		t.Fatalf("readExistingConfig() error = %v", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal([]byte(content), &normalized); err != nil {
		t.Fatal(err)
	}
	logging := normalized["log"].(map[string]any)
	if logging["access"] != "" || logging["error"] != "" || logging["loglevel"] != "error" {
		t.Fatalf("normalized Xray log policy = %+v", logging)
	}
	if _, err := os.Stat(binary + ".invocations"); err != nil {
		t.Fatalf("ordinary root-owned source binary was not invoked directly: %v", err)
	}
}

func TestReadExistingXrayConfigurationStagesRootOwnedInstallerBinary(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	if err := os.Mkdir(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(root, "config.json")
	if err := os.WriteFile(primary, []byte(`{"log":{"loglevel":"info"},"inbounds":[],"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dumped := `{"log":{"loglevel":"error"},"inbounds":[],"outbounds":[]}`
	binary := filepath.Join(root, "xray")
	if err := os.WriteFile(binary, existingDiscoveryCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "xray-dump.json"), []byte(dumped), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary+".control.json", []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	allowFixtureOrphanOwnerPath(t, binary)
	stateDirectory := filepath.Join(root, "state")
	if err := os.Mkdir(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	managed := EngineSpec{ConfigPath: filepath.Join(root, "managed", "config.json")}
	existing := EngineSpec{Binary: binary, ConfigPath: primary, ConfigDirectory: configDirectory}
	executor := &Executor{MigrationMarkerPrefix: filepath.Join(stateDirectory, "agent-state.json.core-migration")}
	content, err := executor.readExistingConfig(context.Background(), core.EngineXray, managed, existing)
	if err != nil {
		t.Fatalf("readExistingConfig() error = %v", err)
	}
	if !strings.Contains(content, `"loglevel":"error"`) {
		t.Fatalf("root-owned installer Xray dump = %s", content)
	}
	if _, err := os.Stat(binary + ".invocations"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("root-owned installer source binary was invoked directly: %v", err)
	}
	if staged, err := filepath.Glob(filepath.Join(stateDirectory, ".qcontrolhub-core-*.tmp")); err != nil || len(staged) != 0 {
		t.Fatalf("protected invocation copies after cleanup = %v, %v", staged, err)
	}
}

func TestReadExistingXrayConfigurationStagesOrphanOwnedInstallerBinary(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	if err := os.Mkdir(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(root, "config.json")
	if err := os.WriteFile(primary, []byte(`{"log":{"loglevel":"info"},"inbounds":[],"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "20-log.json"), []byte(`{"log":{"loglevel":"debug"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dumped := `{"log":{"loglevel":"error"},"inbounds":[],"outbounds":[]}`
	binary := filepath.Join(root, "xray")
	if err := os.WriteFile(binary, existingDiscoveryCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "xray-dump.json"), []byte(dumped), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary+".control.json", []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	allowFixtureOrphanOwnerPath(t, binary)
	assignInactiveOrphanOwner(t, binary)
	if err := os.Chmod(binary, 0o744); err != nil {
		t.Fatal(err)
	}
	stateDirectory := filepath.Join(root, "state")
	if err := os.Mkdir(stateDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	managed := EngineSpec{ConfigPath: filepath.Join(root, "managed", "config.json")}
	existing := EngineSpec{Binary: binary, ConfigPath: primary, ConfigDirectory: configDirectory}
	executor := &Executor{MigrationMarkerPrefix: filepath.Join(stateDirectory, "agent-state.json.core-migration")}
	content, err := executor.readExistingConfig(context.Background(), core.EngineXray, managed, existing)
	if err != nil {
		t.Fatalf("readExistingConfig() error = %v", err)
	}
	if !strings.Contains(content, `"loglevel":"error"`) {
		t.Fatalf("orphan-owned Xray dump = %s", content)
	}
	if _, err := os.Stat(binary + ".invocations"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan-owned source binary was invoked directly: %v", err)
	}
	if staged, err := filepath.Glob(filepath.Join(stateDirectory, ".qcontrolhub-core-*.tmp")); err != nil || len(staged) != 0 {
		t.Fatalf("protected invocation copies after cleanup = %v, %v", staged, err)
	}
}

func TestReadExistingXraySourceDigestTracksEverySupportedFormat(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	if err := os.Mkdir(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"10-base.json":   `{"log":{"loglevel":"info"}}`,
		"20-extra.jsonc": `{"log":{"loglevel":"warning"}}`,
		"30-extra.toml":  `[log]\nloglevel = "error"`,
		"40-extra.yaml":  `log: {loglevel: debug}`,
		"50-extra.yml":   `log: {loglevel: none}`,
		"notes.txt":      "ignored by Xray",
	} {
		if err := os.WriteFile(filepath.Join(configDirectory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec := EngineSpec{ConfigDirectory: configDirectory}
	digest, err := readExistingXraySourceDigest(spec)
	if err != nil {
		t.Fatalf("readExistingXraySourceDigest() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "40-extra.yaml"), []byte(`log: {loglevel: error}`), 0o600); err != nil {
		t.Fatal(err)
	}
	changedDigest, err := readExistingXraySourceDigest(spec)
	if err != nil {
		t.Fatalf("readExistingXraySourceDigest() after YAML change error = %v", err)
	}
	if changedDigest == digest {
		t.Fatal("source digest did not change after an Xray YAML source changed")
	}
}

func TestExistingSingBoxExtendedJSONRejectsMalformedSources(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("config.json", `{"inbounds": []}`)
	for _, source := range []string{"10-broken.json", "20-trailing.json"} {
		if err := os.WriteFile(filepath.Join(configDirectory, source), []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	existing := EngineSpec{ConfigPath: filepath.Join(root, "config.json"), ConfigDirectory: configDirectory}

	if err := os.WriteFile(filepath.Join(configDirectory, "10-broken.json"), []byte(`{"a":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readExistingConfigurationSources(existing); err == nil || !strings.Contains(err.Error(), "decode configuration source") {
		t.Fatalf("invalid JSON source error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(configDirectory, "10-broken.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "20-trailing.json"), []byte(`{"b":1} garbage`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readExistingConfigurationSources(existing); err == nil || !strings.Contains(err.Error(), "trailing data") {
		t.Fatalf("non-comment trailing source error = %v", err)
	}
}

func TestExtendedJSONClosedBlockCommentParses(t *testing.T) {
	decoded, err := decodeExtendedJSON(`{"a": 1 /* closed */ , "b": [1 /* inline */, 2]}`)
	if err != nil {
		t.Fatalf("decodeExtendedJSON() with closed block comment error = %v", err)
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		t.Fatalf("decoded value = %T, want object", decoded)
	}
	b, ok := object["b"].([]any)
	if !ok || len(b) != 2 || b[0] != json.Number("1") || b[1] != json.Number("2") {
		t.Fatalf("closed block comment structure was corrupted: %v", object["b"])
	}
}

func TestExtendedJSONRejectsUnterminatedBlockComments(t *testing.T) {
	for _, content := range []string{
		`{"a":1} /* unterminated`,
		`{"a": /* unterminated`,
		`{"a": 1, /* unterminated`,
	} {
		if _, err := decodeExtendedJSON(content); err == nil {
			t.Errorf("decodeExtendedJSON(%q) unexpectedly accepted an unterminated block comment", content)
		} else if !strings.Contains(err.Error(), "unexpected end of JSON comment") {
			t.Errorf("decodeExtendedJSON(%q) error = %v, want unexpected end of JSON comment", content, err)
		}
	}

	root := t.TempDir()
	configDirectory := filepath.Join(root, "conf.d")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{"inbounds": []}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "10-fragment.json"), []byte(`{"outbounds": []} /* unterminated`), 0o600); err != nil {
		t.Fatal(err)
	}
	existing := EngineSpec{ConfigPath: filepath.Join(root, "config.json"), ConfigDirectory: configDirectory}
	if _, _, err := readExistingConfigurationSources(existing); err == nil || !strings.Contains(err.Error(), "unexpected end of JSON comment") {
		t.Fatalf("directory fragment with unterminated block comment error = %v", err)
	}
}

func TestExistingServiceExecutableAcceptsOnlyFixedForwarder(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	forwarder := filepath.Join(root, "sing-box-forwarder")
	serviceBinary := filepath.Join(root, "sing-box")
	if err := os.WriteFile(forwarder, []byte("#!/bin/sh\nexec /usr/bin/true \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(forwarder, serviceBinary); err != nil {
		t.Fatal(err)
	}
	spec := EngineSpec{Binary: "/usr/bin/true", ServiceBinary: serviceBinary}
	if err := validateExistingServiceExecutable(spec); err != nil {
		t.Fatalf("fixed exec forwarder rejected: %v", err)
	}
	secondLink := filepath.Join(root, "sing-box-second-link")
	if err := os.Symlink(serviceBinary, secondLink); err != nil {
		t.Fatal(err)
	}
	multiHop := spec
	multiHop.ServiceBinary = secondLink
	if err := validateExistingServiceExecutable(multiHop); err == nil || !strings.Contains(err.Error(), "at most one symlink") {
		t.Fatalf("multi-hop service executable error = %v", err)
	}
	if err := os.WriteFile(forwarder, []byte("#!/bin/sh\necho unsafe\nexec /usr/bin/true \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateExistingServiceExecutable(spec); err == nil || !strings.Contains(err.Error(), "fixed exec forwarder") {
		t.Fatalf("arbitrary wrapper error = %v", err)
	}
	realScript := filepath.Join(root, "not-a-core")
	if err := os.WriteFile(realScript, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(forwarder, []byte("#!/bin/sh\nexec "+realScript+" \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	spec.Binary = realScript
	if err := validateExistingServiceExecutable(spec); err == nil || !strings.Contains(err.Error(), "must not be a script") {
		t.Fatalf("script target error = %v", err)
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
