package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

func saveIPQualityResultTx(ctx context.Context, tx pgx.Tx, taskID string, result core.IPQualityResult, archives []core.IPQualityArchive) error {
	if err := validateIPQualityArchives(result, archives); err != nil {
		return err
	}
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
	if err := saveIPQualityArchivesTx(ctx, savepoint, taskID, archives); err != nil {
		return err
	}
	return savepoint.Commit(ctx)
}

// ListIPQualityRecords returns the latest attempt per node on the requested
// local submission date. An empty date returns each node's latest check across
// all dates, with its last successful report if the newest check has no result.
// All access filters run in SQL, before loading report bytes.
func (s *Store) ListIPQualityRecords(ctx context.Context, date, timezone string) ([]core.IPQualityRecord, error) {
	validationDate := date
	if validationDate == "" {
		validationDate = time.Now().UTC().Format(time.DateOnly)
	}
	start, end, err := core.IPQualityDateRange(validationDate, timezone)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	args := []any{}
	dateFilter := ""
	if date != "" {
		args = append(args, start, end)
		dateFilter = " AND t.created_at>=$1 AND t.created_at<$2"
	}
	where := ownerClause(ctx, "t.owner_id", &args)
	where += agentAdministrationClause(ctx, "t.agent_id", &args)
	lastSuccessful := "NULL::jsonb"
	if date == "" {
		lastSuccessful = `CASE WHEN latest.status<>'succeeded' THEN (
			SELECT jsonb_build_object('task_id',t.id,'agent_id',t.agent_id,'status',t.status,
				'created_at',t.created_at,'started_at',t.started_at,'finished_at',t.finished_at,
				'result',previous.result - 'reports_text','archives',
				COALESCE((SELECT jsonb_agg(jsonb_build_object('family',a.family,
					'sha256',a.sha256,'size',octet_length(a.content),'rendered_at',a.rendered_at) ORDER BY a.family)
					FROM ip_quality_archives a WHERE a.task_id=t.id),'[]'::jsonb))
			FROM tasks t JOIN ip_quality_reports previous ON previous.task_id=t.id
			WHERE t.action='ip-quality' AND t.agent_id=latest.agent_id AND t.status='succeeded'
				AND (t.created_at,t.id)<(latest.created_at,latest.id)` + where + `
			ORDER BY t.created_at DESC,t.id DESC LIMIT 1
		) END`
	}
	rows, err := s.pool.Query(ctx, `SELECT latest.id,latest.agent_id,latest.status,COALESCE(latest.error,''),
		latest.created_at,latest.started_at,latest.finished_at,r.result - 'reports_text',
        COALESCE((SELECT jsonb_agg(jsonb_build_object('family',a.family,
            'sha256',a.sha256,'size',octet_length(a.content),'rendered_at',a.rendered_at) ORDER BY a.family)
            FROM ip_quality_archives a WHERE a.task_id=latest.id),'[]'::jsonb),`+lastSuccessful+` FROM (
			SELECT DISTINCT ON (t.agent_id) t.id,t.agent_id,t.status,t.error,t.created_at,t.started_at,t.finished_at
			FROM tasks t JOIN agents a ON a.id=t.agent_id AND a.revoked_at IS NULL
			WHERE t.action='ip-quality'`+dateFilter+where+`
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
		var content, archives, previous []byte
		if err := rows.Scan(&record.TaskID, &record.AgentID, &record.Status, &record.Error,
			&record.CreatedAt, &record.StartedAt, &record.FinishedAt, &content, &archives, &previous); err != nil {
			return nil, err
		}
		if len(content) > 0 {
			if err := json.Unmarshal(content, &record.Result); err != nil {
				return nil, err
			}
		}
		if err := json.Unmarshal(archives, &record.Archives); err != nil {
			return nil, err
		}
		if len(previous) > 0 {
			if err := json.Unmarshal(previous, &record.LastSuccessful); err != nil {
				return nil, err
			}
		}
		records = append(records, record)
	}
	return records, rows.Err()
}
