package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Check the live lease and durable authorization before the panel renders the
// report. CompleteTask rechecks them under its locks.
func (s *Store) CanArchiveIPQualityResult(ctx context.Context, agentID, taskID, leaseID string) (bool, error) {
	var allowed bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks t JOIN agents a ON a.id=t.agent_id
		WHERE t.id=$1 AND t.agent_id=$2 AND t.lease_id=$3 AND t.status='running'
		AND t.action='ip-quality' AND a.revoked_at IS NULL
		AND NOT (`+unauthorizedTaskPrincipalSQL+`) AND NOT (`+unauthorizedHostConfigTaskSQL+`))`,
		taskID, agentID, leaseID).Scan(&allowed)
	return allowed, err
}

func validateIPQualityArchives(result core.IPQualityResult, archives []core.IPQualityArchive) error {
	if len(archives) != len(result.Reports) {
		return fmt.Errorf("%w: IPQuality report rendering is incomplete", ErrInvalid)
	}
	for i, archive := range archives {
		digest := sha256.Sum256(archive.Content)
		if archive.Family != core.IPQualityReportFamily(result.Reports[i]) ||
			len(archive.Content) == 0 || len(archive.Content) > core.MaxIPQualityArchiveBytes ||
			archive.Size != len(archive.Content) || archive.SHA256 != hex.EncodeToString(digest[:]) || archive.RenderedAt.IsZero() {
			return fmt.Errorf("%w: IPQuality archive does not match the report", ErrInvalid)
		}
	}
	return nil
}

func saveIPQualityArchivesTx(ctx context.Context, tx pgx.Tx, taskID string, archives []core.IPQualityArchive) error {
	for _, archive := range archives {
		if _, err := tx.Exec(ctx, `INSERT INTO ip_quality_archives(task_id,family,sha256,rendered_at,content)
			VALUES($1,$2,$3,$4,$5)`, taskID, archive.Family, archive.SHA256, archive.RenderedAt, archive.Content); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetIPQualityArchive(ctx context.Context, taskID string, family int) (core.IPQualityArchive, error) {
	args := []any{taskID, family}
	where := ownerClause(ctx, "t.owner_id", &args) + agentAdministrationClause(ctx, "t.agent_id", &args)
	var archive core.IPQualityArchive
	err := s.pool.QueryRow(ctx, `SELECT r.family,r.sha256,r.rendered_at,r.content
		FROM ip_quality_archives r JOIN tasks t ON t.id=r.task_id JOIN agents a ON a.id=t.agent_id
		WHERE r.task_id=$1 AND r.family=$2 AND a.revoked_at IS NULL`+where, args...).Scan(
		&archive.Family, &archive.SHA256, &archive.RenderedAt, &archive.Content)
	archive.Size = len(archive.Content)
	return archive, mapError(err)
}
