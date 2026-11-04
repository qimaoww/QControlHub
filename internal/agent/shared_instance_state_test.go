package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestSharedDeploymentIdentityRetainsAppliedRevisionAfterFailure(t *testing.T) {
	spec := EngineSpec{ConfigPath: filepath.Join(t.TempDir(), "config.json")}
	content := `{"inbounds":[{"tag":"own","protocol":"http","port":24101}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`
	if err := os.WriteFile(spec.ConfigPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &Executor{}
	deployed := core.Task{ID: "tsk_deployed", Engine: core.EngineXray, ConfigID: "cfg_alice", ConfigVersion: 1, ConfigContent: content}
	if err := executor.commitSharedDeployment(spec, deployed); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sharedStatePath(spec))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("identity permissions: %v %v", info, err)
	}
	failed := deployed
	failed.ID = "tsk_failed"
	failed.ConfigVersion = 2
	executor.recordSharedOperation(spec, failed, errors.New("实例启动失败，已恢复旧配置"))
	state, err := readSharedDeploymentState(spec)
	if err != nil || state.ConfigVersion != 1 || state.AppliedTaskID != deployed.ID || state.TaskID != failed.ID || state.Digest != configurationDigest(content) || !strings.Contains(state.Reason, "启动失败") {
		t.Fatalf("failed attempt replaced applied identity: %+v %v", state, err)
	}
	if err := executor.verifySharedStart(context.Background(), spec, deployed); err != nil {
		t.Fatal(err)
	}
	if err := executor.verifySharedStart(context.Background(), spec, failed); err == nil {
		t.Fatal("start accepted draft v2 over deployed v1")
	}
	if err := os.WriteFile(spec.ConfigPath, []byte(strings.ReplaceAll(content, "24101", "24102")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := executor.verifySharedStart(context.Background(), spec, deployed); err == nil {
		t.Fatal("start attributed a locally modified file to the saved revision")
	}
}

func TestRejectedSharedAttemptDoesNotCreateInstance(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "unprepared")
	executor := &Executor{}
	executor.recordSharedOperation(EngineSpec{ConfigPath: filepath.Join(directory, "config.json")}, core.Task{ID: "tsk_rejected"}, errors.New("quota exhausted"))
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected task prepared an instance: %v", err)
	}
}

func TestStatusIdentifiesRetainedDeploymentWithoutApplyingDraft(t *testing.T) {
	for _, engine := range core.AllEngines() {
		for _, shared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared=%v", engine, shared), func(t *testing.T) {
				protocol, ok := serverconfig.FindProtocol(engine, serverconfig.ProtocolSS2022)
				if !ok {
					t.Fatal("missing preset")
				}
				input, err := serverconfig.NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				input.Port = 32411
				content, err := serverconfig.Generate(engine, input)
				if err != nil {
					t.Fatal(err)
				}
				if shared {
					content, err = serverconfig.SharedInstanceRuntimeContent(engine, content)
					if err != nil {
						t.Fatal(err)
					}
				}
				spec := EngineSpec{ConfigPath: filepath.Join(t.TempDir(), "config")}
				if err := os.WriteFile(spec.ConfigPath, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
				e := &Executor{}
				retained := core.Task{ID: "tsk_status", Engine: engine, Action: core.ActionStatus, ConfigID: "cfg_retained", ConfigVersion: 1, ConfigContent: content, SharedInstance: shared}
				if err := e.identifyRetainedDeployment(context.Background(), spec, retained); err != nil {
					t.Fatal(err)
				}
				state, err := readSharedDeploymentState(spec)
				if err != nil || state.ConfigID != retained.ConfigID || state.ConfigVersion != 1 || state.Digest != configurationDigest(content) {
					t.Fatalf("retained identity not acquired: %+v %v", state, err)
				}
				draft := retained
				draft.ConfigVersion++
				if err := e.identifyRetainedDeployment(context.Background(), spec, draft); err == nil {
					t.Fatal("refresh reported success for a different retained revision")
				}
				changed := strings.ReplaceAll(content, "32411", "32412")
				if err := os.WriteFile(spec.ConfigPath, []byte(changed), 0600); err != nil {
					t.Fatal(err)
				}
				if err := e.identifyRetainedDeployment(context.Background(), spec, retained); err == nil {
					t.Fatal("modified file acquired a committed identity")
				}
				if err := os.Remove(sharedStatePath(spec)); err != nil {
					t.Fatal(err)
				}
				if err := e.identifyRetainedDeployment(context.Background(), spec, retained); err == nil {
					t.Fatal("unrelated legacy file acquired retained identity")
				}
				actual, err := os.ReadFile(spec.ConfigPath)
				if err != nil || string(actual) != changed {
					t.Fatal("status rewrote the running configuration")
				}
			})
		}
	}
}
