//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestManagedCorePrerequisitesAllEngines(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		t.Run(string(engine), func(t *testing.T) {
			root := t.TempDir()
			spec := EngineSpec{Binary: filepath.Join(root, "bin", "core"), ConfigPath: filepath.Join(root, "config", "config.json")}
			state := filepath.Join(root, "state")
			missing, err := managedCorePrerequisitesMissingAt(engine, spec, state)
			if err != nil || !missing {
				t.Fatalf("missing installation: %v, %v", missing, err)
			}
			for _, path := range []string{filepath.Dir(spec.Binary), filepath.Dir(spec.ConfigPath), state} {
				if err := os.Mkdir(path, 0o750); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(spec.ConfigPath, []byte("operator config"), 0o640); err != nil {
				t.Fatal(err)
			}
			if engine == core.EngineShadowsocksRust {
				if err := os.WriteFile(filepath.Join(filepath.Dir(spec.ConfigPath), "qch-mainland-block.acl"), []byte("operator acl"), 0o640); err != nil {
					t.Fatal(err)
				}
			}
			missing, err = managedCorePrerequisitesMissingAt(engine, spec, state)
			if err != nil || missing {
				t.Fatalf("complete installation: %v, %v", missing, err)
			}
			if err := os.Rename(spec.ConfigPath, spec.ConfigPath+".original"); err != nil {
				t.Fatal(err)
			}
			missing, err = managedCorePrerequisitesMissingAt(engine, spec, state)
			if err != nil || !missing {
				t.Fatal("partial bootstrap was not detected")
			}
			if err := os.Symlink(spec.ConfigPath+".original", spec.ConfigPath); err != nil {
				t.Fatal(err)
			}
			if _, err := managedCorePrerequisitesMissingAt(engine, spec, state); err == nil {
				t.Fatal("unsafe config treated as repairable")
			}
		})
	}
}

func TestOpenRCManagedDiscoveryAcceptsActiveAndInactiveCores(t *testing.T) {
	requireAgentRoot(t)
	root := t.TempDir()
	previous := openRCInitRoot
	openRCInitRoot = root
	t.Cleanup(func() { openRCInitRoot = previous })
	for _, active := range []bool{true, false} {
		for engine, spec := range DefaultSpecsForServiceManager(ServiceManagerOpenRC) {
			content, err := os.ReadFile(filepath.Join("../../deploy/openrc", spec.Service))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, spec.Service), content, 0o755); err != nil {
				t.Fatal(err)
			}
			helper := filepath.Join(root, "rc-service-helper")
			output := "#!/bin/sh\necho stopped\nexit 3\n"
			want := "inactive"
			if active {
				output = "#!/bin/sh\necho started\nexit 0\n"
				want = "active"
			}
			if err := os.WriteFile(helper, []byte(output), 0o755); err != nil {
				t.Fatal(err)
			}
			manager := &ServiceManager{kind: ServiceManagerOpenRC, executable: helper}
			status, err := validateManagedServiceForExistingDiscovery(context.Background(), engine, spec, manager)
			if err != nil || status != want {
				t.Fatalf("%s %s: %q %v", engine, want, status, err)
			}
		}
	}
}
