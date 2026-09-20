//go:build linux

package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestUIDAbsentFromThreadStatusesAfterThreadExit(t *testing.T) {
	process := exec.Command("sleep", "30")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = process.Process.Kill()
		_ = process.Wait()
	})
	status, err := os.Open(fmt.Sprintf("/proc/%d/task/%d/status", process.Process.Pid, process.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer status.Close()
	if err := process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = process.Wait()

	// Reopen the retained proc inode to deterministically reproduce the
	// thread exiting between os.ReadFile's open and read operations.
	path := fmt.Sprintf("/proc/self/fd/%d", status.Fd())
	if _, err := os.ReadFile(path); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("read exited thread status = %v, want ESRCH", err)
	}
	if !uidAbsentFromThreadStatuses(60000, []string{path}) {
		t.Fatal("exited thread prevented proving the UID inactive")
	}
	if uidAbsentFromThreadStatuses(os.Getuid(), []string{path, "/proc/self/status"}) {
		t.Fatal("exited thread hid another thread's live credentials")
	}
}

func TestUIDAbsentFromThreadStatusesFailsClosed(t *testing.T) {
	root := t.TempDir()
	status := filepath.Join(root, "status")
	missing := filepath.Join(root, "missing")
	for _, test := range []struct {
		name     string
		contents string
		want     bool
	}{
		{"unrelated", "Name:\thelper\nUid:\t0\t0\t0\t0\n", true},
		{"real UID", "Uid:\t60000\t0\t0\t0\n", false},
		{"effective UID", "Uid:\t0\t60000\t0\t0\n", false},
		{"saved UID", "Uid:\t0\t0\t60000\t0\n", false},
		{"filesystem UID", "Uid:\t0\t0\t0\t60000\n", false},
		{"missing credentials", "Name:\thelper\n", false},
		{"incomplete credentials", "Uid:\t0\t0\t0\n", false},
		{"invalid credentials", "Uid:\t0\tx\t0\t0\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(status, []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := uidAbsentFromThreadStatuses(60000, []string{missing, status}); got != test.want {
				t.Fatalf("UID absent = %v, want %v", got, test.want)
			}
		})
	}
	if uidAbsentFromThreadStatuses(60000, nil) {
		t.Fatal("empty process scan must fail closed")
	}
	if uidAbsentFromThreadStatuses(60000, []string{root}) {
		t.Fatal("non-disappearance read error must fail closed")
	}
}
