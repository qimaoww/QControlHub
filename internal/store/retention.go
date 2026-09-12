package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (s *Store) PruneTasks(ctx context.Context, olderThan time.Time) (int64, error) {
	args := []any{olderThan}
	where := workspaceOwnerClause(ctx, "t.owner_id", &args)
	result, err := s.pool.Exec(ctx, `DELETE FROM tasks t WHERE status IN ('succeeded','failed','canceled') AND COALESCE(finished_at,created_at) < $1
		AND NOT (status='succeeded' AND action IN ('deploy','import-existing') AND NOT EXISTS(
			SELECT 1 FROM tasks newer WHERE newer.agent_id=t.agent_id AND newer.engine=t.engine
			AND newer.status='succeeded' AND newer.action IN ('deploy','import-existing')
			AND (newer.finished_at,newer.id)>(t.finished_at,t.id)))`+where, args...)
	if err != nil {
		return 0, fmt.Errorf("prune tasks: %w", err)
	}
	return result.RowsAffected(), nil
}

func (s *Store) PruneConfigRevisions(ctx context.Context, keep int) (int64, error) {
	if keep < 1 {
		return 0, nil
	}
	args := []any{keep}
	where := workspaceOwnerClause(ctx, "config.owner_id", &args)
	result, err := s.pool.Exec(ctx, `
		WITH old AS (
			SELECT config_id,version FROM (
				SELECT revision.config_id,revision.version,row_number() OVER (PARTITION BY revision.config_id ORDER BY revision.version DESC) AS position
				FROM config_revisions revision JOIN configs config ON config.id=revision.config_id WHERE true`+where+`
			) ranked WHERE position > $1
		)
		DELETE FROM config_revisions revision USING old
		WHERE revision.config_id=old.config_id AND revision.version=old.version
		AND NOT EXISTS(SELECT 1 FROM agent_engine_ownership deployed
			WHERE deployed.config_id=revision.config_id AND deployed.config_version=revision.version)
		AND NOT EXISTS(SELECT 1 FROM (`+latestDeploymentsSQL+`) deployed
			WHERE deployed.config_id=revision.config_id AND deployed.config_version=revision.version)
		AND NOT EXISTS(SELECT 1 FROM tasks t WHERE t.config_id=revision.config_id AND t.config_version=revision.version
			AND t.status IN ('pending','running'))`, args...)
	if err != nil {
		return 0, fmt.Errorf("prune config revisions: %w", err)
	}
	return result.RowsAffected(), nil
}

// MaintainAccountData applies each account's policy only to its own records.
// Host logs and metrics follow the Agent owner; task/revision/audit policy
// follows the record owner, including work performed on a shared Agent.
func (s *Store) MaintainAccountData(ctx context.Context, now time.Time, prune bool) error {
	rows, err := s.pool.Query(ctx, `SELECT owner_id FROM (
		SELECT ''::text AS owner_id UNION SELECT owner_id FROM user_panel_settings
		UNION SELECT owner_id FROM agents UNION SELECT owner_id FROM configs
		UNION SELECT owner_id FROM tasks UNION SELECT owner_id FROM audit_logs
	) owners ORDER BY owner_id`)
	if err != nil {
		return err
	}
	var owners []string
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, owner)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var failures []error
	longestLogRetention := 1
	for _, owner := range owners {
		account := WithConfigScope(ctx, owner, true)
		settings, err := s.PanelSettings(account)
		if err != nil {
			failures = append(failures, err)
			longestLogRetention = 30 // Never delete logs under an unreadable policy.
			continue
		}
		if settings.CoreLogRetentionDays > longestLogRetention {
			longestLogRetention = settings.CoreLogRetentionDays
		}
		failures = append(failures, s.RequeueStaleTasks(account,
			time.Duration(settings.TaskStaleTimeoutSeconds)*time.Second,
			time.Duration(settings.InstallTaskStaleTimeoutSeconds)*time.Second, settings.TaskMaxAttempts))
		if !prune {
			continue
		}
		_, err = s.PruneMetricSamples(account, now.Add(-time.Duration(settings.MetricRetentionDays)*24*time.Hour))
		failures = append(failures, err)
		if settings.AuditRetentionDays > 0 {
			_, err = s.PruneAuditLogs(account, now.Add(-time.Duration(settings.AuditRetentionDays)*24*time.Hour))
			failures = append(failures, err)
		}
		if settings.TaskRetentionDays > 0 {
			_, err = s.PruneTasks(account, now.Add(-time.Duration(settings.TaskRetentionDays)*24*time.Hour))
			failures = append(failures, err)
		}
		_, err = s.PruneConfigRevisions(account, settings.ConfigRevisionRetention)
		failures = append(failures, err)
		args := []any{now.Add(-time.Duration(settings.CoreLogRetentionDays) * 24 * time.Hour)}
		where := workspaceOwnerClause(account, "agent.owner_id", &args)
		_, err = s.pool.Exec(account, `DELETE FROM core_logs log USING agents agent
			WHERE log.agent_id=agent.id AND log.received_at<$1`+where, args...)
		failures = append(failures, err)
	}
	if prune {
		cutoff := now.Add(-time.Duration(longestLogRetention) * 24 * time.Hour)
		_, err = s.PruneCoreLogPartitions(ctx, cutoff)
		failures = append(failures, err)
		_, err = s.PruneCoreLogBatches(ctx, cutoff)
		failures = append(failures, err)
		_, err = s.DropLegacyCoreLogs(ctx, now)
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
