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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,name,last_seen,presence_notification_state FROM agents WHERE revoked_at IS NULL FOR UPDATE`)
	if err != nil {
		return nil, err
	}
	type observed struct {
		id, name, previous, current string
		lastSeen                    time.Time
	}
	observations := make([]observed, 0)
	for rows.Next() {
		var item observed
		if err := rows.Scan(&item.id, &item.name, &item.lastSeen, &item.previous); err != nil {
			rows.Close()
			return nil, err
		}
		item.current = "offline"
		if item.lastSeen.After(now.Add(-offlineAfter)) {
			item.current = "online"
		}
		observations = append(observations, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	transitions := make([]AgentPresenceTransition, 0)
	for _, item := range observations {
		if item.previous == item.current {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE agents SET presence_notification_state=$2 WHERE id=$1`, item.id, item.current); err != nil {
			return nil, err
		}
		if item.previous != "unknown" {
			transitions = append(transitions, AgentPresenceTransition{
				Agent:  core.Agent{ID: item.id, Name: item.name, LastSeen: item.lastSeen, Status: item.current},
				Online: item.current == "online",
			})
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("record agent presence transitions: %w", err)
	}
	return transitions, nil
}

// ClaimTrafficQuotaTransitions marks newly blocked quota generations before
// returning them. A reset increments reset_generation, allowing one new alert
// if the same port reaches its quota again.
func (s *Store) ClaimTrafficQuotaTransitions(ctx context.Context) ([]TrafficQuotaTransition, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
		SELECT policy.id,policy.agent_id,agent.name,policy.name,policy.engine,policy.port,policy.protocol,
			policy.used_bytes,policy.limit_bytes,policy.reset_generation
		FROM port_traffic_policies policy JOIN agents agent ON agent.id=policy.agent_id
		WHERE policy.blocked=true AND policy.quota_enabled=true
			AND policy.quota_notification_generation < policy.reset_generation
		FOR UPDATE OF policy SKIP LOCKED`)
	if err != nil {
		return nil, err
	}
	type claimed struct {
		transition TrafficQuotaTransition
		generation uint64
	}
	claims := make([]claimed, 0)
	for rows.Next() {
		var item claimed
		if err := rows.Scan(&item.transition.Policy.ID, &item.transition.Policy.AgentID, &item.transition.AgentName,
			&item.transition.Policy.Name, &item.transition.Policy.Engine, &item.transition.Policy.Port,
			&item.transition.Policy.Protocol, &item.transition.Policy.UsedBytes, &item.transition.Policy.LimitBytes,
			&item.generation); err != nil {
			rows.Close()
			return nil, err
		}
		item.transition.Policy.Blocked = true
		claims = append(claims, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := make([]TrafficQuotaTransition, 0, len(claims))
	for _, item := range claims {
		if _, err := tx.Exec(ctx, `UPDATE port_traffic_policies SET quota_notification_generation=$2 WHERE id=$1`, item.transition.Policy.ID, item.generation); err != nil {
			return nil, err
		}
		result = append(result, item.transition)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
