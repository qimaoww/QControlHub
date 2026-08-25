package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

type auditExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// RecordAudit appends one entry to the audit trail. The entry is validated
// lightly (bounded lengths) and truncated to the declared column limits so a
// single oversized field cannot poison the log.
func (s *Store) RecordAudit(ctx context.Context, entry core.AuditLogEntry) error {
	return recordAuditWithExecutor(ctx, s.pool, entry)
}

func recordAuditWithExecutor(ctx context.Context, executor auditExecutor, entry core.AuditLogEntry) error {
	entry.Actor = truncateAuditField(strings.TrimSpace(entry.Actor), 40)
	entry.Action = truncateAuditField(strings.TrimSpace(entry.Action), 40)
	entry.Target = truncateAuditField(entry.Target, 500)
	entry.Detail = truncateAuditField(entry.Detail, 2000)
	entry.RemoteIP = truncateAuditField(strings.TrimSpace(entry.RemoteIP), 64)
	if entry.Actor == "" {
		entry.Actor = "admin"
	}
	if entry.Action == "" {
		return fmt.Errorf("%w: audit action is required", ErrInvalid)
	}
	if entry.ActedAt.IsZero() {
		entry.ActedAt = time.Now().UTC()
	}
	_, err := executor.Exec(ctx, `
		INSERT INTO audit_logs (acted_at, actor, action, target, detail, remote_ip)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		entry.ActedAt, entry.Actor, entry.Action, entry.Target, entry.Detail, entry.RemoteIP)
	if err != nil {
		return fmt.Errorf("record audit log: %w", err)
	}
	return nil
}

// truncateAuditField cuts a field to a byte limit without breaking UTF-8.
// Unlike truncate (which appends the task-output marker), audit fields get a
// plain ellipsis so the log stays compact.
func truncateAuditField(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "\uFFFD") + "..."
}

// ListAuditLogs returns the most recent audit entries, newest first.
func (s *Store) ListAuditLogs(ctx context.Context, limit int) ([]core.AuditLogEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, acted_at, actor, action, target, detail, remote_ip
		FROM audit_logs ORDER BY acted_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()
	entries := make([]core.AuditLogEntry, 0, limit)
	for rows.Next() {
		var entry core.AuditLogEntry
		if err := rows.Scan(&entry.ID, &entry.ActedAt, &entry.Actor, &entry.Action, &entry.Target, &entry.Detail, &entry.RemoteIP); err != nil {
			return nil, fmt.Errorf("scan audit log: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit logs: %w", err)
	}
	return entries, nil
}

// PruneAuditLogs deletes entries older than the retention window and returns
// the number of removed rows.
func (s *Store) PruneAuditLogs(ctx context.Context, olderThan time.Time) (int64, error) {
	result, err := s.pool.Exec(ctx, `DELETE FROM audit_logs WHERE acted_at < $1`, olderThan)
	if err != nil {
		return 0, fmt.Errorf("prune audit logs: %w", err)
	}
	return result.RowsAffected(), nil
}
