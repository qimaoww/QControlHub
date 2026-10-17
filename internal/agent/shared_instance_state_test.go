package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
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
