//go:build linux

package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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

func TestMihomoValidationUsesDisposableServiceWritableHome(t *testing.T) {
	requireAgentRoot(t)
	root, err := os.MkdirTemp("/tmp", "qcontrolhub-mihomo-validation-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	configDirectory := filepath.Join(root, "config")
	if err := os.Mkdir(configDirectory, 0o750); err != nil {
		t.Fatal(err)
	}
	const serviceID = 65534
	if err := os.Chown(configDirectory, 0, serviceID); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "mihomo")
	script := `#!/bin/sh
[ "$1" = -t ] && [ "$2" = -d ] && [ -d "$3" ] && [ -w "$3" ] &&
  [ "$4" = -f ] && [ -r "$5" ] || exit 31
printf generated > "$3/generated-by-core"
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	identity := commandIdentity{uid: serviceID, gid: serviceID, groups: []uint32{serviceID}}
	metadata := fileMetadata{mode: 0o640, uid: 0, gid: serviceID, ownershipKnown: true}
	spec := EngineSpec{Binary: binary, ConfigPath: filepath.Join(configDirectory, "config.yaml")}
	if _, err := (&Executor{}).validateSnapshotWithIdentity(
		context.Background(), core.EngineMihomo, spec, "proxies: []\n", &identity, metadata,
	); err != nil {
		t.Fatalf("validate Mihomo snapshot as managed service: %v", err)
	}
	entries, err := os.ReadDir(configDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("Mihomo validation artifacts were not removed: %v", entries)
	}
}

func TestSystemdManagedValidationDoesNotDependOnAgentIdentityCapabilities(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	launcher := filepath.Join(root, "systemd-run")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{
		Services:                 &ServiceManager{kind: ServiceManagerSystemd},
		validationSystemdRunPath: launcher,
	}
	identity := commandIdentity{uid: 65534, gid: 65534, groups: []uint32{65534}}
	output, err := executor.runManagedIdentityCommand(
		context.Background(), root, "XRAY_LOCATION_ASSET="+root, "/usr/bin/true", &identity,
		"run", "-test", "-config", filepath.Join(root, "config.json"),
	)
	if err != nil {
		t.Fatalf("managed validation launcher: %v", err)
	}
	for _, required := range []string{
		"--property=User=65534", "--property=Group=65534", "--property=WorkingDirectory=" + root,
		"--property=ReadWritePaths=" + root, "--setenv=XRAY_LOCATION_ASSET=" + root,
		"/usr/bin/true", "run", "-test", "-config",
	} {
		if !strings.Contains(output, required) {
			t.Errorf("systemd validation invocation missing %q: %s", required, output)
		}
	}
}
