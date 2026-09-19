package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

type ClientConnectionQuery struct {
	AgentID   string
	Engine    core.Engine
	Protocol  string
	Inbound   string
	Transport string
	ClientIP  string
	Port      int
	Since     time.Time
	Until     time.Time
	Before    int64
	Limit     int
	Bucket    string
}

func (q ClientConnectionQuery) Validate() error {
	if q.Since.IsZero() || !q.Until.After(q.Since) || q.Until.Sub(q.Since) > core.ClientConnectionRetention || q.Port < 0 || q.Port > 65535 || q.Before < 0 || q.Limit < 1 || q.Limit > 200 ||
		(q.Engine != "" && !q.Engine.Valid()) || (q.Transport != "" && q.Transport != "tcp" && q.Transport != "udp") || (q.Bucket != "minute" && q.Bucket != "hour" && q.Bucket != "day") || len(q.AgentID) > 100 || len(q.Protocol) > 40 || len(q.Inbound) > 400 {
		return fmt.Errorf("%w: invalid connection query (maximum window: 7 days)", ErrInvalid)
	}
	if q.ClientIP != "" {
		if ip, err := netip.ParseAddr(q.ClientIP); err != nil || ip.Zone() != "" {
			return fmt.Errorf("%w: invalid client IP", ErrInvalid)
		}
	}
	return nil
}

func (s *Store) StoreClientConnections(ctx context.Context, agentID string, report core.ClientConnectionReport) error {
	if err := report.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	// Receive time is authoritative; node clock skew cannot poison retention or
	// timeline ordering. The authenticated WSS session supplies the agent ID.
	now := time.Now().UTC()
	entries := append([]core.ClientConnection{}, report.Connections...)
	for i := range entries {
		client, _ := netip.ParseAddr(entries[i].ClientIP)
		local, _ := netip.ParseAddr(entries[i].LocalIP)
		entries[i].ClientIP = client.Unmap().String()
		entries[i].LocalIP = local.Unmap().String()
	}
	if entries == nil {
		entries = []core.ClientConnection{}
	}
	payload, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO client_connection_sources(agent_id,updated_at,status,detail,truncated)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT(agent_id) DO UPDATE SET updated_at=EXCLUDED.updated_at,
 status=EXCLUDED.status,detail=EXCLUDED.detail,truncated=EXCLUDED.truncated`, agentID, now, report.Status, report.Detail, report.Truncated); err != nil {
		return err
	}
	// DISTINCT also makes duplicate tuples in an otherwise valid report harmless.
	_, err = tx.Exec(ctx, `INSERT INTO client_connections(agent_id,bucket,engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port,first_seen,last_seen)
 SELECT DISTINCT $1::text,$2::timestamptz,engine,protocol,inbound,transport,client_ip::inet,client_port,local_ip::inet,local_port,$3::timestamptz,$3::timestamptz
 FROM jsonb_to_recordset($4::jsonb) AS x(engine text,protocol text,inbound text,transport text,client_ip text,client_port integer,local_ip text,local_port integer)
 ON CONFLICT(agent_id,bucket,engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port)
 DO UPDATE SET last_seen=GREATEST(client_connections.last_seen,EXCLUDED.last_seen)`, agentID, now.Truncate(time.Minute), now, payload)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func connectionHistoryWhere(ctx context.Context, q ClientConnectionQuery) (string, []any) {
	args := []any{q.Since.UTC().Truncate(time.Minute), q.Until.UTC(), q.Since.UTC()}
	where := ` WHERE c.bucket >= $1 AND c.bucket < $2 AND c.last_seen >= $3 AND c.first_seen < $2 AND a.revoked_at IS NULL`
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
		add("c.local_port", q.Port)
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
	// Summary is independent of pagination and counts distinct observed tuples,
	// not the number of minute rows or inferred authenticated sessions.
	tuple := `(c.agent_id,c.engine,c.protocol,c.inbound,c.transport,c.client_ip,c.client_port,c.local_ip,c.local_port)`
	if err := tx.QueryRow(ctx, `SELECT count(DISTINCT `+tuple+`),count(DISTINCT c.client_ip)`+from+where, args...).Scan(&result.Flows, &result.IPs); err != nil {
		return result, err
	}
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
	recordArgs := append([]any{}, args...)
	recordWhere := where
	if q.Before > 0 {
		recordArgs = append(recordArgs, q.Before)
		recordWhere += fmt.Sprintf(" AND c.id<$%d", len(recordArgs))
	}
	recordArgs = append(recordArgs, q.Limit+1)
	rows, err = tx.Query(ctx, `SELECT c.id,c.agent_id,a.name,c.engine,c.protocol,c.inbound,c.transport,host(c.client_ip),c.client_port,host(c.local_ip),c.local_port,c.first_seen,c.last_seen`+from+recordWhere+fmt.Sprintf(` ORDER BY c.id DESC LIMIT $%d`, len(recordArgs)), recordArgs...)
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
	sourceArgs := []any{}
	sourceWhere := ` WHERE a.revoked_at IS NULL` + agentAdministrationClause(ctx, "a.id", &sourceArgs)
	if q.AgentID != "" {
		sourceArgs = append(sourceArgs, q.AgentID)
		sourceWhere += fmt.Sprintf(" AND a.id=$%d", len(sourceArgs))
	}
	rows, err = tx.Query(ctx, `SELECT a.id,a.name,s.updated_at,COALESCE(s.status,'unsupported'),COALESCE(s.detail,''),COALESCE(s.truncated,false)
 FROM agents a LEFT JOIN client_connection_sources s ON s.agent_id=a.id`+sourceWhere+` ORDER BY a.name,a.id`, sourceArgs...)
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
	_, err := s.pool.Exec(ctx, `DELETE FROM client_connections WHERE bucket<$1`, before.UTC())
	return err
}
