package store

import (
	"context"
	"fmt"
)

// ConfigClientMetadata returns decrypted client-only metadata for an exact
// configuration version. Callers apply entries only to the matching tag.
func (s *Store) ConfigClientMetadata(ctx context.Context, configID string, version int) (map[string]string, error) {
	if configID == "" || version < 1 {
		return nil, fmt.Errorf("%w: configuration ID and version are required", ErrInvalid)
	}
	args := []any{configID, version}
	ownerWhere := ownerClause(ctx, "c.owner_id", &args)
	ownerWhere += configAgentAccessClause(ctx, "c.agent_id", "c.engine", &args)
	rows, err := s.pool.Query(ctx, `
		SELECT m.profile_tag,m.content FROM config_client_metadata m
		JOIN configs c ON c.id=m.config_id
		WHERE m.config_id=$1 AND m.config_version=$2 AND c.deleted_at IS NULL`+ownerWhere, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var tag, stored string
		if err := rows.Scan(&tag, &stored); err != nil {
			return nil, err
		}
		opened, err := s.decryptContent(stored)
		if err != nil {
			return nil, err
		}
		result[tag] = opened
	}
	return result, rows.Err()
}
