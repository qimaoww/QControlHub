package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func sharedInstanceRecipient(ctx context.Context, executor storeExecutor, agentID string) (bool, error) {
	if scopeForConfig(ctx).Admin {
		return false, nil
	}
	var shared bool
	err := executor.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agents a JOIN panel_users u ON u.id=$2
		WHERE a.id=$1 AND a.owner_id<>u.id AND u.role='user' AND NOT u.disabled)`,
		agentID, scopeForConfig(ctx).OwnerID).Scan(&shared)
	return shared, err
}

// Only the authenticated recipient's durable allocation determines the target.
// No service name, file path, share ID or user ID is accepted by TaskRequest.
func (s *Store) prepareSharedInstanceOperationTx(ctx context.Context, tx pgx.Tx, task *core.Task, features []string) error {
	if !supportsSharedEngines(features) || !containsFeature(features, core.AgentFeatureSharedCoreInstances) ||
		!containsFeature(features, core.AgentFeatureSharedCoreLifecycle) {
		return fmt.Errorf("%w: 请联系节点所有者升级 Agent（shared-core-lifecycle-v1），以安全操作本人的共享实例", ErrConflict)
	}
	var limit, used int64
	if err := tx.QueryRow(ctx, `SELECT id,limit_bytes,used_bytes FROM agent_shares
		WHERE user_id=$1 AND agent_id=$2 AND enabled AND status='accepted' AND $3=ANY(engines)`,
		scopeForConfig(ctx).OwnerID, task.AgentID, task.Engine).Scan(&task.SharedTrafficID, &limit, &used); err != nil {
		return mapError(err)
	}
	task.SharedInstance = true
	if task.ConfigID != "" {
		return fmt.Errorf("%w: 实例操作的配置由服务器确定，请勿指定配置标识", ErrInvalid)
	}
	if task.Action == core.ActionStatus {
		return nil
	}
	var uncertain bool
	err := tx.QueryRow(ctx, `SELECT state.config_id,state.config_version,state.config_uncertain,
		COALESCE(CASE WHEN c.version=state.config_version THEN c.content ELSE r.content END,'')
		FROM agent_shared_instance_ownership state
		LEFT JOIN configs c ON c.id=state.config_id AND c.owner_id=state.owner_id
		LEFT JOIN config_revisions r ON r.config_id=state.config_id AND r.version=state.config_version
		WHERE state.agent_id=$1 AND state.engine=$2 AND state.share_id=$3 AND state.owner_id=$4`,
		task.AgentID, task.Engine, task.SharedTrafficID, scopeForConfig(ctx).OwnerID).
		Scan(&task.ConfigID, &task.ConfigVersion, &uncertain, &task.ConfigContent)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && task.ConfigID == "" && task.Action != core.ActionStop {
		return fmt.Errorf("%w: 本人实例尚未部署，请先保存并部署配置", ErrConflict)
	}
	if err != nil {
		return err
	}
	if task.Action != core.ActionStart && task.Action != core.ActionRestart {
		// Reads and stops remain available when a quota is exhausted or the
		// previous deployment needs recovery. They cannot open new listeners.
		task.ConfigContent = ""
		return nil
	}
	if uncertain || task.ConfigContent == "" {
		return fmt.Errorf("%w: 实际配置版本尚未核实，请重新部署本人配置后启动", ErrConflict)
	}
	if limit > 0 && used >= limit {
		return fmt.Errorf("%w: 本人共享额度已耗尽，请联系所有者调整额度", ErrConflict)
	}
	task.ConfigContent, err = s.decryptContent(task.ConfigContent)
	if err != nil {
		return err
	}
	// Revalidate the retained revision and its reservations. A newer draft is
	// deliberately not read; start always uses the last deployed version.
	if err := s.prepareSharedTaskTx(ctx, tx, task, scopeForConfig(ctx).OwnerID, features, core.RuntimeState{}); err != nil {
		return err
	}
	task.TLSFrontends, err = s.managedTLSFrontendsTx(ctx, tx, task.ConfigID, task.ConfigVersion, task.Engine, task.ConfigContent)
	return err
}

func requireConfigSnapshotAccess(ctx context.Context, executor storeExecutor, agentID string, engine core.Engine) error {
	if err := requireAgentEngineAccess(ctx, executor, agentID, engine); err != nil {
		return err
	}
	shared, err := sharedInstanceRecipient(ctx, executor, agentID)
	if err != nil || shared {
		return err
	}
	return requireHostConfigRead(ctx, executor, agentID, engine)
}
