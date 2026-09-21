package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// Resolve only an explicitly named inbound against the configuration deployed
// at the observation time. A source port or remote destination is never a
// listener port. Missing history and unnamed/ambiguous inbounds remain unset.
func (s *Store) resolveClientConnectionPorts(ctx context.Context, tx pgx.Tx, records []core.ClientConnectionRecord) error {
	wanted := make([]core.ClientConnectionRecord, 0, len(records))
	byID := make(map[int64]int, len(records))
	for i, r := range records {
		if r.LocalPort == 0 && r.Inbound != "" {
			wanted = append(wanted, r)
			byID[r.ID] = i
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	payload, err := json.Marshal(wanted)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT observed.id,config.id,deployed.config_version,
 CASE WHEN config.version=deployed.config_version THEN config.content ELSE revision.content END
 FROM jsonb_to_recordset($1::jsonb) AS observed(id bigint,agent_id text,engine text,first_seen timestamptz,last_seen timestamptz)
 CROSS JOIN LATERAL (
  SELECT task.config_id,task.config_version,task.status,task.finished_at
  FROM tasks task WHERE task.agent_id=observed.agent_id AND task.engine=observed.engine
   AND task.action IN ('deploy','import-existing') AND task.status<>'canceled'
   AND COALESCE(task.started_at,task.finished_at)<=observed.last_seen
  ORDER BY COALESCE(task.started_at,task.finished_at) DESC,task.id DESC LIMIT 1
 ) deployed
 JOIN configs config ON config.id=deployed.config_id AND config.deleted_at IS NULL
 LEFT JOIN config_revisions revision ON revision.config_id=config.id AND revision.version=deployed.config_version
 WHERE deployed.status='succeeded' AND deployed.finished_at<=observed.first_seen
 AND (config.version=deployed.config_version OR revision.config_id IS NOT NULL)`, payload)
	if err != nil {
		return err
	}
	defer rows.Close()
	type key struct {
		id      string
		version int
		engine  core.Engine
	}
	cache := map[key][]core.PortTrafficEndpoint{}
	for rows.Next() {
		var id int64
		var configID, content string
		var version int
		if err := rows.Scan(&id, &configID, &version, &content); err != nil {
			return err
		}
		index, ok := byID[id]
		if !ok {
			continue
		}
		record := &records[index]
		k := key{configID, version, record.Engine}
		ports, ok := cache[k]
		if !ok {
			plain, err := s.decryptContent(content)
			if err != nil {
				continue
			} // Historical metadata must not hide source IPs.
			ports = serverconfig.DiscoverTrafficPorts(record.Engine, plain)
			cache[k] = ports
		}
		record.LocalPort = connectionInboundPort(record.Inbound, ports)
	}
	return rows.Err()
}

func connectionInboundPort(inbound string, ports []core.PortTrafficEndpoint) int {
	if inbound == "" || len(ports) >= 256 {
		return 0
	}
	port := 0
	for _, p := range ports {
		if p.Name != inbound {
			continue
		}
		if port != 0 && port != p.Port {
			return 0
		}
		port = p.Port
	}
	return port
}
