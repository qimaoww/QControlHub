package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Called while the Agent row is locked. A revoked/disabled principal and
// stale tasks created before isolation must never be dispatched on reconnect.
// Invalidating running leases also prevents later reacceptance from reviving
// pre-revocation work. Execution already in flight may remain uncertain.
func cancelUnauthorizedAgentTasksTx(ctx context.Context, tx pgx.Tx, agentID string, features []string) error {
	_, err := tx.Exec(ctx, `UPDATE tasks t SET status=CASE WHEN t.status='running' THEN 'failed' ELSE 'canceled' END,
		error=CASE WHEN t.status='running' THEN 'Agent sharing authorization changed; previous execution is unknown'
			ELSE 'Agent sharing authorization changed; submit a new task' END,
		finished_at=now(),config_content=NULL,lease_id=NULL
		WHERE t.agent_id=$1 AND t.status IN ('pending','running') AND (
			(t.status='pending' AND t.action IN ('start','restart') AND (`+unsafeEngineStartSQL("t.agent_id", "t.engine")+`))
			OR (`+unauthorizedTaskPrincipalSQL+`)
			OR (`+unauthorizedHostConfigTaskSQL+`)
			OR (t.action IN ('validate','deploy') AND NOT $3::boolean)
			OR (t.engine<>'' AND NOT t.capability_transition AND NOT EXISTS(
				SELECT 1 FROM agents a WHERE a.id=t.agent_id AND a.capabilities ? t.engine))
			OR (t.shared_traffic_id<>'' AND (
				NOT $2::boolean OR NOT EXISTS(
					SELECT 1 FROM agent_shares s JOIN panel_users u ON u.id=s.user_id
					WHERE s.id=t.shared_traffic_id AND NOT u.disabled AND s.enabled AND s.status='accepted'
						AND s.agent_id=t.agent_id AND t.engine=ANY(s.engines)
						AND (s.limit_bytes=0 OR s.used_bytes<s.limit_bytes)
				)
			))
		)`,
		agentID, supportsSharedEngines(features), containsFeature(features, core.AgentFeatureIndependentEgress))
	return err
}

const unauthorizedTaskPrincipalSQL = `(t.owner_id<>'' AND NOT starts_with(t.owner_id,'token_')
	AND NOT EXISTS(SELECT 1 FROM panel_users u WHERE u.id=t.owner_id))
	OR EXISTS(SELECT 1 FROM configs c WHERE c.id=t.config_id AND c.owner_id<>''
		AND NOT starts_with(c.owner_id,'token_') AND NOT EXISTS(SELECT 1 FROM panel_users u WHERE u.id=c.owner_id))
	OR EXISTS(SELECT 1 FROM panel_users u JOIN agents a ON a.id=t.agent_id
	WHERE u.id=t.owner_id AND (u.disabled OR (u.role<>'admin' AND
		(NOT u.permissions ? (CASE WHEN t.capability_transition THEN 'agents.manage' ELSE 'tasks.execute' END)
		 OR (t.action IN ('enable-bbr','disable-bbr','configure-tcp') AND NOT u.permissions ? 'agents.manage')
		 OR (a.owner_id<>u.id AND
			(t.install_if_missing OR t.action NOT IN ('deploy','validate','status') OR (t.action IN ('deploy','validate') AND t.shared_traffic_id='')
			 OR NOT EXISTS(SELECT 1 FROM agent_shares s WHERE s.user_id=u.id AND s.agent_id=t.agent_id
				AND s.enabled AND s.status='accepted' AND t.engine=ANY(s.engines))))))))
	OR EXISTS(SELECT 1 FROM configs c JOIN panel_users u ON u.id=c.owner_id JOIN agents a ON a.id=t.agent_id
		WHERE c.id=t.config_id AND (u.disabled OR (u.role='user' AND a.owner_id<>u.id AND
			(t.shared_traffic_id='' OR NOT EXISTS(SELECT 1 FROM agent_shares s WHERE s.id=t.shared_traffic_id
				AND s.user_id=u.id AND s.agent_id=t.agent_id AND s.enabled AND s.status='accepted' AND t.engine=ANY(s.engines))))))`
