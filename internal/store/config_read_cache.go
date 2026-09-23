package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// CreateTaskWithReadCache validates the request exactly like CreateTask, then
// reuses a readable snapshot if one is still eligible under the Agent lock.
// It never exposes snapshot contents in the returned task.
func (s *Store) CreateTaskWithReadCache(ctx context.Context, request core.TaskRequest, maxAge time.Duration) (core.Task, error) {
	if request.Action != core.ActionReadConfig && request.Action != core.ActionReadManagedConfig {
		return core.Task{}, fmt.Errorf("%w: recent configuration snapshots require a read action", ErrInvalid)
	}
	return s.createTask(ctx, request, maxAge)
}

func (s *Store) RecentReadTask(ctx context.Context, agentID string, engine core.Engine, action core.Action, maxAge time.Duration) (core.Task, error) {
	if err := requireHostConfigRead(ctx, s.pool, agentID, engine); err != nil {
		return core.Task{}, err
	}
	return s.recentReadTask(ctx, s.pool, agentID, engine, action, maxAge)
}

func (s *Store) recentReadTask(ctx context.Context, executor storeExecutor, agentID string, engine core.Engine, action core.Action, maxAge time.Duration) (core.Task, error) {
	if action != core.ActionReadConfig && action != core.ActionReadManagedConfig {
		return core.Task{}, fmt.Errorf("%w: recent configuration snapshots require a read action", ErrInvalid)
	}
	if maxAge <= 0 {
		return core.Task{}, ErrNotFound
	}
	row := executor.QueryRow(ctx, `
		SELECT id,agent_id,action,engine,COALESCE(config_id,''),COALESCE(config_version,0),
		       COALESCE(config_content,''),COALESCE(mainland_access_policies,'[]'::jsonb),COALESCE(core_version,''),COALESCE(core_source,''),status,attempt,COALESCE(lease_id,''),
		       COALESCE(output,''),COALESCE(error,''),created_at,started_at,finished_at,tcp_settings,shared_traffic_id,cnip_source,install_if_missing,shared_instance
		FROM tasks
		WHERE agent_id=$1 AND engine=$2 AND action=$3 AND status='succeeded'
		  AND config_content IS NOT NULL AND finished_at > now()-$4::interval AND owner_id=$5
		  AND NOT EXISTS(
		      SELECT 1 FROM tasks mutation WHERE mutation.agent_id=$1 AND mutation.engine=$2
		        AND (mutation.action IN ('deploy','install','import-existing') OR mutation.install_if_missing)
		        AND mutation.status IN ('pending','running'))
		ORDER BY finished_at DESC LIMIT 1`, agentID, engine, action, intervalString(maxAge), scopeForConfig(ctx).OwnerID)
	task, err := scanTask(row, true)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Task{}, ErrNotFound
	}
	if err != nil {
		return core.Task{}, err
	}
	// A retired key or damaged cache entry is a miss, not a failed read.
	// Only payload decoding errors are ignored; DB and authorization errors
	// remain authoritative and must not create a fallback task.
	content, err := s.decryptContent(task.ConfigContent)
	if err != nil || content == "" {
		return core.Task{}, ErrNotFound
	}
	task.ConfigContent = ""
	return task, nil
}

func invalidateConfigReadSnapshotsTx(ctx context.Context, tx pgx.Tx, task core.Task) error {
	if !task.InstallIfMissing && task.Action != core.ActionDeploy &&
		task.Action != core.ActionInstall && task.Action != core.ActionImportExisting {
		return nil
	}
	// Invalidate before execution: even failed or lost mutations may have
	// changed the file or the validating binary. read-config can target the
	// managed file on legacy Agents, so both source variants must be cleared.
	_, err := tx.Exec(ctx, `UPDATE tasks SET config_content=NULL
		WHERE agent_id=$1 AND engine=$2 AND action IN ('read-config','read-managed-config')
		  AND config_content IS NOT NULL`, task.AgentID, task.Engine)
	return err
}
