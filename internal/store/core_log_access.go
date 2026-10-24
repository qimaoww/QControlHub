package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Log grants deliberately do not use fleet administration. An administrator
// sees instances on their own nodes. Ordinary users see their main services
// and their own accepted, verified shared instances.
func coreLogResourcesSQL(ctx context.Context, args *[]any) string {
	scope, scoped := requestConfigScope(ctx)
	ownerGrant, shareGrant := "true", "true"
	if scoped {
		*args = append(*args, scope.OwnerID)
		principal := fmt.Sprintf("$%d", len(*args))
		live := `(starts_with(` + principal + `::text,'token_') OR EXISTS(SELECT 1 FROM panel_users principal WHERE principal.id=` + principal + ` AND NOT principal.disabled))`
		if scope.Admin && scope.OwnerID == "" {
			live = "true"
		}
		ownerGrant = `(` + live + ` AND (a.owner_id=` + principal
		if scope.Admin {
			ownerGrant += ` OR a.owner_id=''`
		}
		ownerGrant += `))`
		shareGrant = `(` + live + ` AND sh.user_id=` + principal + ` AND sh.enabled AND sh.status='accepted' AND o.engine=ANY(sh.engines) AND NOT u.disabled)`
		if scope.Admin {
			shareGrant = ownerGrant
		}
	}
	return `SELECT a.id agent_id,a.name,a.owner_id node_owner_id,'main'::text instance_id,e.engine,a.owner_id owner_id,
 COALESCE(u.username,'主实例') username,a.features,a.runtime,a.last_seen,a.sharing_revision
 FROM agents a CROSS JOIN unnest(ARRAY['mihomo','xray','sing-box','ss-rust']::text[]) e(engine)
 LEFT JOIN panel_users u ON u.id=a.owner_id
 WHERE a.revoked_at IS NULL AND ` + ownerGrant + `
 UNION ALL
 SELECT a.id,a.name,a.owner_id,o.share_id,o.engine,o.owner_id,u.username,a.features,a.runtime,a.last_seen,a.sharing_revision
 FROM agent_shared_instance_ownership o JOIN agents a ON a.id=o.agent_id
 JOIN agent_shares sh ON sh.id=o.share_id AND sh.agent_id=a.id AND sh.user_id=o.owner_id
 JOIN panel_users u ON u.id=o.owner_id
 WHERE a.revoked_at IS NULL AND ` + shareGrant
}

type CoreLogSource struct {
	AgentID       string      `json:"agent_id"`
	Name          string      `json:"name"`
	InstanceID    string      `json:"instance_id"`
	Engine        core.Engine `json:"engine"`
	OwnerID       string      `json:"owner_id,omitempty"`
	Username      string      `json:"username"`
	Features      []string    `json:"features"`
	Status        string      `json:"status"`
	Source        string      `json:"source,omitempty"`
	LastLogAt     *time.Time  `json:"last_log_at,omitempty"`
	GrantRevision string      `json:"grant_revision"`
	Error         string      `json:"error,omitempty"`
}

func (s *Store) ListCoreLogSources(ctx context.Context) ([]CoreLogSource, error) {
	args := []any{}
	resource := coreLogResourcesSQL(ctx, &args)
	rows, err := s.pool.Query(ctx, `WITH resources AS (`+resource+`)
 SELECT r.agent_id,r.name,r.instance_id,r.engine,r.owner_id,r.username,r.features,md5(r.node_owner_id || r.sharing_revision::text),
 CASE WHEN r.last_seen IS NULL OR r.last_seen<now()-interval '45 seconds' THEN 'offline'
 WHEN NOT(r.features ? 'core-logs-v1') THEN 'unsupported'
 WHEN r.instance_id<>'main' AND NOT(r.features ? 'core-log-instances-v1') THEN 'unsupported'
 WHEN r.instance_id='main' AND NOT(r.features ? 'core-log-status-v1') THEN 'status-unavailable'
 WHEN r.instance_id='main' AND r.runtime->r.engine->>'installed'='false' THEN 'not-installed'
 WHEN r.instance_id='main' THEN COALESCE(r.runtime->r.engine->>'core_log_status','pending')
 WHEN state.updated_at<now()-interval '45 seconds' THEN 'pending'
 ELSE COALESCE(state.status,'pending') END,
 COALESCE(CASE WHEN r.instance_id='main' THEN r.runtime->r.engine->>'core_log_source' ELSE state.source END,''),
 COALESCE(CASE WHEN r.instance_id='main' THEN r.runtime->r.engine->>'core_log_error' ELSE state.error END,''),
 CASE WHEN r.instance_id='main' THEN r.runtime->r.engine->>'core_log_last_at' END,
 CASE WHEN r.instance_id<>'main' THEN state.last_at END
 FROM resources r LEFT JOIN core_log_source_states state ON state.agent_id=r.agent_id AND state.engine=r.engine AND state.instance_id=r.instance_id
 ORDER BY r.name,r.agent_id,r.instance_id,r.engine`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CoreLogSource{}
	for rows.Next() {
		var source CoreLogSource
		var features []byte
		var status, transport, reason string
		var lastLogAtText *string
		var lastLogAt *time.Time
		if err := rows.Scan(&source.AgentID, &source.Name, &source.InstanceID, &source.Engine, &source.OwnerID, &source.Username, &features, &source.GrantRevision, &status, &transport, &reason, &lastLogAtText, &lastLogAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(features, &source.Features); err != nil {
			return nil, err
		}
		source.Status = normalizeCoreLogSourceStatus(status)
		source.Source = normalizeCoreLogSource(transport)
		source.Error = coreLogErrorForStatus(source.Status, reason)
		if source.InstanceID == "main" {
			source.LastLogAt = parseCoreLogLastAtText(lastLogAtText)
		} else {
			source.LastLogAt = lastLogAt
		}
		result = append(result, source)
	}
	return result, rows.Err()
}
