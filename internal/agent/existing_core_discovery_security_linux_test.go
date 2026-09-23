//go:build linux

package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestExistingCoreDiscoveryRejectsNonRootEtcSingBoxBinary(t *testing.T) {
	requireAgentRoot(t)
	if _, err := user.LookupId("65534"); err != nil {
		t.Skipf("UID 65534 is not assigned on this host: %v", err)
	}
	fixture := newExistingCoreDiscoveryFixture(t)
	serviceBinary := fixture.useDirectSingBoxBinary(t, 65534)
	allowFixtureOrphanOwnerPath(t, serviceBinary)
	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, nil,
	)
	if err != nil {
		t.Fatalf("reject non-root /etc-style sing-box binary: %v", err)
	}
	reason := issues[core.EngineSingBox]
	if len(specs) != 0 || !strings.Contains(reason, "root 所有") || strings.Contains(reason, "不在受支持的标准路径") {
		t.Fatalf("non-root /etc-style discovery = specs %+v reason %q", specs, reason)
	}
	state, err := loadExistingCoreDiscoveryState(fixture.discoveryStatePath)
	if err != nil || len(state.Specs) != 0 || state.Issues[core.EngineSingBox] != reason {
		t.Fatalf("persisted non-root discovery state = %+v, %v", state, err)
	}
	executor := &Executor{
		Specs: fixture.managedSpecs,
		ExistingDiscoveryIssues: map[core.Engine]string{
			core.EngineSingBox: reason,
		},
	}
	runtime := executor.Runtime(context.Background())[core.EngineSingBox]
	if runtime.ExistingConfigAvailable || runtime.ExistingConfigUnsupportedReason != reason {
		t.Fatalf("non-root /etc-style runtime = %+v", runtime)
	}
	if _, err := executor.Execute(context.Background(), core.Task{Action: core.ActionReadConfig, Engine: core.EngineSingBox}); err == nil || !strings.Contains(err.Error(), "core tasks are disabled") {
		t.Fatalf("non-root /etc-style read-config error = %v", err)
	}
	if info, err := os.Lstat(serviceBinary); err != nil {
		t.Fatal(err)
	} else if uid, known := fileOwnerUID(info); !known || uid != 65534 {
		t.Fatalf("non-root executable fixture owner = %d, known=%v", uid, known)
	}
}

func TestExistingCoreDiscoveryAcceptsInactiveOrphanOwnedInstallerBinary(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreDiscoveryFixture(t)
	serviceBinary := fixture.useDirectSingBoxBinary(t, -1)
	allowFixtureOrphanOwnerPath(t, serviceBinary)
	assignInactiveOrphanOwner(t, serviceBinary)
	if err := os.Chmod(serviceBinary, 0o744); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(serviceBinary+".control.json", []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, nil,
	)
	if err != nil {
		t.Fatalf("discover inactive orphan-owned installer binary: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("inactive orphan-owned discovery issues = %+v", issues)
	}
	assertDiscoveredSingBoxSpec(t, specs[core.EngineSingBox], serviceBinary, serviceBinary, fixture.configPath, fixture.configDirectory, "")
	if _, err := os.Stat(serviceBinary + ".invocations"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphan-owned source binary was invoked directly: %v", err)
	}
}

func TestExistingCoreDiscoveryRejectsActiveOrphanOwnedInstallerBinary(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreDiscoveryFixture(t)
	serviceBinary := fixture.useDirectSingBoxBinary(t, -1)
	allowFixtureOrphanOwnerPath(t, serviceBinary)
	orphanUID := assignInactiveOrphanOwner(t, serviceBinary)

	process := exec.Command("/bin/sleep", "30")
	process.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
		Uid: uint32(orphanUID), Gid: uint32(orphanUID),
	}}
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = process.Process.Kill()
		_ = process.Wait()
	})

	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, nil,
	)
	if err != nil {
		t.Fatalf("reject active orphan-owned installer binary: %v", err)
	}
	reason := issues[core.EngineSingBox]
	if len(specs) != 0 || !strings.Contains(reason, "root 所有") {
		t.Fatalf("active orphan-owned discovery = specs %+v reason %q", specs, reason)
	}
}

func TestExistingCoreDiscoveryRejectsSymlinkedEtcSingBoxBinary(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	serviceBinary := fixture.useDirectSingBoxBinary(t, -1)
	realBinary := serviceBinary + ".real"
	if err := os.Rename(serviceBinary, realBinary); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realBinary, serviceBinary); err != nil {
		t.Fatal(err)
	}
	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, nil,
	)
	if err != nil {
		t.Fatalf("reject symlinked /etc-style sing-box binary: %v", err)
	}
	reason := issues[core.EngineSingBox]
	if len(specs) != 0 || !strings.Contains(reason, "非符号链接") {
		t.Fatalf("symlinked /etc-style discovery = specs %+v reason %q", specs, reason)
	}
}

func TestExistingCoreDiscoveryRejectsEnvironmentFileDropIn(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	fixture.writeStatus(t, "qagent-sing-box.service", "active")
	dropInPath := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service.d", "50-unexpected-env.conf")
	if err := os.MkdirAll(filepath.Dir(dropInPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dropInPath, []byte("[Service]\nEnvironmentFile=/etc/qagent/unexpected.env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// daemon-reload applies the drop-in, so systemd's effective EnvironmentFiles
	// property becomes non-empty even though the singular EnvironmentFile property
	// does not exist in systemd's D-Bus interface.
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.DropInPaths"), []byte(dropInPath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.EnvironmentFiles"), []byte("/etc/qagent/unexpected.env\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 0 || !strings.Contains(issues[core.EngineSingBox], "安全 unit") {
		t.Fatalf("EnvironmentFile= drop-in discovery = specs %+v issues %+v", specs, issues)
	}
}
