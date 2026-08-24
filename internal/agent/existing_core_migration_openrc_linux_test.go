//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func openRCStatLine(pid int, comm string, ppid int, startTime string) string {
	return fmt.Sprintf("%d (%s) S %d %s%s", pid, comm, ppid, strings.Repeat("0 ", 17), startTime)
}

func writeOpenRCProcIdentity(t *testing.T, procRoot string, pid int, comm string, ppid int, startTime string, executable string, argv []string) {
	t.Helper()
	processRoot := filepath.Join(procRoot, fmt.Sprintf("%d", pid))
	if err := os.MkdirAll(processRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(processRoot, "stat"), []byte(openRCStatLine(pid, comm, ppid, startTime)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, filepath.Join(processRoot, "exe")); err != nil {
		t.Fatal(err)
	}
	cmdline := strings.Join(argv, "\x00") + "\x00"
	if err := os.WriteFile(filepath.Join(processRoot, "cmdline"), []byte(cmdline), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeOpenRCServiceMetadata(t *testing.T, stateRoot string, supervisorRoot string, service string, childPID int, supervisorPID int, pidfileValue string) {
	t.Helper()
	options := filepath.Join(stateRoot, "options", service)
	if err := os.MkdirAll(options, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options, "child_pid"), []byte(fmt.Sprintf("%d\n", childPID)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options, "pidfile"), []byte(pidfileValue+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	supervisorPIDPath := filepath.Join(supervisorRoot, "supervise-"+service+".pid")
	if err := os.MkdirAll(supervisorRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(supervisorPIDPath, []byte(fmt.Sprintf("%d\n", supervisorPID)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func useFakeOpenRCTree(t *testing.T, procRoot string, stateRoot string, supervisorRoot string, supervisorExecutable string) {
	t.Helper()
	previousProc := openRCProcRoot
	previousState := openRCStateRoot
	previousSupervisorRoot := openRCSupervisorRoot
	previousSupervisorExecutable := openRCSupervisorExecutable
	openRCProcRoot = procRoot
	openRCStateRoot = stateRoot
	openRCSupervisorRoot = supervisorRoot
	openRCSupervisorExecutable = supervisorExecutable
	t.Cleanup(func() {
		openRCProcRoot = previousProc
		openRCStateRoot = previousState
		openRCSupervisorRoot = previousSupervisorRoot
		openRCSupervisorExecutable = previousSupervisorExecutable
	})
}

func TestOpenRCBoundServiceProcessValidatesOwnedSupervisedChild(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	realExecutable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	procRoot := t.TempDir()
	stateRoot := t.TempDir()
	supervisorRoot := t.TempDir()
	useFakeOpenRCTree(t, procRoot, stateRoot, supervisorRoot, realExecutable)

	const service = "xray"
	const supervisorPID = 100
	const childPID = 200
	const supervisorStart = "4000"
	const childStart = "5000"
	writeOpenRCProcIdentity(t, procRoot, supervisorPID, "supervise-daemon", 1, supervisorStart, realExecutable,
		[]string{"supervise-daemon", service, "--start", "/bin/sleep", "--", "100"})
	writeOpenRCProcIdentity(t, procRoot, childPID, "xray", supervisorPID, childStart, realExecutable,
		[]string{realExecutable, "run", "-config", "/etc/xray/config.json"})
	writeOpenRCServiceMetadata(t, stateRoot, supervisorRoot, service, childPID, supervisorPID, "/run/supervise-"+service+".pid")

	identity, err := boundOpenRCServiceProcess(context.Background(), service)
	if err != nil {
		t.Fatalf("boundOpenRCServiceProcess() error: %v", err)
	}
	if identity.Supervisor.PID != supervisorPID || identity.Child.PID != childPID {
		t.Fatalf("identity = supervisor %d child %d; want %d/%d", identity.Supervisor.PID, identity.Child.PID, supervisorPID, childPID)
	}
	if identity.Child.ParentPID != supervisorPID {
		t.Fatalf("child parent = %d; want %d", identity.Child.ParentPID, supervisorPID)
	}

	spec, err := discoverOpenRCExistingSpec(context.Background(), core.EngineXray, service, existingDiscoveryCandidateSet{
		executables: []string{realExecutable},
		configs:     []string{"/etc/xray/config.json"},
	})
	if err != nil {
		t.Fatalf("discoverOpenRCExistingSpec() error: %v", err)
	}
	if spec.Service != service || spec.Binary != realExecutable || spec.ConfigPath != "/etc/xray/config.json" {
		t.Fatalf("discovered spec = %+v", spec)
	}
}

func TestOpenRCBoundServiceProcessFailsClosedWithoutSupervisedMetadata(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	realExecutable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	useFakeOpenRCTree(t, t.TempDir(), t.TempDir(), t.TempDir(), realExecutable)
	if _, err := boundOpenRCServiceProcess(context.Background(), "xray"); !errors.Is(err, errOpenRCServiceProcessUnbound) {
		t.Fatalf("unbound error = %v; want errOpenRCServiceProcessUnbound", err)
	}
}

func TestOpenRCVerifyRejectsDecoyCoreProcess(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	realExecutable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	procRoot := t.TempDir()
	stateRoot := t.TempDir()
	supervisorRoot := t.TempDir()
	useFakeOpenRCTree(t, procRoot, stateRoot, supervisorRoot, realExecutable)

	const service = "xray"
	const supervisorPID = 100
	const childPID = 200
	const decoyPID = 300
	writeOpenRCProcIdentity(t, procRoot, supervisorPID, "supervise-daemon", 1, "4000", realExecutable,
		[]string{"supervise-daemon", service, "--start", "/bin/sleep", "--", "100"})
	writeOpenRCProcIdentity(t, procRoot, childPID, "xray", supervisorPID, "5000", realExecutable,
		[]string{realExecutable, "run", "-config", "/etc/xray/config.json"})
	writeOpenRCProcIdentity(t, procRoot, decoyPID, "xray", 1, "6000", realExecutable,
		[]string{realExecutable, "run", "-config", "/etc/xray/config.json"})
	writeOpenRCServiceMetadata(t, stateRoot, supervisorRoot, service, childPID, supervisorPID, "/run/supervise-"+service+".pid")

	existing := EngineSpec{Binary: realExecutable, ConfigPath: "/etc/xray/config.json", Service: service}
	if _, err := verifyOpenRCExistingServiceProcess(context.Background(), core.EngineXray, existing); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("decoy process accepted: %v", err)
	}
}

func TestOpenRCVerifyRejectsChildExecutableDrift(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	realExecutable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	procRoot := t.TempDir()
	stateRoot := t.TempDir()
	supervisorRoot := t.TempDir()
	useFakeOpenRCTree(t, procRoot, stateRoot, supervisorRoot, realExecutable)

	const service = "xray"
	const supervisorPID = 100
	const childPID = 200
	writeOpenRCProcIdentity(t, procRoot, supervisorPID, "supervise-daemon", 1, "4000", realExecutable,
		[]string{"supervise-daemon", service, "--start", "/bin/sleep", "--", "100"})
	writeOpenRCProcIdentity(t, procRoot, childPID, "xray", supervisorPID, "5000", realExecutable,
		[]string{realExecutable, "run", "-config", "/etc/xray/config.json"})
	writeOpenRCServiceMetadata(t, stateRoot, supervisorRoot, service, childPID, supervisorPID, "/run/supervise-"+service+".pid")

	drifted := EngineSpec{Binary: "/usr/bin/other", ConfigPath: "/etc/xray/config.json", Service: service}
	if _, err := verifyOpenRCExistingServiceProcess(context.Background(), core.EngineXray, drifted); err == nil ||
		!strings.Contains(err.Error(), "executable or arguments") {
		t.Fatalf("drifted executable accepted: %v", err)
	}
}

func TestOpenRCWaitForProcessExitFailsWhileBoundChildAlive(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	realExecutable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	procRoot := t.TempDir()
	stateRoot := t.TempDir()
	supervisorRoot := t.TempDir()
	useFakeOpenRCTree(t, procRoot, stateRoot, supervisorRoot, realExecutable)

	const service = "xray"
	const supervisorPID = 100
	const childPID = 200
	writeOpenRCProcIdentity(t, procRoot, supervisorPID, "supervise-daemon", 1, "4000", realExecutable,
		[]string{"supervise-daemon", service, "--start", "/bin/sleep", "--", "100"})
	writeOpenRCProcIdentity(t, procRoot, childPID, "xray", supervisorPID, "5000", realExecutable,
		[]string{realExecutable, "run", "-config", "/etc/xray/config.json"})
	writeOpenRCServiceMetadata(t, stateRoot, supervisorRoot, service, childPID, supervisorPID, "/run/supervise-"+service+".pid")

	identity, err := boundOpenRCServiceProcess(context.Background(), service)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	if err := waitForOpenRCServiceProcessExit(ctx, identity); err == nil {
		t.Fatal("wait accepted a process that is still bound and alive")
	}
}

func TestOpenRCWaitForProcessExitSucceedsAfterStop(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	realExecutable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	procRoot := t.TempDir()
	stateRoot := t.TempDir()
	supervisorRoot := t.TempDir()
	useFakeOpenRCTree(t, procRoot, stateRoot, supervisorRoot, realExecutable)

	const service = "xray"
	const supervisorPID = 100
	const childPID = 200
	writeOpenRCProcIdentity(t, procRoot, supervisorPID, "supervise-daemon", 1, "4000", realExecutable,
		[]string{"supervise-daemon", service, "--start", "/bin/sleep", "--", "100"})
	writeOpenRCProcIdentity(t, procRoot, childPID, "xray", supervisorPID, "5000", realExecutable,
		[]string{realExecutable, "run", "-config", "/etc/xray/config.json"})
	writeOpenRCServiceMetadata(t, stateRoot, supervisorRoot, service, childPID, supervisorPID, "/run/supervise-"+service+".pid")

	identity, err := boundOpenRCServiceProcess(context.Background(), service)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an OpenRC stop that removes the supervised metadata and lets the
	// supervisor and child exit. Removing the /proc entries makes them dead.
	if err := os.Remove(filepath.Join(procRoot, fmt.Sprintf("%d", supervisorPID), "stat")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(procRoot, fmt.Sprintf("%d", childPID), "stat")); err != nil {
		t.Fatal(err)
	}
	supervisorPIDPath := filepath.Join(supervisorRoot, "supervise-"+service+".pid")
	if err := os.Remove(supervisorPIDPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(stateRoot, "options", service, "child_pid")); err != nil {
		t.Fatal(err)
	}
	if err := waitForOpenRCServiceProcessExit(context.Background(), identity); err != nil {
		t.Fatalf("waitForOpenRCServiceProcessExit() error after stop: %v", err)
	}
}

func TestOpenRCHelperExecutableDoesNotFallBackAcrossHelpers(t *testing.T) {
	rcService := openRCHelperExecutable("rc-service", rcServicePath)
	if rcService == rcUpdatePath {
		t.Fatalf("rc-service helper resolved to rc-update: %q", rcService)
	}
	if rcService != rcServicePath && rcService != "/usr/sbin/rc-service" {
		t.Fatalf("unexpected rc-service helper: %q", rcService)
	}
	rcUpdate := openRCHelperExecutable("rc-update", rcUpdatePath)
	if rcUpdate == rcServicePath {
		t.Fatalf("rc-update helper fell back to rc-service: %q", rcUpdate)
	}
	if rcUpdate != rcUpdatePath && rcUpdate != "/usr/sbin/rc-update" {
		t.Fatalf("unexpected rc-update helper: %q", rcUpdate)
	}
}

func useOpenRCTestRunlevels(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	runlevels := filepath.Join(root, "runlevels")
	initRoot := filepath.Join(root, "init.d")
	for _, directory := range []string{filepath.Join(runlevels, "default"), filepath.Join(runlevels, "boot"), initRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	previousRunlevelsRoot := openRCRunlevelsRoot
	previousInitRoot := openRCInitRoot
	openRCRunlevelsRoot = runlevels
	openRCInitRoot = initRoot
	t.Cleanup(func() {
		openRCRunlevelsRoot = previousRunlevelsRoot
		openRCInitRoot = previousInitRoot
	})
	return runlevels, initRoot
}

func writeOpenRCTestService(t *testing.T, initRoot, service string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(initRoot, service), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeOpenRCTestActiveState(t *testing.T, stateRoot, service, state string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(stateRoot, service+".active"), []byte(state+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newOpenRCTestManager(t *testing.T, stateRoot, runlevels, initRoot string) (*ServiceManager, string) {
	t.Helper()
	rcService := filepath.Join(filepath.Dir(stateRoot), "rc-service")
	serviceScript := fmt.Sprintf(`#!/bin/sh
set -eu
state=%q
service=$1
action=$2
printf 'rc-service %%s %%s\n' "$service" "$action" >> "$state/commands.log"
active_file="$state/$service.active"
case "$action" in
  status)
    value=$(cat "$active_file")
    if [ "$value" = active ]; then
      printf 'started\n'
      exit 0
    fi
    printf 'stopped\n'
    exit 3
    ;;
  stop)
    if [ "$service" = qagent-sing-box ] && [ -f "$state/fail-managed-stop-once" ]; then
      rm -f "$state/fail-managed-stop-once"
      printf 'stop timed out\n'
      exit 1
    fi
    printf 'inactive\n' > "$active_file"
    ;;
  start|restart)
    printf 'active\n' > "$active_file"
    ;;
  *) exit 1 ;;
esac
`, stateRoot)
	if err := os.WriteFile(rcService, []byte(serviceScript), 0o700); err != nil {
		t.Fatal(err)
	}
	rcUpdate := filepath.Join(filepath.Dir(stateRoot), "rc-update")
	updateScript := fmt.Sprintf(`#!/bin/sh
set -eu
state=%q
runlevels=%q
init_root=%q
action=$1
service=$2
runlevel=$3
printf 'rc-update %%s %%s %%s\n' "$action" "$service" "$runlevel" >> "$state/commands.log"
link="$runlevels/$runlevel/$service"
case "$action" in
  add)
    [ ! -e "$link" ] && [ ! -L "$link" ] || exit 1
    ln -s "$init_root/$service" "$link"
    ;;
  del)
    if [ ! -L "$link" ]; then
      printf 'service is not in the runlevel %%s\n' "$runlevel"
      exit 1
    fi
    rm -f "$link"
    ;;
  *) exit 1 ;;
esac
`, stateRoot, runlevels, initRoot)
	if err := os.WriteFile(rcUpdate, []byte(updateScript), 0o700); err != nil {
		t.Fatal(err)
	}
	return &ServiceManager{kind: ServiceManagerOpenRC, executable: rcService, enableExecutable: rcUpdate}, filepath.Join(stateRoot, "commands.log")
}

func TestOpenRCRestoreEnableStateSkipsOnlyVerifiedCurrentState(t *testing.T) {
	runlevels, initRoot := useOpenRCTestRunlevels(t)
	stateRoot := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{"sing-box", "qagent-sing-box"} {
		writeOpenRCTestService(t, initRoot, service)
	}
	if err := os.Symlink(filepath.Join(initRoot, "sing-box"), filepath.Join(runlevels, "default", "sing-box")); err != nil {
		t.Fatal(err)
	}
	manager, commandLog := newOpenRCTestManager(t, stateRoot, runlevels, initRoot)

	if err := restoreServiceEnableState(context.Background(), "qagent-sing-box", "disabled", manager); err != nil {
		t.Fatalf("restore already-disabled managed service: %v", err)
	}
	if err := restoreServiceEnableState(context.Background(), "sing-box", "enabled", manager); err != nil {
		t.Fatalf("restore already-enabled existing service: %v", err)
	}
	if contents, err := os.ReadFile(commandLog); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("idempotent restore invoked rc-update: %q, %v", contents, err)
	}

	if err := os.Symlink(filepath.Join(initRoot, "qagent-sing-box"), filepath.Join(runlevels, "boot", "qagent-sing-box")); err != nil {
		t.Fatal(err)
	}
	if err := restoreServiceEnableState(context.Background(), "qagent-sing-box", "disabled", manager); err == nil ||
		!strings.Contains(err.Error(), "outside the single supported default runlevel") {
		t.Fatalf("unsafe non-default enable state was accepted: %v", err)
	}
	if contents, err := os.ReadFile(commandLog); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe runlevel state invoked rc-update: %q, %v", contents, err)
	}
	if err := os.Remove(filepath.Join(runlevels, "boot", "qagent-sing-box")); err != nil {
		t.Fatal(err)
	}
	if err := restoreServiceEnableState(context.Background(), "qagent-sing-box", "enabled", manager); err != nil {
		t.Fatalf("enable disabled managed service: %v", err)
	}
	if err := restoreServiceEnableState(context.Background(), "sing-box", "disabled", manager); err != nil {
		t.Fatalf("disable enabled existing service: %v", err)
	}
	commands, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	wantCommands := "rc-update add qagent-sing-box default\nrc-update del sing-box default\n"
	if string(commands) != wantCommands {
		t.Fatalf("state-changing rc-update commands = %q; want %q", commands, wantCommands)
	}
}

func TestOpenRCMigrationStopFailureRollbackClosesOnFreshReconcile(t *testing.T) {
	requireAgentRoot(t)
	runlevels, initRoot := useOpenRCTestRunlevels(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	fixture.existing.Service = "sing-box"
	fixture.managed.Service = "qagent-sing-box"
	fixture.executor.Specs = map[core.Engine]EngineSpec{core.EngineSingBox: fixture.managed}
	fixture.executor.ExistingSpecs = map[core.Engine]EngineSpec{core.EngineSingBox: fixture.existing}
	for _, service := range []string{fixture.existing.Service, fixture.managed.Service} {
		writeOpenRCTestService(t, initRoot, service)
	}
	if err := os.Symlink(filepath.Join(initRoot, fixture.existing.Service), filepath.Join(runlevels, "default", fixture.existing.Service)); err != nil {
		t.Fatal(err)
	}
	writeOpenRCTestActiveState(t, fixture.stateDirectory, fixture.existing.Service, "active")
	writeOpenRCTestActiveState(t, fixture.stateDirectory, fixture.managed.Service, "inactive")
	manager, commandLog := newOpenRCTestManager(t, fixture.stateDirectory, runlevels, initRoot)
	fixture.executor.Services = manager

	record, err := prepareCoreMigrationFileRollback(
		fixture.markerPrefix,
		core.EngineSingBox,
		fixture.existing,
		fixture.managed,
		coreMigrationRecord{
			State: coreMigrationInProgress, ConfigDigest: coreMigrationConfigDigest(fixture.importedConfig),
			SourceDigest:        coreMigrationSourceDigest(fixture.existing),
			ExistingEnableState: "enabled", ManagedEnableState: "disabled", ManagedInitialState: "inactive",
		},
	)
	if err != nil {
		t.Fatalf("prepare durable OpenRC migration: %v", err)
	}
	if _, err := copyExistingCoreBinary(fixture.existing.Binary, fixture.managed.Binary); err != nil {
		t.Fatalf("stage managed binary: %v", err)
	}
	if _, err := atomicDeploy(fixture.managed.ConfigPath, fixture.importedConfig); err != nil {
		t.Fatalf("stage managed configuration: %v", err)
	}
	if err := verifyCoreMigrationStagedFiles(fixture.managed, record); err != nil {
		t.Fatalf("verify staged OpenRC migration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, "fail-managed-stop-once"), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restoreInterruptedCoreMigration(context.Background(), fixture.markerPrefix, core.EngineSingBox, fixture.existing, fixture.managed, record, manager); err == nil ||
		!strings.Contains(err.Error(), "stop managed service") {
		t.Fatalf("initial stop failure did not leave an interrupted rollback: %v", err)
	}
	if current, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineSingBox); err != nil || current.State != coreMigrationInProgress {
		t.Fatalf("interrupted OpenRC migration marker = %+v, %v", current, err)
	}

	restarted := &Executor{
		Specs:                   map[core.Engine]EngineSpec{core.EngineSingBox: fixture.managed},
		ExistingSpecs:           map[core.Engine]EngineSpec{core.EngineSingBox: fixture.existing},
		ExistingDiscoveryIssues: make(map[core.Engine]string),
		MigrationMarkerPrefix:   fixture.markerPrefix,
		Services:                manager,
	}
	if err := restarted.LoadCoreMigrationState(); err != nil {
		t.Fatalf("load interrupted OpenRC migration: %v", err)
	}
	if err := restarted.ReconcileExistingCoreServices(context.Background()); err != nil {
		t.Fatalf("fresh Agent reconcile interrupted OpenRC rollback: %v", err)
	}
	if current, err := readCoreMigrationRecord(fixture.markerPrefix, core.EngineSingBox); err != nil || current.State != coreMigrationNone {
		t.Fatalf("reconciled OpenRC migration marker = %+v, %v", current, err)
	}
	for service, want := range map[string]string{fixture.existing.Service: "active", fixture.managed.Service: "inactive"} {
		contents, err := os.ReadFile(filepath.Join(fixture.stateDirectory, service+".active"))
		if err != nil || strings.TrimSpace(string(contents)) != want {
			t.Fatalf("%s state = %q, %v; want %s", service, contents, err, want)
		}
	}
	if state, err := openRCServiceEnableState(context.Background(), fixture.existing.Service); err != nil || state != "enabled" {
		t.Fatalf("existing service enable state = %q, %v", state, err)
	}
	if state, err := openRCServiceEnableState(context.Background(), fixture.managed.Service); err != nil || state != "disabled" {
		t.Fatalf("managed service enable state = %q, %v", state, err)
	}
	assertFileContentAndMode(t, fixture.managed.ConfigPath, fixture.originalManagedConfig, 0o600)
	if _, err := os.Lstat(fixture.managed.Binary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("originally absent managed binary remained after reconcile: %v", err)
	}
	for _, kind := range []string{"binary", "config"} {
		if _, err := os.Lstat(coreMigrationBackupPath(fixture.markerPrefix, core.EngineSingBox, kind)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s OpenRC rollback backup remained: %v", kind, err)
		}
	}
	commands, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(commands), "rc-update del qagent-sing-box default") {
		t.Fatalf("reconcile invoked non-idempotent runlevel deletion: %s", commands)
	}
	collector := NewCoreLogCollectorForExecutor(restarted)
	if batch := collector.NextBatch(); batch != nil {
		t.Fatalf("rollback-only reconcile produced a core log batch: %+v", batch)
	}
}

// TestOpenRCBoundServiceProcessRealSupervisedService runs only where a real
// OpenRC supervisor exists: it spins up a supervised service, proves the
// process binding, and confirms the completion wait only succeeds once the
// service is actually stopped.
func TestOpenRCBoundServiceProcessRealSupervisedService(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("real OpenRC supervised binding requires root")
	}
	rcService, err := exec.LookPath("rc-service")
	if err != nil {
		t.Skip("rc-service is not available")
	}
	if _, err := os.Stat("/sbin/supervise-daemon"); err != nil {
		t.Skip("supervise-daemon is not available")
	}
	if err := os.MkdirAll("/run/openrc", 0o755); err != nil {
		t.Skipf("cannot prepare OpenRC runtime state: %v", err)
	}
	if _, err := os.Stat("/run/openrc/softlevel"); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile("/run/openrc/softlevel", nil, 0o644); err != nil {
			t.Skipf("cannot prepare OpenRC softlevel: %v", err)
		}
	}

	const service = "qch-test-openrc"
	initScript := "/etc/init.d/" + service
	optionsDir := filepath.Join(openRCStateRoot, "options", service)
	content := "#!/sbin/openrc-run\ncommand=/bin/sleep\ncommand_args=200\nsupervisor=supervise-daemon\n"
	if err := os.WriteFile(initScript, []byte(content), 0o755); err != nil {
		t.Skipf("cannot install real OpenRC test service: %v", err)
	}
	t.Cleanup(func() {
		_ = exec.Command(rcService, service, "stop").Run()
		_ = os.RemoveAll(optionsDir)
		_ = os.Remove(initScript)
		_ = os.Remove(filepath.Join(openRCSupervisorRoot, "supervise-"+service+".pid"))
	})

	if output, err := exec.Command(rcService, service, "start").CombinedOutput(); err != nil {
		t.Fatalf("rc-service start failed: %v: %s", err, output)
	}
	childPID := waitForOpenRCChildPID(t, service, 3*time.Second)
	identity, err := boundOpenRCServiceProcess(context.Background(), service)
	if err != nil {
		t.Fatalf("boundOpenRCServiceProcess() error on real service: %v", err)
	}
	if identity.Child.PID != childPID {
		t.Fatalf("bound child = %d, metadata child = %d", identity.Child.PID, childPID)
	}
	if identity.Child.ParentPID != identity.Supervisor.PID {
		t.Fatalf("child parent = %d, supervisor = %d", identity.Child.ParentPID, identity.Supervisor.PID)
	}

	if output, err := exec.Command(rcService, service, "stop").CombinedOutput(); err != nil {
		t.Fatalf("rc-service stop failed: %v: %s", err, output)
	}
	if err := waitForOpenRCServiceProcessExit(context.Background(), identity); err != nil {
		t.Fatalf("waitForOpenRCServiceProcessExit() after real stop: %v", err)
	}
}

func waitForOpenRCChildPID(t *testing.T, service string, within time.Duration) int {
	t.Helper()
	path := filepath.Join(openRCStateRoot, "options", service, "child_pid")
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		contents, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := parseOpenRCProcessID(strings.TrimSpace(string(contents)))
			if parseErr == nil {
				return pid
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("OpenRC child PID metadata did not appear for %s", service)
	return 0
}
