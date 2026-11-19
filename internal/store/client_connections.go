package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/geoip"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

// ClientConnectionScope only reads node metadata, using the same host telemetry
// authority as history. Shared kernel grants deliberately do not enter it.
func (s *Store) ClientConnectionScope(ctx context.Context) (string, error) {
	scope, _, err := s.ClientConnectionAuthority(ctx)
	return scope, err
}

func (s *Store) ClientConnectionAuthority(ctx context.Context) (string, int64, error) {
	args := []any{}
	where := ` WHERE a.revoked_at IS NULL` + agentAdministrationClause(ctx, "a.id", &args)
	rows, err := s.pool.Query(ctx, `SELECT ca.revision,ca.installation::text,node.id,node.name,node.owner_id
	  FROM client_connection_authority ca
	  LEFT JOIN LATERAL (SELECT a.id,a.name,a.owner_id FROM agents a`+where+`) node ON true
	  WHERE ca.id=1 ORDER BY node.id`, args...)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	var revision int64
	first := true
	for rows.Next() {
		var installation string
		var id, name, owner *string
		if err := rows.Scan(&revision, &installation, &id, &name, &owner); err != nil {
			return "", 0, err
		}
		if first {
			_, _ = hash.Write([]byte(installation))
			first = false
		}
		if id != nil {
			if err := encoder.Encode([]*string{id, name, owner}); err != nil {
				return "", 0, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	if first {
		return "", 0, pgx.ErrNoRows
	}
	return hex.EncodeToString(hash.Sum(nil)), revision, nil
}

func (s *Store) ClientConnectionAuthorityRevision(ctx context.Context) (int64, error) {
	var revision int64
	err := s.pool.QueryRow(ctx, `SELECT revision FROM client_connection_authority WHERE id=1`).Scan(&revision)
	return revision, err
}

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
	CountryCode      string
	NodeOrder        []string
	Port             int
	Since            time.Time
	Until            time.Time
	Before           int64
	Cursor           string
	Limit            int
	Bucket           string
}

func (q ClientConnectionQuery) Validate() error {
	if q.CountryCode != "" && !geoip.ValidRegionCode(q.CountryCode) {
		return fmt.Errorf("%w: unsupported country/region code", ErrInvalid)
	}
	if len(q.NodeOrder) > 500 {
		return fmt.Errorf("%w: too many ordered nodes", ErrInvalid)
	}
	for _, id := range q.NodeOrder {
		if len(id) == 0 || len(id) > 100 || strings.ContainsRune(id, '\x00') {
			return fmt.Errorf("%w: invalid ordered node", ErrInvalid)
		}
	}
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

func connectionHistoryFilters(q ClientConnectionQuery) (string, []any) {
	args := []any{q.Since.UTC().Truncate(time.Minute), q.Until.UTC(), q.Since.UTC()}
	where := ` WHERE c.bucket >= $1 AND c.bucket < $2 AND c.last_seen >= $3 AND c.first_seen < $2 AND c.source='core_logs'`
	if !q.IncludeNonPublic || q.CountryCode != "" {
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
	if q.CountryCode != "" {
		args = append(args, q.CountryCode)
		where += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM client_connection_locations location WHERE location.client_ip=c.client_ip AND location.country_code=$%d)", len(args))
	}
	if q.Port > 0 {
		add("COALESCE(c.inbound_port,c.local_port)", q.Port)
	}
	return where, args
}

func connectionHistoryWhere(ctx context.Context, q ClientConnectionQuery) (string, []any) {
	where, args := connectionHistoryFilters(q)
	// Client IPs are host telemetry. Shared-engine recipients must not gain
	// access to another owner's client addresses through their engine grants.
	return where + " AND a.revoked_at IS NULL" + agentAdministrationClause(ctx, "a.id", &args), args
}

func connectionGroupWhere(ctx context.Context, q ClientConnectionQuery) (string, []any) {
	where, args := connectionHistoryFilters(q)
	nodes := `SELECT ARRAY(SELECT a.id FROM agents a WHERE a.revoked_at IS NULL` + agentAdministrationClause(ctx, "a.id", &args) + `)`
	return where + ` AND c.agent_id=ANY((` + nodes + `)::text[])`, args
}

func (s *Store) ClientConnectionHistory(ctx context.Context, q ClientConnectionQuery) (core.ClientConnectionHistory, error) {
	result := core.ClientConnectionHistory{Records: []core.ClientConnectionRecord{}, Timeline: []core.ClientConnectionBucket{}, Sources: []core.ClientConnectionSource{}, CountryCodes: []string{}}
	q.CountryCode = strings.ToUpper(strings.TrimSpace(q.CountryCode))
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
	if useConnectionRollups(q) {
		if _, err := tx.Exec(ctx, "SET LOCAL jit = off"); err != nil {
			return result, err
		}
	}
	if !q.RecordsOnly {
		if useConnectionRollups(q) {
			if err := connectionRollupTotals(ctx, tx, q, &result); err != nil {
				return result, err
			}
		} else {
			base := q
			base.CountryCode = ""
			base.IncludeNonPublic = q.IncludeNonPublic && q.CountryCode == ""
			summaryWhere, summaryArgs := connectionGroupWhere(ctx, base)
			summaryArgs = append(summaryArgs, q.CountryCode, netpolicy.NonPublicPrefixes())
			country := fmt.Sprintf("($%d='' OR l.country_code=$%d)", len(summaryArgs)-1, len(summaryArgs)-1)
			public := fmt.Sprintf("NOT (ip.client_ip <<= ANY($%d::inet[]))", len(summaryArgs))
			if err := tx.QueryRow(ctx, `WITH flow AS (
		  SELECT DISTINCT c.agent_id,c.engine,c.protocol,c.inbound,c.transport,c.client_ip,c.client_port,c.local_ip,c.local_port
		  FROM client_connections c`+summaryWhere+`
		), ip AS (SELECT client_ip,count(*) flows FROM flow GROUP BY client_ip)
		SELECT COALESCE(sum(ip.flows) FILTER (WHERE `+country+`),0), count(*) FILTER (WHERE `+country+`),
		  COALESCE(array_agg(DISTINCT l.country_code ORDER BY l.country_code)
		    FILTER (WHERE l.country_code<>'' AND `+public+`), ARRAY[]::varchar[])
		FROM ip LEFT JOIN client_connection_locations l ON l.client_ip=ip.client_ip`, summaryArgs...).Scan(&result.Flows, &result.IPs, &result.CountryCodes); err != nil {
				return result, err
			}
		}
		result.CountryCodes = slices.DeleteFunc(result.CountryCodes, func(code string) bool { return !geoip.ValidRegionCode(code) })
		if !q.SkipTimeline {
			rows, err := connectionTimeline(ctx, tx, q)
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
	// Country membership is determined in this transaction, before grouping,
	// totals and pagination. Unknown and non-public IPs cannot match a country.
	for i := range result.Records {
		result.Records[i].Location.CountryCode = q.CountryCode
	}
	if q.RecordsOnly {
		return result, tx.Commit(ctx)
	}
	sources, sourceArgs := connectionSourceResponse(ctx, q, nil)
	if err := tx.QueryRow(ctx, `SELECT `+sources, sourceArgs...).Scan(&result.Sources); err != nil {
		return result, err
	}
	if result.Sources == nil {
		return result, errClientConnectionPageBudget
	}

	return result, tx.Commit(ctx)
}

func (s *Store) PruneClientConnections(ctx context.Context, before time.Time) error {
	args := []any{before.UTC()}
	where := workspaceOwnerClause(ctx, "a.owner_id", &args)
	_, err := s.pool.Exec(ctx, `DELETE FROM client_connections c USING agents a WHERE c.agent_id=a.id AND c.bucket<$1 AND c.last_seen<$1`+where, args...)
	return err
}
