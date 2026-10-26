//go:build linux

package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Run as the shipped OpenRC Agent in a disposable Alpine node. Seed each
// state directory with core-owned 0700 cache/ and root-owned 0600 cache/legacy.
func TestCoreUpdateOpenRCStatePreparation(t *testing.T) {
	if os.Getenv("QCH_TEST_CORE_UPDATE_OPENRC") != "1" {
		t.Skip("requires the actual OpenRC Agent capability policy in Docker")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires a disposable Docker node")
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{}
	for line := range strings.SplitSeq(string(status), "\n") {
		if key, value, ok := strings.Cut(line, ":"); ok {
			fields[key] = strings.TrimSpace(value)
		}
	}
	capabilities, _ := strconv.ParseUint(fields["CapEff"], 16, 64)
	// Unlike the systemd Agent, the shipped root OpenRC service retains DAC
	// access from its container/host bounding set. Preserve that existing path.
	if os.Geteuid() != 0 || capabilities&2 == 0 || fields["NoNewPrivs"] != "1" {
		t.Fatalf("OpenRC Agent uid=%d capabilities=%s nnp=%s", os.Geteuid(), fields["CapEff"], fields["NoNewPrivs"])
	}
	manager, err := NewServiceManager(ServiceManagerOpenRC)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	executor := &Executor{Services: manager}
	stat, err := exec.LookPath("stat")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	base := DefaultSpecsForServiceManager(ServiceManagerOpenRC)[core.EngineMihomo]
	shared, err := sharedInstanceSpec(core.EngineMihomo, base, "shr_0123456789abcdef", manager.Kind())
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.ensureSharedInstanceService(ctx, core.EngineMihomo, shared); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []EngineSpec{base, shared} {
		state := coreCertificateWorkingDirectory(core.EngineMihomo, spec, manager.Kind())
		if err := prepareSharedStateDirectory(state); err != nil {
			t.Fatalf("existing OpenRC state preparation regressed: %v", err)
		}
		content, err := readConfigurationFile(spec.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := executor.validateCoreUpdateCandidate(ctx, core.EngineMihomo, spec, content, base.Binary, filepath.Dir(base.Binary)); err != nil {
				t.Fatalf("OpenRC candidate preflight: %v", err)
			}
		}
		output, err := executor.runManagedIdentityCommand(ctx, state, "", "/bin/cat", &identity, filepath.Join(state, "cache/legacy"))
		if err != nil || output != "preserved legacy cache" {
			t.Fatalf("legacy cache changed: %s %v", output, err)
		}
		output, err = executor.runManagedIdentityCommand(ctx, state, "", stat, &identity, "-c", "%a", filepath.Join(state, "cache"))
		if err != nil || output != "700" {
			t.Fatalf("owner-only cache directory mode changed: %s %v", output, err)
		}
		if got, err := manager.status(ctx, spec.Service); err != nil || got != "inactive" {
			t.Fatalf("state preparation changed service state: %s %v", got, err)
		}
	}
}
