package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Called in the same transaction as log insertion. Retried batches cannot add
// observations, and rejected/minimum-level-filtered logs never enter history.
func storeClientConnectionLogs(ctx context.Context, tx pgx.Tx, agentID string, entries []core.CoreLogEntry) error {
	type observation struct {
		core.ClientConnection
		LoggedAt time.Time `json:"logged_at"`
	}
	observations := make([]observation, 0, len(entries))
	var receivedAt time.Time
	for _, entry := range entries {
		if entry.ReceivedAt.After(receivedAt) {
			receivedAt = entry.ReceivedAt
		}
		if connection, ok := clientConnectionFromLog(entry); ok {
			observations = append(observations, observation{connection, entry.LoggedAt.UTC()})
		}
	}
	if receivedAt.IsZero() {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO client_connection_sources(agent_id,updated_at,status,detail,truncated,source)
 VALUES($1,$2,'ok','panel core logs',false,'core_logs') ON CONFLICT(agent_id) DO UPDATE
 SET updated_at=EXCLUDED.updated_at,status=EXCLUDED.status,detail=EXCLUDED.detail,truncated=false,source='core_logs'
 WHERE client_connection_sources.source<>'core_logs' OR client_connection_sources.updated_at<=EXCLUDED.updated_at`, agentID, receivedAt); err != nil {
		return err
	}
	if len(observations) == 0 {
		return nil
	}
	payload, err := json.Marshal(observations)
	if err != nil {
		return err
	}
	// Group within each minute before ON CONFLICT: one log batch may contain
	// multiple events for the same tuple, including out-of-order replayed logs.
	_, err = tx.Exec(ctx, `INSERT INTO client_connections(agent_id,bucket,engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port,first_seen,last_seen,source)
 SELECT $1,date_trunc('minute',logged_at),engine,protocol,inbound,transport,client_ip::inet,client_port,local_ip::inet,local_port,min(logged_at),max(logged_at),'core_logs'
 FROM jsonb_to_recordset($2::jsonb) AS x(engine text,protocol text,inbound text,transport text,client_ip text,client_port integer,local_ip text,local_port integer,logged_at timestamptz)
 GROUP BY date_trunc('minute',logged_at),engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port
 ON CONFLICT(agent_id,bucket,engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port)
 DO UPDATE SET first_seen=LEAST(client_connections.first_seen,EXCLUDED.first_seen),last_seen=GREATEST(client_connections.last_seen,EXCLUDED.last_seen),source='core_logs'`, agentID, payload)
	return err
}

// BackfillClientConnectionLogs indexes one bounded page of retained panel logs.
// New uploads are indexed atomically by StoreCoreLogs, so concurrent log IDs
// above or below this finite migration watermark cannot be lost. The cursor
// and observations commit together and the row lock coordinates panel replicas.
func (s *Store) BackfillClientConnectionLogs(ctx context.Context) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var lastID, upperID int64
	if err := tx.QueryRow(ctx, `SELECT last_id,upper_id FROM client_connection_log_backfill WHERE id=1 FOR UPDATE`).Scan(&lastID, &upperID); err != nil {
		return false, err
	}
	if lastID >= upperID {
		return true, tx.Commit(ctx)
	}
	rows, err := tx.Query(ctx, `SELECT id,agent_id,engine,message,logged_at,received_at FROM core_logs
 WHERE id>$1 AND id<=$2 AND received_at>=$3 AND logged_at>=$3 ORDER BY id LIMIT 1000`, lastID, upperID, time.Now().UTC().Add(-core.ClientConnectionRetention))
	if err != nil {
		return false, err
	}
	entries := []core.CoreLogEntry{}
	for rows.Next() {
		var entry core.CoreLogEntry
		if err := rows.Scan(&entry.ID, &entry.AgentID, &entry.Engine, &entry.Message, &entry.LoggedAt, &entry.ReceivedAt); err != nil {
			rows.Close()
			return false, err
		}
		entries = append(entries, entry)
		lastID = entry.ID
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(entries) < 1000 {
		lastID = upperID
	}
	// Match upload/revocation locking: a deleted node must not regain telemetry.
	groups := map[string][]core.CoreLogEntry{}
	for _, entry := range entries {
		groups[entry.AgentID] = append(groups[entry.AgentID], entry)
	}
	for agentID, logs := range groups {
		var active bool
		err := tx.QueryRow(ctx, `SELECT true FROM agents WHERE id=$1 AND revoked_at IS NULL FOR SHARE`, agentID).Scan(&active)
		if err == pgx.ErrNoRows {
			continue
		}
		if err != nil {
			return false, err
		}
		if err := storeClientConnectionLogs(ctx, tx, agentID, logs); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE client_connection_log_backfill SET last_id=$1 WHERE id=1`, lastID); err != nil {
		return false, err
	}
	return lastID >= upperID, tx.Commit(ctx)
}
