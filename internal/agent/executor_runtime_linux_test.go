//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestExecutorRuntimeCachesAndInvalidation(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	unitRoot := filepath.Join(tmp, "units")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(unitRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	callsPath := filepath.Join(tmp, "systemctl.calls")
	fakeSystemctl := filepath.Join(tmp, "systemctl")
	script := "#!/bin/sh\necho \"$@\" >> " + callsPath + "\n" +
		"for arg in \"$@\"; do\n" +
		"  case \"$arg\" in\n" +
		"    --property=LoadState) echo not-found; exit 0;;\n" +
		"    is-active) echo inactive; exit 3;;\n" +
		"  esac\n" +
		"done\n" +
		"exit 0\n"
	if err := os.WriteFile(fakeSystemctl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	prevSystemctl, prevUnitRoot := systemctlPath, existingDiscoveryManagedUnitRoot
	systemctlPath, existingDiscoveryManagedUnitRoot = fakeSystemctl, unitRoot
	defer func() {
		systemctlPath, existingDiscoveryManagedUnitRoot = prevSystemctl, prevUnitRoot
	}()

	verCallsPath := filepath.Join(tmp, "ver.calls")
	fakeBin := filepath.Join(binDir, "sing-box")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\necho call >> "+verCallsPath+"\necho 1.11.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	exec := &Executor{
		Specs: map[core.Engine]EngineSpec{
			core.EngineSingBox: DefaultSpecs()[core.EngineSingBox],
		},
	}

	ctx := context.Background()
	_ = exec.Runtime(ctx)
	count1, _ := os.ReadFile(callsPath)
	_ = exec.Runtime(ctx)
	count2, _ := os.ReadFile(callsPath)

	lines1 := countNonEmptyLines(string(count1))
	lines2 := countNonEmptyLines(string(count2))
	// Second Runtime() skips validateManagedServiceTarget's LoadState check and only probes is-active.
	if lines2-lines1 >= lines1 {
		t.Fatalf("expected second Runtime call to skip cached LoadState check: first=%d secondDelta=%d", lines1, lines2-lines1)
	}

	if v1 := exec.runtimeBinaryVersion(ctx, core.EngineSingBox, fakeBin); v1 != "1.11.0" {
		t.Fatalf("version 1 = %q; want 1.11.0", v1)
	}
	if v2 := exec.runtimeBinaryVersion(ctx, core.EngineSingBox, fakeBin); v2 != "1.11.0" {
		t.Fatalf("version 2 = %q; want 1.11.0", v2)
	}
	verRaw, _ := os.ReadFile(verCallsPath)
	if countNonEmptyLines(string(verRaw)) != 1 {
		t.Fatalf("expected cached binaryVersion to fork once, forked %d times", countNonEmptyLines(string(verRaw)))
	}

	// Touching unit file invalidates fingerprint immediately even before TTL expires.
	if err := os.WriteFile(filepath.Join(unitRoot, DefaultSpecs()[core.EngineSingBox].Service), []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = exec.Runtime(ctx)
	count3, _ := os.ReadFile(callsPath)
	lines3 := countNonEmptyLines(string(count3))
	if lines3-lines2 <= lines2-lines1 {
		t.Fatalf("expected unit file change to bust managed target cache immediately: secondDelta=%d thirdDelta=%d", lines2-lines1, lines3-lines2)
	}

	// Explicit cache invalidation forces re-validation and binary re-check.
	exec.InvalidateRuntimeCaches()
	_ = exec.Runtime(ctx)
	count4, _ := os.ReadFile(callsPath)
	lines4 := countNonEmptyLines(string(count4))
	if lines4-lines3 <= lines2-lines1 {
		t.Fatalf("expected InvalidateRuntimeCaches to force fresh validation: secondDelta=%d fourthDelta=%d", lines2-lines1, lines4-lines3)
	}
	_ = exec.runtimeBinaryVersion(ctx, core.EngineSingBox, fakeBin)
	verRawAfter, _ := os.ReadFile(verCallsPath)
	if countNonEmptyLines(string(verRawAfter)) != 2 {
		t.Fatalf("expected binary version cache invalidation, fork count = %d; want 2", countNonEmptyLines(string(verRawAfter)))
	}
}

func countNonEmptyLines(raw string) int {
	n := 0
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i == len(raw) || raw[i] == '\n' {
			if i > start {
				n++
			}
			start = i + 1
		}
	}
	return n
}

func BenchmarkExecutorRuntimeCached(b *testing.B) {
	tmp := b.TempDir()
	unitRoot := filepath.Join(tmp, "units")
	if err := os.MkdirAll(unitRoot, 0o755); err != nil {
		b.Fatal(err)
	}
	fakeSystemctl := filepath.Join(tmp, "systemctl")
	script := "#!/bin/sh\n" +
		"for arg in \"$@\"; do\n" +
		"  case \"$arg\" in\n" +
		"    --property=LoadState) echo not-found; exit 0;;\n" +
		"    is-active) echo inactive; exit 3;;\n" +
		"  esac\n" +
		"done\n" +
		"exit 0\n"
	if err := os.WriteFile(fakeSystemctl, []byte(script), 0o755); err != nil {
		b.Fatal(err)
	}
	prevSystemctl, prevUnitRoot := systemctlPath, existingDiscoveryManagedUnitRoot
	systemctlPath, existingDiscoveryManagedUnitRoot = fakeSystemctl, unitRoot
	defer func() {
		systemctlPath, existingDiscoveryManagedUnitRoot = prevSystemctl, prevUnitRoot
	}()

	exec := &Executor{Specs: DefaultSpecs()}
	ctx := context.Background()
	_ = exec.Runtime(ctx) // Warm managed target cache
	b.ResetTimer()
	for b.Loop() {
		_ = exec.Runtime(ctx)
	}
}
