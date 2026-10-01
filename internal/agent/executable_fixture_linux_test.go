//go:build linux

package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func writeFixtureExecutable(path string, contents []byte) error {
	// A parallel fork can inherit a writable descriptor until its exec closes
	// it, making this fixture's own exec fail with ETXTBSY. Keep forks out until
	// WriteFile has closed its writer, even though the descriptor is CLOEXEC.
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, contents, 0o700)
}

func TestExecutableFixtureWaitsForInFlightFork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture")
	started, finished := make(chan struct{}), make(chan error, 1)
	syscall.ForkLock.Lock()
	locked := true
	defer func() {
		if locked {
			syscall.ForkLock.Unlock()
		}
	}()
	go func() {
		close(started)
		finished <- writeFixtureExecutable(path, []byte("#!/bin/sh\nprintf fixture-ready\n"))
	}()
	<-started
	select {
	case err := <-finished:
		t.Fatalf("executable fixture was written while a fork was in progress: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	syscall.ForkLock.Unlock()
	locked = false
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("executable fixture did not finish after the fork lock was released")
	}
	output, err := exec.Command(path).Output()
	if err != nil || string(output) != "fixture-ready" {
		t.Fatalf("completed executable fixture output=%q, error=%v", output, err)
	}
}
