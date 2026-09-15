package store

import (
	"context"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const trafficPolicyColumns = `id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,auto_block,quota_enabled,monitoring_enabled,discovered,reset_generation,
       received_bytes,sent_bytes,used_bytes,receive_bps,send_bps,period_start,period_end,blocked,
       enforcement_available,enforcement_error,last_reported_at,created_at,updated_at,last_collected_at,accounting,metadata_managed,
	   COALESCE(share_id,''),share_used_bytes,
	   (SELECT jsonb_build_object('id',share.id,'limit_bytes',share.limit_bytes,
			'used_bytes',share.used_bytes,'port_used_bytes',port_traffic_policies.share_used_bytes,'engines',share.engines,
			'revoked',shared_user.disabled OR NOT share.enabled OR share.status<>'accepted'
				OR NOT EXISTS(SELECT 1 FROM agents shared_agent WHERE shared_agent.id=share.agent_id
					AND shared_agent.features ? 'shared-engines-v1' AND shared_agent.features ? 'shared-traffic-v1'
					AND shared_agent.features ? 'independent-egress-v1'))
		FROM agent_shares share JOIN panel_users shared_user ON shared_user.id=share.user_id WHERE share.id=port_traffic_policies.share_id)`

type trafficPolicyScanner interface {
	Scan(dest ...any) error
}

func scanTrafficPolicy(row trafficPolicyScanner) (core.PortTrafficPolicy, error) {
	var policy core.PortTrafficPolicy
	err := row.Scan(
		&policy.ID, &policy.AgentID, &policy.Name, &policy.Engine, &policy.Port,
		&policy.Protocol, &policy.Cycle, &policy.CycleAnchor, &policy.LimitBytes, &policy.AutoBlock,
		&policy.QuotaEnabled, &policy.MonitoringEnabled, &policy.Discovered, &policy.ResetGeneration, &policy.ReceivedBytes, &policy.SentBytes,
		&policy.UsedBytes, &policy.ReceiveBPS, &policy.SendBPS, &policy.PeriodStart,
		&policy.PeriodEnd, &policy.Blocked, &policy.EnforcementAvailable,
		&policy.EnforcementError, &policy.LastReportedAt, &policy.CreatedAt, &policy.UpdatedAt,
		&policy.LastCollectedAt, &policy.Accounting,
		&policy.MetadataManaged,
		&policy.ShareID, &policy.ShareUsedBytes, &policy.SharedQuota,
	)
	return policy, err
}

func (s *Store) ListPortTrafficPolicies(ctx context.Context) ([]core.PortTrafficPolicy, error) {
	args := []any{}
	where := trafficAccessClause(ctx, "port_traffic_policies.agent_id", "port_traffic_policies.id", &args)
	rows, err := s.pool.Query(ctx, `SELECT `+trafficPolicyColumns+` FROM port_traffic_policies WHERE true`+where+` ORDER BY agent_id,port`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.PortTrafficPolicy, 0)
	for rows.Next() {
		policy, scanErr := scanTrafficPolicy(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, policy)
	}
	return result, rows.Err()
}

func (s *Store) AgentPortTrafficPolicies(ctx context.Context, agentID string) ([]core.PortTrafficPolicy, error) {
	args := []any{agentID}
	where := trafficAccessClause(ctx, "port_traffic_policies.agent_id", "port_traffic_policies.id", &args)
	rows, err := s.pool.Query(ctx, `SELECT `+trafficPolicyColumns+` FROM port_traffic_policies WHERE agent_id=$1 AND monitoring_enabled=true`+where+` ORDER BY port`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.PortTrafficPolicy, 0)
	for rows.Next() {
		policy, scanErr := scanTrafficPolicy(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, policy)
	}
	return result, rows.Err()
}
