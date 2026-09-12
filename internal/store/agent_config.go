package store

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/geoip"
)

const komariUUIDLabel = "komari_uuid"

// AgentRegionCode returns the optional operator-selected display region.
func AgentRegionCode(agent core.Agent) string {
	code := strings.ToUpper(strings.TrimSpace(agent.Labels["region_code"]))
	if !geoip.ValidRegionCode(code) {
		return ""
	}
	return code
}

// SetAgentRegionCode changes only the region preference, preserving all other
// labels. Clearing it restores automatic GeoIP without requiring a migration.
func (s *Store) SetAgentRegionCode(ctx context.Context, id, code string) error {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code != "" && !geoip.ValidRegionCode(code) {
		return fmt.Errorf("%w: unsupported country/region code", ErrInvalid)
	}
	if err := requireAgentAdministration(ctx, s.pool, id); err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE agents SET labels = CASE
			WHEN $2='' THEN COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb) - 'region_code'
			ELSE jsonb_set(COALESCE(NULLIF(labels, 'null'::jsonb), '{}'::jsonb), '{region_code}', to_jsonb($2::text), true)
		END
		WHERE id=$1 AND revoked_at IS NULL`, id, code)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAgentName changes only panel display metadata. Enrollment credentials
// keep their original names, and reconnects continue to use the stable ID/key.
func (s *Store) SetAgentName(ctx context.Context, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 100 || strings.ContainsFunc(name, unicode.IsControl) {
		return fmt.Errorf("%w: agent name must contain 1 to 100 characters without control characters", ErrInvalid)
	}
	if err := requireAgentAdministration(ctx, s.pool, id); err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, `UPDATE agents SET name=$2 WHERE id=$1 AND revoked_at IS NULL`, id, name)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

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
	if err := requireAgentAdministration(ctx, s.pool, id); err != nil {
		return err
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
	var observedPublicIP string
	var offlineThresholdSeconds int
	args := []any{id}
	where := agentAccessClause(ctx, "agents.id", &args)
	err := s.pool.QueryRow(ctx, `
			SELECT id,name,version,os,arch,capabilities,features,labels,runtime,observed_public_ip,
				(SELECT metrics FROM agent_live_state WHERE agent_id=agents.id),
				last_seen,enrolled_at,
				`+agentOfflineThresholdSQL+`,supported_capabilities,`+capabilityTransitionsSQL+`,owner_id
			FROM agents WHERE id=$1 AND revoked_at IS NULL`+where, args...).Scan(
		&agent.ID, &agent.Name, &agent.Version, &agent.OS, &agent.Arch, &capabilities, &features, &labels, &runtimeState, &observedPublicIP, &metricsState, &agent.LastSeen, &agent.EnrolledAt, &offlineThresholdSeconds, &agent.SupportedCapabilities, &agent.CapabilityTransitions, &agent.OwnerID)
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
	if err := decodeAgentMetrics(metricsState, observedPublicIP, &agent.Metrics); err != nil {
		return core.Agent{}, err
	}
	if agent.LastSeen.After(time.Now().UTC().Add(-time.Duration(offlineThresholdSeconds) * time.Second)) {
		agent.Status = "online"
	} else {
		agent.Status = "offline"
	}
	scope := scopeForConfig(ctx)
	agent.CanManage = scope.Admin || agent.OwnerID == scope.OwnerID || strings.HasPrefix(scope.OwnerID, "token_")
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

// AgentConfig returns this user's workspace for an agent/core pair.
// Node-owned configurations cannot accidentally be deployed elsewhere.
func (s *Store) AgentConfig(ctx context.Context, agentID string, engine core.Engine) (core.Config, error) {
	if err := requireAgentAccess(ctx, s.pool, agentID); err != nil {
		return core.Config{}, err
	}
	var config core.Config
	err := s.pool.QueryRow(ctx, `
		SELECT id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,updated_at,owner_id
		FROM configs WHERE agent_id=$1 AND engine=$2 AND owner_id=$3 AND deleted_at IS NULL`, agentID, engine, scopeForConfig(ctx).OwnerID).Scan(
		&config.ID, &config.AgentID, &config.Name, &config.Description, &config.Engine, &config.Content,
		&config.Version, &config.CreatedAt, &config.UpdatedAt, &config.OwnerID)
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
	return s.listAgentConfigs(ctx, "", false)
}

// AgentConfigsForMonitoring includes every user's workspace when reconciling
// shared host counters. A user's save must not prune another user's ports.
func (s *Store) AgentConfigsForMonitoring(ctx context.Context, agentID string) ([]core.Config, error) {
	if !scopeForConfig(ctx).Admin {
		return nil, ErrForbidden
	}
	return s.listAgentConfigs(ctx, agentID, true)
}

// AgentConfigs filters before fetching or decrypting configuration bodies.
func (s *Store) AgentConfigs(ctx context.Context, agentID string) ([]core.Config, error) {
	if agentID == "" {
		return nil, ErrInvalid
	}
	if err := requireAgentAccess(ctx, s.pool, agentID); err != nil {
		return nil, err
	}
	configs, err := s.listAgentConfigs(ctx, agentID, false)
	if err != nil || len(configs) > 0 {
		return configs, err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agents WHERE id=$1 AND revoked_at IS NULL)`, agentID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	return configs, nil
}

func (s *Store) listAgentConfigs(ctx context.Context, agentID string, allOwners bool) ([]core.Config, error) {
	where := "agent_id IS NOT NULL"
	var args []any
	if agentID != "" {
		where = "agent_id=$1"
		args = []any{agentID}
	}
	if !allOwners {
		where += workspaceOwnerClause(ctx, "owner_id", &args)
		where += agentAccessClause(ctx, "configs.agent_id", &args)
	} else {
		// Isolated drafts are not host monitors. Only deployment may bind
		// their administrator-reserved listeners to cumulative accounting.
		where += ` AND NOT EXISTS(SELECT 1 FROM panel_users u JOIN agents a ON a.id=configs.agent_id
			WHERE u.id=configs.owner_id AND u.role='user' AND a.owner_id<>u.id)`
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id,COALESCE(agent_id,''),name,description,engine,content,version,created_at,updated_at,owner_id
		FROM configs WHERE `+where+` AND deleted_at IS NULL
		  AND agent_id IN (SELECT id FROM agents WHERE revoked_at IS NULL)
		ORDER BY updated_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	configs := make([]core.Config, 0)
	for rows.Next() {
		var config core.Config
		if err := rows.Scan(&config.ID, &config.AgentID, &config.Name, &config.Description, &config.Engine, &config.Content,
			&config.Version, &config.CreatedAt, &config.UpdatedAt, &config.OwnerID); err != nil {
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
	args := []any{}
	ownerWhere := ownerClause(ctx, "c.owner_id", &args)
	ownerWhere += agentAccessClause(ctx, "latest.agent_id", &args)
	rows, err := s.pool.Query(ctx, `
		SELECT latest.agent_id,latest.engine,COALESCE(latest.config_id,''),COALESCE(latest.config_version,0),latest.finished_at
		FROM (`+latestDeploymentsSQL+`) latest
		LEFT JOIN configs c ON c.id=latest.config_id
		WHERE true`+ownerWhere+` ORDER BY latest.agent_id,latest.engine`, args...)
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

// Probe the existing (agent_id,engine,finished_at) partial index once per
// possible service instead of scanning every successful deployment retained
// in tasks. Do not restrict this to current capabilities: historical deployed
// services (and revoked-node history) retain the same listing semantics.
const latestDeploymentsSQL = `
	SELECT agent.id AS agent_id,latest.engine,latest.config_id,latest.config_version,latest.finished_at
	FROM agents agent CROSS JOIN unnest(ARRAY['mihomo','xray','sing-box','ss-rust']::text[]) selected(engine)
	CROSS JOIN LATERAL (
		SELECT task.engine,task.config_id,task.config_version,task.finished_at
		FROM tasks task
		WHERE task.agent_id=agent.id AND task.engine=selected.engine
		  AND task.action IN ('deploy','import-existing') AND task.status='succeeded' AND task.finished_at IS NOT NULL
		ORDER BY task.finished_at DESC LIMIT 1
	) latest`

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
	if err := requireAgentAccess(ctx, tx, input.AgentID); err != nil {
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

// ConfigClientMetadata returns decrypted client-only metadata for an exact
// configuration version. Callers apply entries only to the matching tag.
func (s *Store) ConfigClientMetadata(ctx context.Context, configID string, version int) (map[string]string, error) {
	if configID == "" || version < 1 {
		return nil, fmt.Errorf("%w: configuration ID and version are required", ErrInvalid)
	}
	args := []any{configID, version}
	ownerWhere := ownerClause(ctx, "c.owner_id", &args)
	ownerWhere += configAgentAccessClause(ctx, "c.agent_id", &args)
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
