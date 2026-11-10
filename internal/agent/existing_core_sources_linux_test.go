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

func TestInstalledSourcesUseEffectiveContextAndKeepStoppedInstances(t *testing.T) {
	bin := os.Getenv("QCH_PERMISSION_BIN")
	if bin == "" {
		t.Skip("requires verified official cores")
	}
	requireAgentRoot(t)
	root := t.TempDir()
	units := filepath.Join(root, "units")
	if err := os.Mkdir(units, 0o700); err != nil {
		t.Fatal(err)
	}
	environment := filepath.Join(root, "环境 文件")
	config := filepath.Join(root, "配置 with spaces.json")
	if err := os.WriteFile(environment, []byte(fmt.Sprintf("CONFIG=%q\n", config)), 0o600); err != nil {
		t.Fatal(err)
	}
	engines := map[string]core.Engine{"custom-a.service": core.EngineMihomo, "custom-b.service": core.EngineXray, "custom-c.service": core.EngineSingBox, "custom-d.service": core.EngineShadowsocksRust, "custom-e.service": core.EngineXray}
	for service, engine := range engines {
		name := string(engine)
		if engine == core.EngineShadowsocksRust {
			name = "ssserver"
		}
		data, err := os.ReadFile(filepath.Join(bin, name))
		if err != nil {
			t.Fatal(err)
		}
		// Identification must use the real executable, including a renamed Rust core.
		binaryPath := filepath.Join(root, name)
		if engine == core.EngineShadowsocksRust {
			binaryPath = filepath.Join(root, "renamed-rust-core")
		}
		if err := os.WriteFile(binaryPath, data, 0o755); err != nil {
			t.Fatal(err)
		}
		args := `run -c "${CONFIG}"`
		if engine == core.EngineMihomo {
			args = `-f "${CONFIG}" -d ` + fmt.Sprintf("%q", root)
		}
		if engine == core.EngineShadowsocksRust {
			args = `-c "${CONFIG}"`
		}
		unit := fmt.Sprintf("[Service]\nExecStart=/obsolete/core -c /obsolete/config\n[Service]\nExecStart=\nExecStart=%q %s\n", binaryPath, args)
		if err := os.WriteFile(filepath.Join(units, service), []byte(unit), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	helper := filepath.Join(root, "systemctl")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
 list-unit-files) printf 'custom-a.service enabled\ncustom-b.service disabled\ncustom-c.service disabled\ncustom-d.service disabled\ncustom-e.service disabled\nalias-b.service enabled\n' ;;
 cat) cat %q/"$2" ;;
 show)
  case "$3" in
   --property=Id) if [ "$2" = alias-b.service ]; then printf custom-b.service; else printf '%%s' "$2"; fi ;;
   --property=WorkingDirectory) printf '%%s' %q ;;
   --property=EnvironmentFiles) printf '%%s (ignore_errors=no)' '"%s"' ;;
   --property=MainPID) printf 0 ;;
  esac ;;
 *) exit 1 ;;
esac
`, units, root, environment)
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	previous := systemctlPath
	systemctlPath = helper
	t.Cleanup(func() { systemctlPath = previous })
	manager, _ := NewServiceManager(ServiceManagerSystemd)
	sources, issues, err := discoverAllInstalledCoreSources(context.Background(), manager)
	if err != nil || len(issues) != 0 {
		t.Fatalf("discovery: %v %+v", err, issues)
	}
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		want := 1
		if engine == core.EngineXray {
			want = 2
		}
		if len(sources[engine]) != want {
			t.Fatalf("%s: candidates=%+v", engine, sources[engine])
		}
		for _, source := range sources[engine] {
			if source.ConfigPath != config || !strings.Contains(source.SourceContext, "unit_digest") {
				t.Fatalf("lost effective environment/context: %+v", source)
			}
		}
	}
	before := existingSourceID(sources[core.EngineXray][0])
	if err := os.WriteFile(environment, []byte("CONFIG=/srv/replaced.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, changed, err := inspectSystemdCoreSource(context.Background(), "custom-b.service")
	if err != nil || changed.ConfigPath != "/srv/replaced.json" || existingSourceID(changed) == before {
		t.Fatalf("stale environment source retained: %+v %v", changed, err)
	}
}

func TestSystemdEnvironmentAndSourceProcessIdentity(t *testing.T) {
	values, err := parseSystemdEnvironmentFile("A=/srv/path  with  spaces\nB='literal $VALUE'\nC=\"quoted \\\"name\\\"\"\n")
	if err != nil || values["A"] != "/srv/path  with  spaces" || values["B"] != "literal $VALUE" || values["C"] != "quoted \"name\"" {
		t.Fatalf("environment values: %#v %v", values, err)
	}
	root := t.TempDir()
	proc := filepath.Join(root, "123")
	if err := os.Mkdir(proc, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(proc, "cwd")); err != nil {
		t.Fatal(err)
	}
	status := filepath.Join(proc, "status")
	if err := os.WriteFile(status, []byte("Uid:\t0 0 0 0\nGid:\t0 0 0 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := openRCProcRoot
	openRCProcRoot = root
	t.Cleanup(func() { openRCProcRoot = previous })
	if err := verifyExistingProcessContext(123, existingServiceContext{}, root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(status, []byte("Uid:\t0 1000 0 0\nGid:\t0 0 0 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyExistingProcessContext(123, existingServiceContext{}, root); err == nil {
		t.Fatal("process identity drift accepted")
	}
}
