package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestExecutorRequiresIndependentEgressEvenForCustomSpecs(t *testing.T) {
	for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
		t.Run(string(action), func(t *testing.T) {
			dir := t.TempDir()
			path, marker := filepath.Join(dir, "config.yaml"), filepath.Join(dir, "executed")
			binary := filepath.Join(dir, "core")
			writeExecutable(t, binary, "#!/bin/sh\ntouch '"+marker+"'\n")
			if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			executor := &Executor{Specs: map[core.Engine]EngineSpec{
				core.EngineMihomo: {Binary: binary, ConfigPath: path, Service: "qch-test"},
			}}
			_, err := executor.Execute(context.Background(), core.Task{
				Engine: core.EngineMihomo, Action: action,
				ConfigContent: "mode: direct\nlisteners: [{name: a, type: http, port: 21001}]\n",
			})
			if err == nil || !strings.Contains(err.Error(), "独立出口校验失败") {
				t.Fatalf("task did not fail the independent-egress gate: %v", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("unsafe configuration reached the real core")
			}
			content, err := os.ReadFile(path)
			if err != nil || string(content) != "original" {
				t.Fatalf("failed validation changed deployed content: %q %v", content, err)
			}
		})
	}
}
