package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) ReadTaskConfigSnapshot(ctx context.Context, taskID, agentID string, engine core.Engine) (string, error) {
	if err := requireConfigSnapshotAccess(ctx, s.pool, agentID, engine); err != nil {
		return "", err
	}
	var content string
	shared, err := sharedInstanceRecipient(ctx, s.pool, agentID)
	if err != nil {
		return "", err
	}
	args := []any{taskID, agentID, engine, core.ActionReadConfig, core.ActionReadManagedConfig}
	where := ownerClause(ctx, "owner_id", &args)
	if shared {
		where += ` AND shared_instance`
	} else {
		where += ` AND NOT shared_instance`
	}
	where += ` AND (NOT shared_instance OR EXISTS(SELECT 1 FROM agent_shares sh
		WHERE sh.id=tasks.shared_traffic_id AND sh.agent_id=tasks.agent_id AND sh.user_id=tasks.owner_id
		AND sh.enabled AND sh.status='accepted' AND tasks.engine=ANY(sh.engines)))`
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(config_content,'') FROM tasks
		WHERE id=$1 AND agent_id=$2 AND engine=$3 AND action IN ($4,$5) AND status='succeeded'`+where,
		args...).Scan(&content)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && content == "") {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return s.decryptContent(content)
}
