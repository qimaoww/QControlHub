package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
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
	if _, err := tx.Exec(ctx, `DELETE FROM client_connection_rollup_times WHERE agent_id=$1 AND day=$2`, agent, day); err != nil {
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
		if err := saveConnectionTimeline(ctx, tx, agent, day, end); err != nil {
			return false, err
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

// A local calendar day can span 23 hours across a daylight saving change.
func useConnectionRollups(q ClientConnectionQuery) bool { return q.Until.Sub(q.Since) >= 23*time.Hour }

type connectionReadRanges struct {
	prefix, raw, saved, timeFilters string
	args                            []any
}

func connectionRollupRanges(ctx context.Context, q ClientConnectionQuery, wanted []connectionLookupKey) connectionReadRanges {
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
	add := func(field string, value any) {
		args = append(args, value)
		filters += fmt.Sprintf(" AND c.%s=$%d", field, len(args))
	}
	if q.Engine != "" {
		add("engine", string(q.Engine))
	}
	if q.Protocol != "" {
		add("protocol", q.Protocol)
	}
	if q.Inbound != "" {
		add("inbound", q.Inbound)
	}
	if q.Transport != "" {
		add("transport", q.Transport)
	}
	if q.ClientIP != "" {
		ip, _ := netip.ParseAddr(q.ClientIP)
		add("client_ip", ip.Unmap().String())
	}
	if !q.IncludeNonPublic || q.CountryCode != "" {
		args = append(args, netpolicy.NonPublicPrefixes())
		filters += fmt.Sprintf(" AND NOT(c.client_ip <<= ANY($%d::inet[]))", len(args))
	}
	if q.CountryCode != "" {
		args = append(args, q.CountryCode)
		filters += fmt.Sprintf(" AND EXISTS(SELECT 1 FROM client_connection_locations l WHERE l.client_ip=c.client_ip AND l.country_code=$%d)", len(args))
	}
	rawFilters, savedFilters := filters, filters
	if q.Port != 0 {
		args = append(args, q.Port)
		rawFilters += fmt.Sprintf(" AND COALESCE(c.inbound_port,c.local_port)=$%d", len(args))
		savedFilters += fmt.Sprintf(" AND c.port=$%d", len(args))
	}
	// Keep the index range on (agent_id,bucket) even when planner statistics
	// predate a large retention pass. Closed covered days never visit raw rows.
	raw := `ranges r CROSS JOIN LATERAL (SELECT c.* FROM client_connections c
	 WHERE c.agent_id=r.agent_id AND c.bucket>=greatest(r.day,date_trunc('minute',$1::timestamptz))
	  AND c.bucket<least(r.day+interval '24 hours',$2::timestamptz) AND c.source='core_logs'
	  AND c.last_seen>=$1 AND c.first_seen<$2` + rawFilters + ` OFFSET 0) c`
	saved := `days d JOIN client_connection_rollup_masks c ON c.agent_id=d.agent_id AND c.day=d.day WHERE d.covered` + savedFilters
	return connectionReadRanges{prefix: prefix, raw: raw, saved: saved, timeFilters: filters, args: args}
}

func (r connectionReadRanges) chunks() string {
	return connectionPortChunks(r.raw) + ` UNION ALL SELECT c.` + strings.ReplaceAll(connectionChunkColumns, ",", ",c.") + " FROM " + r.saved
}

func connectionMonthlyGroups(ctx context.Context, q ClientConnectionQuery, wanted []connectionLookupKey, limit int) (string, []any) {
	r := connectionRollupRanges(ctx, q, wanted)
	args := append(r.args, limit)
	return connectionMaskGroupsSQL(r.prefix, r.chunks(), len(args)), args
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

// Lightweight pages and enrichment need the observation boundaries and
// serving ports. They can skip decoding flow membership entirely.
func (r connectionReadRanges) records() string {
	const keys = "c.agent_id,c.engine,c.client_ip,c.id,c.first_seen,c.last_seen"
	return "WITH " + r.prefix + " observations AS (SELECT " + keys + ",COALESCE(c.inbound_port,c.local_port) port FROM " + r.raw +
		" UNION ALL SELECT " + keys + ",c.port FROM " + r.saved + ") "
}

func connectionRollupTotals(ctx context.Context, tx pgx.Tx, q ClientConnectionQuery, result *core.ClientConnectionHistory) error {
	base := q
	base.CountryCode = ""
	base.IncludeNonPublic = q.IncludeNonPublic && q.CountryCode == ""
	r := connectionRollupRanges(ctx, base, nil)
	args := append(r.args, q.CountryCode, netpolicy.NonPublicPrefixes())
	country := fmt.Sprintf("($%d='' OR l.country_code=$%d)", len(args)-1, len(args)-1)
	public := fmt.Sprintf("NOT (ip.client_ip <<= ANY($%d::inet[]))", len(args))
	return tx.QueryRow(ctx, "WITH "+r.prefix+" chunks AS ("+r.chunks()+`),
	  flows AS (SELECT client_ip,bit_count(bit_or(mask)) n FROM chunks
	    GROUP BY agent_id,engine,protocol,inbound,transport,client_ip,local_ip,local_port,block),
	  ip AS (SELECT client_ip,sum(n) n FROM flows GROUP BY client_ip)
	  SELECT COALESCE(sum(ip.n) FILTER (WHERE `+country+`),0),count(*) FILTER (WHERE `+country+`),
	  COALESCE(array_agg(DISTINCT l.country_code ORDER BY l.country_code) FILTER(WHERE l.country_code<>'' AND `+public+`),ARRAY[]::varchar[])
	  FROM ip LEFT JOIN client_connection_locations l USING(client_ip)`, args...).Scan(&result.Flows, &result.IPs, &result.CountryCodes)
}

// Only UTC day timelines can reuse daily membership. Minute/hour buckets
// retain precise event times, with scalar deduplication before counting.
func connectionTimeline(ctx context.Context, tx pgx.Tx, q ClientConnectionQuery) (pgx.Rows, error) {
	if q.Bucket == "day" && useConnectionRollups(q) {
		r := connectionRollupRanges(ctx, q, nil)
		raw := `SELECT r.day, c.agent_id,c.engine,c.protocol,c.inbound,c.transport,c.client_ip,c.local_ip,c.local_port,
		 c.client_port/%[1]d block,bit_or(set_bit(B'0'::bit(%[1]d),c.client_port %% %[1]d,1)) mask
		 FROM %s GROUP BY 1,2,3,4,5,6,7,8,9,10`
		return tx.Query(ctx, "WITH "+r.prefix+" chunks AS ("+fmt.Sprintf(raw, connectionPortBlock, r.raw)+`
		 UNION ALL SELECT c.day,c.agent_id,c.engine,c.protocol,c.inbound,c.transport,c.client_ip,c.local_ip,c.local_port,c.block,c.mask FROM `+r.saved+`
		 ), flows AS (SELECT day,client_ip,bit_count(bit_or(mask)) n FROM chunks
		 GROUP BY day,agent_id,engine,protocol,inbound,transport,client_ip,local_ip,local_port,block)
		 SELECT day,sum(n)::bigint,count(DISTINCT client_ip) FROM flows GROUP BY day ORDER BY day`, r.args...)
	}
	if useConnectionRollups(q) && q.Protocol == "" && q.Inbound == "" && q.Transport == "" && q.Port == 0 {
		r := connectionRollupRanges(ctx, q, nil)
		args := append(r.args, q.Bucket)
		grain := fmt.Sprintf("$%d::text", len(args))
		return tx.Query(ctx, "WITH "+r.prefix+" recent AS ("+connectionTimedFlows(r.raw, grain)+`
		 ),counts AS (SELECT bucket,client_ip,count(*) n FROM recent GROUP BY bucket,client_ip
		 UNION ALL SELECT c.day+m.slot*interval '1 minute',c.client_ip,m.n FROM days d
		 JOIN client_connection_rollup_times c ON c.agent_id=d.agent_id AND c.day=d.day
		 CROSS JOIN LATERAL unnest(c.minutes,c.flows) m(slot,n) WHERE d.covered AND c.grain=`+grain+r.timeFilters+`)
		 SELECT bucket,sum(n)::bigint,count(DISTINCT client_ip) FROM counts GROUP BY bucket ORDER BY bucket`, args...)
	}
	where, args := connectionGroupWhere(ctx, q)
	args = append(args, q.Bucket)
	return tx.Query(ctx, fmt.Sprintf(`WITH flows AS (SELECT DISTINCT date_trunc($%d::text,c.bucket,'UTC') bucket,c.agent_id,c.engine,c.protocol,c.inbound,c.transport,c.client_ip,c.client_port,c.local_ip,c.local_port
	 FROM client_connections c`, len(args))+where+`)
	 SELECT bucket,count(*),count(DISTINCT client_ip) FROM flows GROUP BY bucket ORDER BY bucket`, args...)
}

// Include the complete flow key in minute/hour deduplication even for rows
// imported with sub-minute buckets. Only nonempty offsets are persisted.
func connectionTimedFlows(from, grain string) string {
	return "SELECT DISTINCT date_trunc(" + grain + ",c.bucket,'UTC') bucket,c.agent_id,c.engine,c.protocol,c.inbound,c.transport,c.client_ip,c.client_port,c.local_ip,c.local_port FROM " + from
}

func saveConnectionTimeline(ctx context.Context, tx pgx.Tx, agent string, day, end time.Time) error {
	const from = "client_connections c WHERE c.agent_id=$1 AND c.bucket>=$2 AND c.bucket<$3 AND c.source='core_logs'"
	for _, grain := range []string{"hour", "minute"} {
		_, err := tx.Exec(ctx, `INSERT INTO client_connection_rollup_times(agent_id,day,engine,client_ip,grain,minutes,flows)
		 WITH members AS (`+connectionTimedFlows(from, "$4::text")+`),slots AS (
		 SELECT engine,client_ip,(extract(epoch FROM bucket-$2::timestamptz)/60)::smallint slot,count(*) n
		 FROM members GROUP BY engine,client_ip,bucket)
		 SELECT $1,$2,engine,client_ip,$4,array_agg(slot ORDER BY slot),array_agg(n ORDER BY slot)
		 FROM slots GROUP BY engine,client_ip`, agent, day, end, grain)
		if err != nil {
			return err
		}
	}
	return nil
}
