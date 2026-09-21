package store

import (
	"context"
	"encoding/json"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"net/netip"
	"time"
)

type clientConnectionFixtures struct {
	Connections []core.ClientConnection
	Status      string
	Detail      string
	Truncated   bool
}

// Query fixtures; production writes only through stored core logs.
func (s *Store) storeClientConnectionFixtures(ctx context.Context, agentID string, report clientConnectionFixtures) error {
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
	if _, err = tx.Exec(ctx, `INSERT INTO client_connection_sources(agent_id,updated_at,status,detail,truncated,source)
 VALUES($1,$2,$3,$4,$5,'core_logs') ON CONFLICT(agent_id) DO UPDATE SET updated_at=EXCLUDED.updated_at,
 source='core_logs',status=EXCLUDED.status,detail=EXCLUDED.detail,truncated=EXCLUDED.truncated`, agentID, now, report.Status, report.Detail, report.Truncated); err != nil {
		return err
	}
	// DISTINCT also makes duplicate tuples in an otherwise valid report harmless.
	_, err = tx.Exec(ctx, `INSERT INTO client_connections(agent_id,bucket,engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port,first_seen,last_seen,source)
 SELECT DISTINCT $1::text,$2::timestamptz,engine,protocol,inbound,transport,client_ip::inet,client_port,local_ip::inet,local_port,$3::timestamptz,$3::timestamptz,'core_logs'
 FROM jsonb_to_recordset($4::jsonb) AS x(engine text,protocol text,inbound text,transport text,client_ip text,client_port integer,local_ip text,local_port integer)
 ON CONFLICT(agent_id,bucket,engine,protocol,inbound,transport,client_ip,client_port,local_ip,local_port)
 DO UPDATE SET last_seen=GREATEST(client_connections.last_seen,EXCLUDED.last_seen)`, agentID, now.Truncate(time.Minute), now, payload)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
