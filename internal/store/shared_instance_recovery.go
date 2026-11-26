package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// A metrics push and a heartbeat can carry the same sample. Match the stored
// high-water marks without renewing the sample's age or billing it twice.
func settledHeartbeatPolicies(ctx context.Context, tx pgx.Tx, agentID string, usages []core.PortTrafficUsage, receivedAt time.Time, processRequired bool) ([]string, error) {
	var reports []core.PortTrafficUsage
	for _, usage := range usages {
		if usage.ShareID != "" && usage.EnforcementAvailable && !usage.CollectedAt.IsZero() && (!processRequired || usage.ProcessUsage != nil) {
			usage.CollectedAt = usage.CollectedAt.UTC().Truncate(time.Microsecond)
			reports = append(reports, usage)
		}
	}
	if len(reports) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `SELECT p.id FROM jsonb_to_recordset($3::jsonb) AS r(
		policy_id text,share_id text,reset_generation bigint,counter_epoch text,collected_at timestamptz,
		lifetime_received_bytes bigint,lifetime_sent_bytes bigint,blocked boolean,process_usage jsonb)
		JOIN port_traffic_policies p ON p.id=r.policy_id AND p.share_id=r.share_id AND p.reset_generation=r.reset_generation
		JOIN agents ON agents.id=p.agent_id
		WHERE p.agent_id=$1 AND p.monitoring_enabled AND p.enforcement_available AND p.blocked=r.blocked
		AND p.last_reported_at >= $2::timestamptz-make_interval(secs => `+agentOfflineThresholdSQL+`)
		AND p.counter_epoch=r.counter_epoch AND p.last_collected_at=r.collected_at
		AND p.reported_lifetime_received_bytes=COALESCE(r.lifetime_received_bytes,0)
		AND p.reported_lifetime_sent_bytes=COALESCE(r.lifetime_sent_bytes,0)
		AND (r.process_usage IS NULL OR EXISTS(SELECT 1 FROM shared_process_usage u WHERE u.share_id=p.share_id
			AND u.engine=p.engine AND u.epoch=r.process_usage->>'epoch' AND u.used_bytes>=(r.process_usage->>'bytes')::bigint))`, agentID, receivedAt, reports)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

type sharedStartTarget struct {
	owner, share, config string
	engine               core.Engine
	version              int
}

// The heartbeat holds the node lock. Only consider observations accepted from
// this sample and only after the instance's metering policies are healthy.
func reserveSharedStarts(ctx context.Context, tx pgx.Tx, agentID string, receivedAt time.Time, policies []string) ([]sharedStartTarget, error) {
	rows, err := tx.Query(ctx, `UPDATE agent_shared_instance_ownership o SET next_auto_start=now()+interval '5 minutes'
		FROM agent_shares sh WHERE o.agent_id=$1 AND o.observed_at=$2 AND o.desired_running
		AND o.service_status IN ('inactive','failed') AND NOT o.config_uncertain AND NOT o.uncertain
		AND (o.next_auto_start IS NULL OR o.next_auto_start<=now())
		AND sh.id=o.share_id AND sh.user_id=o.owner_id AND sh.agent_id=o.agent_id
		AND sh.enabled AND sh.status='accepted' AND o.engine=ANY(sh.engines)
		AND (sh.limit_bytes=0 OR sh.used_bytes<sh.limit_bytes)
		AND (SELECT bool_and(COALESCE(p.id=ANY($3::text[]) AND p.enforcement_available AND NOT p.blocked,false)) FROM port_traffic_policies p
			WHERE p.agent_id=o.agent_id AND p.share_id=o.share_id AND p.engine=o.engine AND p.monitoring_enabled)
		AND NOT EXISTS(SELECT 1 FROM tasks t WHERE t.agent_id=o.agent_id AND t.shared_traffic_id=o.share_id
			AND t.engine=o.engine AND t.status IN ('pending','running') AND t.action IN ('start','stop','restart','deploy','install'))
		RETURNING o.owner_id,o.share_id,o.config_id,o.engine,o.config_version`, agentID, receivedAt, policies)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (sharedStartTarget, error) {
		var target sharedStartTarget
		err := row.Scan(&target.owner, &target.share, &target.config, &target.engine, &target.version)
		return target, err
	})
}

// Acquire the same user -> node locks as a manual start. A stop, newer
// observation or deployment between the heartbeat commit and this transaction
// supersedes the proposal. Normal task creation and lease authorization apply.
func (s *Store) enqueueSharedStart(ctx context.Context, agentID string, receivedAt time.Time, target sharedStartTarget) error {
	ctx = WithConfigScope(ctx, target.owner, false)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return err
	}
	var id string
	if err := tx.QueryRow(ctx, `SELECT id FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&id); err != nil {
		return err
	}
	var current bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_shared_instance_ownership
		WHERE agent_id=$1 AND engine=$2 AND share_id=$3 AND owner_id=$4 AND config_id=$5 AND config_version=$6
		AND observed_at=$7 AND desired_running AND service_status IN ('inactive','failed') AND NOT config_uncertain AND NOT uncertain)`,
		agentID, target.engine, target.share, target.owner, target.config, target.version, receivedAt).Scan(&current); err != nil || !current {
		return err
	}
	task, err := s.createTaskTx(ctx, tx, core.TaskRequest{AgentID: agentID, Engine: target.engine,
		Action: core.ActionStart, ExpectedInstanceID: &target.share}, 0)
	if err != nil {
		return err
	}
	if !task.Reused {
		if _, err := tx.Exec(ctx, `UPDATE tasks SET shared_auto_start=true WHERE id=$1`, task.ID); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.signalTaskReadyAfterCommit(ctx, agentID)
	return nil
}
