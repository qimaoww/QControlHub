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

func TestManagedInstancesAreExcludedFromExternalSourcesAndCache(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "discovery.json")
	state := existingCoreDiscoveryState{Version: existingCoreDiscoveryStateVersion,
		Sources: map[core.Engine][]existingDiscoverySpec{}, Specs: map[core.Engine]existingDiscoverySpec{}}
	external := EngineSpec{Binary: "/srv/external/core", ConfigPath: "/srv/external/config.json", Service: "external.service"}
	for _, kind := range []string{ServiceManagerSystemd, ServiceManagerOpenRC} {
		for engine, base := range DefaultSpecsForServiceManager(kind) {
			shared, err := sharedInstanceSpec(engine, base, "shr_"+strings.Repeat("a", 16), kind)
			if err != nil {
				t.Fatal(err)
			}
			for _, spec := range []EngineSpec{base, shared, {Binary: external.Binary, ConfigPath: shared.ConfigPath, Service: "renamed.service"}, {Binary: external.Binary, ConfigDirectory: filepath.Dir(shared.ConfigPath), Service: "renamed-directory.service"}} {
				if err := validateExistingSpecPaths(engine, spec); err == nil {
					t.Fatalf("managed source accepted: %+v", spec)
				}
				if _, err := (&Executor{}).readExistingConfigPlan(context.Background(), engine, base, spec); err == nil || !strings.Contains(err.Error(), "managed core instances") {
					t.Fatalf("managed source reached external reads: %v", err)
				}
				state.Sources[engine] = append(state.Sources[engine], discoverySpecFromEngineSpec(spec))
			}
			state.Specs[engine] = discoverySpecFromEngineSpec(shared)
		}
	}
	state.Sources[core.EngineXray] = append(state.Sources[core.EngineXray], discoverySpecFromEngineSpec(external))
	if err := saveExistingCoreDiscoveryState(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadExistingCoreDiscoveryState(path)
	if err != nil || len(loaded.Specs) != 0 || len(loaded.Sources) != 1 || len(loaded.Sources[core.EngineXray]) != 1 || loaded.Sources[core.EngineXray][0].engineSpec() != external {
		t.Fatalf("managed cache was not pruned without losing external sources: %+v %v", loaded, err)
	}
	executor := &Executor{ExistingSpecs: map[core.Engine]EngineSpec{}, ExistingSources: map[core.Engine][]EngineSpec{}}
	for engine, values := range state.Sources {
		for _, value := range values {
			executor.ExistingSources[engine] = append(executor.ExistingSources[engine], value.engineSpec())
		}
		executor.ExistingSpecs[engine] = state.Specs[engine].engineSpec()
		want := 0
		if engine == core.EngineXray {
			want = 1
		}
		if sources := executor.existingSources(engine); len(sources) != want {
			t.Fatalf("%s pending import sources: %+v", engine, sources)
		}
	}
	// A stale single-source mapping must not reappear through the fallback.
	executor.ExistingSources = nil
	if sources := executor.existingSources(core.EngineXray); len(sources) != 0 {
		t.Fatalf("managed fallback exposed: %+v", sources)
	}
	executor.Specs = map[core.Engine]EngineSpec{}
	executor.Services = &ServiceManager{kind: ServiceManagerSystemd, executable: filepath.Join(root, "absent-helper")}
	for engine := range state.Specs {
		executor.Specs[engine] = EngineSpec{Binary: filepath.Join(root, "absent-core"), Service: "fixture-managed.service"}
	}
	for engine, runtime := range executor.Runtime(context.Background()) {
		if runtime.ExistingConfigAvailable || len(runtime.ExistingSources) != 0 {
			t.Fatalf("%s runtime advertises a managed instance for import: %+v", engine, runtime)
		}
	}
	if err := validateExistingSpecPaths(core.EngineXray, external); err != nil {
		t.Fatalf("external source was blocked: %v", err)
	}
}

func TestSystemdManagedAliasesAreSkippedBeforeReadingTheirUnits(t *testing.T) {
	root := t.TempDir()
	helper, marker := filepath.Join(root, "systemctl"), filepath.Join(root, "unit-read")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
 list-unit-files) printf 'alias.service enabled\nqagent-xray@shr_test.service enabled\n' ;;
 show) printf 'qagent-xray@shr_test.service' ;;
 *) touch %s; exit 1 ;;
esac
`, shellQuote(marker))
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	previous := systemctlPath
	systemctlPath = helper
	t.Cleanup(func() { systemctlPath = previous })
	sources, issues, err := discoverAllInstalledCoreSources(context.Background(), defaultSystemdServiceManager())
	if err != nil || len(sources) != 0 || len(issues) != 0 {
		t.Fatalf("managed alias discovery: %+v %+v %v", sources, issues, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("discovery read a managed unit through its alias: %v", err)
	}
}

func TestRenamedManagedServicesAreNotExternalWithRealCores(t *testing.T) {
	bin := os.Getenv("QCH_PERMISSION_BIN")
	if bin == "" {
		t.Skip("requires verified official cores")
	}
	requireAgentRoot(t)
	root := t.TempDir()
	units := filepath.Join(root, "init.d")
	if err := os.Mkdir(units, 0o700); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(root, "systemctl")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
 list-unit-files) for name in mihomo xray sing-box shadowsocks-rust; do printf 'renamed-%%s.service enabled\n' "$name"; done ;;
 show) case "$3" in --property=Id) printf '%%s' "$2" ;; --property=WorkingDirectory) printf / ;; --property=MainPID) printf 0 ;; esac ;;
 cat) cat %s/"$2" ;;
 *) exit 1 ;;
esac
`, shellQuote(units))
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	for engine, base := range DefaultSpecs() {
		shared, err := sharedInstanceSpec(engine, base, "shr_"+strings.Repeat("a", 16), ServiceManagerSystemd)
		if err != nil {
			t.Fatal(err)
		}
		binary := filepath.Join(bin, filepath.Base(base.Binary))
		args := "-c " + shared.ConfigPath
		if engine == core.EngineMihomo {
			args = "-f " + shared.ConfigPath
		} else if engine != core.EngineShadowsocksRust {
			args = "run " + args
		}
		service := "renamed-" + string(engine)
		unit := fmt.Sprintf("[Service]\nExecStart=%s %s\n", binary, args)
		if err := os.WriteFile(filepath.Join(units, service+".service"), []byte(unit), 0o600); err != nil {
			t.Fatal(err)
		}
		openrc := fmt.Sprintf("#!/sbin/openrc-run\ncommand=%q\ncommand_args=%q\n", binary, args)
		if err := os.WriteFile(filepath.Join(units, service), []byte(openrc), 0o700); err != nil {
			t.Fatal(err)
		}
		managedScript := filepath.Join(units, "qagent-"+string(engine))
		marked := "#!/sbin/openrc-run\n# QControlHub managed OpenRC service: qagent-" + string(engine) + "\n" + openrc
		if err := os.WriteFile(managedScript, []byte(marked), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(managedScript, filepath.Join(units, "alias-"+string(engine))); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(units, "copied-"+string(engine)), []byte(marked), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	previous, previousInit := systemctlPath, openRCInitRoot
	systemctlPath, openRCInitRoot = helper, units
	t.Cleanup(func() { systemctlPath, openRCInitRoot = previous, previousInit })
	for _, manager := range []*ServiceManager{defaultSystemdServiceManager(), {kind: ServiceManagerOpenRC, executable: "/missing-helper"}} {
		sources, issues, err := discoverAllInstalledCoreSources(context.Background(), manager)
		if err != nil || len(sources) != 0 || len(issues) != 0 {
			t.Fatalf("renamed managed services in %s: %+v %+v %v", manager.Kind(), sources, issues, err)
		}
	}
}
