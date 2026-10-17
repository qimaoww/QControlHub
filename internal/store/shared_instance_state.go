package store

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func validateSharedInstanceStatus(instance core.SharedInstanceStatus) error {
	if !instance.Engine.Valid() || !core.ValidAgentShareID(instance.ShareID) ||
		len(instance.Reason) > 2048 || !utf8.ValidString(instance.Reason) || len(instance.ConfigID) > 100 ||
		len(instance.TaskID) > 100 || len(instance.AppliedTaskID) > 100 || instance.ConfigVersion < 0 ||
		(instance.ConfigID == "") != (instance.ConfigVersion == 0) {
		return fmt.Errorf("%w: invalid shared instance identity", ErrInvalid)
	}
	switch instance.Status {
	case "active", "inactive", "failed", "unknown", "activating", "deactivating", "reloading":
		return nil
	}
	return fmt.Errorf("%w: invalid shared instance service status", ErrInvalid)
}

// The node lock serializes observations with task claim/completion. A snapshot
// collected before a newer operation cannot overwrite it, even if delivered
// after its result. Only a known deployed revision can acquire an identity.
func recordSharedInstanceObservationTx(ctx context.Context, tx pgx.Tx, agentID string, instance core.SharedInstanceStatus,
	versioned bool, receivedAt time.Time, settled bool, policies []string) error {
	if err := validateSharedInstanceStatus(instance); err != nil {
		return err
	}
	sampledAt := instance.ObservedAt
	if sampledAt.IsZero() {
		sampledAt = receivedAt
	}
	if sampledAt.After(receivedAt.Add(5*time.Minute)) || sampledAt.Before(receivedAt.Add(-24*time.Hour)) {
		return nil // Bad node clock cannot turn an old sample into fresh state.
	}
	_, err := tx.Exec(ctx, `WITH observation AS (
		SELECT state.agent_id,state.engine,state.share_id,
			($6<>'' AND (state.config_id=$6 AND state.config_version=$7 OR EXISTS(
				SELECT 1 FROM tasks deployed WHERE deployed.id=$8 AND deployed.agent_id=state.agent_id
				AND deployed.engine=state.engine AND deployed.shared_instance AND deployed.shared_traffic_id=state.share_id
				AND deployed.config_id=$6 AND deployed.config_version=$7 AND deployed.action='deploy'
				AND deployed.started_at IS NOT NULL AND deployed.status<>'canceled'))) AS identity_verified
		FROM agent_shared_instance_ownership state
		WHERE state.agent_id=$1 AND state.engine=$2 AND state.share_id=$3
		AND (state.sampled_at IS NULL OR state.sampled_at<=$10)
		AND (NOT $12::boolean OR state.last_task_id='' OR state.last_task_id=$9)
		AND NOT EXISTS(SELECT 1 FROM tasks current WHERE current.agent_id=state.agent_id AND current.engine=state.engine
			AND current.shared_instance AND current.shared_traffic_id=state.share_id AND current.status='running'
			AND current.action IN ('deploy','start','stop','restart') AND current.id<>$9)
	)
	UPDATE agent_shared_instance_ownership state SET
		running=$4 IN ('active','activating','reloading'),uncertain=$4='unknown',service_status=$4,status_reason=$5,
		config_id=CASE WHEN obs.identity_verified THEN $6 ELSE state.config_id END,
		config_version=CASE WHEN obs.identity_verified THEN $7 ELSE state.config_version END,
		config_uncertain=CASE WHEN obs.identity_verified THEN false
			WHEN $12::boolean AND state.config_id<>'' THEN true ELSE state.config_uncertain END,
		applied_task_id=CASE WHEN obs.identity_verified AND $8<>'' THEN $8 ELSE state.applied_task_id END,
		deployed_at=CASE WHEN obs.identity_verified AND (state.config_id<>$6 OR state.config_version<>$7 OR state.deployed_at IS NULL)
			THEN COALESCE((SELECT finished_at FROM tasks WHERE id=$8),$11) ELSE state.deployed_at END,
		last_task_id=CASE WHEN state.last_task_id='' THEN $9 ELSE state.last_task_id END,
		observed_at=$11,sampled_at=$10,updated_at=$11,
		traffic_settled=$4 IN ('inactive','failed') AND ($13::boolean OR
			COALESCE(cardinality($14::text[])>0,false)
			AND EXISTS(SELECT 1 FROM port_traffic_policies p WHERE p.agent_id=$1 AND p.share_id=$3 AND p.monitoring_enabled)
			AND NOT EXISTS(SELECT 1 FROM port_traffic_policies p WHERE p.agent_id=$1 AND p.share_id=$3 AND p.monitoring_enabled
				AND NOT(p.id=ANY($14::text[]))))
	FROM observation obs WHERE state.agent_id=obs.agent_id AND state.engine=obs.engine AND state.share_id=obs.share_id`,
		agentID, instance.Engine, instance.ShareID, instance.Status, instance.Reason, instance.ConfigID,
		instance.ConfigVersion, instance.AppliedTaskID, instance.TaskID, sampledAt, receivedAt, versioned, settled, policies)
	return err
}
