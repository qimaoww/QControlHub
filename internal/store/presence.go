package store

import (
	"context"
	"fmt"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type AgentPresenceTransition struct {
	Agent  core.Agent
	Online bool
}

type TrafficQuotaTransition struct {
	Policy    core.PortTrafficPolicy
	AgentName string
}

// AgentPresenceTransitions atomically records online/offline state changes.
// The first observation establishes a baseline and intentionally emits no
// event, preventing a control-plane restart from alerting for every node.
func (s *Store) AgentPresenceTransitions(ctx context.Context, now time.Time, offlineAfter time.Duration) ([]AgentPresenceTransition, error) {
	// Lock only changed rows, and skip rows currently being updated by a
	// heartbeat or another monitor. Skipped transitions are retried next tick.
	// Keeping observation and update in one statement avoids holding every
	// Agent row across N remote UPDATE round trips.
	rows, err := s.pool.Query(ctx, `
		WITH changed AS MATERIALIZED (
			SELECT id,presence_notification_state AS previous,
			       CASE WHEN last_seen > $1 THEN 'online' ELSE 'offline' END AS current
			FROM agents
			WHERE revoked_at IS NULL AND presence_notification_state <>
			      CASE WHEN last_seen > $1 THEN 'online' ELSE 'offline' END
			FOR UPDATE SKIP LOCKED
		)
		UPDATE agents AS agent SET presence_notification_state=changed.current
		FROM changed WHERE agent.id=changed.id
		RETURNING agent.id,agent.name,agent.last_seen,changed.previous,changed.current`, now.UTC().Add(-offlineAfter))
	if err != nil {
		return nil, fmt.Errorf("record agent presence transitions: %w", err)
	}
	defer rows.Close()
	transitions := make([]AgentPresenceTransition, 0)
	for rows.Next() {
		var transition AgentPresenceTransition
		var previous string
		if err := rows.Scan(&transition.Agent.ID, &transition.Agent.Name, &transition.Agent.LastSeen, &previous, &transition.Agent.Status); err != nil {
			return nil, err
		}
		if previous != "unknown" {
			transition.Online = transition.Agent.Status == "online"
			transitions = append(transitions, transition)
		}
	}
	return transitions, rows.Err()
}

// ClaimTrafficQuotaTransitions marks newly blocked quota generations before
// returning them. A reset increments reset_generation, allowing one new alert
// if the same port reaches its quota again.
func (s *Store) ClaimTrafficQuotaTransitions(ctx context.Context) ([]TrafficQuotaTransition, error) {
	rows, err := s.pool.Query(ctx, `
		WITH claimed AS MATERIALIZED (
			SELECT policy.id,agent.name AS agent_name
			FROM port_traffic_policies policy JOIN agents agent ON agent.id=policy.agent_id
			WHERE policy.blocked=true AND policy.quota_enabled=true
				AND policy.quota_notification_generation < policy.reset_generation
			FOR UPDATE OF policy SKIP LOCKED
		)
		UPDATE port_traffic_policies AS policy SET quota_notification_generation=policy.reset_generation
		FROM claimed WHERE policy.id=claimed.id
		RETURNING policy.id,policy.agent_id,claimed.agent_name,policy.name,policy.engine,policy.port,policy.protocol,
			policy.used_bytes,policy.limit_bytes,policy.reset_generation`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TrafficQuotaTransition, 0)
	for rows.Next() {
		var item TrafficQuotaTransition
		if err := rows.Scan(&item.Policy.ID, &item.Policy.AgentID, &item.AgentName,
			&item.Policy.Name, &item.Policy.Engine, &item.Policy.Port,
			&item.Policy.Protocol, &item.Policy.UsedBytes, &item.Policy.LimitBytes,
			&item.Policy.ResetGeneration); err != nil {
			return nil, err
		}
		item.Policy.Blocked = true
		result = append(result, item)
	}
	return result, rows.Err()
}
