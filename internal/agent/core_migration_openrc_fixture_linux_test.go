//go:build linux

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
