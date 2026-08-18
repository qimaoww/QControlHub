package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestProductionAgentUnitAllowsOnlyMetadataOwnershipCapability(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile("../../deploy/systemd/qagent.service")
	if err != nil {
		t.Fatal(err)
	}
	unit := string(contents)
	if !strings.Contains(unit, "CapabilityBoundingSet=CAP_CHOWN") || !strings.Contains(unit, "AmbientCapabilities=CAP_CHOWN") {
		t.Fatal("production Agent unit does not retain CAP_CHOWN for atomic metadata preservation")
	}
	if strings.Contains(unit, "CapabilityBoundingSet=CAP_CHOWN ") || strings.Contains(unit, "AmbientCapabilities=CAP_CHOWN ") {
		t.Fatal("production Agent unit grants capabilities beyond CAP_CHOWN")
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

func TestExecutorDryRunHonorsActionAndEngineWhitelist(t *testing.T) {
	t.Parallel()
	destination := filepath.Join(t.TempDir(), "config.yaml")
	executor := &Executor{
		DryRun: true,
		Specs: map[core.Engine]EngineSpec{
			core.EngineMihomo: {
				Binary:     "binary-that-must-not-be-invoked",
				ConfigPath: destination,
				Service:    "mihomo.service",
			},
		},
	}
	validConfig := "mixed-port: 7890\nproxies: []\n"
	for _, action := range []core.Action{
		core.ActionValidate,
		core.ActionDeploy,
		core.ActionStart,
		core.ActionStop,
		core.ActionRestart,
		core.ActionStatus,
		core.ActionInstall,
	} {
		t.Run(string(action), func(t *testing.T) {
			task := core.Task{
				Action:        action,
				Engine:        core.EngineMihomo,
				ConfigContent: validConfig,
			}
			if action == core.ActionInstall {
				task.CoreVersion = core.CoreVersionStable
			}
			output, err := executor.Execute(context.Background(), task)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !strings.Contains(output, "dry-run") {
				t.Fatalf("Execute() output = %q, want dry-run marker", output)
			}
		})
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("dry-run deployment touched destination: stat error = %v", err)
	}

	rejected := []core.Task{
		{Action: core.Action("restart; rm -rf /"), Engine: core.EngineMihomo},
		{Action: core.ActionStatus, Engine: core.Engine("mihomo;evil")},
		{Action: core.ActionStatus, Engine: core.EngineXray},
		{Action: core.ActionInstall, Engine: core.EngineMihomo, CoreVersion: "https://evil.example/core"},
	}
	for _, task := range rejected {
		if _, err := executor.Execute(context.Background(), task); err == nil {
			t.Fatalf("Execute() accepted non-whitelisted task: action=%q engine=%q", task.Action, task.Engine)
		}
	}
}

func TestExecutorDryRunStillValidatesDeploymentContent(t *testing.T) {
	t.Parallel()
	executor := &Executor{
		DryRun: true,
		Specs: map[core.Engine]EngineSpec{
			core.EngineXray: {Binary: "xray", ConfigPath: filepath.Join(t.TempDir(), "config.json"), Service: "xray"},
		},
	}
	if _, err := executor.Execute(context.Background(), core.Task{
		Action:        core.ActionDeploy,
		Engine:        core.EngineXray,
		ConfigContent: `{"inbounds":`,
	}); err == nil {
		t.Fatal("dry-run deployment accepted malformed configuration")
	}
}
