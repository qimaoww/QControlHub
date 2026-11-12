//go:build linux

package networkdiag

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNextTraceEngineLock(t *testing.T) {
	b, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "github.com/nxtrace/NTrace-core "+NextTraceEngineVersion+"\n") {
		t.Fatal("engine capability version differs from locked library")
	}
	info, _ := debug.ReadBuildInfo()
	if err := validateNextTraceBuild(info); err != nil {
		t.Fatal(err)
	}
}

func TestNextTraceCapabilityCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := nextTraceCapability(ctx)
	if result.Error == "" || result.Version != "" || result.AutoInstall {
		t.Fatalf("canceled backend advertised available: %+v", result)
	}
	// A failed probe must not poison the following call.
	result = nextTraceCapability(context.Background())
	if result.Error == "" && !strings.Contains(result.Version, NextTraceEngineVersion) {
		t.Fatalf("missing real engine identity: %+v", result)
	}
	if result.AutoInstall {
		t.Fatal("built-in engine requested external installation")
	}
}

func TestNextTraceRejectsUnknownAndReplacedVersions(t *testing.T) {
	for _, module := range []*debug.Module{nil, {Path: "github.com/nxtrace/NTrace-core", Version: "v1.7.5"}, {Path: "github.com/nxtrace/NTrace-core", Version: NextTraceEngineVersion, Replace: &debug.Module{Path: "local"}}} {
		info := &debug.BuildInfo{}
		if module != nil {
			info.Deps = []*debug.Module{module}
		}
		if validateNextTraceBuild(info) == nil {
			t.Fatal("accepted unverified engine build")
		}
	}
}

func TestBoundedExecutableProbeFailures(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/probe"
	for _, tc := range []struct {
		name, script string
		mode         os.FileMode
		reason       error
	}{
		{"permission", "#!/bin/sh\nexit 0\n", 0600, os.ErrPermission},
		{"format", "invalid executable", 0700, syscall.ENOEXEC},
		{"timeout", "#!/bin/sh\nsleep 10\n", 0700, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.script), tc.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			_, err := probeDiagnosticCommand(context.Background(), path)
			if !errors.Is(err, tc.reason) {
				t.Fatalf("got %v, want %v", err, tc.reason)
			}
			if time.Since(start) > 3*time.Second {
				t.Fatal("probe exceeded bound")
			}
		})
	}
	if _, err := probeDiagnosticCommand(context.Background(), dir+"/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := probeDiagnosticCommand(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'unsupported argument'\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out, err := probeDiagnosticCommand(context.Background(), path)
	var status *exec.ExitError
	if !errors.As(err, &status) || status.ExitCode() != 7 || !strings.Contains(string(out), "unsupported argument") {
		t.Fatalf("lost exit evidence: %q %v", out, err)
	}
}

func TestLegacyCLIIndependenceInSandbox(t *testing.T) {
	if os.Getenv("QCH_NEXTTRACE_ISOLATED") != "1" {
		t.Skip("requires disposable filesystem and NET_RAW")
	}
	if err := os.MkdirAll(filepath.Dir(NextTracePath), 0755); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(NextTracePath)
	for _, fixture := range []struct {
		name, body string
		mode       os.FileMode
	}{
		{"missing", "", 0}, {"no-execute", "invalid", 0600}, {"incompatible", "#!/bin/sh\necho NextTrace-v0\n", 0700}, {"hang", "#!/bin/sh\nsleep 60\n", 0700},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			_ = os.Remove(NextTracePath)
			if fixture.mode != 0 {
				if err := os.WriteFile(NextTracePath, []byte(fixture.body), fixture.mode); err != nil {
					t.Fatal(err)
				}
			}
			cap := nextTraceCapability(context.Background())
			if cap.Error != "" || cap.AutoInstall || !strings.Contains(cap.Version, NextTraceEngineVersion) {
				t.Fatalf("legacy CLI gated embedded engine: %+v", cap)
			}
			if err := PrepareTool(context.Background(), "nexttrace"); err != nil {
				t.Fatal(err)
			}
			if fixture.mode != 0 {
				b, err := os.ReadFile(NextTracePath)
				if err != nil || string(b) != fixture.body {
					t.Fatal("preparation modified legacy file", err)
				}
			}
		})
	}
}

func TestNextTracePermissionFailureInSandbox(t *testing.T) {
	if os.Getenv("QCH_NEXTTRACE_NO_RAW") != "1" {
		t.Skip("requires disposable container without NET_RAW")
	}
	cap := nextTraceCapability(context.Background())
	if cap.Error == "" || cap.Version != "" || cap.AutoInstall {
		t.Fatalf("raw-socket failure hidden: %+v", cap)
	}
	t.Log(cap.Error)
}
