package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// SetAgentEngineCapability is the compatibility wrapper for internal callers.
func (s *Store) SetAgentEngineCapability(ctx context.Context, id string, engine core.Engine, enabled bool) error {
	_, err := s.ChangeAgentEngineCapability(ctx, id, engine, enabled)
	return err
}

type EngineCapabilityChange struct {
	Enabled bool   `json:"enabled"`
	TaskID  string `json:"task_id,omitempty"`
}

const capabilityTransitionsSQL = `(SELECT COALESCE(jsonb_object_agg(engine,
	jsonb_build_object('task_id',id,'status',status,'enabled',action='start')), '{}'::jsonb)
	FROM (SELECT DISTINCT ON (engine) id,engine,status,action FROM tasks
	WHERE agent_id=agents.id AND capability_transition ORDER BY engine,created_at DESC,id DESC) transitions)`

// ChangeAgentEngineCapability starts/stops installed cores using a durable Agent
// task. The effective capability changes only after a successful acknowledgement.
func (s *Store) ChangeAgentEngineCapability(ctx context.Context, id string, engine core.Engine, enabled bool) (EngineCapabilityChange, error) {
	if err := requireAgentAdministration(ctx, s.pool, id); err != nil {
		return EngineCapabilityChange{}, err
	}
	change := EngineCapabilityChange{}
	if !engine.Valid() {
		return change, fmt.Errorf("%w: unsupported engine", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return change, err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return change, err
	}
	if err := requireAgentAdministration(ctx, tx, id); err != nil {
		return change, err
	}
	var selected, supported []core.Engine
	var runtime map[core.Engine]core.RuntimeState
	err = tx.QueryRow(ctx, `SELECT capabilities,COALESCE(supported_capabilities,capabilities),runtime
		FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, id).Scan(&selected, &supported, &runtime)
	if errors.Is(err, pgx.ErrNoRows) {
		return change, ErrNotFound
	}
	if err != nil {
		return change, err
	}
	if enabled && !containsEngine(supported, engine) {
		return change, fmt.Errorf("%w: Agent did not declare support for this engine; update its QCH_AGENT_ENGINES and re-enroll first", ErrInvalid)
	}
	change.Enabled = containsEngine(selected, engine)
	if err := rejectPendingCapabilityTransition(ctx, tx, id, engine); err != nil {
		return change, err
	}
	if containsEngine(selected, engine) == enabled {
		return change, tx.Commit(ctx)
	}
	var busy bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE agent_id=$1 AND engine=$2 AND status IN ('pending','running'))`, id, engine).Scan(&busy); err != nil {
		return change, err
	}
	if busy {
		return change, fmt.Errorf("%w: 该内核有待处理或执行中的任务，请完成或取消后再切换能力", ErrConflict)
	}
	// Unsafe discovery can report an active service with Installed=false.
	// Reject it before the uninstalled fast path can change management access.
	if reason := runtime[engine].ExistingConfigUnsupportedReason; reason != "" {
		return change, fmt.Errorf("%w: %s", ErrConflict, reason)
	}
	if runtime[engine].Installed {
		action := core.ActionStop
		if enabled {
			action = core.ActionStart
			if err := requireSafeEngineStart(ctx, tx, id, engine); err != nil {
				if !errors.Is(err, ErrConflict) {
					return change, err
				}
				// Restore management access without starting an unverified
				// on-disk configuration. The next authorized deploy starts it.
				if err := setEngineCapability(ctx, tx, id, engine, true, selected); err != nil {
					return change, err
				}
				change.Enabled = true
				return change, tx.Commit(ctx)
			}
		}
		change.TaskID, err = core.NewID("tsk")
		if err != nil {
			return change, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tasks(id,agent_id,engine,action,status,created_at,capability_transition,owner_id)
			VALUES($1,$2,$3,$4,'pending',$5,true,$6)`, change.TaskID, id, engine, action, time.Now().UTC(), scopeForConfig(ctx).OwnerID); err != nil {
			return change, err
		}
		if err := tx.Commit(ctx); err != nil {
			return change, err
		}
		s.signalTaskReady(id)
		return change, nil
	}
	if err := setEngineCapability(ctx, tx, id, engine, enabled, selected); err != nil {
		return change, err
	}
	change.Enabled = enabled
	return change, tx.Commit(ctx)
}

func rejectPendingCapabilityTransition(ctx context.Context, tx pgx.Tx, id string, engine core.Engine) error {
	var busy bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE agent_id=$1 AND engine=$2
		AND capability_transition AND status IN ('pending','running'))`, id, engine).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return fmt.Errorf("%w: 内核正在切换能力，请等待启停任务完成；离线节点将在上线后执行", ErrConflict)
	}
	return nil
}

func setEngineCapability(ctx context.Context, tx pgx.Tx, id string, engine core.Engine, enabled bool, selected []core.Engine) error {
	next := []core.Engine{}
	for _, candidate := range core.AllEngines() {
		if (candidate == engine && enabled) || (candidate != engine && containsEngine(selected, candidate)) {
			next = append(next, candidate)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET supported_capabilities=COALESCE(supported_capabilities,capabilities),capabilities=$2 WHERE id=$1`, id, next); err != nil {
		return err
	}
	return nil
}
