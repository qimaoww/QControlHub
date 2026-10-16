//go:build linux

package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

const legacyEmptySingBoxAccounting = `{"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"}],"experimental":{"v2ray_api":{"listen":"127.0.0.1:10086","stats":{"enabled":true,"inbounds":["failed"],"outbounds":[]}}}}`

func TestSingBoxEmptyAccountingRemovesLegacyAPI(t *testing.T) {
	executor := &Executor{}
	prepared, warning := executor.prepareNativeAccountingContent(context.Background(), core.EngineSingBox, EngineSpec{}, legacyEmptySingBoxAccounting)
	if warning != "" || strings.Contains(prepared, "v2ray_api") {
		t.Fatalf("empty accounting requires optional API build tag: %s %s", prepared, warning)
	}
	for _, shared := range []bool{false, true} {
		runtime := prepared
		if shared {
			var err error
			runtime, err = serverconfig.SharedInstanceRuntimeContent(core.EngineSingBox, runtime)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := serverconfig.ValidateIndependentEgress(core.EngineSingBox, runtime); err != nil {
			t.Fatal(err)
		}
		if endpoints := serverconfig.DiscoverTrafficPorts(core.EngineSingBox, runtime); len(endpoints) != 0 {
			t.Fatalf("cleanup opened ingress: %+v", endpoints)
		}
	}
}

func TestSingBoxEmptyAccountingTaskValidation(t *testing.T) {
	root := os.Getenv("QCH_PERMISSION_BIN")
	if root == "" {
		t.Skip("requires verified official sing-box binary")
	}
	requireAgentRoot(t)
	// This fixture uses a unique temporary configuration and no managed TLS
	// state. Refuse to interact with a real host's shared frontend registry.
	if managedTLSStateExists() {
		t.Skip("requires a disposable host without existing managed TLS state")
	}
	binary := filepath.Join(root, "sing-box")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(legacyEmptySingBoxAccounting), 0o600); err != nil {
		t.Fatal(err)
	}
	version, err := exec.Command(binary, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("inspect official binary: %v %s", err, version)
	}
	if !strings.Contains(string(version), "with_v2ray_api") {
		output, err := exec.Command(binary, "check", "-c", path).CombinedOutput()
		if err == nil || !strings.Contains(string(output), "with_v2ray_api") {
			t.Fatalf("old empty task did not reproduce missing API build: %v %s", err, output)
		}
	}
	executor := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineSingBox: {
		Binary: binary, ConfigPath: path, Service: "qch-empty-sing-box.service",
	}}}
	output, err := executor.Execute(context.Background(), core.Task{
		Action: core.ActionValidate, Engine: core.EngineSingBox, ConfigContent: legacyEmptySingBoxAccounting,
	})
	if err != nil {
		t.Fatalf("official sing-box rejected repaired legacy task: %v %s", err, output)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != legacyEmptySingBoxAccounting {
		t.Fatal("validation changed the installed configuration")
	}
}
