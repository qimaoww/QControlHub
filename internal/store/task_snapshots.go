package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) ReadTaskConfigSnapshot(ctx context.Context, taskID, agentID string, engine core.Engine) (string, error) {
	if err := requireHostConfigRead(ctx, s.pool, agentID, engine); err != nil {
		return "", err
	}
	var content string
	args := []any{taskID, agentID, engine, core.ActionReadConfig, core.ActionReadManagedConfig}
	where := ownerClause(ctx, "owner_id", &args)
	err := s.pool.QueryRow(ctx, `
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
