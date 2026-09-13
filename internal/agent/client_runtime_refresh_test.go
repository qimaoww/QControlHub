package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestLifecycleResultsRequestImmediateRuntimeRefresh(t *testing.T) {
	for _, success := range []bool{true, false} {
		for _, action := range []core.Action{core.ActionInstall, core.ActionDeploy, core.ActionImportExisting, core.ActionStart, core.ActionStop, core.ActionRestart, core.ActionReadConfig, core.ActionValidate} {
			t.Run(fmt.Sprintf("%s/%t", action, success), func(t *testing.T) {
				client := &Client{executor: &Executor{}, runtimeRefresh: make(chan struct{}, 1), executeFunc: func(context.Context, core.Task) (string, error) {
					if !success {
						return "", errors.New("test failure")
					}
					return "done", nil
				}}
				outgoing := make(chan core.WireMessage, 2)
				task := core.Task{ID: "task-refresh", Action: action, Engine: core.EngineShadowsocksRust}
				client.executeTaskForSession(context.Background(), context.Background(), task, outgoing)
				result := <-outgoing
				if result.Type != core.WireResult || result.Result.Result.Success != success {
					t.Fatalf("bad result: %+v", result)
				}
				want := 1
				if action == core.ActionReadConfig || action == core.ActionValidate {
					want = 0
				}
				if len(client.runtimeRefresh) != want {
					t.Fatalf("refreshes=%d want=%d", len(client.runtimeRefresh), want)
				}
				client.executeTaskForSession(context.Background(), context.Background(), task, outgoing)
				if len(client.runtimeRefresh) != want {
					t.Fatal("refresh requests were not coalesced")
				}
			})
		}
	}
}

func TestPresetAutoInstallValidationRefreshesRuntime(t *testing.T) {
	for _, success := range []bool{false, true} {
		client := &Client{executor: &Executor{}, runtimeRefresh: make(chan struct{}, 1),
			config: ClientConfig{StatePath: filepath.Join(t.TempDir(), "agent-state.json")},
			executeFunc: func(context.Context, core.Task) (string, error) {
				if !success {
					return "", errors.New("validation failed after installation")
				}
				return "installed and validated", nil
			}}
		outgoing := make(chan core.WireMessage, 2)
		task := core.Task{ID: "auto-install-refresh", Action: core.ActionValidate, Engine: core.EngineXray, InstallIfMissing: true}
		client.executeTaskForSession(context.Background(), context.Background(), task, outgoing)
		if len(client.runtimeRefresh) != 1 {
			t.Fatal("compound validation did not refresh the installed state")
		}
		client.executeTaskForSession(context.Background(), context.Background(), task, outgoing)
		if len(client.runtimeRefresh) != 1 {
			t.Fatal("cached refresh was not coalesced")
		}
	}
}
