package store

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

type ClientConnectionQuery struct {
	// GroupByIP deduplicates within each agent and engine, never across them.
	GroupByIP bool
	// RecordsOnly is used by deferred location enrichment; authorization and
	// detail filters remain identical, without repeating expensive aggregates.
	RecordsOnly      bool
	SkipTimeline     bool
	IncludeNonPublic bool
	AgentID          string
	Engine           core.Engine
	Protocol         string
	Inbound          string
	Transport        string
	ClientIP         string
	Port             int
	Since            time.Time
	Until            time.Time
	Before           int64
	Cursor           string
	Limit            int
	Bucket           string
}

func (q ClientConnectionQuery) Validate() error {
	if q.Cursor != "" {
		if _, err := decodeConnectionIPCursor(q.Cursor); err != nil || !q.GroupByIP {
			return fmt.Errorf("%w: invalid connection cursor", ErrInvalid)
		}
	}
	if q.GroupByIP && q.Before != 0 {
		return fmt.Errorf("%w: grouped connections require a cursor", ErrInvalid)
	}
	if q.Since.IsZero() || !q.Until.After(q.Since) || q.Until.Sub(q.Since) > core.ClientConnectionQueryWindow || q.Port < 0 || q.Port > 65535 || q.Before < 0 || q.Limit < 1 || q.Limit > 200 ||
		(q.Engine != "" && !q.Engine.Valid()) || (q.Transport != "" && q.Transport != "tcp" && q.Transport != "udp") || (q.Bucket != "minute" && q.Bucket != "hour" && q.Bucket != "day") || len(q.AgentID) > 100 || len(q.Protocol) > 40 || len(q.Inbound) > 400 {
		return fmt.Errorf("%w: invalid connection query (maximum window: 32 days)", ErrInvalid)
	}
	if q.ClientIP != "" {
		if ip, err := netip.ParseAddr(q.ClientIP); err != nil || ip.Zone() != "" {
			return fmt.Errorf("%w: invalid client IP", ErrInvalid)
		}
	}
	return nil
}

func connectionHistoryWhere(ctx context.Context, q ClientConnectionQuery) (string, []any) {
	args := []any{q.Since.UTC().Truncate(time.Minute), q.Until.UTC(), q.Since.UTC()}
	where := ` WHERE c.bucket >= $1 AND c.bucket < $2 AND c.last_seen >= $3 AND c.first_seen < $2 AND a.revoked_at IS NULL AND c.source='core_logs'`
	if !q.IncludeNonPublic {
		args = append(args, netpolicy.NonPublicPrefixes())
		where += fmt.Sprintf(" AND NOT (c.client_ip <<= ANY($%d::inet[]))", len(args))
	}
	add := func(column string, value any) {
		args = append(args, value)
		where += fmt.Sprintf(" AND %s=$%d", column, len(args))
	}
	if q.AgentID != "" {
		add("c.agent_id", q.AgentID)
	}
	if q.Engine != "" {
		add("c.engine", q.Engine)
	}
	if q.Protocol != "" {
		add("c.protocol", q.Protocol)
	}
	if q.Inbound != "" {
		add("c.inbound", q.Inbound)
	}
	if q.Transport != "" {
		add("c.transport", q.Transport)
	}
	if q.ClientIP != "" {
		ip, _ := netip.ParseAddr(q.ClientIP)
		add("c.client_ip", ip.Unmap().String())
	}
	if q.Port > 0 {
		add("COALESCE(c.inbound_port,c.local_port)", q.Port)
	}
	// Client IPs are host telemetry. Shared-engine recipients must not gain
	// access to another owner's client addresses through their engine grants.
	where += agentAdministrationClause(ctx, "c.agent_id", &args)
	return where, args
}

func (s *Store) ClientConnectionHistory(ctx context.Context, q ClientConnectionQuery) (core.ClientConnectionHistory, error) {
	result := core.ClientConnectionHistory{Records: []core.ClientConnectionRecord{}, Timeline: []core.ClientConnectionBucket{}, Sources: []core.ClientConnectionSource{}}
	if err := q.Validate(); err != nil {
		return result, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	where, args := connectionHistoryWhere(ctx, q)
	from := ` FROM client_connections c JOIN agents a ON a.id=c.agent_id`
	if !q.RecordsOnly {
		// Summary is independent of pagination and counts distinct observed tuples,
		// not the number of minute rows or inferred authenticated sessions.
		tuple := `(c.agent_id,c.engine,c.protocol,c.inbound,c.transport,c.client_ip,c.client_port,c.local_ip,c.local_port)`
		if err := tx.QueryRow(ctx, `SELECT count(DISTINCT `+tuple+`),count(DISTINCT c.client_ip)`+from+where, args...).Scan(&result.Flows, &result.IPs); err != nil {
			return result, err
		}
		if !q.SkipTimeline {
			timelineArgs := append(append([]any{}, args...), q.Bucket)
			rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT date_trunc($%d::text,c.bucket,'UTC') AS time,count(DISTINCT `+tuple+`),count(DISTINCT c.client_ip)`+from+where+` GROUP BY time ORDER BY time`, len(timelineArgs)), timelineArgs...)
			if err != nil {
				return result, err
			}
			for rows.Next() {
				var bucket core.ClientConnectionBucket
				if err := rows.Scan(&bucket.Time, &bucket.Flows, &bucket.IPs); err != nil {
					rows.Close()
					return result, err
				}
				result.Timeline = append(result.Timeline, bucket)
			}
			rows.Close()
			if err = rows.Err(); err != nil {
				return result, err
			}
		}
	}

	if q.GroupByIP {
		result.Records, result.NextCursor, result.PageCursor, err = s.clientConnectionIPRecords(ctx, tx, q, where, args)
		if err != nil {
			return result, err
		}
	} else {
		recordArgs := append([]any{}, args...)
		recordWhere := where
		if q.Before > 0 {
			recordArgs = append(recordArgs, q.Before)
			recordWhere += fmt.Sprintf(" AND c.id<$%d", len(recordArgs))
		}
		recordArgs = append(recordArgs, q.Limit+1)
		rows, err := tx.Query(ctx, `SELECT c.id,c.agent_id,a.name,c.engine,c.protocol,c.inbound,c.transport,host(c.client_ip),c.client_port,CASE WHEN c.local_port=0 THEN '' ELSE host(c.local_ip) END,COALESCE(c.inbound_port,c.local_port),c.first_seen,c.last_seen`+from+recordWhere+fmt.Sprintf(` ORDER BY c.id DESC LIMIT $%d`, len(recordArgs)), recordArgs...)
		if err != nil {
			return result, err
		}
		for rows.Next() {
			var r core.ClientConnectionRecord
			if err := rows.Scan(&r.ID, &r.AgentID, &r.AgentName, &r.Engine, &r.Protocol, &r.Inbound, &r.Transport, &r.ClientIP, &r.ClientPort, &r.LocalIP, &r.LocalPort, &r.FirstSeen, &r.LastSeen); err != nil {
				rows.Close()
				return result, err
			}
			result.Records = append(result.Records, r)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return result, err
		}
		if len(result.Records) > q.Limit {
			result.Records = result.Records[:q.Limit]
			result.NextBefore = result.Records[len(result.Records)-1].ID
		}
	}
	if q.RecordsOnly {
		return result, tx.Commit(ctx)
	}
	sourceArgs := []any{}
	sourceWhere := ` WHERE a.revoked_at IS NULL` + agentAdministrationClause(ctx, "a.id", &sourceArgs)
	if q.AgentID != "" {
		sourceArgs = append(sourceArgs, q.AgentID)
		sourceWhere += fmt.Sprintf(" AND a.id=$%d", len(sourceArgs))
	}
	rows, err := tx.Query(ctx, `SELECT a.id,a.name,s.updated_at,COALESCE(s.status,'no_logs'),COALESCE(s.detail,''),COALESCE(s.truncated,false)
 FROM agents a LEFT JOIN client_connection_sources s ON s.agent_id=a.id AND s.source='core_logs'`+sourceWhere+` ORDER BY a.name,a.id`, sourceArgs...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var source core.ClientConnectionSource
		if err := rows.Scan(&source.AgentID, &source.AgentName, &source.UpdatedAt, &source.Status, &source.Detail, &source.Truncated); err != nil {
			rows.Close()
			return result, err
		}
		result.Sources = append(result.Sources, source)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (s *Store) PruneClientConnections(ctx context.Context, before time.Time) error {
	args := []any{before.UTC()}
	where := workspaceOwnerClause(ctx, "a.owner_id", &args)
	_, err := s.pool.Exec(ctx, `DELETE FROM client_connections c USING agents a WHERE c.agent_id=a.id AND c.bucket<$1 AND c.last_seen<$1`+where, args...)
	return err
}
