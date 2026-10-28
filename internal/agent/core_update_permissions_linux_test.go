//go:build linux

package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Run inside a disposable systemd Docker node, with this test binary as the
// shipped qagent.service ExecStart. Prepare two state directories containing
// preserved.txt ("preserve state\n"), one core-owned and one legacy root-owned.
func TestMihomoUpdateWithAgentServiceSandbox(t *testing.T) {
	if os.Getenv("QCH_TEST_CORE_UPDATE_SANDBOX") != "1" {
		t.Skip("requires a disposable systemd node using the shipped Agent sandbox")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("refusing installed-path test outside Docker")
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
	if os.Geteuid() != 0 || fields["CapEff"] != "0000000000001401" || fields["NoNewPrivs"] != "1" {
		t.Fatalf("test is not using the actual Agent identity/capabilities: uid=%d cap=%s nnp=%s", os.Geteuid(), fields["CapEff"], fields["NoNewPrivs"])
	}
	input := os.Getenv("QCH_TEST_CORE_INPUT")
	old := verifyCoreUpdateTestArchive(t, filepath.Join(input, "mihomo-old.gz"), "8451100836c9eda194331c2babfad490b2faf30cebc1a04b0e76fd8ac2d35d10")
	archive := verifyCoreUpdateTestArchive(t, filepath.Join(input, "mihomo-new.gz"), "132f6f3edf9a65e62f04853aed3358472e0b2b7666daa8419edfa2ddf5bb6323")
	manager := defaultSystemdServiceManager()
	base := DefaultSpecs()[core.EngineMihomo]
	shared, err := sharedInstanceSpec(core.EngineMihomo, base, "shr_0123456789abcdef", manager.Kind())
	if err != nil {
		t.Fatal(err)
	}
	executor := &Executor{Services: manager, coreStateHelperPath: "/usr/local/lib/qagent/qagent"}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if err := prepareSharedInstanceDirectory(core.EngineMihomo, shared, manager.Kind()); err != nil {
		t.Fatal(err)
	}
	if err := executor.ensureSharedInstanceService(ctx, core.EngineMihomo, shared); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []EngineSpec{base, shared} {
		state := coreCertificateWorkingDirectory(core.EngineMihomo, spec, manager.Kind())
		if err := prepareSharedStateDirectory(state); !os.IsPermission(err) && !errors.Is(err, syscall.EROFS) {
			t.Fatalf("baseline must reproduce bounded-root directory failure at %s: %v", state, err)
		}
		t.Logf("before fix: Agent cannot prepare %s directly", state)
	}
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []EngineSpec{base, shared} {
		if err := executor.prepareCoreUpdateStateDirectory(ctx, core.EngineMihomo, spec); err != nil {
			t.Fatalf("prepare existing/legacy state before starting services: %v", err)
		}
	}
	for _, scenario := range []struct {
		name                   string
		mainStopped, stopped   bool
		incompatible, rollback bool
	}{
		{name: "running main and shared"},
		{name: "stopped shared", stopped: true},
		{name: "stopped main", mainStopped: true},
		{name: "incompatible running shared", incompatible: true},
		{name: "incompatible stopped shared", incompatible: true, stopped: true},
		{name: "activation failure rollback", rollback: true},
		{name: "rollback preserves stopped shared", rollback: true, stopped: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			for _, spec := range []EngineSpec{base, shared} {
				if _, err := manager.command(ctx, spec.Service, core.ActionStop); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				for _, spec := range []EngineSpec{base, shared} {
					_, _ = manager.command(context.Background(), spec.Service, core.ActionStop)
				}
			})
			binary, err := os.OpenFile(base.Binary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
			if err != nil {
				t.Fatal(err)
			}
			if err := extractCoreBinary(core.EngineMihomo, "mihomo-old.gz", writeCoreUpdateArchive(t, old), binary); err != nil {
				binary.Close()
				t.Fatal(err)
			}
			if err := binary.Close(); err != nil {
				t.Fatal(err)
			}
			oldBinary, err := os.ReadFile(base.Binary)
			if err != nil {
				t.Fatal(err)
			}
			configs := map[EngineSpec]string{}
			pids := map[EngineSpec]string{}
			for index, spec := range []EngineSpec{base, shared} {
				content := fmt.Sprintf("mixed-port: %d\nallow-lan: false\nlog-level: silent\nrules: ['MATCH,DIRECT']\n", 25191+index)
				if err := os.WriteFile(spec.ConfigPath, []byte(content), 0o640); err != nil {
					t.Fatal(err)
				}
				if err := ensureDefaultManagedConfigurationAccess(core.EngineMihomo, spec, manager); err != nil {
					t.Fatal(err)
				}
				if index == 0 && !scenario.mainStopped || index == 1 && !scenario.stopped {
					if out, err := serviceCommandAndVerifyWithManager(ctx, manager, spec.Service, core.ActionStart); err != nil {
						t.Fatalf("start original %s: %v %s", spec.Service, err, out)
					}
				}
				if index == 1 && scenario.incompatible {
					content += "proxies: [{name: rejected, type: unknown-update-fixture}]\n"
					if err := os.WriteFile(spec.ConfigPath, []byte(content), 0o640); err != nil {
						t.Fatal(err)
					}
				}
				configs[spec] = content
				pids[spec], _ = systemdUnitProperty(ctx, spec.Service, "MainPID")
			}
			updater := mihomoPermissionTestUpdater(archive)
			transaction := managedCoreUpdateTransaction(executor, core.EngineMihomo, base, manager)
			if scenario.rollback {
				transaction = coreStateActivationFailure{transaction}
			}
			out, updateErr := updater.installWithTransaction(ctx, core.EngineMihomo, base, "v1.19.33", "", manager, true, transaction)
			if (scenario.incompatible || scenario.rollback) != (updateErr != nil) {
				t.Fatalf("unexpected update result: %v %s", updateErr, out)
			}
			if scenario.incompatible && !strings.Contains(updateErr.Error(), "compatibility check failed before binary/resource replacement") {
				t.Fatalf("failed at a different stage: %v", updateErr)
			}
			gotBinary, _ := os.ReadFile(base.Binary)
			if (scenario.incompatible || scenario.rollback) && !bytes.Equal(gotBinary, oldBinary) {
				t.Fatal("failed update changed the installed old binary")
			}
			for index, spec := range []EngineSpec{base, shared} {
				gotConfig, _ := os.ReadFile(spec.ConfigPath)
				if string(gotConfig) != configs[spec] {
					t.Fatal("update changed deployed configuration")
				}
				pid, _ := systemdUnitProperty(ctx, spec.Service, "MainPID")
				stopped := index == 0 && scenario.mainStopped || index == 1 && scenario.stopped
				if stopped != (pid == "0") || scenario.incompatible && pid != pids[spec] {
					t.Fatalf("wrong instance state %s: before=%s after=%s", spec.Service, pids[spec], pid)
				}
				state := coreCertificateWorkingDirectory(core.EngineMihomo, spec, manager.Kind())
				output, err := executor.runManagedIdentityCommand(ctx, state, "", "/usr/bin/cat", &identity, filepath.Join(state, "preserved.txt"))
				if err != nil || strings.TrimSpace(output) != "preserve state" {
					t.Fatalf("state data lost: %v %s", err, output)
				}
				if err := executor.prepareCoreUpdateStateDirectory(ctx, core.EngineMihomo, spec); err != nil {
					t.Fatalf("repeated state preparation: %v", err)
				}
			}
		})
	}
	fresh, err := sharedInstanceSpec(core.EngineMihomo, base, "shr_fedcba9876543210", manager.Kind())
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareSharedInstanceDirectory(core.EngineMihomo, fresh, manager.Kind()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := executor.prepareCoreUpdateStateDirectory(ctx, core.EngineMihomo, fresh); err != nil {
			t.Fatalf("prepare new inactive instance: %v", err)
		}
	}
	state, err := os.Lstat(coreCertificateWorkingDirectory(core.EngineMihomo, fresh, manager.Kind()))
	if err != nil || state.Mode().Perm() != 0o750 || metadataFromFileInfo(state).uid != int(identity.uid) || metadataFromFileInfo(state).gid != int(identity.gid) {
		t.Fatalf("new instance state metadata: %v %v", state, err)
	}
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		spec := DefaultSpecs()[engine]
		if err := executor.prepareCoreUpdateStateDirectory(ctx, engine, spec); err != nil {
			t.Fatalf("shared state preparation for %s: %v", engine, err)
		}
		if got, err := manager.status(ctx, spec.Service); err != nil || got != "inactive" {
			t.Fatalf("state preparation changed %s state: %s %v", engine, got, err)
		}
	}
}

func writeCoreUpdateArchive(t *testing.T, contents []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mihomo.gz")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mihomoPermissionTestUpdater(archive []byte) *CoreUpdater {
	digest := sha256.Sum256(archive)
	metadata, _ := json.Marshal(githubRelease{TagName: "v1.19.33", Assets: []githubReleaseAsset{{
		Name: "mihomo-linux-amd64-v1.19.33.gz", Size: int64(len(archive)), Digest: "sha256:" + hex.EncodeToString(digest[:]),
		BrowserDownloadURL: "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.33/mihomo-linux-amd64-v1.19.33.gz",
	}}})
	updater := NewCoreUpdater()
	updater.goarch, updater.libc = "amd64", "gnu"
	updater.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := archive
		if request.URL.Host == "api.github.com" {
			body = metadata
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
	})}
	return updater
}

type coreStateActivationFailure struct{ coreUpdateTransaction }

func (failure coreStateActivationFailure) Activate(ctx context.Context) (string, error) {
	output, err := failure.coreUpdateTransaction.Activate(ctx)
	return output, errors.Join(err, errors.New("isolated post-activation failure"))
}
