package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) checkIPQualityTaskTx(ctx context.Context, tx pgx.Tx, agentID string, features []string) error {
	if !containsFeature(features, core.AgentFeatureIPQuality) {
		return fmt.Errorf("%w: 请先升级 Agent，以支持 IPQuality 检测", ErrConflict)
	}
	var online, busy bool
	if err := tx.QueryRow(ctx, `SELECT last_seen>now()-`+agentOfflineThresholdSQL+`*interval '1 second',
		EXISTS(SELECT 1 FROM tasks WHERE agent_id=$1 AND action='ip-quality'
			AND status IN ('pending','running') AND owner_id<>$2)
		FROM agents WHERE id=$1`, agentID,
		scopeForConfig(ctx).OwnerID).Scan(&online, &busy); err != nil {
		return err
	}
	if !online {
		return fmt.Errorf("%w: 节点离线，请重连后再执行 IPQuality 检测", ErrConflict)
	}
	if busy {
		return fmt.Errorf("%w: 此节点已有其他账号的 IPQuality 检测任务", ErrConflict)
	}
	return nil
}

func saveIPQualityResultTx(ctx context.Context, tx pgx.Tx, taskID string, result core.IPQualityResult) error {
	content, err := json.Marshal(result)
	if err != nil {
		return err
	}
	// Valid JSON can still contain strings/numbers JSONB cannot represent.
	// Isolate those data errors so the parent transaction can fail and ACK
	// the task instead of replaying an unsavable result on every reconnect.
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	defer savepoint.Rollback(ctx)
	if _, err := savepoint.Exec(ctx, `INSERT INTO ip_quality_reports(task_id,result) VALUES($1,$2)`, taskID, content); err != nil {
		if rollbackErr := savepoint.Rollback(ctx); rollbackErr != nil {
			return rollbackErr
		}
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && (strings.HasPrefix(pgError.Code, "22") ||
			pgError.Code == "23514" && pgError.TableName == "ip_quality_reports") {
			return fmt.Errorf("%w: IPQuality report contains unsupported text, numbers, or an oversized stored representation", ErrInvalid)
		}
		return err
	}
	return savepoint.Commit(ctx)
}

// ListIPQualityRecords returns the latest attempt per node on the requested
// local submission date. All filters run in SQL, before loading report bytes.
func (s *Store) ListIPQualityRecords(ctx context.Context, date, timezone string) ([]core.IPQualityRecord, error) {
	start, end, err := core.IPQualityDateRange(date, timezone)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	args := []any{start, end}
	where := ownerClause(ctx, "t.owner_id", &args)
	where += agentAdministrationClause(ctx, "t.agent_id", &args)
	rows, err := s.pool.Query(ctx, `SELECT latest.id,latest.agent_id,latest.status,COALESCE(latest.error,''),
		latest.created_at,latest.started_at,latest.finished_at,r.result FROM (
			SELECT DISTINCT ON (t.agent_id) t.id,t.agent_id,t.status,t.error,t.created_at,t.started_at,t.finished_at
			FROM tasks t JOIN agents a ON a.id=t.agent_id AND a.revoked_at IS NULL
			WHERE t.action='ip-quality' AND t.created_at>=$1 AND t.created_at<$2`+where+`
			ORDER BY t.agent_id,t.created_at DESC,t.id DESC
		) latest LEFT JOIN ip_quality_reports r ON r.task_id=latest.id
		ORDER BY latest.created_at DESC,latest.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]core.IPQualityRecord, 0)
	for rows.Next() {
		var record core.IPQualityRecord
		var content []byte
		if err := rows.Scan(&record.TaskID, &record.AgentID, &record.Status, &record.Error,
			&record.CreatedAt, &record.StartedAt, &record.FinishedAt, &content); err != nil {
			return nil, err
		}
		if len(content) > 0 {
			if err := json.Unmarshal(content, &record.Result); err != nil {
				return nil, err
			}
		}
		records = append(records, record)
	}
	return records, rows.Err()
}
