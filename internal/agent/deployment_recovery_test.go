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

func TestDeploymentRecoveryRestoresRuntimeState(t *testing.T) {
	const listening = `{"inbounds":[{"type":"socks","tag":"old","listen_port":23456}],"outbounds":[{"type":"direct","tag":"direct"}]}`
	for _, kind := range []string{ServiceManagerSystemd, ServiceManagerOpenRC} {
		for _, tc := range []struct {
			name, content, status, action string
		}{
			{"first deployment", "", "inactive", "stop"},
			{"empty previous config", `{"inbounds":[],"outbounds":[{"type":"direct"}]}`, "active", "stop"},
			{"stopped previous service", listening, "inactive", "stop"},
			{"running previous service", listening, "active", "restart"},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "config.json")
				if tc.content != "" {
					if _, err := atomicDeploy(path, tc.content); err != nil {
						t.Fatal(err)
					}
				}
				backup, err := atomicDeploy(path, listening)
				if err != nil {
					t.Fatal(err)
				}
				calls, state := filepath.Join(dir, "calls"), filepath.Join(dir, "state")
				if err = os.WriteFile(state, []byte("active"), 0600); err != nil {
					t.Fatal(err)
				}
				helper := filepath.Join(dir, "services")
				writeExecutable(t, helper, "#!/bin/sh\n"+
					"case \"$1 $2\" in *is-active*|*status*) cat '"+state+"'; [ \"$(cat '"+state+"')\" = active ]; exit ;; esac\n"+
					"printf '%s %s\\n' \"$1\" \"$2\" >> '"+calls+"'\n"+
					"case \"$1 $2\" in *stop*) echo inactive > '"+state+"' ;; *) echo active > '"+state+"' ;; esac\n")
				manager := &ServiceManager{kind: kind, executable: helper}
				output, err := recoverConfigurationDeployment(context.Background(), core.EngineSingBox, EngineSpec{Service: "qagent-sing-box.service", ConfigPath: path}, manager, backup, tc.status)
				if err != nil {
					t.Fatalf("recovery: %v\n%s", err, output)
				}
				data, _ := os.ReadFile(calls)
				if !strings.Contains(string(data), tc.action) || strings.Count(string(data), "\n") != 1 {
					t.Fatalf("wrong recovery action: %s", data)
				}
				data, err = os.ReadFile(path)
				if tc.content == "" {
					if !os.IsNotExist(err) || !strings.Contains(output, "removed newly created") {
						t.Fatalf("first deployment rollback: %s %v", output, err)
					}
				} else if err != nil || string(data) != tc.content {
					t.Fatalf("old content not restored: %s %v", data, err)
				}
			})
		}
	}
}

func TestFailedRollbackCannotConfirmOldDeploymentFromRestoredBytes(t *testing.T) {
	const oldContent = `{"inbounds":[{"type":"socks","tag":"old","listen_port":23456}],"outbounds":[{"type":"direct","tag":"direct"}]}`
	for _, kind := range []string{ServiceManagerSystemd, ServiceManagerOpenRC} {
		for _, rollbackFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/rollback-fails=%v", kind, rollbackFails), func(t *testing.T) {
				dir := t.TempDir()
				spec := EngineSpec{Binary: filepath.Join(dir, "core"), ConfigPath: filepath.Join(dir, "config.json"), Service: "qagent-sing-box.service"}
				writeExecutable(t, spec.Binary, "#!/bin/sh\nexit 0\n")
				if err := os.WriteFile(spec.ConfigPath, []byte(oldContent), 0600); err != nil {
					t.Fatal(err)
				}
				first := filepath.Join(dir, "first")
				helper := filepath.Join(dir, "services")
				restart := "if [ ! -e '" + first + "' ]; then touch '" + first + "'; exit 1; fi\nexit 0\n"
				if rollbackFails {
					restart = "exit 1\n"
				}
				writeExecutable(t, helper, "#!/bin/sh\ncase \"$1 $2\" in *is-active*|*status*) echo active; exit 0 ;; esac\n"+restart)
				manager := &ServiceManager{kind: kind, executable: helper}
				e := &Executor{Specs: map[core.Engine]EngineSpec{core.EngineSingBox: spec}, Services: manager}
				retained := core.Task{ID: "tsk_old", Engine: core.EngineSingBox, ConfigID: "cfg_old", ConfigVersion: 1, ConfigContent: oldContent}
				if err := e.commitSharedDeployment(spec, retained); err != nil {
					t.Fatal(err)
				}
				draft := retained
				draft.ID, draft.Action, draft.ConfigVersion = "tsk_failed", core.ActionDeploy, 2
				draft.ConfigContent = strings.ReplaceAll(oldContent, "23456", "23457")
				if _, err := e.Execute(context.Background(), draft); err == nil || strings.Contains(err.Error(), "rollback failed") != rollbackFails {
					t.Fatalf("unexpected apply/rollback result: %v", err)
				}
				actual, err := os.ReadFile(spec.ConfigPath)
				if err != nil || string(actual) != oldContent {
					t.Fatalf("old bytes were not restored: %s %v", actual, err)
				}
				observation := e.deploymentStatus(context.Background(), retained.Engine, spec, "")
				if (observation.ConfigID == "") != rollbackFails {
					t.Fatalf("incorrect identity after rollback: %+v", observation)
				}
				if err := e.identifyRetainedDeployment(context.Background(), spec, retained); (err != nil) != rollbackFails {
					t.Fatalf("refresh bypassed unresolved service application: %v", err)
				}
			})
		}
	}
}
