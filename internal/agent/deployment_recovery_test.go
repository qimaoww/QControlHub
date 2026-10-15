package agent

import (
	"context"
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
