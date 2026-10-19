package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// This root-owned sidecar commits only after the configuration, process and
// TLS checks succeed. Draft saves never change it. Its digest detects local
// edits rather than attaching a stale panel version to an unrelated file.
type sharedDeploymentState struct {
	ConfigID      string `json:"config_id"`
	ConfigVersion int    `json:"config_version"`
	AppliedTaskID string `json:"applied_task_id"`
	TaskID        string `json:"task_id"`
	Digest        string `json:"digest"`
	Reason        string `json:"reason,omitempty"`
}

func sharedStatePath(spec EngineSpec) string {
	return filepath.Join(filepath.Dir(spec.ConfigPath), ".qch-instance.json")
}

func configurationDigest(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func readSharedDeploymentState(spec EngineSpec) (sharedDeploymentState, error) {
	var state sharedDeploymentState
	content, err := readConfigurationFile(sharedStatePath(spec))
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal([]byte(content), &state); err != nil {
		return state, err
	}
	if state.ConfigID == "" && (state.ConfigVersion != 0 || state.Digest != "") ||
		state.ConfigID != "" && (state.ConfigVersion < 1 || len(state.Digest) != 64) {
		return state, errors.New("invalid shared deployment identity")
	}
	return state, nil
}

func writeSharedDeploymentState(spec EngineSpec, state sharedDeploymentState) error {
	// Recording a rejected task must never prepare a new instance directory.
	// Only the authorized executor's preparation step may create that directory.
	directory, err := os.Lstat(filepath.Dir(spec.ConfigPath))
	if err != nil {
		return err
	}
	if !directory.IsDir() || directory.Mode()&os.ModeSymlink != 0 {
		return errors.New("instance metadata requires a prepared configuration directory")
	}
	content, err := json.Marshal(state)
	if err != nil {
		return err
	}
	backup, err := atomicDeploy(sharedStatePath(spec), string(content)+"\n")
	if err == nil && backup != "" {
		// This sidecar contains identity metadata only. Configuration backups
		// remain under the ordinary deployment recovery policy.
		_ = os.Remove(backup)
	}
	return err
}

func (e *Executor) commitSharedDeployment(spec EngineSpec, task core.Task) error {
	e.sharedStateMu.Lock()
	defer e.sharedStateMu.Unlock()
	return writeSharedDeploymentState(spec, sharedDeploymentState{
		ConfigID: task.ConfigID, ConfigVersion: task.ConfigVersion, AppliedTaskID: task.ID,
		TaskID: task.ID, Digest: configurationDigest(task.ConfigContent),
	})
}

func (e *Executor) recordSharedOperation(spec EngineSpec, task core.Task, resultErr error) {
	e.sharedStateMu.Lock()
	defer e.sharedStateMu.Unlock()
	state, err := readSharedDeploymentState(spec)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return
	}
	state.TaskID, state.Reason = task.ID, ""
	if resultErr != nil {
		state.Reason = limitStateValue(coreDiagnosticRedactor(task.Engine, task.ConfigContent)(resultErr.Error()), 1000)
	}
	_ = writeSharedDeploymentState(spec, state)
}

func rememberSharedStopReason(spec EngineSpec, reason string) {
	state, err := readSharedDeploymentState(spec)
	if err == nil {
		state.Reason = reason
		_ = writeSharedDeploymentState(spec, state)
	}
}

// Older deployed instances can acquire an identity on their first authorized
// start. The exact retained revision must reproduce the bytes already on disk.
func (e *Executor) verifySharedStart(ctx context.Context, spec EngineSpec, task core.Task) error {
	actual, err := readConfigurationFile(spec.ConfigPath)
	if err != nil {
		return fmt.Errorf("读取本人已部署配置失败，请重新部署: %w", err)
	}
	state, err := readSharedDeploymentState(spec)
	if errors.Is(err, os.ErrNotExist) {
		prepared, warning := e.prepareNativeAccountingContent(ctx, task.Engine, spec, task.ConfigContent)
		if warning != "" {
			return errors.New(warning)
		}
		prepared, err = serverconfig.SharedInstanceRuntimeContent(task.Engine, prepared)
		if err != nil || prepared != actual {
			return errors.New("旧实例文件与已部署版本不一致，请保存并部署本人配置以恢复版本关联")
		}
		task.ConfigContent = actual
		if err := e.commitSharedDeployment(spec, task); err != nil {
			return err
		}
		state, err = readSharedDeploymentState(spec)
	}
	if err != nil {
		return fmt.Errorf("读取实例版本标识失败，请重新部署: %w", err)
	}
	if state.ConfigID != task.ConfigID || state.ConfigVersion != task.ConfigVersion || state.Digest != configurationDigest(actual) {
		return errors.New("实例文件与服务器已部署版本不一致，请重新部署本人配置")
	}
	if _, err := serverconfig.SharedTrafficEndpoints(task.Engine, actual); err != nil {
		return err
	}
	return nil
}

func (e *Executor) sharedInstanceStatus(ctx context.Context, engine core.Engine, shareID string) core.SharedInstanceStatus {
	result := core.SharedInstanceStatus{Engine: engine, ShareID: shareID, Status: "unknown"}
	base := DefaultSpecsForServiceManager(e.serviceManager().Kind())[engine]
	spec, err := sharedInstanceSpec(engine, base, shareID, e.serviceManager().Kind())
	if err != nil {
		result.Reason = "实例标识无效"
		return result
	}
	result.Status, err = serviceStatusWithManager(ctx, e.serviceManager(), spec.Service)
	if err != nil {
		result.Status, result.Reason = "unknown", "实例服务状态查询失败"
	}
	if state, stateErr := readSharedDeploymentState(spec); stateErr == nil {
		result.TaskID = state.TaskID
		if err == nil {
			result.Reason = state.Reason
		}
		if content, readErr := readConfigurationFile(spec.ConfigPath); readErr == nil && configurationDigest(content) == state.Digest {
			result.ConfigID, result.ConfigVersion, result.AppliedTaskID = state.ConfigID, state.ConfigVersion, state.AppliedTaskID
		} else if result.Reason == "" {
			result.Reason = "实例配置文件与部署版本不一致，请重新部署"
		}
	}
	result.ObservedAt = time.Now().UTC()
	return result
}
