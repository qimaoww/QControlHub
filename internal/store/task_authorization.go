package store

import (
	"context"
	"fmt"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

// Check durable permissions while holding the user lock. API middleware alone
// cannot authorize work that may execute after a role or permission change.
func requireTaskPermission(ctx context.Context, executor storeExecutor, action core.Action, transition bool) error {
	if scopeForConfig(ctx).Admin {
		return nil
	}
	permission := core.PermissionTasksExecute
	if transition {
		permission = core.PermissionAgentsManage
	}
	var denied bool
	if err := executor.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM panel_users
		WHERE id=$1 AND (disabled OR (role<>'admin' AND
			(NOT permissions ? $2 OR ($3::boolean AND NOT permissions ? 'agents.manage')))))`,
		scopeForConfig(ctx).OwnerID, permission, action.SystemBBR()).Scan(&denied); err != nil {
		return err
	}
	if denied {
		return fmt.Errorf("%w: task permission is no longer granted", ErrForbidden)
	}
	return nil
}

func (s *Store) openExecutionConfig(task *core.Task) error {
	if task.ConfigContent != "" {
		content, err := s.decryptContent(task.ConfigContent)
		if err != nil {
			return err
		}
		task.ConfigContent = content
	}
	if task.Action == core.ActionValidate || task.Action == core.ActionDeploy {
		return serverconfig.ValidateIndependentEgress(task.Engine, task.ConfigContent)
	}
	return nil
}

// A host read is authorized against the configuration present when it runs,
// not merely when it was queued. The Agent lock and single running lease
// serialize this check with deployment ownership changes.
const unauthorizedHostConfigTaskSQL = `(t.action IN ('read-config','read-managed-config')
		OR (t.action='import-existing' AND t.status='pending'))
	AND EXISTS(SELECT 1 FROM panel_users reader WHERE reader.id=t.owner_id AND reader.role<>'admin')
	AND (NOT EXISTS(SELECT 1 FROM agents a WHERE a.id=t.agent_id AND a.owner_id=t.owner_id)
		OR EXISTS(SELECT 1 FROM agent_engine_ownership state
			WHERE state.agent_id=t.agent_id AND state.engine=t.engine
				AND (state.config_uncertain OR (state.owner_id<>t.owner_id AND state.config_id<>''))))`
