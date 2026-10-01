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

func TestOpenRCWaitForProcessExitAcceptsUnreapedProcesses(t *testing.T) {
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
	// In a container, the stopped child and supervisor can remain in /proc
	// as zombies until PID 1 reaps them. Their original start times still match.
	for _, process := range []struct {
		pid, parent int
		comm, start string
	}{
		{supervisorPID, 1, "supervise-daemon", "4000"},
		{childPID, supervisorPID, "xray", "5000"},
	} {
		stat := strings.Replace(openRCStatLine(process.pid, process.comm, process.parent, process.start), ") S ", ") Z ", 1)
		if err := os.WriteFile(filepath.Join(procRoot, fmt.Sprintf("%d", process.pid), "stat"), []byte(stat), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(stateRoot, "options", service, "child_pid")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(supervisorRoot, "supervise-"+service+".pid")); err != nil {
		t.Fatal(err)
	}
	if err := waitForOpenRCServiceProcessExit(context.Background(), identity); err != nil {
		t.Fatalf("waitForOpenRCServiceProcessExit() with stopped zombies: %v", err)
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
	var childIdentity *openRCProcessIdentity
	t.Cleanup(func() {
		_ = exec.Command(rcService, service, "stop").Run()
		// An early OpenRC stop can leave an orphan. Only reap the exact child
		// captured by this fixture, after checking its original start time.
		if childIdentity != nil {
			if process, err := os.FindProcess(childIdentity.PID); err == nil {
				if alive, err := openRCProcessIdentityAlive(*childIdentity); err == nil && alive {
					_ = process.Kill()
				}
				_ = process.Release()
			}
		}
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
	childIdentity = &identity.Child
	if identity.Child.PID != childPID {
		t.Fatalf("bound child = %d, metadata child = %d", identity.Child.PID, childPID)
	}
	if identity.Child.ParentPID != identity.Supervisor.PID {
		t.Fatalf("child parent = %d, supervisor = %d", identity.Child.ParentPID, identity.Supervisor.PID)
	}
	waitForOpenRCSupervisorLoop(t, identity, 10*time.Second)

	if output, err := exec.Command(rcService, service, "stop").CombinedOutput(); err != nil {
		t.Fatalf("rc-service stop failed: %v: %s", err, output)
	}
	if err := waitForOpenRCServiceProcessExit(context.Background(), identity); err != nil {
		supervisorAlive, supervisorErr := openRCProcessIdentityAlive(identity.Supervisor)
		childAlive, childErr := openRCProcessIdentityAlive(identity.Child)
		_, bindingErr := boundOpenRCServiceProcess(context.Background(), service)
		t.Fatalf("waitForOpenRCServiceProcessExit() after real stop: %v; supervisor alive=%t (%v), child alive=%t (%v), binding=%v",
			err, supervisorAlive, supervisorErr, childAlive, childErr, bindingErr)
	}
}
