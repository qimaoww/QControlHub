//go:build linux

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type existingCoreHelperMutation struct {
	At      int    `json:"at"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

func writeExistingCoreHelperMutations(t *testing.T, binary string, at int, mutations map[string]string) {
	t.Helper()
	control := struct {
		Mutations []existingCoreHelperMutation `json:"mutations"`
	}{}
	for path, content := range mutations {
		control.Mutations = append(control.Mutations, existingCoreHelperMutation{At: at, Path: path, Content: content})
	}
	contents, err := json.Marshal(control)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary+".control.json", contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

type existingCoreDiscoveryFixture struct {
	root               string
	stateDirectory     string
	discoveryStatePath string
	markerPrefix       string
	realBinary         string
	serviceBinary      string
	configPath         string
	configDirectory    string
	managedSpecs       map[core.Engine]EngineSpec
}

func (fixture *existingCoreDiscoveryFixture) useDirectSingBoxBinary(t *testing.T, owner int) string {
	t.Helper()
	directory := filepath.Join(fixture.root, "etc", "sing-box", "bin")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	serviceBinary := filepath.Join(directory, "sing-box")
	if err := os.WriteFile(serviceBinary, existingDiscoveryCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	if owner >= 0 {
		if err := os.Chown(serviceBinary, owner, owner); err != nil {
			t.Fatal(err)
		}
	}
	fixture.realBinary = serviceBinary
	fixture.serviceBinary = serviceBinary
	existingDiscoveryCandidates[core.EngineSingBox] = existingDiscoveryCandidateSet{
		services:          []string{"sing-box.service", "singbox.service"},
		executables:       []string{serviceBinary},
		directExecutables: []string{serviceBinary},
		configs:           []string{fixture.configPath},
	}
	fixture.writeExecStart(t, "sing-box.service", systemdExecStart(
		serviceBinary,
		serviceBinary+" run -c "+fixture.configPath+" -C "+fixture.configDirectory,
	))
	fixture.writeExecStart(t, "singbox.service", systemdExecStart(
		serviceBinary,
		serviceBinary+" run -c "+fixture.configPath+" -C "+fixture.configDirectory,
	))
	return serviceBinary
}

func newExistingCoreDiscoveryFixture(t *testing.T) existingCoreDiscoveryFixture {
	t.Helper()
	root := t.TempDir()
	stateDirectory := filepath.Join(root, "systemctl")
	configDirectory := filepath.Join(root, "conf.d")
	managedDirectory := filepath.Join(root, "managed")
	managedUnitDirectory := filepath.Join(root, "managed-units")
	agentStateDirectory := filepath.Join(root, "agent-state")
	for _, directory := range []string{stateDirectory, configDirectory, managedDirectory, managedUnitDirectory, agentStateDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(root, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"inbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "10-outbounds.json"), []byte(`{"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(existingDiscoveryCoreHelper) == 0 {
		t.Fatal("discovery core helper was not built")
	}
	realBinary := filepath.Join(root, existingDiscoveryCoreHelperName)
	if err := os.WriteFile(realBinary, existingDiscoveryCoreHelper, 0o700); err != nil {
		t.Fatal(err)
	}
	serviceBinary := filepath.Join(root, "sing-box")
	if err := os.Symlink(realBinary, serviceBinary); err != nil {
		t.Fatal(err)
	}
	fakeSystemctl := filepath.Join(root, "fake-systemctl")
	script := "#!/bin/sh\nset -eu\nstate=" + shellQuote(stateDirectory) + "\ncommand=$1\nshift\nservice=$1\nshift\ncase \"$command\" in\n  is-active) value=$(cat \"$state/$service.active\"); printf '%s\\n' \"$value\"; [ \"$value\" = active ] ;;\n  is-enabled) value=$(cat \"$state/$service.enabled\"); printf '%s\\n' \"$value\"; [ \"$value\" = enabled ] ;;\n  show) property=ExecStart; for argument in \"$@\"; do case \"$argument\" in --property=*) property=${argument#--property=} ;; esac; done; case \"$property\" in ExecStart) if [ \"$service\" = qagent-sing-box.service ]; then cat \"$state/$service.managed-exec-start\"; else cat \"$state/$service.exec-start\"; fi ;; LoadState) cat \"$state/$service.load-state\" ;; FragmentPath) cat \"$state/$service.fragment-path\" ;; Description|User|Group) cat \"$state/$service.$(printf '%s' \"$property\" | tr '[:upper:]' '[:lower:]')\" ;; Type|WorkingDirectory|RootDirectory|RootImage|BindPaths|BindReadOnlyPaths|Environment|EnvironmentFiles|DropInPaths) cat \"$state/$service.$property\" ;; ExecCondition|ExecStartPre|ExecStartPost|ExecReload|ExecStop|ExecStopPost) cat \"$state/$service.$property\" ;; *) exit 1 ;; esac ;;\n  *) exit 1 ;;\nesac\n"
	if err := os.WriteFile(fakeSystemctl, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	previousSystemctl := systemctlPath
	previousCandidates := existingDiscoveryCandidates
	previousManagedUnitRoot := existingDiscoveryManagedUnitRoot
	systemctlPath = fakeSystemctl
	existingDiscoveryManagedUnitRoot = managedUnitDirectory
	existingDiscoveryCandidates = map[core.Engine]existingDiscoveryCandidateSet{
		core.EngineSingBox: {
			services:    []string{"sing-box.service", "singbox.service"},
			executables: []string{serviceBinary},
			configs:     []string{configPath},
		},
	}
	t.Cleanup(func() {
		systemctlPath = previousSystemctl
		existingDiscoveryCandidates = previousCandidates
		existingDiscoveryManagedUnitRoot = previousManagedUnitRoot
	})
	fixture := existingCoreDiscoveryFixture{
		root: root, stateDirectory: stateDirectory,
		discoveryStatePath: filepath.Join(agentStateDirectory, "agent-state.json.existing-cores"),
		markerPrefix:       filepath.Join(agentStateDirectory, "agent-state.json.core-migration"),
		realBinary:         realBinary, serviceBinary: serviceBinary,
		configPath: configPath, configDirectory: configDirectory,
		managedSpecs: map[core.Engine]EngineSpec{core.EngineSingBox: DefaultSpecs()[core.EngineSingBox]},
	}
	managedUnitPath := filepath.Join(managedUnitDirectory, "qagent-sing-box.service")
	managedSpec := DefaultSpecs()[core.EngineSingBox]
	managedUnitContents := strings.Join(managedCoreUnitLines(core.EngineSingBox, managedSpec), "\n") + "\n"
	if err := os.WriteFile(managedUnitPath, []byte(managedUnitContents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDirectory, "qagent-sing-box.service.load-state"), []byte("loaded\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDirectory, "qagent-sing-box.service.fragment-path"), []byte(managedUnitPath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDirectory, "qagent-sing-box.service.managed-exec-start"), []byte(systemdExecStart(
		DefaultSpecs()[core.EngineSingBox].Binary,
		DefaultSpecs()[core.EngineSingBox].Binary+" run -c "+DefaultSpecs()[core.EngineSingBox].ConfigPath,
	)), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"description": "sing-box core managed by QAgent\n",
		"user":        "qcontrolhub-core\n",
		"group":       "qcontrolhub-core\n",
	} {
		if err := os.WriteFile(filepath.Join(stateDirectory, "qagent-sing-box.service."+name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, hook := range []string{"ExecCondition", "ExecStartPre", "ExecStartPost", "ExecReload", "ExecStop", "ExecStopPost"} {
		if err := os.WriteFile(filepath.Join(stateDirectory, "qagent-sing-box.service."+hook), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for property, value := range map[string]string{
		"Type":              "simple\n",
		"WorkingDirectory":  "/var/lib/qcontrolhub-sing-box\n",
		"RootDirectory":     "\n",
		"RootImage":         "\n",
		"BindPaths":         "\n",
		"BindReadOnlyPaths": "\n",
		"Environment":       "\n",
		"EnvironmentFiles":  "\n",
		"DropInPaths":       "\n",
	} {
		if err := os.WriteFile(filepath.Join(stateDirectory, "qagent-sing-box.service."+property), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture.writeStatus(t, "sing-box.service", "active")
	fixture.writeStatus(t, "singbox.service", "inactive")
	fixture.writeStatus(t, "qagent-sing-box.service", "inactive")
	fixture.writeEnableState(t, "sing-box.service", "enabled")
	fixture.writeEnableState(t, "singbox.service", "disabled")
	fixture.writeEnableState(t, "qagent-sing-box.service", "disabled")
	fixture.writeExecStart(t, "sing-box.service", systemdExecStart(
		serviceBinary,
		serviceBinary+" run -c "+configPath+" -C "+configDirectory,
	))
	fixture.writeExecStart(t, "singbox.service", systemdExecStart(
		serviceBinary,
		serviceBinary+" run -c "+configPath+" -C "+configDirectory,
	))
	return fixture
}

func allowFixtureOrphanOwnerPath(t *testing.T, path string) {
	t.Helper()
	previous := installerOrphanOwnerExecutables
	allowed := make(map[string]struct{}, len(previous)+1)
	for candidate := range previous {
		allowed[candidate] = struct{}{}
	}
	allowed[filepath.Clean(path)] = struct{}{}
	installerOrphanOwnerExecutables = allowed
	t.Cleanup(func() {
		installerOrphanOwnerExecutables = previous
	})
}

func assignInactiveOrphanOwner(t *testing.T, path string) int {
	t.Helper()
	for candidate := 60000; candidate <= 60100; candidate++ {
		if err := os.Chown(path, candidate, candidate); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fileOwnerIsInactiveAndUnassigned(info) {
			return candidate
		}
	}
	t.Skip("could not find an inactive unassigned UID for the discovery fixture")
	return -1
}

func (fixture existingCoreDiscoveryFixture) writeStatus(t *testing.T, service, status string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, service+".active"), []byte(status+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (fixture existingCoreDiscoveryFixture) writeEnableState(t *testing.T, service, state string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, service+".enabled"), []byte(state+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (fixture existingCoreDiscoveryFixture) writeExecStart(t *testing.T, service, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.stateDirectory, service+".exec-start"), []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertDiscoveredSingBoxSpec(t *testing.T, spec EngineSpec, realBinary, serviceBinary, configPath, configDirectory, workDirectory string) {
	t.Helper()
	if spec.Binary != realBinary || spec.ServiceBinary != serviceBinary || spec.ConfigPath != configPath ||
		spec.ConfigDirectory != configDirectory || spec.WorkingDirectory != workDirectory || spec.Service != "sing-box.service" {
		t.Fatalf("discovered sing-box spec = %+v", spec)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
