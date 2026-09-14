package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func trafficAccessClause(ctx context.Context, agentColumn, policyColumn string, args *[]any) string {
	clause := agentAccessClause(ctx, agentColumn, args)
	if scopeForConfig(ctx).Admin {
		return clause
	}
	*args = append(*args, scopeForConfig(ctx).OwnerID)
	owner := fmt.Sprintf("$%d", len(*args))
	return clause + ` AND (starts_with(` + owner + `::text,'token_')
		OR EXISTS(SELECT 1 FROM agents owned_agent WHERE owned_agent.id=` + agentColumn + ` AND owned_agent.owner_id=` + owner + `)
		OR EXISTS(SELECT 1 FROM port_traffic_policies owned_policy JOIN agent_shares owned_share ON owned_share.id=owned_policy.share_id
			WHERE owned_policy.id=` + policyColumn + ` AND owned_share.user_id=` + owner + `
				AND owned_policy.engine=ANY(owned_share.engines)))`
}

func lockTrafficMutationPolicy(ctx context.Context, tx pgx.Tx, id string) (string, error) {
	if err := lockAgentUser(ctx, tx); err != nil {
		return "", err
	}
	var agentID string
	if err := tx.QueryRow(ctx, `SELECT agent_id FROM port_traffic_policies WHERE id=$1`, id).Scan(&agentID); err != nil {
		return "", mapError(err)
	}
	if err := requireAgentAdministration(ctx, tx, agentID); err != nil {
		return "", err
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&agentID); err != nil {
		return "", mapError(err)
	}
	var shareID string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(share_id,'') FROM port_traffic_policies WHERE id=$1 FOR UPDATE`, id).Scan(&shareID); err != nil {
		return "", mapError(err)
	}
	if shareID != "" {
		return "", fmt.Errorf("%w: reserved shared-port accounting cannot be reset, disabled or edited; manage its user allocation instead", ErrConflict)
	}
	return agentID, nil
}
