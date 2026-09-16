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
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestWireGuardDeployValidationAndRestartRollback(t *testing.T) {
	requireAgentRoot(t)
	if err := checkOutboundMarkRouting(context.Background()); err != nil {
		t.Skipf("outbound mark routing prerequisite: %v", err)
	}
	for _, scenario := range []string{"missing gvisor", "native check failure", "restart failure"} {
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			spec := EngineSpec{
				Binary: filepath.Join(directory, "sing-box"), ConfigPath: filepath.Join(directory, "config.json"),
				Service: "qagent-sing-box.service",
			}
			protocol, _ := serverconfig.FindProtocol(core.EngineSingBox, serverconfig.ProtocolWireGuard)
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.Tag, input.Port = "wg-old", 51820
			original, err := serverconfig.Generate(core.EngineSingBox, input)
			if err != nil {
				t.Fatal(err)
			}
			originalPlan, err := serverconfig.PrepareIndependentEgress(core.EngineSingBox, original)
			if err != nil {
				t.Fatal(err)
			}
			original = originalPlan.Content
			if err := os.WriteFile(spec.ConfigPath, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := writeManagedConfigFiles(core.EngineSingBox, spec.ConfigPath, original, fileMetadata{mode: 0o600}); err != nil {
				t.Fatal(err)
			}
			input.Tag, input.Port = "wg-new", 51821
			replacement, err := serverconfig.Generate(core.EngineSingBox, input)
			if err != nil {
				t.Fatal(err)
			}
			tags, checkExit := "with_wireguard,with_gvisor", 0
			if scenario == "missing gvisor" {
				tags = "with_wireguard"
			}
			if scenario == "native check failure" {
				checkExit = 1
			}
			coreScript := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\nversion) printf 'sing-box version 1.14.0\\nTags: %s\\n'; exit 0;;\ncheck) exit %d;;\nesac\nexit 1\n", tags, checkExit)
			if err := os.WriteFile(spec.Binary, []byte(coreScript), 0o700); err != nil {
				t.Fatal(err)
			}
			servicePath, serviceLog := filepath.Join(directory, "service-helper"), filepath.Join(directory, "service.log")
			failedSnapshot := filepath.Join(directory, "failed-config.json")
			serviceScript := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$1" >> %q
case "$1" in
restart)
  if grep -q '"tag": "wg-new"' %q; then
    cp %q %q
    exit 1
  fi
  exit 0;;
is-active) echo active; exit 0;;
esac
exit 1
`, serviceLog, spec.ConfigPath, spec.ConfigPath, failedSnapshot)
			if err := os.WriteFile(servicePath, []byte(serviceScript), 0o700); err != nil {
				t.Fatal(err)
			}
			executor := &Executor{
				Specs:    map[core.Engine]EngineSpec{core.EngineSingBox: spec},
				Services: &ServiceManager{kind: ServiceManagerSystemd, executable: servicePath},
			}
			output, deployErr := executor.Execute(context.Background(), core.Task{
				Action: core.ActionDeploy, Engine: core.EngineSingBox, ConfigContent: replacement,
			})
			if deployErr == nil {
				t.Fatal("injected failure did not fail deployment")
			}
			assertFileContentAndMode(t, spec.ConfigPath, original, 0o600)
			// A rollback must reuse the exact previous immutable bundle.
			if err := writeManagedConfigFiles(core.EngineSingBox, spec.ConfigPath, original, fileMetadata{mode: 0o600}); err != nil {
				t.Fatalf("previous bundle was damaged: %v", err)
			}
			commands, serviceErr := os.ReadFile(serviceLog)
			backups, err := filepath.Glob(spec.ConfigPath + ".bak-*")
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "restart failure" {
				if !strings.Contains(deployErr.Error(), "previous configuration was restored") ||
					strings.Count(string(commands), "restart\n") != 2 || len(backups) != 0 {
					t.Fatalf("restart did not recover the previous revision: %v\n%s\n%s", deployErr, commands, output)
				}
				failed, err := os.ReadFile(failedSnapshot)
				if err != nil || !strings.Contains(string(failed), `"tag": "wg-new"`) || !strings.Contains(string(failed), "qch-trf-51821-") {
					t.Fatalf("restart did not see the prepared replacement configuration: %v", err)
				}
			} else {
				if !os.IsNotExist(serviceErr) || len(backups) != 0 {
					t.Fatalf("failed validation changed service or runtime backups: %v %s", serviceErr, commands)
				}
				if scenario == "missing gvisor" && !strings.Contains(deployErr.Error(), "with_gvisor") {
					t.Fatalf("capability check was not reached: %v", deployErr)
				}
			}
		})
	}
}
