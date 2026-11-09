package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/netpolicy"
)

// A sync position is independent of the stable node/engine/IP page cursor.
// Snapshots are bounded, session/authority/query scoped and disposable. A miss
// returns an authoritative page, never a delta against an unknown baseline.
const connectionSyncBudget = 32 << 20
const connectionSyncGroups = 40000

type connectionGroup struct {
	Record   core.ClientConnectionRecord
	Flows    int64
	Revision int64
}
type connectionSnapshot struct {
	mu         sync.Mutex
	epoch      string
	revision   int64
	groups     map[string]connectionGroup
	ordered    []connectionGroup
	projected  bool
	ips, flows int64
	countries  []string
	created    time.Time
	touched    atomic.Int64
	bytes      atomic.Int64
}

func connectionGroupKey(r core.ClientConnectionRecord) string {
	b, _ := json.Marshal([]string{r.AgentID, string(r.Engine), r.ClientIP})
	return string(b)
}
func (s *Store) connectionSnapshot(key string) *connectionSnapshot {
	s.connectionSyncMu.Lock()
	defer s.connectionSyncMu.Unlock()
	if s.connectionSync == nil {
		s.connectionSync = map[string]*connectionSnapshot{}
	}
	for k, v := range s.connectionSync {
		if time.Since(time.Unix(0, v.touched.Load())) > 24*time.Hour {
			delete(s.connectionSync, k)
		}
	}
	entry := s.connectionSync[key]
	if entry == nil {
		entry = &connectionSnapshot{}
		s.connectionSync[key] = entry
	}
	entry.touched.Store(time.Now().UnixNano())
	return entry
}
func (s *Store) trimConnectionSnapshots() {
	s.connectionSyncMu.Lock()
	defer s.connectionSyncMu.Unlock()
	for {
		var size int64
		oldest := ""
		at := int64(1<<63 - 1)
		for k, v := range s.connectionSync {
			size += v.bytes.Load()
			if v.touched.Load() < at {
				oldest = k
				at = v.touched.Load()
			}
		}
		if len(s.connectionSync) <= 24 && size <= connectionSyncBudget {
			return
		}
		delete(s.connectionSync, oldest)
	}
}

// SyncClientConnections re-aggregates only dirty business groups, including
// mutations to older rows, deletes and geography corrections. The committed
// revision row prevents a late-committing transaction falling behind a cursor.
// Very large working sets fall back to the ordinary paginated API.
func (s *Store) SyncClientConnections(ctx context.Context, q ClientConnectionQuery, scope, token string) (core.ClientConnectionHistory, bool, error) {
	empty := core.ClientConnectionHistory{}
	if !q.GroupByIP || !q.SkipTimeline || q.RecordsOnly || scope == "" {
		return empty, false, nil
	}
	if err := q.Validate(); err != nil {
		return empty, true, err
	}
	identity := q
	identity.Cursor = ""
	payload, _ := json.Marshal([]any{scope, identity})
	digest := sha256.Sum256(payload)
	entry := s.connectionSnapshot(hex.EncodeToString(digest[:]))
	entry.mu.Lock()
	defer entry.mu.Unlock()
	defer s.trimConnectionSnapshots()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return empty, true, err
	}
	defer tx.Rollback(ctx)
	var revision int64
	if err = tx.QueryRow(ctx, `SELECT revision FROM client_connection_revision WHERE id=1`).Scan(&revision); err != nil {
		return empty, true, err
	}
	// Preserve the selected country only at projection time. Country choices and
	// distinct IP/flow totals must cover the complete authorized query, not a page.
	base := q
	base.CountryCode = ""
	if q.CountryCode != "" {
		base.IncludeNonPublic = false
	}
	var changed []core.ClientConnectionRecord
	if entry.groups == nil || time.Since(entry.created) > 24*time.Hour {
		groups, e := s.readConnectionGroups(ctx, tx, base, nil, revision)
		if e != nil {
			return empty, true, e
		}
		if len(groups) > connectionSyncGroups {
			return empty, false, nil
		}
		entry.groups = groups
		entry.projected = false
		entry.created = time.Now()
		var nonce [16]byte
		if _, err = rand.Read(nonce[:]); err != nil {
			return empty, true, err
		}
		entry.epoch = hex.EncodeToString(nonce[:])
	} else if revision != entry.revision {
		// Authority is revalidated by the API before and after this transaction.
		args := []any{entry.revision}
		where := ` WHERE ch.revision>$1 AND (ch.agent_id='' OR (a.revoked_at IS NULL` + agentAdministrationClause(ctx, "a.id", &args) + `))`
		rows, e := tx.Query(ctx, `SELECT ch.agent_id,ch.engine,host(ch.client_ip) FROM client_connection_changes ch LEFT JOIN agents a ON a.id=ch.agent_id`+where, args...)
		if e != nil {
			return empty, true, e
		}
		keys := map[string]core.ClientConnectionRecord{}
		for rows.Next() {
			var r core.ClientConnectionRecord
			if e = rows.Scan(&r.AgentID, &r.Engine, &r.ClientIP); e != nil {
				rows.Close()
				return empty, true, e
			}
			if r.AgentID == "" {
				for key, g := range entry.groups {
					if g.Record.ClientIP == r.ClientIP {
						keys[key] = g.Record
					}
				}
			} else {
				keys[connectionGroupKey(r)] = r
			}
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return empty, true, e
		}
		for _, r := range keys {
			changed = append(changed, r)
		}
		if len(changed) > 0 {
			entry.projected = false
			updates, e := s.readConnectionGroups(ctx, tx, base, changed, revision)
			if e != nil {
				return empty, true, e
			}
			for key := range keys {
				delete(entry.groups, key)
			}
			for key, g := range updates {
				entry.groups[key] = g
			}
		}
	}
	entry.revision = revision
	var bytes int64
	if !entry.projected {
		for _, g := range entry.groups {
			bytes += int64(512 + len(g.Record.AgentName)*2 + len(g.Record.Endpoints)*128)
		}
		entry.bytes.Store(bytes)
	} else {
		bytes = entry.bytes.Load()
	}
	if len(entry.groups) > connectionSyncGroups || bytes > connectionSyncBudget {
		entry.groups = nil
		entry.bytes.Store(0)
		return empty, false, nil
	}
	result, err := connectionSnapshotPage(entry, q, token)
	if err != nil {
		return empty, true, err
	}
	result.Sources, err = s.connectionSyncSources(ctx, tx, q)
	if err != nil {
		return empty, true, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, true, err
	}
	return result, true, nil
}

func (s *Store) readConnectionGroups(ctx context.Context, tx pgx.Tx, q ClientConnectionQuery, wanted []core.ClientConnectionRecord, revision int64) (map[string]connectionGroup, error) {
	where, args := connectionHistoryWhere(ctx, q)
	if wanted != nil {
		data, _ := json.Marshal(wanted)
		args = append(args, data)
		where += fmt.Sprintf(` AND (c.agent_id,c.engine,c.client_ip) IN (SELECT agent_id,engine,client_ip::inet FROM jsonb_to_recordset($%d::jsonb) AS wanted(agent_id text,engine text,client_ip text))`, len(args))
	}
	rows, err := tx.Query(ctx, connectionGroupsSQL(where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := map[string]connectionGroup{}
	for rows.Next() {
		var g connectionGroup
		var ports []int32
		r := &g.Record
		if err = rows.Scan(&r.ID, &r.AgentID, &r.AgentName, &r.Engine, &r.ClientIP, &r.FirstSeen, &r.LastSeen, &g.Flows, &ports, &r.Location.CountryCode, &r.Location.Country, &r.Location.Province); err != nil {
			return nil, err
		}
		ip, _ := netip.ParseAddr(r.ClientIP)
		r.Location.NonPublic = !netpolicy.IsPublicAddress(ip)
		sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
		for _, port := range ports {
			r.Endpoints = append(r.Endpoints, core.ClientConnectionEndpoint{AgentID: r.AgentID, AgentName: r.AgentName, Engine: r.Engine, LocalPort: int(port)})
		}
		g.Revision = revision
		groups[connectionGroupKey(*r)] = g
	}
	return groups, rows.Err()
}
func connectionSnapshotPage(entry *connectionSnapshot, q ClientConnectionQuery, token string) (core.ClientConnectionHistory, error) {
	result := core.ClientConnectionHistory{Records: []core.ClientConnectionRecord{}, Timeline: []core.ClientConnectionBucket{}, Sources: []core.ClientConnectionSource{}, CountryCodes: []string{}, RecordKeys: []string{}}
	since := int64(-1)
	if parts := strings.Split(token, ":"); len(parts) == 2 && parts[0] == entry.epoch {
		if n, e := strconv.ParseInt(parts[1], 10, 64); e == nil && n >= 0 && n <= entry.revision {
			since = n
			result.Delta = true
		}
	}
	result.SyncToken = entry.epoch + ":" + strconv.FormatInt(entry.revision, 10)
	rank := map[string]int{}
	for i, id := range q.NodeOrder {
		if _, ok := rank[id]; !ok {
			rank[id] = i + 1
		}
	}
	key := func(r core.ClientConnectionRecord) connectionIPCursor {
		n := rank[r.AgentID]
		if n == 0 {
			n = 2147483647
		}
		return connectionIPCursor{Scope: "node_engine_ip", Rank: n, Endpoint: []string{r.AgentName, r.AgentID, string(r.Engine)}, IP: r.ClientIP}
	}
	compare := func(a, b connectionIPCursor) int {
		if a.Rank < b.Rank {
			return -1
		}
		if a.Rank > b.Rank {
			return 1
		}
		for i := range a.Endpoint {
			if n := strings.Compare(a.Endpoint[i], b.Endpoint[i]); n != 0 {
				return n
			}
		}
		x, _ := netip.ParseAddr(a.IP)
		y, _ := netip.ParseAddr(b.IP)
		return x.Compare(y)
	}
	if !entry.projected {
		ips, countries := map[string]bool{}, map[string]bool{}
		groups := []connectionGroup{}
		for _, g := range entry.groups {
			r := g.Record
			if !r.Location.NonPublic && r.Location.CountryCode != "" {
				countries[r.Location.CountryCode] = true
			}
			if q.CountryCode != "" && (r.Location.NonPublic || r.Location.CountryCode != q.CountryCode) {
				continue
			}
			result.Flows += g.Flows
			ips[r.ClientIP] = true
			groups = append(groups, g)
		}
		result.IPs = int64(len(ips))
		for code := range countries {
			result.CountryCodes = append(result.CountryCodes, code)
		}
		sort.Strings(result.CountryCodes)
		sort.Slice(groups, func(i, j int) bool { return compare(key(groups[i].Record), key(groups[j].Record)) < 0 })
		entry.ordered = groups
		entry.ips = result.IPs
		entry.flows = result.Flows
		entry.countries = result.CountryCodes
		entry.projected = true
	}
	result.IPs = entry.ips
	result.Flows = entry.flows
	result.CountryCodes = entry.countries
	groups := entry.ordered

	if q.Cursor != "" {
		cursor, err := decodeConnectionIPCursor(q.Cursor)
		if err != nil {
			return result, err
		}
		start := sort.Search(len(groups), func(i int) bool {
			n := compare(key(groups[i].Record), cursor)
			return n > 0 || n == 0 && cursor.Inclusive
		})
		groups = groups[start:]
	}
	if len(groups) > q.Limit {
		result.NextCursor = key(groups[q.Limit-1].Record).encode()
		groups = groups[:q.Limit]
	}
	if len(groups) > 0 {
		first := key(groups[0].Record)
		first.Inclusive = true
		result.PageCursor = first.encode()
	}
	for _, g := range groups {
		result.RecordKeys = append(result.RecordKeys, connectionGroupKey(g.Record))
		if g.Revision > since {
			result.Records = append(result.Records, g.Record)
		}
	}
	return result, nil
}
func (s *Store) connectionSyncSources(ctx context.Context, tx pgx.Tx, q ClientConnectionQuery) ([]core.ClientConnectionSource, error) {
	args := []any{}
	where := ` WHERE a.revoked_at IS NULL` + agentAdministrationClause(ctx, "a.id", &args)
	if q.AgentID != "" {
		args = append(args, q.AgentID)
		where += fmt.Sprintf(` AND a.id=$%d`, len(args))
	}
	rows, err := tx.Query(ctx, `SELECT a.id,a.name,s.updated_at,COALESCE(s.status,'no_logs'),COALESCE(s.detail,''),COALESCE(s.truncated,false) FROM agents a LEFT JOIN client_connection_sources s ON s.agent_id=a.id AND s.source='core_logs'`+where+` ORDER BY a.name,a.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := []core.ClientConnectionSource{}
	for rows.Next() {
		var r core.ClientConnectionSource
		if err = rows.Scan(&r.AgentID, &r.AgentName, &r.UpdatedAt, &r.Status, &r.Detail, &r.Truncated); err != nil {
			return nil, err
		}
		sources = append(sources, r)
	}
	return sources, rows.Err()
}

func connectionGroupsSQL(where string) string {
	return `SELECT min(c.id),c.agent_id,a.name,c.engine,host(c.client_ip),min(c.first_seen),max(c.last_seen),
 count(DISTINCT (c.protocol,c.inbound,c.transport,c.client_port,c.local_ip,c.local_port)),
 array_agg(DISTINCT COALESCE(c.inbound_port,c.local_port)),COALESCE(l.country_code,''),COALESCE(l.country,''),COALESCE(l.province,'')
 FROM client_connections c JOIN agents a ON a.id=c.agent_id LEFT JOIN client_connection_locations l ON l.client_ip=c.client_ip` + where + `
 GROUP BY c.agent_id,a.name,c.engine,c.client_ip,l.country_code,l.country,l.province LIMIT 40001`
}
