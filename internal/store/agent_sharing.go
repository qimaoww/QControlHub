package store

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Store) AgentSharing(ctx context.Context, agentID string) (core.AgentSharing, error) {
	if err := requireAgentAdministration(ctx, s.pool, agentID); err != nil {
		return core.AgentSharing{}, err
	}
	result := core.AgentSharing{Shares: []core.AgentShare{}}
	if err := s.pool.QueryRow(ctx, `SELECT sharing_revision FROM agents WHERE id=$1 AND revoked_at IS NULL`, agentID).Scan(&result.Revision); err != nil {
		return result, mapError(err)
	}
	rows, err := s.pool.Query(ctx, `SELECT s.id,s.user_id,u.username,u.display_name,s.agent_id,a.name,s.enabled,
		s.status,s.invitation_revision,s.limit_bytes,s.used_bytes,s.created_at,s.updated_at,
		ARRAY(SELECT p.port FROM agent_share_ports p WHERE p.share_id=s.id ORDER BY p.port)
		FROM agent_shares s JOIN panel_users u ON u.id=s.user_id JOIN agents a ON a.id=s.agent_id
		WHERE s.agent_id=$1 ORDER BY u.username,s.id`, agentID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var share core.AgentShare
		if err := rows.Scan(&share.ID, &share.UserID, &share.Username, &share.DisplayName, &share.AgentID, &share.AgentName,
			&share.Enabled, &share.Status, &share.InvitationRevision, &share.LimitBytes, &share.UsedBytes, &share.CreatedAt, &share.UpdatedAt, &share.Ports); err != nil {
			return result, err
		}
		result.Shares = append(result.Shares, share)
	}
	return result, rows.Err()
}

// SetAgentSharing replaces grants only on the caller's own node. Recipients
// are addressed by exact username; it never grants access to a user directory.
// Omitted grants are revoked, with their ledger and reservations retained.
func (s *Store) SetAgentSharing(ctx context.Context, agentID string, request core.AgentSharingRequest) (core.AgentSharing, error) {
	if request.Revision < 1 || len(request.Shares) > 512 {
		return core.AgentSharing{}, fmt.Errorf("%w: a sharing revision and at most 512 recipients are required", ErrInvalid)
	}
	names := make([]string, 0, len(request.Shares))
	seen := make(map[string]bool)
	recipients := append([]core.AgentSharingRecipient(nil), request.Shares...)
	for i := range recipients {
		recipient := &recipients[i]
		recipient.Username = strings.ToLower(strings.TrimSpace(recipient.Username))
		if recipient.Username == "" || len(recipient.Username) > 64 || seen[recipient.Username] || recipient.LimitBytes > math.MaxInt64 {
			return core.AgentSharing{}, fmt.Errorf("%w: invalid or duplicate sharing recipient", ErrInvalid)
		}
		seen[recipient.Username] = true
		names = append(names, recipient.Username)
		recipient.Ports = append([]int(nil), recipient.Ports...)
		sort.Ints(recipient.Ports)
		if len(recipient.Ports) > 256 {
			return core.AgentSharing{}, fmt.Errorf("%w: at most 256 shared ports", ErrInvalid)
		}
		for j, port := range recipient.Ports {
			if port < 1 || port > 65535 || port == 10085 || port == 10086 || (j > 0 && recipient.Ports[j-1] == port) {
				return core.AgentSharing{}, fmt.Errorf("%w: invalid, reserved or duplicate shared port", ErrInvalid)
			}
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.AgentSharing{}, err
	}
	defer tx.Rollback(ctx)
	// Match allocation/task lock order: all users first, then the Agent. The
	// stable ordering also covers two owners sharing their nodes to each other.
	rows, err := tx.Query(ctx, `SELECT id,username,role,disabled FROM panel_users
		WHERE username=ANY($1::text[]) OR id=$2 OR id IN(SELECT user_id FROM agent_shares WHERE agent_id=$3)
		ORDER BY id FOR UPDATE`, names, scopeForConfig(ctx).OwnerID, agentID)
	if err != nil {
		return core.AgentSharing{}, err
	}
	users := make(map[string]core.User)
	for rows.Next() {
		var user core.User
		if err := rows.Scan(&user.ID, &user.Username, &user.Role, &user.Disabled); err != nil {
			rows.Close()
			return core.AgentSharing{}, err
		}
		users[user.Username] = user
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return core.AgentSharing{}, err
	}
	if err := requireAgentAdministration(ctx, tx, agentID); err != nil {
		return core.AgentSharing{}, err
	}
	var revision int64
	var owner string
	var features []string
	if err := tx.QueryRow(ctx, `SELECT sharing_revision,owner_id,features FROM agents
		WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&revision, &owner, &features); err != nil {
		return core.AgentSharing{}, mapError(err)
	}
	if revision != request.Revision {
		return core.AgentSharing{}, fmt.Errorf("%w: Agent sharing changed; reload before saving", ErrConflict)
	}
	if err := lockAgentSharesTx(ctx, tx, []string{agentID}); err != nil {
		return core.AgentSharing{}, err
	}
	userIDs := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		user, exists := users[recipient.Username]
		enabled := recipient.Enabled == nil || *recipient.Enabled
		if !exists || user.Role == core.RoleAdmin || user.ID == owner || (enabled && user.Disabled) {
			return core.AgentSharing{}, fmt.Errorf("%w: recipient is unavailable or already owns this Agent", ErrInvalid)
		}
		if enabled && !containsFeature(features, core.AgentFeatureSharedTraffic) {
			return core.AgentSharing{}, fmt.Errorf("%w: upgrade the Agent before sharing it", ErrConflict)
		}
		userIDs = append(userIDs, user.ID)
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_shares SET enabled=false,invitation_revision=invitation_revision+1,updated_at=now()
		WHERE agent_id=$1 AND enabled AND NOT(user_id=ANY($2::text[]))`, agentID, userIDs); err != nil {
		return core.AgentSharing{}, err
	}
	for _, recipient := range recipients {
		userID := users[recipient.Username].ID
		enabled := recipient.Enabled == nil || *recipient.Enabled
		if err := s.setAgentShareTx(ctx, tx, userID, agentID, recipient.LimitBytes, recipient.Ports, enabled, recipient.Reinvite); err != nil {
			return core.AgentSharing{}, err
		}
	}
	if err := cancelUnauthorizedAgentTasksTx(ctx, tx, agentID, features); err != nil {
		return core.AgentSharing{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET sharing_revision=sharing_revision+1 WHERE id=$1`, agentID); err != nil {
		return core.AgentSharing{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE panel_users SET agent_access_revision=agent_access_revision+1
		WHERE id IN(SELECT user_id FROM agent_shares WHERE agent_id=$1)`, agentID); err != nil {
		return core.AgentSharing{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.AgentSharing{}, err
	}
	return s.AgentSharing(ctx, agentID)
}
