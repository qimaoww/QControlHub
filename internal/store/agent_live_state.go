package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Agent live state keeps the high-frequency half of an Agent row in its own
// table.
//
// Every metrics push rewrites this snapshot: once per second per node on a
// default install, which makes it the busiest write in the panel. Holding it as
// a narrow side table with reserved page space keeps that rewrite on-page as a
// heap-only tuple, so it writes no index entry and leaves no dead index tuple
// behind. The agents row keeps identity, capability, and routing fields, and
// drops to a width where a heartbeat update can also stay on-page.
//
// The snapshot excludes the control-plane observed public address on purpose.
// That value comes from the authenticated WSS peer, survives Agent reconnects,
// and is written on its own schedule, so it lives in its own agents column
// where a later metrics push cannot clobber it: no snapshot ever carries that
// key.

// upsertAgentLiveStateSQL replaces the stored snapshot for one Agent.
//
// An Agent that reports no network interfaces keeps the previously stored list,
// so a partial report cannot erase a usable address snapshot. This preserves
// the merge the single-table layout performed.
const upsertAgentLiveStateSQL = `
	INSERT INTO agent_live_state (agent_id,metrics,updated_at)
	VALUES ($1,$2::jsonb,now())
	ON CONFLICT (agent_id) DO UPDATE SET
		metrics=CASE
			WHEN $2::jsonb ? 'network_interfaces' OR NOT (agent_live_state.metrics ? 'network_interfaces') THEN $2::jsonb
			ELSE $2::jsonb || jsonb_build_object('network_interfaces', agent_live_state.metrics->'network_interfaces')
		END,
		updated_at=now()`

// recordAgentLiveState stores the latest metrics snapshot for one Agent. The
// caller has already established that the Agent exists and is not revoked.
func (s *Store) recordAgentLiveState(ctx context.Context, agentID string, metricsState []byte) error {
	if _, err := s.pool.Exec(ctx, upsertAgentLiveStateSQL, agentID, metricsState); err != nil {
		return fmt.Errorf("store agent live state: %w", err)
	}
	return nil
}

// decodeAgentMetrics rebuilds the metrics a panel response carries from the
// Agent-reported snapshot plus the control-plane observed address, which are
// stored in different tables.
//
// A missing snapshot decodes as empty rather than as an error: an Agent that
// has not pushed metrics since the schema split simply has none, and the
// observed address alone is still worth returning.
func decodeAgentMetrics(snapshot []byte, observedAddress string, metrics *core.HostMetrics) error {
	if len(snapshot) > 0 {
		if err := json.Unmarshal(snapshot, metrics); err != nil {
			return err
		}
	}
	metrics.ObservedPublicIP = observedAddress
	return nil
}

// clearAgentLiveStateProbes drops the Agent-probed public addresses from the
// stored snapshot.
//
// A heartbeat that carries no metrics means the Agent can no longer probe, so
// addresses it probed earlier must stop being advertised. The network interface
// snapshot is Agent-reported state as well and is retained, matching the merge
// the single-table layout performed. Reports are serialized per Agent, so
// rewriting the snapshot here cannot race another writer of the same row.
func (s *Store) clearAgentLiveStateProbes(ctx context.Context, agentID string) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE agent_live_state
		SET metrics = metrics - 'public_ipv4' - 'public_ipv6' - 'public_ipv4_source' - 'public_ipv6_source',
		    updated_at = now()
		WHERE agent_id=$1 AND metrics ?| ARRAY['public_ipv4','public_ipv6','public_ipv4_source','public_ipv6_source']`,
		agentID); err != nil {
		return fmt.Errorf("clear agent live state probes: %w", err)
	}
	return nil
}

// splitAgentLiveState moves the Agent-reported metrics snapshot out of the
// agents row, gives the observed address a column of its own, and squeezes the
// row for on-page updates.
//
// The move is a metadata operation: it renames the old column out of the way
// and drops it, which frees the storage without rewriting the table, so an
// upgrade stays fast even though the snapshots themselves can be large.
// Retained snapshots are preserved in the new table.
//
// This runs before schemaSQL, so on a database created by this version the
// agents table does not exist yet and there is nothing to move: schemaSQL then
// creates both tables in their final shape.
func splitAgentLiveState(ctx context.Context, tx pgx.Tx) error {
	var hasAgentsTable bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass('agents') IS NOT NULL`).Scan(&hasAgentsTable); err != nil {
		return fmt.Errorf("inspect agents table: %w", err)
	}
	if !hasAgentsTable {
		return nil
	}
	// Created here rather than in schemaSQL: the backfill below needs the table
	// before the (idempotent) schema body runs, and both writers of this table
	// live in this file.
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS agent_live_state (
			agent_id text PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,
			metrics jsonb NOT NULL DEFAULT '{}'::jsonb,
			updated_at timestamptz NOT NULL DEFAULT now()
		) WITH (fillfactor = 70)`); err != nil {
		return fmt.Errorf("create agent live state: %w", err)
	}
	var hasMetrics bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM pg_attribute
			WHERE attrelid = to_regclass('agents') AND attname = 'metrics' AND NOT attisdropped
		)`).Scan(&hasMetrics); err != nil {
		return fmt.Errorf("inspect agents metrics column: %w", err)
	}
	if hasMetrics {
		if _, err := tx.Exec(ctx, `ALTER TABLE agents ADD COLUMN IF NOT EXISTS observed_public_ip text NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add observed public address column: %w", err)
		}
		// The address has to be read out of the snapshot before that column is
		// discarded below.
		if _, err := tx.Exec(ctx, `
			UPDATE agents SET observed_public_ip = COALESCE(metrics->>'observed_public_ip','')
			WHERE metrics ? 'observed_public_ip'`); err != nil {
			return fmt.Errorf("carry observed public address: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO agent_live_state (agent_id,metrics,updated_at)
			SELECT id,metrics,now() FROM agents WHERE metrics IS NOT NULL
			ON CONFLICT (agent_id) DO NOTHING`); err != nil {
			return fmt.Errorf("backfill agent live state: %w", err)
		}
		// Discarding the payload is what makes this cheap. Renaming first keeps
		// the operation a catalogue update: the column and its out-of-line
		// storage are released together and no row is rewritten row by row.
		if _, err := tx.Exec(ctx, `ALTER TABLE agents RENAME COLUMN metrics TO metrics_retired_v50`); err != nil {
			return fmt.Errorf("retire agents metrics column: %w", err)
		}
		if _, err := tx.Exec(ctx, `ALTER TABLE agents DROP COLUMN metrics_retired_v50`); err != nil {
			return fmt.Errorf("drop retired agents metrics column: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE agents SET (fillfactor = 70)`); err != nil {
		return fmt.Errorf("reserve agent page space: %w", err)
	}
	// agents_active_seen_idx is built on last_seen and last_seen changes on
	// every heartbeat, so while that index exists no heartbeat can ever be a
	// heap-only update. The panel reads it once per second for an online count
	// over a table with one row per node, which a sequential scan answers just
	// as well and far more cheaply than maintaining a btree entry per heartbeat
	// forever.
	if _, err := tx.Exec(ctx, `DROP INDEX IF EXISTS agents_active_seen_idx`); err != nil {
		return fmt.Errorf("drop agent last seen index: %w", err)
	}
	return nil
}
