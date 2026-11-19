package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

// Port membership is exact. Split the 16-bit source port space into blocks
// so sparse combinations do not need a full 8 KiB bitmap.
const connectionPortBlock = 4096

const connectionChunkColumns = "agent_id,engine,client_ip,protocol,inbound,transport,local_ip,local_port,port,block,mask,id,first_seen,last_seen"

func connectionPortChunks(from string) string {
	return fmt.Sprintf(`SELECT c.agent_id,c.engine,c.client_ip,c.protocol,c.inbound,c.transport,c.local_ip,c.local_port,
	 COALESCE(c.inbound_port,c.local_port) port,c.client_port/%[1]d block,
	 bit_or(set_bit(B'0'::bit(%[1]d),c.client_port %% %[1]d,1)) mask,
	 min(c.id) id,min(c.first_seen) first_seen,max(c.last_seen) last_seen
	 FROM %s GROUP BY 1,2,3,4,5,6,7,8,9,10`, connectionPortBlock, from)
}

// Build one completed node/day in its own transaction. A concurrent late
// observation invalidates precisely that day before its transaction commits.
// Readers see either a complete rollup or the raw observations under MVCC.
func (s *Store) BuildClientConnectionRollup(ctx context.Context) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var agent string
	var day time.Time
	err = tx.QueryRow(ctx, `SELECT agent_id,day FROM client_connection_rollup_days
	 WHERE state='pending' AND day<date_trunc('day',now(),'UTC') ORDER BY day,agent_id
	 LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&agent, &day)
	if errors.Is(err, pgx.ErrNoRows) {
		idle, err := discoverConnectionRollupDay(ctx, tx)
		if err != nil {
			return false, err
		}
		return idle, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM client_connection_rollup_masks WHERE agent_id=$1 AND day=$2`, agent, day); err != nil {
		return false, err
	}
	const observations = `client_connections c WHERE c.agent_id=$1 AND c.bucket>=$2 AND c.bucket<$3 AND c.source='core_logs'`
	end := day.Add(24 * time.Hour)
	// Older/imported rows can have observation times outside the bucket. Preserve
	// the original overlap predicate for those days instead of rounding them.
	var irregular bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+observations+` AND (c.last_seen<$2 OR c.first_seen>=$3))`, agent, day, end).Scan(&irregular); err != nil {
		return false, err
	}
	state := "raw"
	if !irregular {
		tag, err := tx.Exec(ctx, `INSERT INTO client_connection_rollup_masks(day,`+connectionChunkColumns+`)
		 SELECT $2,chunks.* FROM (`+connectionPortChunks(observations)+`) chunks`, agent, day, end)
		if err != nil {
			return false, err
		}
		if tag.RowsAffected() == 0 {
			_, err = tx.Exec(ctx, `DELETE FROM client_connection_rollup_days WHERE agent_id=$1 AND day=$2`, agent, day)
			if err != nil {
				return false, err
			}
			return false, tx.Commit(ctx)
		}
		state = "ready"
	}
	if _, err := tx.Exec(ctx, `UPDATE client_connection_rollup_days SET state=$3 WHERE agent_id=$1 AND day=$2`, agent, day, state); err != nil {
		return false, err
	}
	return false, tx.Commit(ctx)
}

func discoverConnectionRollupDay(ctx context.Context, tx pgx.Tx) (bool, error) {
	var day *time.Time
	err := tx.QueryRow(ctx, `SELECT (SELECT date_trunc('day',bucket,'UTC') FROM client_connections
	 WHERE bucket>=s.next_day ORDER BY bucket LIMIT 1)
	 FROM client_connection_rollup_scan s WHERE id=1 AND next_day<'infinity' FOR UPDATE SKIP LOCKED`).Scan(&day)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if day == nil {
		_, err = tx.Exec(ctx, `UPDATE client_connection_rollup_scan SET next_day='infinity' WHERE id=1`)
		return true, err
	}
	end := day.Add(24 * time.Hour)
	if _, err := tx.Exec(ctx, `INSERT INTO client_connection_rollup_days(agent_id,day)
	 SELECT DISTINCT agent_id,$1::timestamptz FROM client_connections WHERE bucket>=$1 AND bucket<$2 AND source='core_logs'
	 ORDER BY 1 ON CONFLICT DO NOTHING`, *day, end); err != nil {
		return false, err
	}
	_, err = tx.Exec(ctx, `UPDATE client_connection_rollup_scan SET next_day=$1 WHERE id=1`, end)
	return false, err
}

// A full-window baseline only needs the set of distinct flow tuples and the
// independent set of serving ports. OR the source port masks across both days
// and endpoint variants before counting bits. Adding daily counts would count
// a connection twice when it appears on consecutive days.
func connectionMaskGroupsSQL(prefix, chunks string, limitArg int) string {
	return fmt.Sprintf(`WITH %s chunks AS (%s), parts AS (
	  SELECT agent_id,engine,client_ip,port,grouping(port) AS flow,
	   min(id) id,min(first_seen) first_seen,max(last_seen) last_seen,
	   CASE WHEN grouping(port)=1 THEN bit_count(bit_or(mask)) END n
	  FROM chunks GROUP BY agent_id,engine,client_ip,
	   GROUPING SETS ((protocol,inbound,transport,local_ip,local_port,block),(port))
	), totals AS (
	  SELECT agent_id,engine,client_ip,min(id) id,min(first_seen) first_seen,max(last_seen) last_seen,
	   sum(n)::bigint flows,array_agg(port) FILTER (WHERE flow=0) ports
	  FROM parts GROUP BY agent_id,engine,client_ip LIMIT $%d
	)
	SELECT t.id,t.agent_id,a.name,t.engine,host(t.client_ip),t.first_seen,t.last_seen,t.flows,t.ports,
	 COALESCE(l.country_code,''),COALESCE(l.country,''),COALESCE(l.province,'')
	FROM totals t JOIN agents a ON a.id=t.agent_id LEFT JOIN client_connection_locations l ON l.client_ip=t.client_ip`, prefix, chunks, limitArg)
}

func connectionMonthlyGroups(ctx context.Context, q ClientConnectionQuery, wanted []connectionLookupKey, limit int) (string, []any) {
	args := []any{q.Since, q.Until}
	nodes := `SELECT a.id FROM agents a WHERE a.revoked_at IS NULL` + agentAdministrationClause(ctx, "a.id", &args)
	if q.AgentID != "" {
		args = append(args, q.AgentID)
		nodes += fmt.Sprintf(" AND a.id=$%d", len(args))
	}
	prefix := `nodes AS MATERIALIZED (` + nodes + `), days AS MATERIALIZED (
	 SELECT d.agent_id,d.day,(d.state='ready' AND d.day>=$1 AND d.day+interval '24 hours'<=$2) covered
	 FROM nodes JOIN client_connection_rollup_days d ON d.agent_id=nodes.id
	 WHERE d.day>=date_trunc('day',$1::timestamptz,'UTC') AND d.day<$2
	),`
	// Until discovery has visited a retained day, a node with no marker may
	// still have pre-upgrade observations. Enumerate only those missing ranges.
	prefix += ` ranges AS (
	 SELECT agent_id,day FROM days WHERE NOT covered UNION ALL
	 SELECT nodes.id,h.day FROM client_connection_rollup_scan scan CROSS JOIN nodes
	 CROSS JOIN LATERAL generate_series(greatest(scan.next_day,date_trunc('day',$1::timestamptz,'UTC')),
	 $2::timestamptz-interval '1 microsecond',interval '24 hours') AS h(day)
	 WHERE scan.next_day<$2 AND NOT EXISTS(SELECT 1 FROM days d WHERE d.agent_id=nodes.id AND d.day=h.day)
	),`
	filters, args := connectionWantedGroups("", args, wanted)
	if q.Engine != "" {
		args = append(args, string(q.Engine))
		filters += fmt.Sprintf(" AND c.engine=$%d", len(args))
	}
	if !q.IncludeNonPublic {
		args = append(args, netpolicy.NonPublicPrefixes())
		filters += fmt.Sprintf(" AND NOT(c.client_ip <<= ANY($%d::inet[]))", len(args))
	}
	// Keep the index range on (agent_id,bucket) even when planner statistics
	// predate a large retention pass. Closed covered days never visit raw rows.
	raw := `ranges r CROSS JOIN LATERAL (SELECT c.* FROM client_connections c
	 WHERE c.agent_id=r.agent_id AND c.bucket>=greatest(r.day,date_trunc('minute',$1::timestamptz))
	  AND c.bucket<least(r.day+interval '24 hours',$2::timestamptz) AND c.source='core_logs'
	  AND c.last_seen>=$1 AND c.first_seen<$2` + filters + ` OFFSET 0) c`
	chunks := connectionPortChunks(raw) + ` UNION ALL SELECT c.` + strings.ReplaceAll(connectionChunkColumns, ",", ",c.") + `
	 FROM days d JOIN client_connection_rollup_masks c ON c.agent_id=d.agent_id AND c.day=d.day WHERE d.covered` + filters
	args = append(args, limit)
	return connectionMaskGroupsSQL(prefix, chunks, len(args)), args
}

func connectionWantedGroups(filters string, args []any, wanted []connectionLookupKey) (string, []any) {
	if wanted != nil {
		data, _ := json.Marshal(wanted)
		args = append(args, data)
		filters += fmt.Sprintf(` AND (c.agent_id,c.engine,c.client_ip) IN (SELECT agent_id,engine,client_ip::inet
		 FROM jsonb_to_recordset($%d::jsonb) AS targets(agent_id text,engine text,client_ip text))`, len(args))
	}
	return filters, args
}
