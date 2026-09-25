package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) persistClientConnectionPorts(ctx context.Context, tx pgx.Tx, records []core.ClientConnectionRecord) error {
	if len(records) == 0 {
		return nil
	}
	if err := s.resolveClientConnectionPorts(ctx, tx, records); err != nil {
		return err
	}
	payload, err := json.Marshal(records)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE client_connections c SET inbound_port=observed.local_port
 FROM jsonb_to_recordset($1::jsonb) AS observed(id bigint,local_port integer) WHERE c.id=observed.id`, payload)
	return err
}

// BackfillClientConnectionPorts snapshots existing history in bounded, resumable
// batches. A zero port records an unknown listener; NULL means not processed yet.
func (s *Store) BackfillClientConnectionPorts(ctx context.Context) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,agent_id,engine,inbound,local_port,first_seen,last_seen
 FROM client_connections WHERE source='core_logs' AND inbound_port IS NULL
 ORDER BY id LIMIT 1000 FOR UPDATE`)
	if err != nil {
		return false, err
	}
	var records []core.ClientConnectionRecord
	for rows.Next() {
		var r core.ClientConnectionRecord
		if err := rows.Scan(&r.ID, &r.AgentID, &r.Engine, &r.Inbound, &r.LocalPort, &r.FirstSeen, &r.LastSeen); err != nil {
			rows.Close()
			return false, err
		}
		records = append(records, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if err := s.persistClientConnectionPorts(ctx, tx, records); err != nil {
		return false, err
	}
	return len(records) < 1000, tx.Commit(ctx)
}
