package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

const komariUUIDLabel = "komari_uuid"

// AgentKomariUUID returns the optional Komari node UUID stored with the
// QControlHub agent's display labels. Keeping this as an agent preference
// avoids coupling the enrollment protocol to a third-party monitor.
func AgentKomariUUID(agent core.Agent) string {
	return strings.TrimSpace(agent.Labels[komariUUIDLabel])
}

// SetAgentKomariUUID updates or clears the optional Komari node UUID.
func (s *Store) SetAgentKomariUUID(ctx context.Context, id, uuid string) error {
	uuid = strings.TrimSpace(uuid)
	if len(uuid) > 100 || strings.ContainsAny(uuid, "\r\n\t") {
		return fmt.Errorf("%w: Komari server UUID is invalid", ErrInvalid)
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE agents SET labels = CASE
			WHEN $2='' THEN COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb) - $3::text
			ELSE jsonb_set(COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb), ARRAY[$3::text], to_jsonb($2::text), true)
		END
		WHERE id=$1 AND revoked_at IS NULL`, id, uuid, komariUUIDLabel)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetAgent(ctx context.Context, id string) (core.Agent, error) {
	var agent core.Agent
	var capabilities, features, labels, runtimeState, metricsState []byte
	var offlineThresholdSeconds int
	err := s.pool.QueryRow(ctx, `
			SELECT id,name,version,os,arch,capabilities,features,labels,runtime,metrics,last_seen,enrolled_at,
				(SELECT agent_offline_threshold_seconds FROM panel_settings WHERE id=1)
			FROM agents WHERE id=$1 AND revoked_at IS NULL`, id).Scan(
		&agent.ID, &agent.Name, &agent.Version, &agent.OS, &agent.Arch, &capabilities, &features, &labels, &runtimeState, &metricsState, &agent.LastSeen, &agent.EnrolledAt, &offlineThresholdSeconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Agent{}, ErrNotFound
	}
	if err != nil {
		return core.Agent{}, err
	}
	if err := json.Unmarshal(capabilities, &agent.Capabilities); err != nil {
		return core.Agent{}, err
	}
	if err := json.Unmarshal(features, &agent.Features); err != nil {
		return core.Agent{}, err
	}
	if err := json.Unmarshal(labels, &agent.Labels); err != nil {
		return core.Agent{}, err
	}
	if err := json.Unmarshal(runtimeState, &agent.Runtime); err != nil {
		return core.Agent{}, err
	}
	if err := json.Unmarshal(metricsState, &agent.Metrics); err != nil {
		return core.Agent{}, err
	}
	if agent.LastSeen.After(time.Now().UTC().Add(-time.Duration(offlineThresholdSeconds) * time.Second)) {
		agent.Status = "online"
	} else {
		agent.Status = "offline"
	}
	return agent, nil
}

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

// SetAgentClientPreferences also persists the selected address family. The
// automatic mode is represented by an absent label so older Agents and
// control-plane versions keep their original behavior.
func (s *Store) SetAgentClientPreferences(ctx context.Context, id string, address, name, addressMode *string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if address != nil {
		if _, err := tx.Exec(ctx, `UPDATE agents SET labels = CASE WHEN $2 = '' THEN COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb) - 'client_address' ELSE jsonb_set(COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb), '{client_address}', to_jsonb($2::text), true) END WHERE id=$1 AND revoked_at IS NULL`, id, *address); err != nil {
			return err
		}
	}
	if name != nil {
		if _, err := tx.Exec(ctx, `UPDATE agents SET labels = CASE WHEN $2 = '' THEN COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb) - 'client_name' ELSE jsonb_set(COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb), '{client_name}', to_jsonb($2::text), true) END WHERE id=$1 AND revoked_at IS NULL`, id, *name); err != nil {
			return err
		}
	}
	if addressMode != nil {
		if _, err := tx.Exec(ctx, `UPDATE agents SET labels = CASE WHEN $2 = '' OR $2 = 'auto' THEN COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb) - 'client_address_mode' ELSE jsonb_set(COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb), '{client_address_mode}', to_jsonb($2::text), true) END WHERE id=$1 AND revoked_at IS NULL`, id, *addressMode); err != nil {
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

// AgentConfig returns the one active configuration owned by an agent/core
// pair. Node-owned configurations cannot accidentally be deployed elsewhere.
func (s *Store) AgentConfig(ctx context.Context, agentID string, engine core.Engine) (core.Config, error) {
	var config core.Config
	err := s.pool.QueryRow(ctx, `
		SELECT id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,updated_at
		FROM configs WHERE agent_id=$1 AND engine=$2 AND deleted_at IS NULL`, agentID, engine).Scan(
		&config.ID, &config.AgentID, &config.Name, &config.Description, &config.Engine, &config.Content,
		&config.Version, &config.CreatedAt, &config.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.Config{}, ErrNotFound
	}
	if err != nil {
		return core.Config{}, err
	}
	config.Content, err = s.decryptContent(config.Content)
	if err != nil {
		return core.Config{}, err
	}
	return config, nil
}

// ListAgentConfigs returns every active node-owned configuration. The control
// plane uses this for fleet-level deployment drift and listener summaries;
// general configuration workspaces remain isolated through ListConfigs.
func (s *Store) ListAgentConfigs(ctx context.Context) ([]core.Config, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,updated_at
		FROM configs WHERE agent_id IS NOT NULL AND deleted_at IS NULL ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	configs := make([]core.Config, 0)
	for rows.Next() {
		var config core.Config
		if err := rows.Scan(&config.ID, &config.AgentID, &config.Name, &config.Description, &config.Engine, &config.Content,
			&config.Version, &config.CreatedAt, &config.UpdatedAt); err != nil {
			return nil, err
		}
		config.Content, err = s.decryptContent(config.Content)
		if err != nil {
			return nil, err
		}
		configs = append(configs, config)
	}
	return configs, rows.Err()
}

func (s *Store) LatestDeployments(ctx context.Context) ([]core.Deployment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (agent_id,engine) agent_id,engine,COALESCE(config_id,''),COALESCE(config_version,0),finished_at
		FROM tasks
		WHERE action IN ('deploy','import-existing') AND status='succeeded' AND finished_at IS NOT NULL
		ORDER BY agent_id,engine,finished_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.Deployment, 0)
	for rows.Next() {
		var deployment core.Deployment
		if err := rows.Scan(&deployment.AgentID, &deployment.Engine, &deployment.ConfigID, &deployment.ConfigVersion, &deployment.DeployedAt); err != nil {
			return nil, err
		}
		result = append(result, deployment)
	}
	return result, rows.Err()
}

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
		if len(metadataMutation.OriginalTag) > 64 || len(metadataMutation.Tag) > 64 {
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.Config{}, err
	}
	defer tx.Rollback(ctx)
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
	if !containsEngine(capabilities, input.Engine) {
		return core.Config{}, fmt.Errorf("%w: agent does not advertise the requested engine", ErrInvalid)
	}

	var currentID string
	var currentVersion int
	err = tx.QueryRow(ctx, `SELECT id,version FROM configs
		WHERE agent_id=$1 AND engine=$2 AND deleted_at IS NULL FOR UPDATE`, input.AgentID, input.Engine).Scan(&currentID, &currentVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return core.Config{}, err
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
			(id,agent_id,name,description,engine,content,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,1,$7,$7)`, currentID, input.AgentID, name,
			description, input.Engine, storedContent, now)
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
	err = tx.QueryRow(ctx, `SELECT id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,updated_at
		FROM configs WHERE id=$1`, currentID).Scan(&saved.ID, &saved.AgentID, &saved.Name, &saved.Description,
		&saved.Engine, &saved.Content, &saved.Version, &saved.CreatedAt, &saved.UpdatedAt)
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
			if _, err := tx.Exec(ctx, `UPDATE mainland_access_policies SET config_version=$3,updated_at=now()
				WHERE agent_id=$1 AND engine=$2 AND config_version=$4`, input.AgentID, input.Engine, saved.Version, currentVersion); err != nil {
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
	if err := tx.Commit(ctx); err != nil {
		return core.Config{}, err
	}
	return saved, nil
}

// ConfigClientMetadata returns decrypted client-only metadata for an exact
// configuration version. Callers apply entries only to the matching tag.
func (s *Store) ConfigClientMetadata(ctx context.Context, configID string, version int) (map[string]string, error) {
	if configID == "" || version < 1 {
		return nil, fmt.Errorf("%w: configuration ID and version are required", ErrInvalid)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT profile_tag,content FROM config_client_metadata
		WHERE config_id=$1 AND config_version=$2`, configID, version)
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
