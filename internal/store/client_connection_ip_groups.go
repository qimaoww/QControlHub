package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) clientConnectionIPRecords(ctx context.Context, tx pgx.Tx, q ClientConnectionQuery, where string, args []any) ([]core.ClientConnectionRecord, int64, error) {
	params := append([]any{}, args...)
	having := ""
	if q.Before > 0 {
		params = append(params, q.Before)
		having = fmt.Sprintf(" HAVING min(c.id)<$%d", len(params))
	}
	params = append(params, q.Limit+1)
	// The earliest row ID is stable as new observations arrive. Apply the
	// cursor to whole IP groups, never to raw rows before grouping.
	rows, err := tx.Query(ctx, `SELECT min(c.id),host(c.client_ip),min(c.first_seen),max(c.last_seen)
 FROM client_connections c JOIN agents a ON a.id=c.agent_id`+where+`
 GROUP BY c.client_ip`+having+fmt.Sprintf(` ORDER BY min(c.id) DESC LIMIT $%d`, len(params)), params...)
	if err != nil {
		return nil, 0, err
	}
	records := []core.ClientConnectionRecord{}
	for rows.Next() {
		var r core.ClientConnectionRecord
		if err := rows.Scan(&r.ID, &r.ClientIP, &r.FirstSeen, &r.LastSeen); err != nil {
			rows.Close()
			return nil, 0, err
		}
		records = append(records, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var next int64
	if len(records) > q.Limit {
		records = records[:q.Limit]
		next = records[len(records)-1].ID
	}
	if q.RecordsOnly || len(records) == 0 {
		return records, next, nil
	}
	if err := s.clientConnectionIPEndpoints(ctx, tx, records, where, args); err != nil {
		return nil, 0, err
	}
	return records, next, nil
}

func (s *Store) clientConnectionIPEndpoints(ctx context.Context, tx pgx.Tx, records []core.ClientConnectionRecord, where string, args []any) error {
	ips := make([]string, len(records))
	index := map[string]int{}
	for i, r := range records {
		ips[i] = r.ClientIP
		index[r.ClientIP] = i
	}
	params := append(append([]any{}, args...), ips)
	// Collapse source ports with identical observation windows first.
	// Keep deployment boundaries separate so a historical port change is not
	// lost when multiple visits to the same IP are combined into one page row.
	rows, err := tx.Query(ctx, `WITH observations AS MATERIALIZED (
 SELECT min(c.id) AS id,c.client_ip,c.agent_id,a.name AS agent_name,c.engine,c.inbound,c.local_port,c.first_seen,c.last_seen
 FROM client_connections c JOIN agents a ON a.id=c.agent_id`+where+fmt.Sprintf(` AND c.client_ip=ANY($%d::inet[])
 GROUP BY c.client_ip,c.agent_id,a.name,c.engine,c.inbound,c.local_port,c.first_seen,c.last_seen
 ) SELECT min(observed.id),host(observed.client_ip),observed.agent_id,observed.agent_name,observed.engine,observed.inbound,observed.local_port,min(observed.first_seen),max(observed.last_seen)
 FROM observations observed
 LEFT JOIN LATERAL (
  SELECT task.id,task.finished_at FROM tasks task
  WHERE observed.local_port=0 AND observed.inbound<>''
   AND task.agent_id=observed.agent_id AND task.engine=observed.engine
   AND task.action IN ('deploy','import-existing') AND task.status<>'canceled'
   AND COALESCE(task.started_at,task.finished_at)<=observed.last_seen
  ORDER BY COALESCE(task.started_at,task.finished_at) DESC,task.id DESC LIMIT 1
 ) deployed ON true
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
		i := index[r.ClientIP]
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
			if x.AgentName != y.AgentName {
				return x.AgentName < y.AgentName
			}
			if x.AgentID != y.AgentID {
				return x.AgentID < y.AgentID
			}
			if x.Engine != y.Engine {
				return x.Engine < y.Engine
			}
			return x.LocalPort < y.LocalPort
		})
	}
	return nil
}
