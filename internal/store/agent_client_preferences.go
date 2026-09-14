package store

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// SetAgentClientAddress stores the operator-provided address used when
// building client connection profiles. It lives in the agent labels so the
// value survives Agent reconnects. Older enrollments may have stored a JSON
// null instead of an empty labels object, so normalize that shape on update.
func (s *Store) SetAgentClientAddress(ctx context.Context, id, address string) error {
	return s.SetAgentClientPreferences(ctx, id, &address, nil, nil)
}

// SetAgentClientDetails updates the optional client endpoint and display name.
// A nil value leaves that field unchanged; an empty value removes it.
func (s *Store) SetAgentClientDetails(ctx context.Context, id string, address, name *string) error {
	return s.SetAgentClientPreferences(ctx, id, address, name, nil)
}

// SetAgentClientPreferences persists the node-wide client display defaults. The
// automatic address mode is represented by an absent label so older Agents and
// control-plane versions keep their original behavior.
func (s *Store) SetAgentClientPreferences(ctx context.Context, id string, address, name, addressMode *string) error {
	return s.setAgentClientPreferences(ctx, id, "client_name", "", "", address, name, addressMode)
}

// SetAgentClientProfilePreferences scopes the client display parameters to one
// verified listening endpoint. Name, connection address, and address family all
// live under profile-scoped labels so changing one shared node never rewrites
// another. An explicit empty name opts that endpoint out of the legacy name and
// restores its inbound tag; an explicit empty address restores the automatic
// node address.
func (s *Store) SetAgentClientProfilePreferences(ctx context.Context, id, nameLabel string, address, name, addressMode *string) error {
	digest, err := hex.DecodeString(strings.TrimPrefix(nameLabel, core.ClientProfileNameLabelPrefix))
	if !strings.HasPrefix(nameLabel, core.ClientProfileNameLabelPrefix) || err != nil || len(digest) != 32 {
		return fmt.Errorf("%w: invalid client profile name scope", ErrInvalid)
	}
	suffix := strings.TrimPrefix(nameLabel, core.ClientProfileNameLabelPrefix)
	return s.setAgentClientPreferences(ctx, id, nameLabel, core.ClientProfileAddressLabelPrefix+suffix, core.ClientProfileFamilyLabelPrefix+suffix, address, name, addressMode)
}

func (s *Store) setAgentClientPreferences(ctx context.Context, id, nameLabel, addressLabel, addressModeLabel string, address, name, addressMode *string) error {
	if err := requireAgentAdministration(ctx, s.pool, id); err != nil {
		return err
	}
	if name != nil && (!utf8.ValidString(*name) || utf8.RuneCountInString(*name) > 100 || strings.ContainsFunc(*name, unicode.IsControl)) {
		return fmt.Errorf("%w: client name must not exceed 100 characters or contain control characters", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	addressKey := addressLabel
	if addressKey == "" {
		addressKey = "client_address"
	}
	if address != nil {
		if _, err := tx.Exec(ctx, `UPDATE agents SET labels = CASE WHEN $2 = '' THEN COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb) - $3::text ELSE jsonb_set(COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb), ARRAY[$3::text], to_jsonb($2::text), true) END WHERE id=$1 AND revoked_at IS NULL`, id, *address, addressKey); err != nil {
			return err
		}
	}
	if name != nil {
		if _, err := tx.Exec(ctx, `UPDATE agents SET labels = CASE WHEN $2 = '' AND $3 = 'client_name' THEN COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb) - $3::text ELSE jsonb_set(COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb), ARRAY[$3::text], to_jsonb($2::text), true) END WHERE id=$1 AND revoked_at IS NULL`, id, *name, nameLabel); err != nil {
			return err
		}
	}
	if addressMode != nil {
		// Automatic selection removes the key for both scopes: an absent label
		// already means automatic, so storing the literal value adds no meaning.
		modeKey, remove := addressModeLabel, *addressMode == "" || *addressMode == "auto"
		if modeKey == "" {
			modeKey = "client_address_mode"
		}
		if remove {
			if _, err := tx.Exec(ctx, `UPDATE agents SET labels = COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb) - $2::text WHERE id=$1 AND revoked_at IS NULL`, id, modeKey); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `UPDATE agents SET labels = jsonb_set(COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb), ARRAY[$2::text], to_jsonb($3::text), true) WHERE id=$1 AND revoked_at IS NULL`, id, modeKey, *addressMode); err != nil {
			return err
		}
	}
	if address == nil && name == nil && addressMode == nil {
		return nil
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agents WHERE id=$1 AND revoked_at IS NULL)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}
