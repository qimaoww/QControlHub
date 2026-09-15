package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// SaveAgentConfig creates or updates an agent-owned configuration using an
// optimistic version check. expectedVersion must be zero for the first save.
func (s *Store) SaveAgentConfig(ctx context.Context, input core.Config, expectedVersion int) (core.Config, error) {
	return s.saveAgentConfig(ctx, input, expectedVersion, nil)
}

// ConfigClientMetadataMutation replaces client-only metadata for one inbound
// in the new configuration version. Content is encrypted with the same keyring
// as configuration payloads and never sent to an Agent.
type ConfigClientMetadataMutation struct {
	OriginalTag string
	Tag         string
	Content     string
	Delete      bool
}

func (s *Store) SaveAgentConfigWithClientMetadata(ctx context.Context, input core.Config, expectedVersion int, mutation ConfigClientMetadataMutation) (core.Config, error) {
	if !mutation.Delete && strings.TrimSpace(mutation.Content) != "" && s.cryptor == nil {
		return core.Config{}, fmt.Errorf("%w: QCH_CONFIG_ENCRYPTION_KEY is required for client-only configuration secrets", ErrSecretUnavailable)
	}
	return s.saveAgentConfig(ctx, input, expectedVersion, &mutation)
}

func (s *Store) saveAgentConfig(ctx context.Context, input core.Config, expectedVersion int, metadataMutation *ConfigClientMetadataMutation) (core.Config, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Config{}, err
	}
	defer tx.Rollback(ctx)
	saved, err := s.saveAgentConfigTx(ctx, tx, input, expectedVersion, metadataMutation)
	if err != nil {
		return core.Config{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.Config{}, err
	}
	return saved, nil
}

func (s *Store) saveAgentConfigTx(ctx context.Context, tx pgx.Tx, input core.Config, expectedVersion int, metadataMutation *ConfigClientMetadataMutation) (core.Config, error) {
	if err := lockAgentUser(ctx, tx); err != nil {
		return core.Config{}, err
	}
	if err := requireAgentEngineAccess(ctx, tx, input.AgentID, input.Engine); err != nil {
		return core.Config{}, err
	}
	if metadataMutation != nil && !metadataMutation.Delete && strings.TrimSpace(metadataMutation.Content) != "" && s.cryptor == nil {
		return core.Config{}, fmt.Errorf("%w: QCH_CONFIG_ENCRYPTION_KEY is required for client-only configuration secrets", ErrSecretUnavailable)
	}
	if input.AgentID == "" {
		return core.Config{}, fmt.Errorf("%w: agent ID is required", ErrInvalid)
	}
	if err := core.ValidateConfig(input.Engine, input.Content); err != nil {
		return core.Config{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if expectedVersion < 0 {
		return core.Config{}, fmt.Errorf("%w: invalid configuration version", ErrInvalid)
	}
	name, description, err := validateConfigMetadata(input.Name, input.Description)
	if err != nil {
		return core.Config{}, err
	}
	storedContent, err := s.encryptContent(input.Content)
	if err != nil {
		return core.Config{}, err
	}
	storedMetadata := ""
	if metadataMutation != nil {
		metadataMutation.OriginalTag = strings.TrimSpace(metadataMutation.OriginalTag)
		metadataMutation.Tag = strings.TrimSpace(metadataMutation.Tag)
		if utf8.RuneCountInString(metadataMutation.OriginalTag) > 64 || utf8.RuneCountInString(metadataMutation.Tag) > 64 {
			return core.Config{}, fmt.Errorf("%w: client metadata tag is too long", ErrInvalid)
		}
		if !metadataMutation.Delete && metadataMutation.Content != "" {
			if len(metadataMutation.Content) > 65536 {
				return core.Config{}, fmt.Errorf("%w: client metadata is too large", ErrInvalid)
			}
			storedMetadata, err = s.encryptContent(metadataMutation.Content)
			if err != nil {
				return core.Config{}, err
			}
		}
	}
	var capabilitiesJSON []byte
	if err := tx.QueryRow(ctx, `SELECT capabilities FROM agents WHERE id=$1 AND revoked_at IS NULL FOR UPDATE`, input.AgentID).Scan(&capabilitiesJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return core.Config{}, fmt.Errorf("agent: %w", ErrNotFound)
		}
		return core.Config{}, err
	}
	var capabilities []core.Engine
	if err := json.Unmarshal(capabilitiesJSON, &capabilities); err != nil {
		return core.Config{}, err
	}
	if err := rejectPendingCapabilityTransition(ctx, tx, input.AgentID, input.Engine); err != nil {
		return core.Config{}, err
	}
	if !containsEngine(capabilities, input.Engine) {
		return core.Config{}, fmt.Errorf("%w: agent does not advertise the requested engine", ErrInvalid)
	}

	var currentID string
	var currentVersion int
	ownerID := scopeForConfig(ctx).OwnerID
	err = tx.QueryRow(ctx, `SELECT id,version FROM configs
		WHERE agent_id=$1 AND engine=$2 AND owner_id=$3 AND deleted_at IS NULL FOR UPDATE`, input.AgentID, input.Engine, ownerID).Scan(&currentID, &currentVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return core.Config{}, err
	}
	if input.ID != "" && input.ID != currentID {
		return core.Config{}, ErrNotFound
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if expectedVersion != 0 {
			return core.Config{}, fmt.Errorf("%w: configuration changed; reload before saving", ErrConflict)
		}
		currentID, err = core.NewID("cfg")
		if err != nil {
			return core.Config{}, err
		}
		now := time.Now().UTC()
		_, err = tx.Exec(ctx, `INSERT INTO configs
			(id,agent_id,name,description,engine,content,version,created_at,updated_at,owner_id)
			VALUES ($1,$2,$3,$4,$5,$6,1,$7,$7,$8)`, currentID, input.AgentID, name,
			description, input.Engine, storedContent, now, ownerID)
		if err != nil {
			return core.Config{}, mapError(err)
		}
	} else {
		if expectedVersion != currentVersion {
			return core.Config{}, fmt.Errorf("%w: configuration changed; reload before saving", ErrConflict)
		}
		_, err = tx.Exec(ctx, `UPDATE configs SET name=$2,description=$3,content=$4,version=version+1,updated_at=now()
				WHERE id=$1`, currentID, name, description, storedContent)
		if err != nil {
			return core.Config{}, err
		}
	}
	var saved core.Config
	err = tx.QueryRow(ctx, `SELECT id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,updated_at,owner_id
		FROM configs WHERE id=$1`, currentID).Scan(&saved.ID, &saved.AgentID, &saved.Name, &saved.Description,
		&saved.Engine, &saved.Content, &saved.Version, &saved.CreatedAt, &saved.UpdatedAt, &saved.OwnerID)
	if err != nil {
		return core.Config{}, err
	}
	saved.Content, err = s.decryptContent(saved.Content)
	if err != nil {
		return core.Config{}, err
	}
	if err := s.insertConfigRevision(ctx, tx, saved); err != nil {
		return core.Config{}, err
	}
	if currentVersion > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO config_client_metadata (config_id,config_version,profile_tag,content,created_at)
			SELECT config_id,$2,profile_tag,content,now()
			FROM config_client_metadata WHERE config_id=$1 AND config_version=$3
			ON CONFLICT (config_id,config_version,profile_tag) DO NOTHING`, currentID, saved.Version, currentVersion); err != nil {
			return core.Config{}, err
		}
		if input.Engine == core.EngineShadowsocksRust {
			if _, err := tx.Exec(ctx, `UPDATE mainland_access_policies SET config_version=$2,updated_at=now()
				WHERE config_id=$1 AND config_version=$3`, saved.ID, saved.Version, currentVersion); err != nil {
				return core.Config{}, err
			}
		}
	}
	if metadataMutation != nil {
		for _, tag := range []string{metadataMutation.OriginalTag, metadataMutation.Tag} {
			if tag == "" {
				continue
			}
			if _, err := tx.Exec(ctx, `DELETE FROM config_client_metadata WHERE config_id=$1 AND config_version=$2 AND profile_tag=$3`, currentID, saved.Version, tag); err != nil {
				return core.Config{}, err
			}
		}
		if !metadataMutation.Delete && metadataMutation.Tag != "" && storedMetadata != "" {
			if _, err := tx.Exec(ctx, `
				INSERT INTO config_client_metadata (config_id,config_version,profile_tag,content,created_at)
				VALUES ($1,$2,$3,$4,now())`, currentID, saved.Version, metadataMutation.Tag, storedMetadata); err != nil {
				return core.Config{}, mapError(err)
			}
		}
	}
	return saved, nil
}
