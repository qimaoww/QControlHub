package store

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

type connectionIPCursor struct {
	Scope     string   `json:"scope"`
	Rank      int      `json:"rank"`
	Endpoint  []string `json:"endpoint"`
	IP        string   `json:"ip"`
	Inclusive bool     `json:"inclusive,omitempty"`
}

func decodeConnectionIPCursor(value string) (connectionIPCursor, error) {
	var cursor connectionIPCursor
	if len(value) > 4096 {
		return cursor, fmt.Errorf("cursor too long")
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor, err
	}
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return cursor, err
	}
	ip, err := netip.ParseAddr(cursor.IP)
	if err != nil || ip.Zone() != "" || cursor.Scope != "node_engine_ip" || cursor.Rank < 1 || len(cursor.Endpoint) != 3 || !core.Engine(cursor.Endpoint[2]).Valid() {
		return cursor, fmt.Errorf("invalid cursor")
	}
	for _, value := range cursor.Endpoint {
		if strings.ContainsRune(value, '\x00') {
			return cursor, fmt.Errorf("invalid cursor endpoint")
		}
	}
	return cursor, nil
}

func (cursor connectionIPCursor) encode() string {
	payload, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func (s *Store) clientConnectionIPRecords(ctx context.Context, tx pgx.Tx, q ClientConnectionQuery, where string, args []any) ([]core.ClientConnectionRecord, string, string, error) {
	params := append([]any{}, args...)
	params = append(params, q.NodeOrder)
	rank := fmt.Sprintf(`COALESCE(array_position($%d::text[],c.agent_id),2147483647)`, len(params))
	const endpoint = `ARRAY[a.name::text,c.agent_id::text,c.engine::text] COLLATE "C"`
	having := ""
	if q.Cursor != "" {
		cursor, err := decodeConnectionIPCursor(q.Cursor)
		if err != nil {
			return nil, "", "", err
		}
		operator := ">"
		if cursor.Inclusive {
			operator = ">="
		}
		params = append(params, cursor.Rank, cursor.Endpoint, cursor.IP)
		having = fmt.Sprintf(` HAVING (`+rank+`,`+endpoint+`,c.client_ip) %s ($%d::int,$%d::text[] COLLATE "C",$%d::inet)`, operator, len(params)-2, len(params)-1, len(params))
	}
	params = append(params, q.Limit+1)
	// Apply the cursor to whole IP groups. IP provides a deterministic tie-break
	// independent of connection arrival times and IDs.
	rows, err := tx.Query(ctx, `SELECT min(c.id),host(c.client_ip),min(c.first_seen),max(c.last_seen),`+rank+`,`+endpoint+`
 FROM client_connections c JOIN agents a ON a.id=c.agent_id`+where+`
 GROUP BY c.engine,c.agent_id,a.name,c.client_ip`+having+` ORDER BY `+rank+`,`+endpoint+fmt.Sprintf(`,c.client_ip LIMIT $%d`, len(params)), params...)
	if err != nil {
		return nil, "", "", err
	}
	records := []core.ClientConnectionRecord{}
	keys := []connectionIPCursor{}
	for rows.Next() {
		var r core.ClientConnectionRecord
		var key connectionIPCursor
		if err := rows.Scan(&r.ID, &r.ClientIP, &r.FirstSeen, &r.LastSeen, &key.Rank, &key.Endpoint); err != nil {
			rows.Close()
			return nil, "", "", err
		}
		key.IP = r.ClientIP
		key.Scope = "node_engine_ip"
		r.AgentName, r.AgentID, r.Engine = key.Endpoint[0], key.Endpoint[1], core.Engine(key.Endpoint[2])
		keys = append(keys, key)
		records = append(records, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", "", err
	}
	var next, page string
	if len(records) > q.Limit {
		records = records[:q.Limit]
		next = keys[len(records)-1].encode()
	}
	if len(records) > 0 {
		keys[0].Inclusive = true
		page = keys[0].encode()
	}
	if q.RecordsOnly || len(records) == 0 {
		return records, next, page, nil
	}
	if err := s.clientConnectionIPEndpoints(ctx, tx, records, where, args); err != nil {
		return nil, "", "", err
	}
	return records, next, page, nil
}

func (s *Store) clientConnectionIPEndpoints(ctx context.Context, tx pgx.Tx, records []core.ClientConnectionRecord, where string, args []any) error {
	groups := make([]connectionLookupKey, len(records))
	index := make(map[connectionLookupKey]int, len(records))
	for i, r := range records {
		groups[i] = connectionLookup(r)
		index[groups[i]] = i
	}
	payload, err := json.Marshal(groups)
	if err != nil {
		return err
	}
	params := append(append([]any{}, args...), payload, clientConnectionPageBudget/connectionEndpointEstimate+1)
	// DISTINCT already removes duplicate ports. Stream the bounded result
	// directly into the page without a second copy of all observations.
	rows, err := tx.Query(ctx, `SELECT DISTINCT host(c.client_ip),c.agent_id,c.engine,COALESCE(c.inbound_port,c.local_port)
 FROM client_connections c JOIN agents a ON a.id=c.agent_id`+where+fmt.Sprintf(`
 AND (c.agent_id,c.engine,c.client_ip) IN (
  SELECT agent_id,engine,client_ip::inet FROM jsonb_to_recordset($%d::jsonb) AS wanted(agent_id text,engine text,client_ip text)
 ) LIMIT $%d`, len(params)-1, len(params)), params...)
	if err != nil {
		return err
	}
	defer rows.Close()
	remaining := int64(clientConnectionPageBudget)
	for _, r := range records {
		remaining -= connectionRecordBytes(r)
	}
	for rows.Next() {
		var key connectionLookupKey
		var port int
		if err := rows.Scan(&key.ClientIP, &key.AgentID, &key.Engine, &port); err != nil {
			return err
		}
		i := index[key]
		r := &records[i]
		endpoint := core.ClientConnectionEndpoint{AgentID: r.AgentID, AgentName: r.AgentName, Engine: r.Engine, LocalPort: port}
		remaining -= connectionEndpointBytes(endpoint)
		if remaining < 0 {
			return errClientConnectionPageBudget
		}
		r.Endpoints = append(r.Endpoints, endpoint)
	}
	for i := range records {
		slices.SortFunc(records[i].Endpoints, func(a, b core.ClientConnectionEndpoint) int {
			return cmp.Compare(a.LocalPort, b.LocalPort)
		})
	}
	return rows.Err()
}
