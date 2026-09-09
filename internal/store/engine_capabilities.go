package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// SetAgentEngineCapability only changes eligibility for new management work.
// It never uninstalls, stops a service, or deletes its configuration/history.
func (s *Store) SetAgentEngineCapability(ctx context.Context, id string, engine core.Engine, enabled bool) error {
	if !engine.Valid() {
		return fmt.Errorf("%w: unsupported engine", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var selected, supported []core.Engine
	err = tx.QueryRow(ctx, `SELECT capabilities,COALESCE(supported_capabilities,capabilities)
		FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, id).Scan(&selected, &supported)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if enabled && !containsEngine(supported, engine) {
		return fmt.Errorf("%w: Agent did not declare support for this engine; update its QCH_AGENT_ENGINES and re-enroll first", ErrInvalid)
	}
	if containsEngine(selected, engine) == enabled {
		return tx.Commit(ctx)
	}
	if !enabled {
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE agent_id=$1 AND engine=$2 AND status IN ('pending','running'))`, id, engine).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return fmt.Errorf("%w: 该内核有待处理或执行中的任务，请完成或取消后再关闭能力", ErrConflict)
		}
	}
	next := []core.Engine{}
	for _, candidate := range core.AllEngines() {
		if (candidate == engine && enabled) || (candidate != engine && containsEngine(selected, candidate)) {
			next = append(next, candidate)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET supported_capabilities=COALESCE(supported_capabilities,capabilities),capabilities=$2 WHERE id=$1`, id, next); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
