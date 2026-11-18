package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func restoreConnectionSnapshot(ctx context.Context, tx pgx.Tx, key string, now time.Time) (*connectionSnapshotData, error) {
	var position, generation string
	err := tx.QueryRow(ctx, `SELECT snapshot::text,generation FROM client_connection_history_cache WHERE key=$1`, key).Scan(&position, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT group_key,data FROM client_connection_history_groups WHERE cache_key=$1`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := map[string]connectionGroup{}
	for rows.Next() {
		var key string
		var group connectionGroup
		if err := rows.Scan(&key, &group); err != nil {
			return nil, err
		}
		group.addr, _ = netip.ParseAddr(group.Record.ClientIP)
		group.Revision = 1
		groups[key] = group
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return newConnectionSnapshot(groups, position, generation, now), nil
}

func (s *Store) saveConnectionSnapshot(ctx context.Context, key string, data *connectionSnapshotData, replace bool, dirty map[string]core.ClientConnectionRecord, groups map[string]connectionGroup) error {
	rows := make([][]any, 0, len(groups))
	for groupKey, group := range groups {
		payload, err := json.Marshal(group)
		if err != nil {
			return err
		}
		rows = append(rows, []any{key, groupKey, payload})
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	next := connectionDigest([]any{data.epoch, data.revision, data.position})
	if data.generation == "" {
		tag, err := tx.Exec(ctx, `INSERT INTO client_connection_history_cache (key,snapshot,generation) VALUES ($1,$2::text::pg_snapshot,$3) ON CONFLICT DO NOTHING`, key, data.position, next)
		if err != nil || tag.RowsAffected() != 1 {
			return err
		}
	} else {
		tag, err := tx.Exec(ctx, `UPDATE client_connection_history_cache SET snapshot=$2::text::pg_snapshot,generation=$3,updated_at=now() WHERE key=$1 AND generation=$4`, key, data.position, next, data.generation)
		if err != nil || tag.RowsAffected() != 1 {
			return err
		}
		// Delete old versions of changed groups; rows outside the dirty set
		// retain their stored representation across partial refreshes.
		if replace {
			_, err = tx.Exec(ctx, `DELETE FROM client_connection_history_groups WHERE cache_key=$1`, key)
		} else {
			keys := make([]string, 0, len(dirty))
			for key := range dirty {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			_, err = tx.Exec(ctx, `DELETE FROM client_connection_history_groups WHERE cache_key=$1 AND group_key=ANY($2)`, key, keys)
		}
		if err != nil {
			return err
		}
	}
	if len(rows) != 0 {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"client_connection_history_groups"}, []string{"cache_key", "group_key", "data"}, pgx.CopyFromRows(rows)); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	data.generation = next
	return nil
}

func (s *Store) pruneClientConnectionCache(ctx context.Context, now time.Time) error {
	// A reader with xmin at/below a removed xid must rebuild. This also
	// covers a long-running writer committing just before a retention pass.
	_, err := s.pool.Exec(ctx, `WITH removed AS (
	  DELETE FROM client_connection_journal WHERE changed_at < $1 RETURNING xid
	), floor AS (SELECT xid FROM removed ORDER BY xid DESC LIMIT 1)
	UPDATE client_connection_journal_floor j SET xid=floor.xid FROM floor WHERE j.id=1 AND j.xid<floor.xid`, now.Add(-connectionJournalTTL))
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM client_connection_history_cache WHERE updated_at < $1`, now.Add(-connectionSyncTTL))
	return err
}
