package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func applySharedTrafficUsageTx(ctx context.Context, tx pgx.Tx, agentID string, usages []core.PortTrafficUsage) error {
	var shared []core.PortTrafficUsage
	for _, usage := range usages {
		if usage.ShareID != "" {
			shared = append(shared, usage)
		}
	}
	if len(shared) == 0 {
		return nil
	}
	payload, err := json.Marshal(shared)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `WITH input AS (
		SELECT * FROM jsonb_to_recordset($2::jsonb) AS i(policy_id text,share_id text,share_used_bytes bigint)
	), deltas AS MATERIALIZED (
		SELECT p.id,p.share_id,i.share_used_bytes-p.share_used_bytes AS delta
		FROM port_traffic_policies p JOIN input i ON i.policy_id=p.id AND i.share_id=p.share_id
		WHERE p.agent_id=$1 AND p.monitoring_enabled AND i.share_used_bytes>p.share_used_bytes
	), written AS (
		UPDATE port_traffic_policies p SET share_used_bytes=p.share_used_bytes+d.delta
		FROM deltas d WHERE p.id=d.id RETURNING p.share_id,d.delta
	), totals AS (SELECT share_id,SUM(delta) AS delta FROM written GROUP BY share_id)
	UPDATE agent_shares share SET used_bytes=LEAST(9223372036854775807::numeric,share.used_bytes::numeric+totals.delta)::bigint,
		updated_at=now() FROM totals WHERE share.id=totals.share_id`, agentID, payload)
	return err
}
