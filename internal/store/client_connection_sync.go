package store

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"runtime/debug"
	"slices"
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

// Keep the in-process working set bounded even when several accounts query
// distinct windows. Durable entries share the journal's retention boundary.
const (
	connectionSyncBudget       = 32 << 20
	connectionSnapshotEntries  = 64
	connectionSnapshotBackoff  = time.Minute
	clientConnectionPageBudget = 1 << 20
	connectionLookupEstimate   = 256
	connectionSourceEstimate   = 192
	connectionSyncGroups       = 40000
	connectionSyncTTL          = 24 * time.Hour
	connectionJournalTTL       = 2 * connectionSyncTTL
	connectionRecordEstimate   = 512
	connectionEndpointEstimate = 128
)

var (
	errConnectionSnapshotBudget   = errors.New("client connection snapshot exceeded its budget")
	errConnectionSnapshotBusy     = errors.New("client connection snapshot builder busy")
	errClientConnectionPageBudget = fmt.Errorf("查询结果过大，请缩小时间范围、筛选条件或减少每页条数：%w", ErrInvalid)
)

func connectionCacheBudget() int64 {
	// Leave headroom for the previous view, new projection, page encoding
	// and the rest of the panel when Go has a configured soft heap limit.
	return min(connectionSyncBudget, debug.SetMemoryLimit(-1)/8)
}

func connectionEndpointBytes(ep core.ClientConnectionEndpoint) int64 {
	return int64(connectionEndpointEstimate + len(ep.AgentID) + len(ep.AgentName) + len(ep.Engine))
}

func connectionRecordBytes(r core.ClientConnectionRecord) int64 {
	size := int64(connectionRecordEstimate + len(r.AgentID) + len(r.AgentName) + len(r.Engine) +
		len(r.ClientIP) + len(r.Protocol) + len(r.Inbound) + len(r.Transport) + len(r.LocalIP) +
		len(r.Location.CountryCode) + len(r.Location.Country) + len(r.Location.Province))
	for _, ep := range r.Endpoints {
		size += connectionEndpointBytes(ep)
	}
	return size
}

type connectionLookupKey struct {
	AgentID  string      `json:"agent_id"`
	Engine   core.Engine `json:"engine"`
	ClientIP string      `json:"client_ip"`
}

func connectionLookup(r core.ClientConnectionRecord) connectionLookupKey {
	return connectionLookupKey{r.AgentID, r.Engine, r.ClientIP}
}

type connectionGroup struct {
	Record   core.ClientConnectionRecord
	Flows    int64
	Revision int64 `json:"-"`
	addr     netip.Addr
}

type connectionSnapshot struct {
	mu      sync.Mutex
	pending *connectionSyncFlight
	backoff time.Time
	data    *connectionSnapshotData
	touched atomic.Int64
	bytes   atomic.Int64
}

type connectionSyncFlight struct {
	done        chan struct{}
	data        *connectionSnapshotData
	sources     []core.ClientConnectionSource
	err         error
	recordsOnly bool
}

// A published snapshot is immutable except for its lazily computed projection.
// The projection lock never encloses database I/O.
type connectionSnapshotData struct {
	epoch, position, generation string
	revision                    int64
	groups                      map[string]connectionGroup
	byIP                        map[string][]string
	bytes                       int64
	created                     time.Time
	projection                  *connectionProjectionCache
}

func connectionGroupKey(r core.ClientConnectionRecord) string {
	b, _ := json.Marshal([]string{r.AgentID, string(r.Engine), r.ClientIP})
	return string(b)
}

func connectionDigest(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *Store) connectionSnapshot(key string) *connectionSnapshot {
	s.connectionSyncMu.Lock()
	defer s.connectionSyncMu.Unlock()
	if s.connectionSync == nil {
		s.connectionSync = map[string]*connectionSnapshot{}
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
		at := time.Now().UnixNano()
		for k, v := range s.connectionSync {
			size += v.bytes.Load() + connectionRecordEstimate
			if v.touched.Load() <= at {
				oldest, at = k, v.touched.Load()
			}
		}
		if len(s.connectionSync) == 0 || size <= connectionCacheBudget() && len(s.connectionSync) <= connectionSnapshotEntries && time.Since(time.Unix(0, at)) < connectionSyncTTL {
			return
		}
		delete(s.connectionSync, oldest)
	}
}

func connectionBaseQuery(q ClientConnectionQuery) ClientConnectionQuery {
	q.Since, q.Until = q.Since.UTC().Truncate(time.Microsecond), q.Until.UTC().Truncate(time.Microsecond)
	q.Cursor, q.CountryCode, q.Bucket = "", "", ""
	q.NodeOrder, q.Limit = nil, 0
	q.RecordsOnly = false
	if q.ClientIP != "" {
		ip, _ := netip.ParseAddr(q.ClientIP)
		q.ClientIP = ip.Unmap().String()
	}
	return q
}

// Cache complete historical day/month windows exactly as requested, including
// the client's UTC offset. Arbitrary sliding and highly selective queries use
// the bounded in-process tier. Sealed windows can still receive late changes.
func persistConnectionQuery(q ClientConnectionQuery, now time.Time) bool {
	return !q.Until.After(now) && q.Until.Sub(q.Since) >= 23*time.Hour &&
		q.Since.Equal(q.Since.Truncate(time.Minute)) && q.Until.Equal(q.Until.Truncate(time.Minute)) &&
		q.ClientIP == "" && q.Port == 0 && q.Protocol == "" && q.Inbound == "" && q.Transport == ""
}

// SyncClientConnections builds the aggregate once for an authorized base
// query. A token is bound to its projection; it never conveys authority.
func (s *Store) SyncClientConnections(ctx context.Context, q ClientConnectionQuery, scope, token string) (core.ClientConnectionHistory, bool, error) {
	if !q.GroupByIP || !q.SkipTimeline || scope == "" {
		return core.ClientConnectionHistory{}, false, nil
	}
	if err := q.Validate(); err != nil {
		return core.ClientConnectionHistory{}, true, err
	}
	base := connectionBaseQuery(q)
	key := connectionDigest([]any{scope, scopeForConfig(ctx), base})
	entry := s.connectionSnapshot(key)
	defer s.trimConnectionSnapshots()
	for {
		entry.mu.Lock()
		if active := entry.pending; active != nil {
			entry.mu.Unlock()
			select {
			case <-ctx.Done():
				return core.ClientConnectionHistory{}, true, ctx.Err()
			case <-active.done:
				// Overlapping pages share the same consistent DB view. A
				// canceled leader or a location-only view cannot serve this
				// request's complete response; start a new read in that case.
				if active.err != nil || active.recordsOnly && !q.RecordsOnly {
					continue
				}
				return active.page(q, token)
			}
		}
		if time.Now().Before(entry.backoff) {
			entry.mu.Unlock()
			return core.ClientConnectionHistory{}, false, nil
		}
		flight := &connectionSyncFlight{done: make(chan struct{}), recordsOnly: q.RecordsOnly}
		previous := entry.data
		entry.pending = flight
		entry.mu.Unlock()
		flight.data, flight.sources, flight.err = s.refreshConnectionSnapshot(ctx, base, key, previous, q.RecordsOnly)
		entry.mu.Lock()
		switch {
		case errors.Is(flight.err, errConnectionSnapshotBusy):
			// An unrelated builder is using the work budget. Leave any
			// existing snapshot available to future requests.
			flight.err = nil
		case errors.Is(flight.err, errConnectionSnapshotBudget):
			flight.err = nil
			entry.data = nil
			entry.bytes.Store(0)
			entry.backoff = time.Now().Add(connectionSnapshotBackoff)
		case flight.err == nil:
			entry.data = flight.data
			if flight.data != nil {
				entry.bytes.Store(flight.data.bytes)
			} else {
				entry.bytes.Store(0)
			}
		}
		close(flight.done)
		entry.pending = nil
		entry.mu.Unlock()
		return flight.page(q, token)
	}
}

func (f *connectionSyncFlight) page(q ClientConnectionQuery, token string) (core.ClientConnectionHistory, bool, error) {
	if f.err != nil || f.data == nil {
		return core.ClientConnectionHistory{}, f.err != nil, f.err
	}
	result, err := connectionSnapshotPage(f.data, q, token)
	if !q.RecordsOnly {
		result.Sources = f.sources
	}
	return result, true, err
}

func (s *Store) refreshConnectionSnapshot(ctx context.Context, q ClientConnectionQuery, key string, previous *connectionSnapshotData, recordsOnly bool) (*connectionSnapshotData, []core.ClientConnectionSource, error) {
	budget := connectionCacheBudget() / 2
	if budget < connectionRecordEstimate {
		return nil, nil, errConnectionSnapshotBudget
	}
	now := time.Now()
	if previous != nil && (now.Sub(previous.created) >= connectionSyncTTL || previous.bytes > budget) {
		previous = nil
	}
	building := false
	acquire := func() bool {
		if !building {
			building = s.connectionSyncWork.TryLock()
		}
		return building
	}
	defer func() {
		if building {
			s.connectionSyncWork.Unlock()
		}
	}()
	if previous == nil && !acquire() {
		return nil, nil, errConnectionSnapshotBusy
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	durable := persistConnectionQuery(q, now)
	position, sources, unchanged, err := connectionSyncPosition(ctx, tx, q, recordsOnly, previous)
	if err != nil {
		return nil, nil, err
	}
	if previous == nil && durable {
		previous, err = restoreConnectionSnapshot(ctx, tx, key, now, budget)
		if err != nil {
			return nil, nil, err
		}
	}
	var changeKeys map[string]connectionLookupKey
	complete := false
	generation := ""
	if previous != nil {
		generation = previous.generation
		complete = true
		if !unchanged && position != previous.position {
			if !acquire() {
				return nil, nil, errConnectionSnapshotBusy
			}
			complete, changeKeys, err = connectionDirtyGroups(ctx, tx, q, previous, budget)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	var data *connectionSnapshotData
	var upserts map[string]connectionGroup
	if !complete {
		upserts, err = s.readConnectionGroups(ctx, tx, q, nil, 1, budget)
		if err != nil {
			return nil, nil, err
		}
		data = newConnectionSnapshot(upserts, position, generation, now)
	} else if len(changeKeys) == 0 {
		data = previous
	} else {
		upserts, err = s.readConnectionGroups(ctx, tx, q, slices.Collect(maps.Values(changeKeys)), previous.revision+1, budget)
		if err != nil {
			return nil, nil, err
		}
		size, count := previous.bytes, len(previous.groups)
		for key := range changeKeys {
			next, exists := upserts[key]
			old, had := previous.groups[key]
			if exists && had && equalConnectionGroup(next, old) || !exists && !had {
				delete(changeKeys, key)
				delete(upserts, key)
				continue
			}
			if had {
				size -= connectionSnapshotGroupBytes(key, old)
				count--
			}
			if exists {
				size += connectionSnapshotGroupBytes(key, next)
				count++
			}
		}
		if size > budget || count > connectionSyncGroups {
			return nil, nil, errConnectionSnapshotBudget
		}
		if len(changeKeys) == 0 {
			data = previous
		} else {
			groups := maps.Clone(previous.groups)
			for key := range changeKeys {
				delete(groups, key)
			}
			maps.Copy(groups, upserts)
			data = &connectionSnapshotData{epoch: previous.epoch, position: position, generation: generation,
				revision: previous.revision + 1, groups: groups, created: previous.created}
			data.index()
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}

	if durable && data != previous && (len(data.groups) != 0 || data.generation != "") {
		// The repeatable-read view has already released its connection. Only the
		// derived rows that changed are written, guarded by a cross-process CAS.
		if err := s.saveConnectionSnapshot(ctx, key, data, !complete, changeKeys, upserts); err != nil && ctx.Err() == nil {
			slog.Warn("persist client connection history cache", "error", err)
		}
	}
	if data == previous && position != data.position {
		// Advance the in-memory read position without rewriting any stored
		// rows. Reuse the projection even during unrelated telemetry writes.
		data = &connectionSnapshotData{epoch: data.epoch, position: position, generation: data.generation,
			revision: data.revision, groups: data.groups, byIP: data.byIP, bytes: data.bytes, created: data.created,
			projection: data.projection}
	}
	return data, sources, nil
}

func equalConnectionGroup(a, b connectionGroup) bool {
	x, y := a.Record, b.Record
	return a.Flows == b.Flows && x.ID == y.ID && x.AgentName == y.AgentName &&
		x.Location == y.Location && x.FirstSeen.Equal(y.FirstSeen) && x.LastSeen.Equal(y.LastSeen) &&
		slices.Equal(x.Endpoints, y.Endpoints)
}

func newConnectionSnapshot(groups map[string]connectionGroup, position, generation string, now time.Time) *connectionSnapshotData {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	data := &connectionSnapshotData{epoch: hex.EncodeToString(nonce[:]), position: position, generation: generation,
		groups: groups, revision: 1, created: now}
	data.index()
	return data
}

// The base allowance includes the record, its map/index entries and two
// projection copies. Charge variable strings and endpoints independently.
func connectionSnapshotGroupBytes(key string, group connectionGroup) int64 {
	return connectionRecordBytes(group.Record) + int64(len(key)) + connectionRecordEstimate
}

func (data *connectionSnapshotData) index() {
	data.projection = &connectionProjectionCache{}
	data.byIP = make(map[string][]string)
	for key, group := range data.groups {
		data.byIP[group.Record.ClientIP] = append(data.byIP[group.Record.ClientIP], key)
		data.bytes += connectionSnapshotGroupBytes(key, group)
	}
}

func connectionDirtyGroups(ctx context.Context, tx pgx.Tx, q ClientConnectionQuery, old *connectionSnapshotData, budget int64) (bool, map[string]connectionLookupKey, error) {
	var complete bool
	err := tx.QueryRow(ctx, `SELECT xid < pg_snapshot_xmin($1::text::pg_snapshot) FROM client_connection_journal_floor WHERE id=1`, old.position).Scan(&complete)
	if err != nil || !complete {
		return complete, nil, err
	}
	args := []any{old.position, q.Since.Truncate(time.Minute), q.Until.Add(-time.Microsecond), q.AgentID, string(q.Engine), q.ClientIP}
	agentClause := agentAdministrationClause(ctx, "a.id", &args)
	limit := min(connectionSyncGroups, int(budget/4/connectionLookupEstimate))
	args = append(args, limit+1)
	// Snapshot visibility is the high-water range plus transactions active
	// at the last read. Probe those IDs directly so an unrelated long writer
	// cannot make every confirmation scan the entire intervening journal.
	rows, err := tx.Query(ctx, `WITH changes AS (
	   SELECT agent_id,engine,client_ip,day FROM client_connection_journal WHERE xid >= pg_snapshot_xmax($1::text::pg_snapshot)
	   UNION ALL
	   SELECT pending.* FROM pg_snapshot_xip($1::text::pg_snapshot) AS active(xid)
	   CROSS JOIN LATERAL (
	     SELECT agent_id,engine,client_ip,day FROM client_connection_journal WHERE xid=active.xid OFFSET 0
	   ) pending
	 )
	 SELECT DISTINCT ch.agent_id,ch.engine,host(ch.client_ip) FROM changes ch
	 LEFT JOIN agents a ON a.id=ch.agent_id
	 WHERE ($6='' OR ch.client_ip=NULLIF($6,'')::inet)
	 AND (ch.agent_id='' OR (ch.day BETWEEN ($2::timestamptz AT TIME ZONE 'UTC')::date AND ($3::timestamptz AT TIME ZONE 'UTC')::date
	   AND ($4='' OR ch.agent_id=$4) AND ($5='' OR ch.engine=$5) AND (a.id IS NULL OR (a.revoked_at IS NULL`+agentClause+fmt.Sprintf(`)))) LIMIT $%d`, len(args)), args...)
	if err != nil {
		return false, nil, err
	}
	defer rows.Close()
	keys := map[string]connectionLookupKey{}
	seen := 0
	for rows.Next() {
		seen++
		if seen > limit {
			return false, nil, nil
		}
		var r core.ClientConnectionRecord
		if err := rows.Scan(&r.AgentID, &r.Engine, &r.ClientIP); err != nil {
			return false, nil, err
		}
		if r.AgentID == "" {
			for _, key := range old.byIP[r.ClientIP] {
				keys[key] = connectionLookup(old.groups[key].Record)
				if len(keys) > limit {
					return false, nil, nil
				}
			}
		} else {
			keys[connectionGroupKey(r)] = connectionLookup(r)
			if len(keys) > limit {
				return false, nil, nil
			}
		}
	}
	return true, keys, rows.Err()
}

func (s *Store) readConnectionGroups(ctx context.Context, tx pgx.Tx, q ClientConnectionQuery, wanted []connectionLookupKey, revision int64, budget int64) (map[string]connectionGroup, error) {
	where, args := connectionGroupWhere(ctx, q)
	if wanted != nil {
		data, _ := json.Marshal(wanted)
		args = append(args, data)
		where += fmt.Sprintf(` AND (c.agent_id,c.engine,c.client_ip) IN (SELECT agent_id,engine,client_ip::inet FROM jsonb_to_recordset($%d::jsonb) AS wanted(agent_id text,engine text,client_ip text))`, len(args))
	}
	limit := min(connectionSyncGroups, int(budget/(2*connectionRecordEstimate)))
	rows, err := tx.Query(ctx, connectionGroupsSQL(where, len(args)+1), append(args, limit+1)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := map[string]connectionGroup{}
	var remaining = budget
	for rows.Next() {
		if len(groups) >= limit {
			return nil, errConnectionSnapshotBudget
		}
		var g connectionGroup
		var ports []int32
		r := &g.Record
		if err = rows.Scan(&r.ID, &r.AgentID, &r.AgentName, &r.Engine, &r.ClientIP, &r.FirstSeen, &r.LastSeen, &g.Flows, &ports, &r.Location.CountryCode, &r.Location.Country, &r.Location.Province); err != nil {
			return nil, err
		}
		key := connectionGroupKey(*r)
		cost := connectionSnapshotGroupBytes(key, g) + int64(len(ports))*connectionEndpointBytes(core.ClientConnectionEndpoint{AgentID: r.AgentID, AgentName: r.AgentName, Engine: r.Engine})
		if cost > remaining {
			return nil, errConnectionSnapshotBudget
		}
		remaining -= cost
		g.addr, _ = netip.ParseAddr(r.ClientIP)
		r.Location.NonPublic = !netpolicy.IsPublicAddress(g.addr)
		slices.Sort(ports)
		r.Endpoints = make([]core.ClientConnectionEndpoint, 0, len(ports))
		for _, port := range ports {
			r.Endpoints = append(r.Endpoints, core.ClientConnectionEndpoint{AgentID: r.AgentID, AgentName: r.AgentName, Engine: r.Engine, LocalPort: int(port)})
		}
		g.Revision = revision
		groups[key] = g
	}
	return groups, rows.Err()
}

type connectionProjectionCache struct {
	sync.Mutex
	orderID string
	view    *connectionProjection
}

type connectionProjection struct {
	countries []string
	pages     map[string]*connectionProjectionRows
}

type connectionProjectionRows struct {
	groups     []connectionGroup
	ips, flows int64
}

func (p *connectionProjectionRows) add(group connectionGroup, ips map[string]bool) {
	p.groups = append(p.groups, group)
	p.flows += group.Flows
	if !ips[group.Record.ClientIP] {
		ips[group.Record.ClientIP] = true
		p.ips++
	}
}

type connectionOrderKey struct {
	rank               int
	name, node, engine string
	ip                 netip.Addr
}

func compareConnectionOrder(a, b connectionOrderKey) int {
	if d := cmp.Compare(a.rank, b.rank); d != 0 {
		return d
	}
	if d := cmp.Compare(a.name, b.name); d != 0 {
		return d
	}
	if d := cmp.Compare(a.node, b.node); d != 0 {
		return d
	}
	if d := cmp.Compare(a.engine, b.engine); d != 0 {
		return d
	}
	return a.ip.Compare(b.ip)
}

func connectionSnapshotPage(entry *connectionSnapshotData, q ClientConnectionQuery, token string) (core.ClientConnectionHistory, error) {
	result := core.ClientConnectionHistory{Records: []core.ClientConnectionRecord{}, Timeline: []core.ClientConnectionBucket{}, Sources: []core.ClientConnectionSource{}, CountryCodes: []string{}, RecordKeys: []string{}}
	projectionID := connectionDigest([]any{q.CountryCode, q.NodeOrder})
	epoch := connectionDigest([]any{entry.epoch, projectionID, q.Limit, q.Cursor})[:32]
	since := int64(-1)
	if parts := strings.Split(token, ":"); len(parts) == 2 && parts[0] == epoch {
		if n, e := strconv.ParseInt(parts[1], 10, 64); e == nil && n >= 1 && n <= entry.revision {
			since, result.Delta = n, true
		}
	}
	result.SyncToken = epoch + ":" + strconv.FormatInt(entry.revision, 10)
	rank := map[string]int{}
	for i, id := range q.NodeOrder {
		if _, ok := rank[id]; !ok {
			rank[id] = i + 1
		}
	}
	getRank := func(node string) int {
		if n := rank[node]; n != 0 {
			return n
		}
		return 2147483647
	}
	orderKey := func(g connectionGroup) connectionOrderKey {
		r := g.Record
		return connectionOrderKey{getRank(r.AgentID), r.AgentName, r.AgentID, string(r.Engine), g.addr}
	}
	cursorKey := func(r core.ClientConnectionRecord) connectionIPCursor {
		return connectionIPCursor{Scope: "node_engine_ip", Rank: getRank(r.AgentID), Endpoint: []string{r.AgentName, r.AgentID, string(r.Engine)}, IP: r.ClientIP}
	}
	orderID := connectionDigest(q.NodeOrder)
	cache := entry.projection
	cache.Lock()
	projection := cache.view
	if projection == nil || cache.orderID != orderID {
		ips, countries := map[string]bool{}, map[string]bool{}
		all := &connectionProjectionRows{groups: make([]connectionGroup, 0, len(entry.groups))}
		for _, g := range entry.groups {
			r := g.Record
			if !r.Location.NonPublic && r.Location.CountryCode != "" {
				countries[r.Location.CountryCode] = true
			}
			all.add(g, ips)
		}
		sort.Slice(all.groups, func(i, j int) bool {
			return compareConnectionOrder(orderKey(all.groups[i]), orderKey(all.groups[j])) < 0
		})
		projection = &connectionProjection{countries: append([]string{}, slices.Sorted(maps.Keys(countries))...),
			pages: map[string]*connectionProjectionRows{"": all}}
		cache.view, cache.orderID = projection, orderID
	}
	rows := projection.pages[q.CountryCode]
	if rows == nil {
		rows = &connectionProjectionRows{}
		ips := map[string]bool{}
		for _, g := range projection.pages[""].groups {
			r := g.Record
			if !r.Location.NonPublic && r.Location.CountryCode == q.CountryCode {
				rows.add(g, ips)
			}
		}
		projection.pages[q.CountryCode] = rows
	}
	cache.Unlock()
	result.IPs, result.Flows, result.CountryCodes = rows.ips, rows.flows, projection.countries
	groups := rows.groups

	if q.Cursor != "" {
		cursor, err := decodeConnectionIPCursor(q.Cursor)
		if err != nil {
			return result, err
		}
		ip, _ := netip.ParseAddr(cursor.IP)
		anchor := connectionOrderKey{cursor.Rank, cursor.Endpoint[0], cursor.Endpoint[1], cursor.Endpoint[2], ip}
		start := sort.Search(len(groups), func(i int) bool {
			n := compareConnectionOrder(orderKey(groups[i]), anchor)
			return n > 0 || n == 0 && cursor.Inclusive
		})
		groups = groups[start:]
	}
	if len(groups) > q.Limit {
		result.NextCursor = cursorKey(groups[q.Limit-1].Record).encode()
		groups = groups[:q.Limit]
	}
	if len(groups) > 0 {
		first := cursorKey(groups[0].Record)
		first.Inclusive = true
		result.PageCursor = first.encode()
	}
	remaining := int64(clientConnectionPageBudget)
	for _, g := range groups {
		if !q.RecordsOnly {
			remaining -= connectionRecordBytes(g.Record)
			if remaining < 0 {
				return core.ClientConnectionHistory{}, errClientConnectionPageBudget
			}
		} else {
			g.Record.Endpoints = nil
		}
		result.RecordKeys = append(result.RecordKeys, connectionGroupKey(g.Record))
		if g.Revision > since {
			result.Records = append(result.Records, g.Record)
		}
	}
	return result, nil
}

func connectionSyncPosition(ctx context.Context, tx pgx.Tx, q ClientConnectionQuery, recordsOnly bool, previous *connectionSnapshotData) (string, []core.ClientConnectionSource, bool, error) {
	var position string
	var unchanged bool
	var sources []core.ClientConnectionSource
	q.RecordsOnly = recordsOnly
	sourceResponse, args := connectionSourceResponse(ctx, q, nil)

	journalQuiet := "false"
	if previous != nil {
		args = append(args, previous.position)
		old := fmt.Sprintf("$%d::text::pg_snapshot", len(args))
		// Check for new journal entries in the same round trip as the read
		// position. Commits to unrelated tables need no dirty-group scan.
		journalQuiet = `pg_current_snapshot()::text = ` + fmt.Sprintf("$%d", len(args)) + ` OR (
		  (SELECT xid < pg_snapshot_xmin(` + old + `) FROM client_connection_journal_floor WHERE id=1)
		  AND NOT EXISTS(SELECT 1 FROM client_connection_journal WHERE xid >= pg_snapshot_xmax(` + old + `))
		  AND NOT EXISTS(SELECT 1 FROM pg_snapshot_xip(` + old + `) active(xid)
		    WHERE EXISTS(SELECT 1 FROM client_connection_journal WHERE xid=active.xid OFFSET 0))
		)`
	}
	err := tx.QueryRow(ctx, `SELECT pg_current_snapshot()::text,`+sourceResponse+`,`+journalQuiet, args...).Scan(&position, &sources, &unchanged)
	if err == nil && sources == nil {
		err = errClientConnectionPageBudget
	}
	return position, sources, unchanged, err
}

// Bound source metadata before materializing it in either the SQL or Go
// response. NULL signals overflow; an empty authorized result is still [].
func connectionSourceResponse(ctx context.Context, q ClientConnectionQuery, args []any) (string, []any) {
	if q.RecordsOnly {
		return `'[]'::jsonb`, args
	}
	where := ` WHERE a.revoked_at IS NULL` + agentAdministrationClause(ctx, "a.id", &args)
	if q.AgentID != "" {
		args = append(args, q.AgentID)
		where += fmt.Sprintf(` AND a.id=$%d`, len(args))
	}
	args = append(args, clientConnectionPageBudget/connectionSourceEstimate+1, clientConnectionPageBudget, connectionSourceEstimate)
	return fmt.Sprintf(`(WITH present AS MATERIALIZED (
		SELECT a.id agent_id,a.name agent_name,s.updated_at,COALESCE(s.status,'no_logs') status,
		  COALESCE(s.detail,'') detail,COALESCE(s.truncated,false) truncated
		FROM agents a LEFT JOIN client_connection_sources s ON s.agent_id=a.id AND s.source='core_logs'`+where+`
		LIMIT $%d
	  )
	  SELECT CASE WHEN COALESCE(sum(octet_length(agent_id)+octet_length(agent_name)+octet_length(status)+octet_length(detail)+$%d::bigint),0)<=$%d
	    THEN COALESCE((SELECT jsonb_agg(p ORDER BY agent_name,agent_id) FROM present p),'[]'::jsonb)
	  END FROM present)`, len(args)-2, len(args), len(args)-1), args
}

func connectionGroupsSQL(where string, limitArg int) string {
	// The port can vary within a flow, so retain the observed port variants
	// while counting each scalar flow tuple only once.
	return fmt.Sprintf(`WITH variant AS MATERIALIZED (
	 SELECT c.agent_id,c.engine,c.client_ip,c.protocol,c.inbound,c.transport,c.client_port,c.local_ip,c.local_port,
	   min(c.id) id,min(c.first_seen) first_seen,max(c.last_seen) last_seen,COALESCE(c.inbound_port,c.local_port) port
	 FROM client_connections c`+where+`
	 GROUP BY c.agent_id,c.engine,c.client_ip,c.protocol,c.inbound,c.transport,c.client_port,c.local_ip,c.local_port,COALESCE(c.inbound_port,c.local_port)
	), flows AS (
	 SELECT *,row_number() OVER (PARTITION BY agent_id,engine,client_ip,protocol,inbound,transport,client_port,local_ip,local_port) variant_number
	 FROM variant
	), totals AS (
	 SELECT min(id) id,agent_id,engine,client_ip,min(first_seen) first_seen,max(last_seen) last_seen,
	   count(*) FILTER (WHERE variant_number=1) flows,array_agg(DISTINCT port) ports
	 FROM flows GROUP BY agent_id,engine,client_ip LIMIT $%d
	)
	SELECT t.id,t.agent_id,a.name,t.engine,host(t.client_ip),t.first_seen,t.last_seen,t.flows,t.ports,
	  COALESCE(l.country_code,''),COALESCE(l.country,''),COALESCE(l.province,'')
	FROM totals t JOIN agents a ON a.id=t.agent_id LEFT JOIN client_connection_locations l ON l.client_ip=t.client_ip`, limitArg)
}
