package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) ListIPQualitySchedules(ctx context.Context) ([]core.IPQualitySchedule, error) {
	args := []any{}
	// A plan controls the whole node, even when an administrator enabled it.
	// Its state follows host-management visibility, not the submitter's private
	// report workspace. Do not expose the submitter identity or report data.
	where := agentAdministrationClause(ctx, "q.agent_id", &args)
	rows, err := s.pool.Query(ctx, `SELECT q.agent_id,q.enabled,q.next_run_at
		FROM ip_quality_schedules q JOIN agents a ON a.id=q.agent_id
		WHERE a.revoked_at IS NULL`+where+` ORDER BY q.agent_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.IPQualitySchedule, 0)
	for rows.Next() {
		var schedule core.IPQualitySchedule
		if err := rows.Scan(&schedule.AgentID, &schedule.Enabled, &schedule.NextRunAt); err != nil {
			return nil, err
		}
		result = append(result, schedule)
	}
	return result, rows.Err()
}

func (s *Store) SetIPQualitySchedule(ctx context.Context, agentID string, enabled bool) (core.IPQualitySchedule, error) {
	scope := scopeForConfig(ctx)
	// Compatibility role tokens have no durable grants to recheck tomorrow.
	// The break-glass administrator uses the stable empty-owner workspace.
	if strings.HasPrefix(scope.OwnerID, "token_") {
		return core.IPQualitySchedule{}, fmt.Errorf("%w: 每日检测需要持久账号或管理员令牌", ErrForbidden)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.IPQualitySchedule{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return core.IPQualitySchedule{}, err
	}
	var features []string
	if err := tx.QueryRow(ctx, `SELECT features FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&features); err != nil {
		return core.IPQualitySchedule{}, mapError(err)
	}
	if err := requireAgentAdministration(ctx, tx, agentID); err != nil {
		return core.IPQualitySchedule{}, err
	}
	if err := requireTaskPermission(ctx, tx, core.ActionIPQuality, false); err != nil {
		return core.IPQualitySchedule{}, err
	}
	if enabled && !containsFeature(features, core.AgentFeatureIPQuality) {
		return core.IPQualitySchedule{}, fmt.Errorf("%w: 请先升级 Agent，以支持 IPQuality 检测", ErrConflict)
	}
	var result core.IPQualitySchedule
	err = tx.QueryRow(ctx, `INSERT INTO ip_quality_schedules(agent_id,owner_id,enabled,next_run_at)
		VALUES($1,$2,$3,now()) ON CONFLICT(agent_id) DO UPDATE SET
		owner_id=EXCLUDED.owner_id,enabled=EXCLUDED.enabled,updated_at=now(),
		next_run_at=CASE WHEN EXCLUDED.enabled AND
			(NOT ip_quality_schedules.enabled OR ip_quality_schedules.owner_id<>EXCLUDED.owner_id)
			THEN now() ELSE ip_quality_schedules.next_run_at END
		RETURNING agent_id,enabled,next_run_at`, agentID, scope.OwnerID, enabled).
		Scan(&result.AgentID, &result.Enabled, &result.NextRunAt)
	if err != nil {
		return core.IPQualitySchedule{}, err
	}
	return result, tx.Commit(ctx)
}

// QueueDueIPQualityChecks is an opt-in daily workflow. It never performs
// network probes in the control plane, and missed days do not create a burst.
func (s *Store) QueueDueIPQualityChecks(ctx context.Context, now time.Time) error {
	rows, err := s.pool.Query(ctx, `SELECT q.agent_id,q.owner_id FROM ip_quality_schedules q
		JOIN agents ON agents.id=q.agent_id WHERE q.enabled AND q.next_run_at<=$1
		AND agents.revoked_at IS NULL AND agents.features ? $2
		AND agents.last_seen>now()-`+agentOfflineThresholdSQL+`*interval '1 second'
		ORDER BY q.next_run_at,q.agent_id LIMIT 100`, now, core.AgentFeatureIPQuality)
	if err != nil {
		return err
	}
	type candidate struct{ agentID, ownerID string }
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.agentID, &item.ownerID); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var failures []error
	for _, item := range candidates {
		if err := s.queueIPQualityCheck(ctx, item.agentID, item.ownerID, now); err != nil &&
			!errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *Store) queueIPQualityCheck(ctx context.Context, agentID, ownerID string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Match task creation and account purge: durable user -> Agent -> schedule.
	// Resolve the current role under that lock, never a saved administrator bit.
	admin, authorized := ownerID == "", true
	if ownerID != "" {
		var role core.Role
		var disabled bool
		err := tx.QueryRow(ctx, `SELECT role,disabled FROM panel_users WHERE id=$1 FOR SHARE`, ownerID).Scan(&role, &disabled)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		admin, authorized = role == core.RoleAdmin, err == nil && !disabled
	}
	scoped := WithConfigScope(ctx, ownerID, admin)
	var id string
	if err := tx.QueryRow(ctx, `SELECT id FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&id); err != nil {
		return mapError(err)
	}
	var due bool
	if err := tx.QueryRow(ctx, `SELECT enabled AND next_run_at<=$3 FROM ip_quality_schedules
		WHERE agent_id=$1 AND owner_id=$2 FOR UPDATE`, agentID, ownerID, now).Scan(&due); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if !due {
		return nil
	}
	if authorized {
		err = requireTaskPermission(scoped, tx, core.ActionIPQuality, false)
		if err == nil {
			err = requireAgentAdministration(scoped, tx, agentID)
		}
		if err != nil && !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) {
			return err
		}
		authorized = err == nil
	}
	if !authorized {
		_, err := tx.Exec(ctx, `UPDATE ip_quality_schedules SET enabled=false,updated_at=now() WHERE agent_id=$1`, agentID)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	task, err := s.createTaskTx(scoped, tx, core.TaskRequest{AgentID: agentID, Action: core.ActionIPQuality}, 0)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE ip_quality_schedules SET next_run_at=$2,updated_at=now() WHERE agent_id=$1`,
		agentID, now.Add(24*time.Hour)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if !task.Reused {
		s.signalTaskReady(agentID)
	}
	return nil
}
