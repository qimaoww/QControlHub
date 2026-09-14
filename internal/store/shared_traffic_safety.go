package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Starting a retained config does not prove which listeners are on disk after
// a failed deployment, nor restore billing after reserved ports were released.
// Shared cores must be restarted by deploying an authorized exact snapshot.
func requireSafeEngineStart(ctx context.Context, tx pgx.Tx, agentID string, engine core.Engine) error {
	var unsafe bool
	if err := tx.QueryRow(ctx, `SELECT `+unsafeEngineStartSQL("$1", "$2"), agentID, engine).Scan(&unsafe); err != nil {
		return err
	}
	if unsafe {
		return fmt.Errorf("%w: redeploy the user's configuration instead of starting a shared or uncertain core", ErrConflict)
	}
	return nil
}

func unsafeEngineStartSQL(agent, engine string) string {
	return `EXISTS(SELECT 1 FROM agent_engine_ownership state JOIN agents a ON a.id=state.agent_id
		LEFT JOIN panel_users u ON u.id=state.owner_id
		WHERE state.agent_id=` + agent + ` AND state.engine=` + engine + `
		AND (state.config_uncertain OR COALESCE(u.disabled OR (u.role='user' AND a.owner_id<>u.id),false)))
		OR EXISTS(SELECT 1 FROM port_traffic_policies p WHERE p.agent_id=` + agent + ` AND p.engine=` + engine + ` AND p.share_id IS NOT NULL)`
}
