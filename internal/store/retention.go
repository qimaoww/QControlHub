package store

import (
	"context"
	"fmt"
	"time"
)

func (s *Store) PruneTasks(ctx context.Context, olderThan time.Time) (int64, error) {
	result, err := s.pool.Exec(ctx, `DELETE FROM tasks t WHERE status IN ('succeeded','failed','canceled') AND COALESCE(finished_at,created_at) < $1
		AND NOT (status='succeeded' AND action IN ('deploy','import-existing') AND NOT EXISTS(
			SELECT 1 FROM tasks newer WHERE newer.agent_id=t.agent_id AND newer.engine=t.engine
			AND newer.status='succeeded' AND newer.action IN ('deploy','import-existing')
			AND (newer.finished_at,newer.id)>(t.finished_at,t.id)))`, olderThan)
	if err != nil {
		return 0, fmt.Errorf("prune tasks: %w", err)
	}
	return result.RowsAffected(), nil
}

func (s *Store) PruneConfigRevisions(ctx context.Context, keep int) (int64, error) {
	if keep < 1 {
		return 0, nil
	}
	result, err := s.pool.Exec(ctx, `
		WITH old AS (
			SELECT config_id,version FROM (
				SELECT config_id,version,row_number() OVER (PARTITION BY config_id ORDER BY version DESC) AS position
				FROM config_revisions
			) ranked WHERE position > $1
		)
		DELETE FROM config_revisions revision USING old
		WHERE revision.config_id=old.config_id AND revision.version=old.version
		AND NOT EXISTS(SELECT 1 FROM agent_engine_ownership deployed
			WHERE deployed.config_id=revision.config_id AND deployed.config_version=revision.version)
		AND NOT EXISTS(SELECT 1 FROM tasks t WHERE t.config_id=revision.config_id AND t.config_version=revision.version
			AND t.status IN ('pending','running'))`, keep)
	if err != nil {
		return 0, fmt.Errorf("prune config revisions: %w", err)
	}
	return result.RowsAffected(), nil
}
