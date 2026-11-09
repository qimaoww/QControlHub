package store

import (
	"context"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/notify"
)

// NotificationRegionResolver reuses the server's cached GeoIP country lookup.
// It runs only in a delivery worker, outside database transactions, under a
// short deadline. Failure leaves the node name visible without a flag.
type NotificationRegionResolver func(context.Context, core.Agent) string

// The event must pass notificationEventAllowed before reaching this helper.
// Shared recipients may see the owner's explicit display region, but automatic
// GeoIP requires both host authority and metrics.read, as on the node page.
func (s *Store) notificationAgentRegion(ctx context.Context, x storeExecutor, owner string, e notify.Event, automatic bool) (string, *core.Agent, error) {
	if e.Test || e.AgentID == "" {
		return "", nil, nil
	}
	var code string
	if err := x.QueryRow(ctx, `SELECT COALESCE(labels->>'region_code','') FROM agents WHERE id=$1 AND revoked_at IS NULL`, e.AgentID).Scan(&code); err != nil {
		return "", nil, err
	}
	agent := core.Agent{ID: e.AgentID, Name: e.Agent, Labels: map[string]string{"region_code": code}}
	if code = AgentRegionCode(agent); code != "" {
		return code, nil, nil
	}
	if !automatic || !s.notificationCanPermission(ctx, x, owner, core.PermissionMetricsRead) || requireAgentAdministration(s.notificationScope(ctx, x, owner), x, e.AgentID) != nil {
		return "", nil, nil
	}
	if err := x.QueryRow(ctx, `SELECT COALESCE((SELECT metrics FROM agent_live_state WHERE agent_id=$1),'{}')`, e.AgentID).Scan(&agent.Metrics); err != nil {
		return "", nil, err
	}
	return "", &agent, nil
}
