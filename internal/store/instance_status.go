package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// A refresh carries only this principal's retained revision. It may be used
// to identify a legacy deployment even when a failed attempt made it uncertain.
// A node administrator does not inherit another principal's configuration.
func (s *Store) prepareStatusSnapshotTx(ctx context.Context, tx pgx.Tx, task *core.Task, features []string) error {
	if task.Action != core.ActionStatus {
		return nil
	}
	if !task.SharedInstance && !containsFeature(features, core.AgentFeatureInstanceStatus) {
		var uncertain bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_engine_ownership
			WHERE agent_id=$1 AND engine=$2 AND (uncertain OR config_uncertain)
			AND NOT EXISTS(SELECT 1 FROM tasks current WHERE current.agent_id=$1 AND current.engine=$2
				AND NOT current.shared_instance AND current.status IN ('pending','running')
				AND current.action IN ('deploy','import-existing','start','restart','stop','uninstall')))`, task.AgentID, task.Engine).Scan(&uncertain); err != nil {
			return err
		}
		if uncertain {
			return fmt.Errorf("%w: 请升级 Agent 后刷新实例状态，以核实之前失败的部署", ErrConflict)
		}
		return nil
	}
	err := tx.QueryRow(ctx, `SELECT state.config_id,state.config_version,
		COALESCE(revision.content,CASE WHEN config.version=state.config_version THEN config.content END,'')
		FROM (
			SELECT config_id,config_version,owner_id FROM agent_engine_ownership
			WHERE agent_id=$1 AND engine=$2 AND NOT $3::boolean
			UNION ALL SELECT config_id,config_version,owner_id FROM agent_shared_instance_ownership
			WHERE agent_id=$1 AND engine=$2 AND $3::boolean AND share_id=$4
		) state JOIN configs config ON config.id=state.config_id AND config.owner_id=state.owner_id
		LEFT JOIN config_revisions revision ON revision.config_id=state.config_id AND revision.version=state.config_version
		WHERE state.owner_id=$5 AND state.config_id<>''`, task.AgentID, task.Engine, task.SharedInstance,
		task.SharedTrafficID, scopeForConfig(ctx).OwnerID).Scan(&task.ConfigID, &task.ConfigVersion, &task.ConfigContent)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	task.ConfigContent, err = s.decryptContent(task.ConfigContent)
	if err != nil {
		return err
	}
	// The retained source includes stopped objects. Reproduce the exact
	// deployed revision's management state before the Agent accounts it.
	task.ConfigContent, err = s.compileConfigManagementRuntime(ctx, tx, task.ConfigID, task.ConfigVersion, task.Engine, task.ConfigContent)
	return err
}

// Only the authenticated, leased target can restore the main instance. A
// service-status string alone cannot confirm configuration ownership. Retain
// config_uncertain until the Agent verifies the on-disk deployment identity.
func recordHostInstanceObservationTx(ctx context.Context, tx pgx.Tx, taskID string, instance core.SharedInstanceStatus) error {
	if instance.ShareID != "" || validateInstanceStatus(instance) != nil || instance.TaskID != taskID {
		return fmt.Errorf("%w: invalid main instance observation", ErrInvalid)
	}
	now := time.Now().UTC()
	if instance.ObservedAt.IsZero() || instance.ObservedAt.After(now.Add(5*time.Minute)) || instance.ObservedAt.Before(now.Add(-24*time.Hour)) {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE agent_engine_ownership state SET
		running=$3 IN ('active','activating','reloading'),uncertain=$3='unknown',
		config_uncertain=CASE WHEN $4<>'' AND state.config_id=$4 AND state.config_version=$5
			AND EXISTS(SELECT 1 FROM configs c WHERE c.id=$4 AND c.owner_id=state.owner_id)
			THEN false
			WHEN state.config_id='' AND $4='' AND $3 IN ('inactive','failed')
				AND task.action='import-existing' AND task.status='failed' AND state.owner_id=task.owner_id
			THEN false WHEN state.config_id<>'' THEN true ELSE state.config_uncertain END,
		traffic_config_confirmed=state.traffic_config_confirmed OR ($4<>'' AND state.config_id=$4 AND state.config_version=$5
			AND EXISTS(SELECT 1 FROM configs c WHERE c.id=$4 AND c.owner_id=state.owner_id)),
		updated_at=now()
		FROM tasks task WHERE task.id=$1 AND NOT task.shared_instance AND task.engine=$2
		AND $6::timestamptz>=task.started_at
		AND state.agent_id=task.agent_id AND state.engine=task.engine
		AND NOT EXISTS(SELECT 1 FROM tasks newer WHERE newer.agent_id=task.agent_id AND newer.engine=task.engine
			AND NOT newer.shared_instance AND newer.action IN ('deploy','import-existing','start','restart','stop','uninstall')
			AND newer.id<>task.id AND (newer.status='running' OR newer.started_at>task.started_at))`,
		taskID, instance.Engine, instance.Status, instance.ConfigID, instance.ConfigVersion, instance.ObservedAt)
	return err
}
