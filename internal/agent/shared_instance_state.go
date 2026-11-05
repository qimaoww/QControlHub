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
	Applying      bool   `json:"applying,omitempty"`
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
	if err == nil && state.Applying {
		return errors.New("实例部署尚未核实，请重新部署已确认的配置")
	}
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
	base := DefaultSpecsForServiceManager(e.serviceManager().Kind())[engine]
	spec, err := sharedInstanceSpec(engine, base, shareID, e.serviceManager().Kind())
	if err != nil {
		return core.SharedInstanceStatus{Engine: engine, ShareID: shareID, Status: "unknown", Reason: "实例标识无效"}
	}
	return e.deploymentStatus(ctx, engine, spec, shareID)
}

func (e *Executor) deploymentStatus(ctx context.Context, engine core.Engine, spec EngineSpec, shareID string) core.SharedInstanceStatus {
	result := core.SharedInstanceStatus{Engine: engine, ShareID: shareID, Status: "unknown"}
	var err error
	result.Status, err = serviceStatusWithManager(ctx, e.serviceManager(), spec.Service)
	if err != nil {
		result.Status, result.Reason = "unknown", "实例服务状态查询失败"
	}
	if state, stateErr := readSharedDeploymentState(spec); stateErr == nil {
		result.TaskID = state.TaskID
		if err == nil {
			result.Reason = state.Reason
		}
		if content, readErr := readConfigurationFile(spec.ConfigPath); !state.Applying && readErr == nil && configurationDigest(content) == state.Digest {
			result.ConfigID, result.ConfigVersion, result.AppliedTaskID = state.ConfigID, state.ConfigVersion, state.AppliedTaskID
		} else if result.Reason == "" {
			result.Reason = "实例配置文件与部署版本不一致，请重新部署"
		}
	}
	result.ObservedAt = time.Now().UTC()
	return result
}

// A status task may bootstrap pre-sidecar deployments, but only by reproducing
// the server's retained deployment, never its newer saved draft. This performs
// no service operation and never rewrites the configuration.
func (e *Executor) identifyRetainedDeployment(ctx context.Context, spec EngineSpec, task core.Task) error {
	if task.ConfigID == "" || task.ConfigVersion < 1 || task.ConfigContent == "" {
		return nil
	}
	unverified := errors.New("运行配置尚未核实，请在对应实例中重新部署已确认的配置")
	actual, err := readConfigurationFile(spec.ConfigPath)
	if err != nil {
		return unverified
	}
	state, err := readSharedDeploymentState(spec)
	if err == nil && state.Applying {
		return unverified
	}
	if err == nil && state.ConfigID != "" {
		if state.Digest != configurationDigest(actual) || state.ConfigID != task.ConfigID || state.ConfigVersion != task.ConfigVersion {
			return unverified
		}
		return nil // A local edit must not silently reassign a committed identity.
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return unverified
	}
	err = nil
	// Exact bytes are sufficient for legacy configurations without compiled
	// accounting. Otherwise reproduce only the retained server snapshot.
	prepared := task.ConfigContent
	if prepared != actual {
		var warning string
		prepared, warning = e.prepareNativeAccountingContent(ctx, task.Engine, spec, task.ConfigContent)
		if warning != "" {
			return unverified
		}
	}
	if task.SharedInstance {
		prepared, err = serverconfig.SharedInstanceRuntimeContent(task.Engine, prepared)
	}
	if err != nil || prepared != actual {
		return unverified
	}
	task.ConfigContent = actual
	return e.commitSharedDeployment(spec, task)
}
