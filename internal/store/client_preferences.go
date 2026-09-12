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

// ClientProfileScope is resolved from an authenticated user's actual deployed
// revision. Neither configuration identity nor label keys come from the API
// request body.
type ClientProfileScope struct {
	ConfigID string
	Version  int
	Label    string
}

func (s *Store) SetConfigClientPreferences(ctx context.Context, agentID string, profile ClientProfileScope, address, name, family *string) error {
	suffix := strings.TrimPrefix(profile.Label, core.ClientProfileNameLabelPrefix)
	digest, err := hex.DecodeString(suffix)
	if !strings.HasPrefix(profile.Label, core.ClientProfileNameLabelPrefix) || err != nil || len(digest) != 32 {
		return fmt.Errorf("%w: invalid client profile name scope", ErrInvalid)
	}
	if name != nil && (!utf8.ValidString(*name) || utf8.RuneCountInString(*name) > 100 || strings.ContainsFunc(*name, unicode.IsControl)) {
		return fmt.Errorf("%w: client name must not exceed 100 characters or contain control characters", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentUser(ctx, tx); err != nil {
		return err
	}
	if err := requireAgentAccess(ctx, tx, agentID); err != nil {
		return err
	}
	var locked string
	if err := tx.QueryRow(ctx, `SELECT id FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, agentID).Scan(&locked); err != nil {
		return mapError(err)
	}
	args := []any{agentID, profile.ConfigID, profile.Version}
	where := ownerClause(ctx, "config.owner_id", &args)
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM (`+latestDeploymentsSQL+`) deployed
		JOIN configs config ON config.id=deployed.config_id
		WHERE deployed.agent_id=$1 AND deployed.config_id=$2 AND deployed.config_version=$3 AND config.deleted_at IS NULL`+where+`)`, args...).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("%w: deployed profile changed; refresh before saving", ErrNotFound)
	}
	for _, change := range []struct {
		label string
		value *string
		clear bool
	}{
		{profile.Label, name, false},
		{core.ClientProfileAddressLabelPrefix + suffix, address, address != nil && *address == ""},
		{core.ClientProfileFamilyLabelPrefix + suffix, family, family != nil && (*family == "" || *family == "auto")},
	} {
		if change.value == nil {
			continue
		}
		if change.clear {
			_, err = tx.Exec(ctx, `DELETE FROM config_client_preferences WHERE config_id=$1 AND label=$2`, profile.ConfigID, change.label)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO config_client_preferences(config_id,label,value) VALUES($1,$2,$3)
				ON CONFLICT(config_id,label) DO UPDATE SET value=EXCLUDED.value`, profile.ConfigID, change.label, *change.value)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
