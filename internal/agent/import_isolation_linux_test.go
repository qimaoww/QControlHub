//go:build linux

package agent

import (
	"context"
	"errors"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedTargetReadFailureRetainsRedactedCause(t *testing.T) {
	previous := systemctlPath
	systemctlPath = filepath.Join(t.TempDir(), "systemctl")
	t.Cleanup(func() { systemctlPath = previous })
	writeExecutable(t, systemctlPath, "#!/bin/sh\nprintf '%s\\n' 'fixture systemd bus failure: permission denied; password=private-fixture-value' >&2\nexit 23\n")
	manager := &ServiceManager{kind: ServiceManagerSystemd}
	spec := DefaultSpecsForServiceManager(ServiceManagerSystemd)[core.EngineXray]
	for _, read := range []struct {
		name string
		read func(context.Context) (string, error)
	}{
		{"managed target", func(ctx context.Context) (string, error) {
			return validateManagedServiceTarget(ctx, core.EngineXray, spec, manager)
		}},
		{"effective property", func(ctx context.Context) (string, error) {
			return systemdUnitProperty(ctx, spec.Service, "Description")
		}},
	} {
		t.Run(read.name, func(t *testing.T) {
			_, err := read.read(context.Background())
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 23 || !strings.Contains(err.Error(), "fixture systemd bus failure: permission denied") {
				t.Fatalf("lost service read cause or exit status: %v", err)
			}
			if strings.Contains(err.Error(), "private-fixture-value") || !strings.Contains(err.Error(), "[redacted]") {
				t.Fatal("service read diagnostic was not safely redacted")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := read.read(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("service read lost cancellation cause: %v", err)
			}
		})
	}
}

func TestExternalSourceFailureDoesNotChangeManagedReadValidationOrRuntime(t *testing.T) {
	requireAgentRoot(t)
	fixture := newExistingCoreMigrationFixture(t, false)
	if err := os.WriteFile(fixture.managed.Binary, existingDiscoveryCoreHelper, 0700); err != nil {
		t.Fatal(err)
	}
	issue := "外部服务配置不可导入：protected path validation failed"
	fixture.executor.ExistingDiscoveryIssues = map[core.Engine]string{core.EngineXray: issue}
	before, _ := os.ReadFile(fixture.existing.ConfigPath)
	for _, action := range []core.Action{core.ActionReadManagedConfig, core.ActionValidate, core.ActionStatus} {
		task := core.Task{Action: action, Engine: core.EngineXray, ConfigContent: `{"inbounds":[{"tag":"manual","listen":"127.0.0.1","port":21000,"protocol":"socks","settings":{"auth":"noauth"}}],"outbounds":[{"protocol":"freedom","tag":"direct"}]}`}
		if _, err := fixture.executor.Execute(context.Background(), task); err != nil {
			t.Fatalf("ordinary %s depends on import discovery: %v", action, err)
		}
	}
	state := fixture.executor.Runtime(context.Background())[core.EngineXray]
	if !state.Installed || state.ServiceStatus != "inactive" || state.ExistingConfigUnsupportedReason != issue || state.ManagedOperationBlockedReason != "" {
		t.Fatalf("external service contaminated managed runtime: %+v", state)
	}
	for _, action := range []core.Action{core.ActionReadConfig, core.ActionImportExisting} {
		if _, err := fixture.executor.Execute(context.Background(), core.Task{Action: action, Engine: core.EngineXray}); err == nil || !strings.Contains(err.Error(), issue) {
			t.Fatalf("source safety was bypassed for %s: %v", action, err)
		}
	}
	after, _ := os.ReadFile(fixture.existing.ConfigPath)
	if string(before) != string(after) {
		t.Fatal("ordinary operations modified external source")
	}
	fixture.assertServiceState(t, "xray.service", "active", "enabled")
}

func TestUnreadableMigrationStateBlocksOnlyItsEngine(t *testing.T) {
	fixture := newExistingCoreMigrationFixture(t, false)
	fixture.executor.Specs[core.EngineMihomo] = EngineSpec{Binary: fixture.managed.Binary, ConfigPath: fixture.managed.ConfigPath, Service: "qagent-mihomo.service"}
	if err := os.WriteFile(coreMigrationMarkerPath(fixture.markerPrefix, core.EngineXray), []byte("invalid migration state"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := fixture.executor.LoadCoreMigrationState(); err == nil {
		t.Fatal("corrupt migration state was ignored")
	}
	if fixture.executor.ManagedSafetyIssues[core.EngineXray] == "" || fixture.executor.ManagedSafetyIssues[core.EngineMihomo] != "" {
		t.Fatal("migration safety scope was lost")
	}
}
