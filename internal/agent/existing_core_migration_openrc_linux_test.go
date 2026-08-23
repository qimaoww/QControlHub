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
