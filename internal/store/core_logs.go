package store

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const coreLogRetention = 7 * 24 * time.Hour

type CoreLogQuery struct {
	AgentID string
	Engine  core.Engine
	Level   string
	Search  string
	Before  int64
	Limit   int
}

func (s *Store) StoreCoreLogs(ctx context.Context, agentID string, batch core.CoreLogBatch) error {
	if !validCoreLogBatchID(batch.ID) || len(batch.Entries) == 0 || len(batch.Entries) > core.MaxCoreLogBatchEntries {
		return fmt.Errorf("%w: invalid core log batch", ErrInvalid)
	}
	receivedAt := time.Now().UTC()
	entries := make([]core.CoreLogEntry, 0, len(batch.Entries))
	for _, entry := range batch.Entries {
		entry.Message = strings.TrimSpace(strings.ToValidUTF8(entry.Message, "�"))
		entry.Level = normalizeCoreLogLevel(entry.Level)
		if !entry.Engine.Valid() || entry.Level == "" || entry.Message == "" ||
			!utf8.ValidString(entry.Message) || len([]byte(entry.Message)) > core.MaxCoreLogMessageBytes || strings.ContainsRune(entry.Message, '\x00') {
			return fmt.Errorf("%w: invalid core log entry", ErrInvalid)
		}
		if entry.LoggedAt.IsZero() || entry.LoggedAt.After(receivedAt.Add(5*time.Minute)) {
			entry.LoggedAt = receivedAt
		}
		if entry.LoggedAt.Before(receivedAt.Add(-coreLogRetention)) {
			continue
		}
		entries = append(entries, entry)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		INSERT INTO core_log_batches (id,agent_id,received_at) VALUES ($1,$2,$3)
		ON CONFLICT (id) DO NOTHING`, batch.ID, agentID, receivedAt)
	if err != nil {
		return mapError(err)
	}
	if command.RowsAffected() == 0 {
		var existingAgentID string
		if err := tx.QueryRow(ctx, `SELECT agent_id FROM core_log_batches WHERE id=$1`, batch.ID).Scan(&existingAgentID); err != nil {
			return mapError(err)
		}
		if existingAgentID != agentID {
			return fmt.Errorf("%w: core log batch belongs to another agent", ErrInvalid)
		}
		return tx.Commit(ctx)
	}
	for index, entry := range entries {
		if _, err := tx.Exec(ctx, `
			INSERT INTO core_logs (batch_id,entry_index,agent_id,engine,level,message,logged_at,received_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			batch.ID, index, agentID, entry.Engine, entry.Level, entry.Message, entry.LoggedAt.UTC(), receivedAt); err != nil {
			return mapError(err)
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) PruneCoreLogs(ctx context.Context, olderThan time.Time) (int64, error) {
	command, err := s.pool.Exec(ctx, `DELETE FROM core_log_batches WHERE received_at < $1`, olderThan.UTC())
	if err != nil {
		return 0, fmt.Errorf("prune core logs: %w", err)
	}
	return command.RowsAffected(), nil
}

func (s *Store) ListCoreLogs(ctx context.Context, query CoreLogQuery) ([]core.CoreLogEntry, error) {
	if query.Limit == 0 {
		query.Limit = 200
	}
	if query.Limit < 1 || query.Limit > 500 || query.Before < 0 {
		return nil, fmt.Errorf("%w: invalid core log query", ErrInvalid)
	}
	query.AgentID = strings.TrimSpace(query.AgentID)
	query.Level = normalizeCoreLogLevel(query.Level)
	query.Search = strings.TrimSpace(query.Search)
	if len(query.Search) > 120 || (query.Engine != "" && !query.Engine.Valid()) {
		return nil, fmt.Errorf("%w: invalid core log query", ErrInvalid)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id,agent_id,engine,level,message,logged_at,received_at
		FROM core_logs
		WHERE ($1='' OR agent_id=$1)
		  AND ($2='' OR engine=$2)
		  AND ($3='' OR level=$3)
		  AND ($4='' OR position(lower($4) in lower(message)) > 0)
		  AND ($5=0 OR id<$5)
		ORDER BY id DESC LIMIT $6`, query.AgentID, query.Engine, query.Level, query.Search, query.Before, query.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.CoreLogEntry, 0, query.Limit)
	for rows.Next() {
		var entry core.CoreLogEntry
		if err := rows.Scan(&entry.ID, &entry.AgentID, &entry.Engine, &entry.Level, &entry.Message, &entry.LoggedAt, &entry.ReceivedAt); err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

func validCoreLogBatchID(value string) bool {
	if len(value) != 20 || !strings.HasPrefix(value, "log_") {
		return false
	}
	for _, character := range value[4:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func normalizeCoreLogLevel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return "debug"
	case "info", "notice":
		return "info"
	case "warn", "warning":
		return "warning"
	case "err", "error":
		return "error"
	case "crit", "critical", "alert", "emerg", "emergency", "fatal", "panic":
		return "critical"
	case "":
		return ""
	default:
		return ""
	}
}
