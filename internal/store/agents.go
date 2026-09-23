package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) ListAgents(ctx context.Context) ([]core.Agent, error) {
	args := []any{}
	where := agentAccessClause(ctx, "agents.id", &args)
	query := scopedAgentsSQL(ctx, &args)
	rows, err := s.pool.Query(ctx, query+where+` ORDER BY enrolled_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	agents, err := scanAgents(ctx, rows)
	if err != nil {
		return nil, err
	}
	return agents, s.overlaySharedInstanceRuntime(ctx, agents)
}

// ListAgentsWithEnrollmentCommands pipelines the panel's two independent
// reads on one connection. Only callers authorized to manage enrollment should
// use it; ciphertext is verified locally and never included in the response.
func (s *Store) ListAgentsWithEnrollmentCommands(ctx context.Context) ([]core.Agent, error) {
	args := []any{}
	where := agentAccessClause(ctx, "agents.id", &args)
	query := scopedAgentsSQL(ctx, &args)
	batch := &pgx.Batch{}
	batch.Queue(query+where+` ORDER BY enrolled_at DESC`, args...)
	enrollmentQuery, enrollmentArgs := enrollmentAvailabilityQuery(ctx, nil)
	batch.Queue(enrollmentQuery, enrollmentArgs...)
	results := s.pool.SendBatch(ctx, batch)
	defer results.Close()
	rows, err := results.Query()
	if err != nil {
		return nil, err
	}
	agents, err := scanAgents(ctx, rows)
	if err != nil {
		return nil, err
	}
	rows, err = results.Query()
	if err != nil {
		return nil, err
	}
	available, err := s.scanEnrollmentCommandAvailability(rows)
	if err != nil {
		return nil, err
	}
	if err := results.Close(); err != nil {
		return nil, err
	}
	for index := range agents {
		agents[index].EnrollmentCommandAvailable = available[agents[index].ID]
	}
	return agents, s.overlaySharedInstanceRuntime(ctx, agents)
}

const listAgentsSQL = listAgentsSQLBase + ` ORDER BY enrolled_at DESC`

const agentOfflineThresholdSQL = `(CASE WHEN agents.owner_id='' THEN (SELECT agent_offline_threshold_seconds FROM panel_settings WHERE id=1)
	ELSE COALESCE((SELECT (runtime->>'agent_offline_threshold_seconds')::integer FROM user_panel_settings WHERE owner_id=agents.owner_id),45) END)`

const agentColumnsSQL = `
			SELECT id,name,version,os,arch,capabilities,features,labels,runtime,observed_public_ip,
				(SELECT metrics FROM agent_live_state WHERE agent_id=agents.id),
				last_seen,enrolled_at,
				` + agentOfflineThresholdSQL + `,supported_capabilities,` + capabilityTransitionsSQL + `,owner_id,admin_hidden`

const agentSelectFromSQL = ` FROM agents WHERE revoked_at IS NULL`

const listAgentsSQLBase = agentColumnsSQL + `,'{}'::text[]` + agentSelectFromSQL

func scopedAgentsSQL(ctx context.Context, args *[]any) string {
	if scopeForConfig(ctx).Admin {
		return listAgentsSQLBase
	}
	*args = append(*args, scopeForConfig(ctx).OwnerID)
	return agentColumnsSQL + fmt.Sprintf(`,COALESCE((SELECT engines FROM agent_shares
		WHERE agent_id=agents.id AND user_id=$%d AND enabled AND status='accepted'),'{}'::text[])`, len(*args)) + agentSelectFromSQL
}

func scanAgents(ctx context.Context, rows pgx.Rows) ([]core.Agent, error) {
	defer rows.Close()
	agents := make([]core.Agent, 0)
	now := time.Now().UTC()
	for rows.Next() {
		var agent core.Agent
		var capabilities, features, labels, runtimeState, metricsState []byte
		var observedPublicIP string
		var offlineThresholdSeconds int
		if err := rows.Scan(&agent.ID, &agent.Name, &agent.Version, &agent.OS, &agent.Arch, &capabilities, &features, &labels, &runtimeState, &observedPublicIP, &metricsState, &agent.LastSeen, &agent.EnrolledAt, &offlineThresholdSeconds, &agent.SupportedCapabilities, &agent.CapabilityTransitions, &agent.OwnerID, &agent.AdminHidden, &agent.SharedEngines); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(capabilities, &agent.Capabilities); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(features, &agent.Features); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(labels, &agent.Labels); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(runtimeState, &agent.Runtime); err != nil {
			return nil, err
		}
		if err := decodeAgentMetrics(metricsState, observedPublicIP, &agent.Metrics); err != nil {
			return nil, err
		}
		if agent.LastSeen.After(now.Add(-time.Duration(offlineThresholdSeconds) * time.Second)) {
			agent.Status = "online"
		} else {
			agent.Status = "offline"
		}
		scopeAgentPresentation(ctx, &agent)
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

// AgentName returns the display name of an active registered agent.
func (s *Store) AgentName(ctx context.Context, id string) (string, error) {
	if err := requireAgentAccess(ctx, s.pool, id); err != nil {
		return "", err
	}
	var name string
	if err := s.pool.QueryRow(ctx, `SELECT name FROM agents WHERE id=$1 AND revoked_at IS NULL`, id).Scan(&name); err != nil {
		return "", err
	}
	return name, nil
}

func cloneLabels(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
