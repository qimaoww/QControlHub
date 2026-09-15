//go:build linux

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestExistingCoreDiscoveryFindsAndRefreshesAfterAgentRestart(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, nil,
	)
	if err != nil {
		t.Fatalf("initial discovery after upgraded Agent restart: %v", err)
	}
	assertDiscoveredSingBoxSpec(t, specs[core.EngineSingBox], fixture.realBinary, fixture.serviceBinary, fixture.configPath, fixture.configDirectory, "")
	if len(issues) != 0 {
		t.Fatalf("initial discovery issues = %+v", issues)
	}
	info, err := os.Stat(fixture.discoveryStatePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("discovery state mode = %v, %v", info, err)
	}

	refreshedDirectory := filepath.Join(fixture.root, "refreshed-conf.d")
	if err := os.Mkdir(refreshedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(refreshedDirectory, "20-route.json"), []byte(`{"route":{"final":"direct"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.writeExecStart(t, "sing-box.service", systemdExecStart(
		fixture.serviceBinary,
		fixture.serviceBinary+" run -c "+fixture.configPath+" -C "+refreshedDirectory,
	))
	specs, issues, err = RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, nil,
	)
	if err != nil {
		t.Fatalf("refresh discovery after restart: %v", err)
	}
	assertDiscoveredSingBoxSpec(t, specs[core.EngineSingBox], fixture.realBinary, fixture.serviceBinary, fixture.configPath, refreshedDirectory, "")
	if len(issues) != 0 {
		t.Fatalf("refreshed discovery issues = %+v", issues)
	}
	state, err := loadExistingCoreDiscoveryState(fixture.discoveryStatePath)
	if err != nil {
		t.Fatalf("reload refreshed discovery state: %v", err)
	}
	if got := state.Specs[core.EngineSingBox].ConfigDirectory; got != refreshedDirectory {
		t.Fatalf("persisted refreshed config directory = %q", got)
	}
}

func TestExistingCoreDiscoveryAcceptsFreshAgentWithoutManagedCoreUnit(t *testing.T) {
	fixture := newExistingCoreDiscoveryFixture(t)
	managedUnit := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service")
	if err := os.Remove(managedUnit); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.load-state"), []byte("not-found\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	specs, issues, err := RefreshExistingCoreDiscovery(
		context.Background(), fixture.discoveryStatePath, fixture.markerPrefix,
		fixture.managedSpecs, nil,
	)
	if err != nil {
		t.Fatalf("discover existing sing-box before a panel-managed unit exists: %v", err)
	}
	assertDiscoveredSingBoxSpec(t, specs[core.EngineSingBox], fixture.realBinary, fixture.serviceBinary, fixture.configPath, fixture.configDirectory, "")
	if len(issues) != 0 {
		t.Fatalf("fresh Agent discovery issues = %+v", issues)
	}
}

func TestExistingCoreDiscoveryReportsAmbiguousAndUnsupportedServices(t *testing.T) {
	t.Run("ambiguous", func(t *testing.T) {
		fixture := newExistingCoreDiscoveryFixture(t)
		fixture.writeStatus(t, "singbox.service", "active")
		specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(specs) != 0 || !strings.Contains(issues[core.EngineSingBox], "多个活动") {
			t.Fatalf("ambiguous discovery = specs %+v issues %+v", specs, issues)
		}
	})

	t.Run("unsupported wrapper", func(t *testing.T) {
		fixture := newExistingCoreDiscoveryFixture(t)
		wrapper := filepath.Join(fixture.root, "complex-wrapper")
		contents := fmt.Sprintf("#!/bin/sh\nset -eu\nexport FOO=bar\nexec %s \"$@\"\n# extra\n", fixture.realBinary)
		if err := os.WriteFile(wrapper, []byte(contents), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(fixture.serviceBinary); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(wrapper, fixture.serviceBinary); err != nil {
			t.Fatal(err)
		}
		specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
		if err != nil {
			t.Fatal(err)
		}
		reason := issues[core.EngineSingBox]
		if len(specs) != 0 || !strings.Contains(reason, "wrapper 不在安全支持范围") {
			t.Fatalf("unsupported-wrapper discovery = specs %+v reason %q", specs, reason)
		}
		state, err := loadExistingCoreDiscoveryState(fixture.discoveryStatePath)
		if err != nil || state.Issues[core.EngineSingBox] != reason {
			t.Fatalf("persisted unsupported-wrapper issue = %+v, %v", state.Issues, err)
		}
		runtime := (&Executor{
			Specs:                   fixture.managedSpecs,
			ExistingDiscoveryIssues: map[core.Engine]string{core.EngineSingBox: reason},
		}).Runtime(context.Background())[core.EngineSingBox]
		if runtime.Installed || runtime.ServiceStatus != "active" || runtime.ExistingConfigUnsupportedReason != reason || runtime.ExistingConfigAvailable {
			t.Fatalf("unsupported-wrapper runtime = %+v", runtime)
		}
	})

	t.Run("custom managed unit", func(t *testing.T) {
		fixture := newExistingCoreDiscoveryFixture(t)
		unitPath := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service")
		if err := os.WriteFile(unitPath, []byte("[Unit]\nDescription=administrator unit\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(specs) != 0 || !strings.Contains(issues[core.EngineSingBox], "QAgent 专用服务") {
			t.Fatalf("custom managed unit discovery = specs %+v issues %+v", specs, issues)
		}
	})

	t.Run("active managed unit", func(t *testing.T) {
		fixture := newExistingCoreDiscoveryFixture(t)
		fixture.writeStatus(t, "qagent-sing-box.service", "active")
		specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(issues) != 0 || specs[core.EngineSingBox].Service != "sing-box.service" {
			t.Fatalf("safe active managed unit discovery = specs %+v issues %+v", specs, issues)
		}
		activeState, readErr := os.ReadFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.active"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if got := strings.TrimSpace(string(activeState)); got != "active" {
			t.Fatalf("discovery mutated active managed unit to %q", got)
		}
	})

	t.Run("active managed ExecStart drift", func(t *testing.T) {
		fixture := newExistingCoreDiscoveryFixture(t)
		fixture.writeStatus(t, "qagent-sing-box.service", "active")
		if err := os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.managed-exec-start"), []byte(systemdExecStart(
			DefaultSpecs()[core.EngineSingBox].Binary,
			DefaultSpecs()[core.EngineSingBox].Binary+" run -c /etc/qagent/sing-box/changed.json",
		)), 0o600); err != nil {
			t.Fatal(err)
		}
		specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(specs) != 0 || !strings.Contains(issues[core.EngineSingBox], "安全 unit") {
			t.Fatalf("drifted active managed unit discovery = specs %+v issues %+v", specs, issues)
		}
	})

	t.Run("effective managed identity and hooks", func(t *testing.T) {
		tests := map[string]func(existingCoreDiscoveryFixture) error{
			"user override": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.user"), []byte("root\n"), 0o600)
			},
			"start hook": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.ExecStartPre"), []byte("/bin/true\n"), 0o600)
			},
			"comment marker": func(fixture existingCoreDiscoveryFixture) error {
				unitPath := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service")
				return os.WriteFile(unitPath, []byte("[Unit]\n#Description=sing-box core managed by QAgent\n[Service]\nUser=qcontrolhub-core\nGroup=qcontrolhub-core\nExecStart="+DefaultSpecs()[core.EngineSingBox].Binary+" run -c "+DefaultSpecs()[core.EngineSingBox].ConfigPath+"\n"), 0o600)
			},
			"duplicate marker": func(fixture existingCoreDiscoveryFixture) error {
				unitPath := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service")
				return os.WriteFile(unitPath, []byte("[Unit]\nDescription=sing-box core managed by QAgent\nDescription=sing-box core managed by QAgent\n[Service]\nUser=qcontrolhub-core\nGroup=qcontrolhub-core\nExecStart="+DefaultSpecs()[core.EngineSingBox].Binary+" run -c "+DefaultSpecs()[core.EngineSingBox].ConfigPath+"\n"), 0o600)
			},
		}
		for name, mutate := range tests {
			t.Run(name, func(t *testing.T) {
				fixture := newExistingCoreDiscoveryFixture(t)
				fixture.writeStatus(t, "qagent-sing-box.service", "active")
				if err := mutate(fixture); err != nil {
					t.Fatal(err)
				}
				specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(specs) != 0 || !strings.Contains(issues[core.EngineSingBox], "安全 unit") {
					t.Fatalf("unsafe effective managed unit discovery = specs %+v issues %+v", specs, issues)
				}
			})
		}
	})

	t.Run("effective execution context and drop-ins", func(t *testing.T) {
		tests := map[string]func(existingCoreDiscoveryFixture) error{
			"root directory": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.RootDirectory"), []byte("/sandbox\n"), 0o600)
			},
			"bind read-only paths": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.BindReadOnlyPaths"), []byte("/source:/target\n"), 0o600)
			},
			"environment": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.Environment"), []byte("QCH_UNEXPECTED=1\n"), 0o600)
			},
			"environment file": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.EnvironmentFiles"), []byte("/etc/qagent/unexpected.env\n"), 0o600)
			},
			"working directory": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.WorkingDirectory"), []byte("/other\n"), 0o600)
			},
			"type": func(fixture existingCoreDiscoveryFixture) error {
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.Type"), []byte("oneshot\n"), 0o600)
			},
			"unknown fragment context": func(fixture existingCoreDiscoveryFixture) error {
				unitPath := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service")
				contents, err := os.ReadFile(unitPath)
				if err != nil {
					return err
				}
				contents = []byte(strings.Replace(string(contents), "WorkingDirectory=/var/lib/qcontrolhub-sing-box\n", "RootDirectory=/sandbox\nWorkingDirectory=/var/lib/qcontrolhub-sing-box\n", 1))
				return os.WriteFile(unitPath, contents, 0o600)
			},
			"unknown drop-in": func(fixture existingCoreDiscoveryFixture) error {
				path := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service.d", "99-unknown.conf")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					return err
				}
				if err := os.WriteFile(path, []byte("[Service]\nEnvironment=QCH_UNEXPECTED=1\n"), 0o600); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.DropInPaths"), []byte(path+"\n"), 0o600)
			},
			"modified project drop-in": func(fixture existingCoreDiscoveryFixture) error {
				path := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service.d", "10-qcontrolhub-bind-low-ports.conf")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					return err
				}
				if err := os.WriteFile(path, []byte("[Service]\nEnvironment=QCH_UNEXPECTED=1\n"), 0o600); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.DropInPaths"), []byte(path+"\n"), 0o600)
			},
		}
		for name, mutate := range tests {
			t.Run(name, func(t *testing.T) {
				fixture := newExistingCoreDiscoveryFixture(t)
				fixture.writeStatus(t, "qagent-sing-box.service", "active")
				if err := mutate(fixture); err != nil {
					t.Fatal(err)
				}
				specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(specs) != 0 || !strings.Contains(issues[core.EngineSingBox], "安全 unit") {
					t.Fatalf("unsafe execution context discovery = specs %+v issues %+v", specs, issues)
				}
			})
		}
	})

	t.Run("project-managed capability and log drop-ins", func(t *testing.T) {
		fixture := newExistingCoreDiscoveryFixture(t)
		fixture.writeStatus(t, "qagent-sing-box.service", "active")
		dropInDirectory := filepath.Join(existingDiscoveryManagedUnitRoot, "qagent-sing-box.service.d")
		if err := os.MkdirAll(dropInDirectory, 0o700); err != nil {
			t.Fatal(err)
		}
		paths := []string{
			filepath.Join(dropInDirectory, "10-qcontrolhub-bind-low-ports.conf"),
			filepath.Join(dropInDirectory, "20-qcontrolhub-volatile-logs.conf"),
		}
		contents := [][]byte{[]byte(managedCoreCapabilityDropIn), []byte(managedCoreLogDropIn)}
		for index, path := range paths {
			if err := os.WriteFile(path, contents[index], 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(fixture.stateDirectory, "qagent-sing-box.service.DropInPaths"), []byte(strings.Join(paths, " ")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		specs, issues, err := RefreshExistingCoreDiscovery(context.Background(), fixture.discoveryStatePath, fixture.markerPrefix, fixture.managedSpecs, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(issues) != 0 || specs[core.EngineSingBox].Service != "sing-box.service" {
			t.Fatalf("project-managed drop-ins were rejected: specs=%+v issues=%+v", specs, issues)
		}
	})
}
