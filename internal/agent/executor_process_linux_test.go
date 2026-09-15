//go:build linux

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
