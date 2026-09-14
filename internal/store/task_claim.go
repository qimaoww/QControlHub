package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// RunningTask returns the task lease currently owned by an agent. A reconnecting
// Agent can resume result delivery without waiting for the stale-lease janitor.
// If the Agent no longer advertises the protocol required by the running task
// (for example a Mihomo development mirror install after the Agent was
// downgraded), the task is failed atomically instead of being delivered to an
// Agent that would silently fall back to the official repository.
func (s *Store) RunningTask(ctx context.Context, agentID string) (*core.Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var featuresJSON []byte
	if err := tx.QueryRow(ctx, `SELECT features FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&featuresJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var features []string
	if err := json.Unmarshal(featuresJSON, &features); err != nil {
		return nil, err
	}
	if err := cancelUnauthorizedAgentTasksTx(ctx, tx, agentID, features); err != nil {
		return nil, err
	}
	row := tx.QueryRow(ctx, `
		SELECT id,agent_id,action,engine,COALESCE(config_id,''),COALESCE(config_version,0),
		       COALESCE(config_content,''),COALESCE(mainland_access_policies,'[]'::jsonb),COALESCE(core_version,''),COALESCE(core_source,''),status,attempt,COALESCE(lease_id,''),
		       COALESCE(output,''),COALESCE(error,''),created_at,started_at,finished_at,tcp_settings,shared_traffic_id,cnip_source,install_if_missing
		FROM tasks WHERE agent_id=$1 AND status='running'
		ORDER BY started_at DESC LIMIT 1`, agentID)
	task, err := scanTask(row, true)
	if errors.Is(err, pgx.ErrNoRows) {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, commitErr
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var unauthorized bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks t WHERE t.id=$1
		AND ((`+unauthorizedTaskPrincipalSQL+`) OR (`+unauthorizedHostConfigTaskSQL+`)))`,
		task.ID).Scan(&unauthorized); err != nil {
		return nil, err
	}
	if unauthorized {
		if _, err := tx.Exec(ctx, `UPDATE tasks SET status='failed',error='user authorization changed; execution before disconnect is unknown',
			finished_at=now(),config_content=NULL,lease_id=NULL WHERE id=$1 AND status='running'`, task.ID); err != nil {
			return nil, err
		}
		return nil, tx.Commit(ctx)
	}
	if task.CNIPSource != nil && !containsFeature(features, core.AgentFeatureCNIPSource) {
		return nil, fmt.Errorf("%w: Agent no longer supports CN IP sources", ErrConflict)
	}
	if task.SharedTrafficID != "" {
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT $2::boolean AND EXISTS(SELECT 1 FROM agent_shares s JOIN panel_users u ON u.id=s.user_id
			WHERE s.id=$1 AND s.agent_id=$3 AND $4=ANY(s.engines) AND NOT u.disabled
				AND s.enabled AND s.status='accepted' AND (s.limit_bytes=0 OR s.used_bytes<s.limit_bytes))`,
			task.SharedTrafficID, supportsSharedEngines(features), agentID, task.Engine).Scan(&allowed); err != nil {
			return nil, err
		}
		if !allowed {
			if _, err := tx.Exec(ctx, `UPDATE tasks SET status='failed',error='shared Agent authorization changed; execution before disconnect is unknown',
				finished_at=now(),config_content=NULL,lease_id=NULL WHERE id=$1 AND status='running'`, task.ID); err != nil {
				return nil, err
			}
			return nil, tx.Commit(ctx)
		}
	}
	if (isMihomoMirrorTask(task) && !containsFeature(features, core.AgentFeatureMihomoDevelopmentSource)) ||
		(task.Action.SystemBBR() && !containsFeature(features, core.AgentFeatureSystemBBR)) ||
		(task.InstallIfMissing && !containsFeature(features, core.AgentFeaturePresetAutoInstall)) {
		message := "Agent no longer advertises mihomo-development-source-v1; the mirror development task cannot be safely resumed and it is unknown whether the previous Agent executed it before the connection was lost"
		if task.Action.SystemBBR() {
			message = "Agent no longer advertises system-bbr-v1; TCP tuning cannot safely resume and previous execution before disconnect is unknown"
		}
		if task.InstallIfMissing {
			message = "Agent no longer advertises preset-auto-install-v1; automatic installation cannot safely resume and previous execution before disconnect is unknown"
		}
		if _, updateErr := tx.Exec(ctx, `
			UPDATE tasks SET status='failed', error=$2, finished_at=now(), config_content=NULL, lease_id=NULL
			WHERE id=$1 AND status='running'`, task.ID,
			message); updateErr != nil {
			return nil, updateErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, commitErr
		}
		return nil, nil
	}
	if err := s.openExecutionConfig(&task); err != nil {
		if _, updateErr := tx.Exec(ctx, `UPDATE tasks SET status='failed',error=$2,finished_at=now(),
			config_content=NULL,lease_id=NULL WHERE id=$1`, task.ID, truncate(err.Error(), 8<<10)); updateErr != nil {
			return nil, updateErr
		}
		return nil, tx.Commit(ctx)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &task, nil
}

func (s *Store) ClaimTask(ctx context.Context, agentID string) (*core.Task, error) {
	// Most fallback polls find no work. Avoid opening a transaction and locking
	// the Agent (blocking live metrics) for these idle polls. This is only a
	// hint: the transactional checks below remain authoritative when work exists.
	var pending bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM tasks WHERE agent_id=$1 AND status='pending'
	)`, agentID).Scan(&pending); err != nil {
		return nil, err
	}
	if !pending {
		return nil, nil
	}
	leaseID, err := core.NewToken()
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var featuresJSON []byte
	if err := tx.QueryRow(ctx, `SELECT features FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&featuresJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var features []string
	if err := json.Unmarshal(featuresJSON, &features); err != nil {
		return nil, err
	}
	if err := cancelUnauthorizedAgentTasksTx(ctx, tx, agentID, features); err != nil {
		return nil, err
	}
	mirrorSupported := containsFeature(features, core.AgentFeatureMihomoDevelopmentSource)
	row := tx.QueryRow(ctx, `
		WITH next_task AS (
			SELECT t.id FROM tasks t
			WHERE t.agent_id=$1 AND t.status='pending'
			  AND NOT EXISTS (SELECT 1 FROM tasks running WHERE running.agent_id=$1 AND running.status='running')
			  AND ($3::boolean OR NOT (t.action='install' AND t.engine='mihomo' AND t.core_version='development' AND COALESCE(t.core_source,'')='mirror'))
			  AND ($4::boolean OR t.action NOT IN ('enable-bbr','disable-bbr','configure-tcp'))
              AND ($5::boolean OR t.cnip_source IS NULL)
			  AND ($6::boolean OR NOT t.install_if_missing)
			ORDER BY t.created_at ASC FOR UPDATE OF t SKIP LOCKED LIMIT 1
		)
		UPDATE tasks t SET status='running',started_at=now(),attempt=attempt+1,lease_id=$2
		FROM next_task n WHERE t.id=n.id
		RETURNING t.id,t.agent_id,t.action,t.engine,COALESCE(t.config_id,''),COALESCE(t.config_version,0),
		          COALESCE(t.config_content,''),COALESCE(t.mainland_access_policies,'[]'::jsonb),COALESCE(t.core_version,''),COALESCE(t.core_source,''),t.status,t.attempt,COALESCE(t.lease_id,''),COALESCE(t.output,''),COALESCE(t.error,''),
		          t.created_at,t.started_at,t.finished_at,t.tcp_settings,t.shared_traffic_id,t.cnip_source,t.install_if_missing`, agentID, leaseID, mirrorSupported, containsFeature(features, core.AgentFeatureSystemBBR), containsFeature(features, core.AgentFeatureCNIPSource), containsFeature(features, core.AgentFeaturePresetAutoInstall))
	task, err := scanTask(row, true)
	if err == nil {
		if configErr := s.openExecutionConfig(&task); configErr != nil {
			if _, updateErr := tx.Exec(ctx, `UPDATE tasks SET status='failed',error=$2,finished_at=now(),
				config_content=NULL,lease_id=NULL WHERE id=$1`, task.ID, truncate(configErr.Error(), 8<<10)); updateErr != nil {
				return nil, updateErr
			}
			return nil, tx.Commit(ctx)
		}
		if markErr := markEngineExecutionTx(ctx, tx, task); markErr != nil {
			return nil, markErr
		}
		if err := invalidateConfigReadSnapshotsTx(ctx, tx, task); err != nil {
			return nil, err
		}
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return nil, commitErr
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// isMihomoMirrorTask reports whether a task is the explicit third-party
// vernesong/mihomo mirror install that requires mihomo-development-source-v1.
func isMihomoMirrorTask(task core.Task) bool {
	return task.Action == core.ActionInstall &&
		task.Engine == core.EngineMihomo &&
		task.CoreVersion == core.CoreVersionDevelopment &&
		task.CoreSource == string(core.CoreSourceMirror)
}

func (s *Store) RequeueStaleTasks(ctx context.Context, age, installAge time.Duration, maxAttempts int) error {
	if installAge < age {
		installAge = age
	}
	args := []any{intervalString(age), intervalString(installAge), maxAttempts}
	where := workspaceOwnerClause(ctx, "owner_id", &args)
	_, err := s.pool.Exec(ctx, `
		UPDATE tasks SET
			status=CASE WHEN attempt >= $3 THEN 'failed' ELSE 'pending' END,
			error=CASE WHEN attempt >= $3 THEN 'agent did not report a result before the execution lease expired' ELSE error END,
			finished_at=CASE WHEN attempt >= $3 THEN now() ELSE NULL END,
			started_at=CASE WHEN attempt >= $3 THEN started_at ELSE NULL END,
			config_content=CASE WHEN attempt >= $3 THEN NULL ELSE config_content END,
			lease_id=NULL
		WHERE status='running' AND started_at < now() - CASE WHEN action='install' OR install_if_missing THEN $2::interval ELSE $1::interval END`+where,
		args...)
	return err
}
