package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

type connectionIPCursor struct {
	Scope     string   `json:"scope"`
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
	if err != nil || ip.Zone() != "" || cursor.Scope != "engine_node_ip" || len(cursor.Endpoint) != 3 || !core.Engine(cursor.Endpoint[0]).Valid() {
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
	const endpoint = `ARRAY[c.engine,a.name,c.agent_id] COLLATE "C"`
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
		params = append(params, cursor.Endpoint, cursor.IP)
		having = fmt.Sprintf(` HAVING (`+endpoint+`,c.client_ip) %s ($%d::text[] COLLATE "C",$%d::inet)`, operator, len(params)-1, len(params))
	}
	params = append(params, q.Limit+1)
	// Apply the cursor to whole IP groups. IP provides a deterministic tie-break
	// independent of connection arrival times and IDs.
	rows, err := tx.Query(ctx, `SELECT min(c.id),host(c.client_ip),min(c.first_seen),max(c.last_seen),`+endpoint+`
 FROM client_connections c JOIN agents a ON a.id=c.agent_id`+where+`
 GROUP BY c.engine,c.agent_id,a.name,c.client_ip`+having+` ORDER BY `+endpoint+fmt.Sprintf(`,c.client_ip LIMIT $%d`, len(params)), params...)
	if err != nil {
		return nil, "", "", err
	}
	records := []core.ClientConnectionRecord{}
	keys := []connectionIPCursor{}
	for rows.Next() {
		var r core.ClientConnectionRecord
		var key connectionIPCursor
		if err := rows.Scan(&r.ID, &r.ClientIP, &r.FirstSeen, &r.LastSeen, &key.Endpoint); err != nil {
			rows.Close()
			return nil, "", "", err
		}
		key.IP = r.ClientIP
		key.Scope = "engine_node_ip"
		r.Engine, r.AgentName, r.AgentID = core.Engine(key.Endpoint[0]), key.Endpoint[1], key.Endpoint[2]
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
	type groupKey struct {
		AgentID string      `json:"agent_id"`
		Engine  core.Engine `json:"engine"`
		IP      string      `json:"client_ip"`
	}
	groups := make([]groupKey, len(records))
	index := map[groupKey]int{}
	for i, r := range records {
		groups[i] = groupKey{r.AgentID, r.Engine, r.ClientIP}
		index[groups[i]] = i
	}
	payload, err := json.Marshal(groups)
	if err != nil {
		return err
	}
	params := append(append([]any{}, args...), payload)
	// Collapse source ports with identical observation windows first.
	// Keep deployment boundaries separate so a historical port change is not
	// lost when multiple visits to the same IP are combined into one page row.
	// Build the task timeline once per agent/engine instead of scanning task
	// history for every repeated observation.
	rows, err := tx.Query(ctx, `WITH observations AS MATERIALIZED (
 SELECT min(c.id) AS id,c.client_ip,c.agent_id,a.name AS agent_name,c.engine,c.inbound,c.local_port,c.first_seen,c.last_seen
 FROM client_connections c JOIN agents a ON a.id=c.agent_id`+where+fmt.Sprintf(`
 AND (c.agent_id,c.engine,c.client_ip) IN (
  SELECT agent_id,engine,client_ip::inet FROM jsonb_to_recordset($%d::jsonb) AS wanted(agent_id text,engine text,client_ip text)
 )
 GROUP BY c.client_ip,c.agent_id,a.name,c.engine,c.inbound,c.local_port,c.first_seen,c.last_seen
 ), deployments AS MATERIALIZED (
  SELECT DISTINCT ON (task.agent_id,task.engine,COALESCE(task.started_at,task.finished_at))
   task.id,task.agent_id,task.engine,task.finished_at,COALESCE(task.started_at,task.finished_at) AS effective_at
  FROM tasks task JOIN (
   SELECT DISTINCT agent_id,engine FROM observations WHERE local_port=0 AND inbound<>''
  ) wanted ON wanted.agent_id=task.agent_id AND wanted.engine=task.engine
  WHERE task.action IN ('deploy','import-existing') AND task.status<>'canceled'
   AND COALESCE(task.started_at,task.finished_at) IS NOT NULL
  ORDER BY task.agent_id,task.engine,COALESCE(task.started_at,task.finished_at),task.id DESC
 ), deployment_windows AS MATERIALIZED (
  SELECT *,lead(effective_at) OVER (PARTITION BY agent_id,engine ORDER BY effective_at) AS next_at FROM deployments
 ) SELECT min(observed.id),host(observed.client_ip),observed.agent_id,observed.agent_name,observed.engine,observed.inbound,observed.local_port,min(observed.first_seen),max(observed.last_seen)
 FROM observations observed
 LEFT JOIN deployment_windows deployed ON observed.local_port=0 AND observed.inbound<>''
  AND deployed.agent_id=observed.agent_id AND deployed.engine=observed.engine
  AND deployed.effective_at<=observed.last_seen AND (deployed.next_at IS NULL OR observed.last_seen<deployed.next_at)
 GROUP BY observed.client_ip,observed.agent_id,observed.agent_name,observed.engine,observed.inbound,observed.local_port,deployed.id,(deployed.finished_at<=observed.first_seen)`, len(params)), params...)
	if err != nil {
		return err
	}
	observations := []core.ClientConnectionRecord{}
	for rows.Next() {
		var r core.ClientConnectionRecord
		if err := rows.Scan(&r.ID, &r.ClientIP, &r.AgentID, &r.AgentName, &r.Engine, &r.Inbound, &r.LocalPort, &r.FirstSeen, &r.LastSeen); err != nil {
			rows.Close()
			return err
		}
		observations = append(observations, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if err := s.resolveClientConnectionPorts(ctx, tx, observations); err != nil {
		return err
	}
	seen := make([]map[core.ClientConnectionEndpoint]bool, len(records))
	for _, r := range observations {
		i := index[groupKey{r.AgentID, r.Engine, r.ClientIP}]
		endpoint := core.ClientConnectionEndpoint{AgentID: r.AgentID, AgentName: r.AgentName, Engine: r.Engine, LocalPort: r.LocalPort}
		if seen[i] == nil {
			seen[i] = map[core.ClientConnectionEndpoint]bool{}
		}
		if !seen[i][endpoint] {
			records[i].Endpoints = append(records[i].Endpoints, endpoint)
			seen[i][endpoint] = true
		}
	}
	for i := range records {
		sort.Slice(records[i].Endpoints, func(a, b int) bool {
			x, y := records[i].Endpoints[a], records[i].Endpoints[b]
			if x.Engine != y.Engine {
				return x.Engine < y.Engine
			}
			if x.AgentName != y.AgentName {
				return x.AgentName < y.AgentName
			}
			if x.AgentID != y.AgentID {
				return x.AgentID < y.AgentID
			}
			return x.LocalPort < y.LocalPort
		})
	}
	return nil
}
