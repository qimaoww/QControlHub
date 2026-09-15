package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestUnsupportedExistingServiceBlocksEveryCoreAction(t *testing.T) {
	t.Parallel()
	reason := "multiple active sing-box services were detected"
	executor := &Executor{
		Specs: map[core.Engine]EngineSpec{
			core.EngineSingBox: {Binary: "/unused/sing-box", ConfigPath: "/unused/config.json", Service: "qagent-sing-box.service"},
		},
		ExistingDiscoveryIssues: map[core.Engine]string{core.EngineSingBox: reason},
	}
	for _, action := range []core.Action{
		core.ActionValidate, core.ActionDeploy, core.ActionStart, core.ActionStop,
		core.ActionRestart, core.ActionStatus, core.ActionInstall, core.ActionReadConfig, core.ActionReadManagedConfig,
		core.ActionImportExisting,
	} {
		t.Run(string(action), func(t *testing.T) {
			_, err := executor.Execute(context.Background(), core.Task{
				Action: action, Engine: core.EngineSingBox, ConfigContent: `{"inbounds":[]}`,
			})
			if err == nil || !strings.Contains(err.Error(), "core tasks are disabled") || !strings.Contains(err.Error(), reason) {
				t.Fatalf("Execute(%s) error = %v", action, err)
			}
		})
	}
}

func TestServiceVerificationRejectsTransientActiveState(t *testing.T) {
	t.Parallel()
	statuses := []string{"active", "active", "failed"}
	index := 0
	status, err := waitForServiceState(context.Background(), "active", 20*time.Millisecond, time.Millisecond, func(context.Context) (string, error) {
		if index < len(statuses)-1 {
			value := statuses[index]
			index++
			return value, nil
		}
		return statuses[len(statuses)-1], nil
	})
	if err != nil || status != "failed" {
		t.Fatalf("transient active verification = %q, %v; want failed", status, err)
	}
}

func TestServiceVerificationRequiresStableActiveState(t *testing.T) {
	t.Parallel()
	status, err := waitForServiceState(context.Background(), "active", 5*time.Millisecond, time.Millisecond, func(context.Context) (string, error) {
		return "active", nil
	})
	if err != nil || status != "active" {
		t.Fatalf("stable active verification = %q, %v", status, err)
	}
}

func TestExecutorRejectsUnsafeTasksAndPaths(t *testing.T) {
	t.Parallel()
	executor := &Executor{
		Specs: map[core.Engine]EngineSpec{
			core.EngineMihomo: {
				Binary:     "relative-binary",
				ConfigPath: filepath.Join(t.TempDir(), "config.yaml"),
				Service:    "qagent-mihomo.service",
			},
		},
	}
	rejected := []core.Task{
		{Action: core.Action("restart; rm -rf /"), Engine: core.EngineMihomo},
		{Action: core.ActionStatus, Engine: core.Engine("mihomo;evil")},
		{Action: core.ActionStatus, Engine: core.EngineXray},
		{Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: "https://evil.example/core"},
		{Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: core.CoreVersionStable, CoreSource: string(core.CoreSourceMirror)},
		{Action: core.ActionInstall, Engine: core.EngineXray, CoreVersion: core.CoreVersionDevelopment, CoreSource: string(core.CoreSourceMirror)},
		{Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: core.CoreVersionDevelopment, CoreSource: "private"},
	}
	for _, task := range rejected {
		if _, err := executor.Execute(context.Background(), task); err == nil {
			t.Fatalf("Execute() accepted non-whitelisted task: action=%q engine=%q", task.Action, task.Engine)
		}
	}
}
