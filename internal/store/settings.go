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

const panelSettingsColumns = `revision,panel_name,panel_description,time_zone,time_display,ui_font_scale,default_config_editor,
	task_page_size,task_poll_interval_ms,agent_heartbeat_interval_seconds,agent_metrics_interval_seconds,
	agent_offline_threshold_seconds,task_stale_timeout_seconds,install_task_stale_timeout_seconds,task_max_attempts,
	public_ip_probe_interval_seconds,core_log_minimum_level,core_log_retention_days,agent_core_log_max_mib,
	agent_core_log_rotate_count,metric_retention_days,audit_retention_days,task_retention_days,config_revision_retention,
	webhook_url,notify_task_failed,notify_agent_offline,notify_agent_online,notify_traffic_quota,komari_url,komari_api_key,updated_at,
	COALESCE(default_agent_engines, '["mihomo","xray","sing-box","ss-rust"]'::jsonb)`

func scanPanelSettings(row pgx.Row) (core.PanelSettings, error) {
	var value core.PanelSettings
	err := row.Scan(
		&value.Revision, &value.PanelName, &value.PanelDescription, &value.TimeZone, &value.TimeDisplay, &value.UIFontScale, &value.DefaultConfigEditor,
		&value.TaskPageSize, &value.TaskPollIntervalMS, &value.AgentHeartbeatIntervalSeconds, &value.AgentMetricsIntervalSeconds,
		&value.AgentOfflineThresholdSeconds, &value.TaskStaleTimeoutSeconds, &value.InstallTaskStaleTimeoutSeconds, &value.TaskMaxAttempts,
		&value.PublicIPProbeIntervalSeconds, &value.CoreLogMinimumLevel, &value.CoreLogRetentionDays, &value.AgentCoreLogMaxMiB,
		&value.AgentCoreLogRotateCount, &value.MetricRetentionDays, &value.AuditRetentionDays, &value.TaskRetentionDays, &value.ConfigRevisionRetention,
		&value.WebhookURL, &value.NotifyTaskFailed, &value.NotifyAgentOffline, &value.NotifyAgentOnline, &value.NotifyTrafficQuota, &value.KomariURL, &value.KomariAPIKey, &value.UpdatedAt,
		&value.DefaultAgentEngines,
	)
	return value, err
}

func (s *Store) PanelSettings(ctx context.Context) (core.PanelSettings, error) {
	return s.panelSettingsForOwner(ctx, s.pool, scopeForConfig(ctx).OwnerID)
}

func (s *Store) panelSettingsForOwner(ctx context.Context, executor storeExecutor, ownerID string) (core.PanelSettings, error) {
	if ownerID != "" {
		var content string
		var revision int64
		var updatedAt time.Time
		err := executor.QueryRow(ctx, `SELECT content,revision,updated_at FROM user_panel_settings WHERE owner_id=$1`, ownerID).
			Scan(&content, &revision, &updatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return core.DefaultPanelSettings(), nil
		}
		if err != nil {
			return core.PanelSettings{}, err
		}
		content, err = s.decryptContent(content)
		if err != nil {
			return core.PanelSettings{}, err
		}
		var settings core.PanelSettings
		if err := json.Unmarshal([]byte(content), &settings); err != nil {
			return core.PanelSettings{}, err
		}
		settings.Revision, settings.UpdatedAt = revision, updatedAt
		return settings, nil
	}
	settings, err := scanPanelSettings(executor.QueryRow(ctx, `SELECT `+panelSettingsColumns+` FROM panel_settings WHERE id=1`))
	if err != nil {
		return core.PanelSettings{}, fmt.Errorf("read panel settings: %w", err)
	}
	return settings, nil
}

// Agent runtime policy always follows its owner, never the current viewer or
// a user to whom the node happens to be shared.
func (s *Store) AgentPanelSettings(ctx context.Context, agentID string) (core.PanelSettings, error) {
	var owner string
	if err := s.pool.QueryRow(ctx, `SELECT owner_id FROM agents WHERE id=$1 AND revoked_at IS NULL`, agentID).Scan(&owner); err != nil {
		return core.PanelSettings{}, mapError(err)
	}
	return s.panelSettingsForOwner(ctx, s.pool, owner)
}

// Arguments are store-owned SQL identifiers/literals, never request input.
func agentRuntimeSettingSQL(agentAlias, field, fallback string) string {
	return `CASE WHEN ` + agentAlias + `.owner_id='' THEN (SELECT ` + field + ` FROM panel_settings WHERE id=1)
		ELSE COALESCE((SELECT runtime->>'` + field + `' FROM user_panel_settings WHERE owner_id=` + agentAlias + `.owner_id),` + fallback + `) END`
}

// SavePanelSettings is the compatibility path for internal callers and older
// clients. The API uses SavePanelSettingsRevision to reject stale browser tabs.
func (s *Store) SavePanelSettings(ctx context.Context, settings core.PanelSettings) (core.PanelSettings, error) {
	return s.savePanelSettings(ctx, settings, 0)
}

func (s *Store) SavePanelSettingsRevision(ctx context.Context, settings core.PanelSettings, expectedRevision int64) (core.PanelSettings, error) {
	if expectedRevision < 1 {
		return core.PanelSettings{}, fmt.Errorf("%w: settings revision is required", ErrInvalid)
	}
	return s.savePanelSettings(ctx, settings, expectedRevision)
}

func (s *Store) savePanelSettings(ctx context.Context, settings core.PanelSettings, expectedRevision int64) (core.PanelSettings, error) {
	if scope := scopeForConfig(ctx); scope.OwnerID == "" && !scope.Admin {
		return core.PanelSettings{}, ErrForbidden
	}
	settings.PanelName = strings.TrimSpace(settings.PanelName)
	settings.PanelDescription = strings.TrimSpace(settings.PanelDescription)
	settings.CoreLogMinimumLevel = strings.ToLower(strings.TrimSpace(settings.CoreLogMinimumLevel))
	settings.WebhookURL = strings.TrimSpace(settings.WebhookURL)
	settings.KomariURL = strings.TrimSpace(settings.KomariURL)
	settings.KomariAPIKey = strings.TrimSpace(settings.KomariAPIKey)
	current, err := s.PanelSettings(ctx)
	if err != nil {
		return core.PanelSettings{}, err
	}
	// Omitted/null means a legacy client; [] explicitly disables all defaults.
	if settings.DefaultAgentEngines == nil {
		settings.DefaultAgentEngines = current.DefaultAgentEngines
	}
	// Requests produced before schema v35 have none of the new reporting
	// fields. Preserve every new setting as one unit so a legacy cosmetic save
	// cannot silently reset operational policy or notification switches.
	if settings.AgentHeartbeatIntervalSeconds == 0 {
		settings.TimeZone = current.TimeZone
		settings.TimeDisplay = current.TimeDisplay
		settings.UIFontScale = current.UIFontScale
		settings.DefaultConfigEditor = current.DefaultConfigEditor
		settings.AgentHeartbeatIntervalSeconds = current.AgentHeartbeatIntervalSeconds
		settings.AgentMetricsIntervalSeconds = current.AgentMetricsIntervalSeconds
		settings.AgentOfflineThresholdSeconds = current.AgentOfflineThresholdSeconds
		settings.TaskStaleTimeoutSeconds = current.TaskStaleTimeoutSeconds
		settings.InstallTaskStaleTimeoutSeconds = current.InstallTaskStaleTimeoutSeconds
		settings.TaskMaxAttempts = current.TaskMaxAttempts
		settings.PublicIPProbeIntervalSeconds = current.PublicIPProbeIntervalSeconds
		settings.CoreLogRetentionDays = current.CoreLogRetentionDays
		settings.AgentCoreLogMaxMiB = current.AgentCoreLogMaxMiB
		settings.AgentCoreLogRotateCount = current.AgentCoreLogRotateCount
		settings.MetricRetentionDays = current.MetricRetentionDays
		settings.AuditRetentionDays = current.AuditRetentionDays
		settings.TaskRetentionDays = current.TaskRetentionDays
		settings.ConfigRevisionRetention = current.ConfigRevisionRetention
		settings.NotifyTaskFailed = current.NotifyTaskFailed
		settings.NotifyAgentOffline = current.NotifyAgentOffline
		settings.NotifyAgentOnline = current.NotifyAgentOnline
		settings.NotifyTrafficQuota = current.NotifyTrafficQuota
	}
	// Older frontends send a revision and all known operational fields but do
	// not know about settings added later (typography, then Komari). Preserve
	// the typography value when it is absent; the API layer similarly preserves
	// omitted Komari fields.
	if settings.UIFontScale == 0 {
		settings.UIFontScale = current.UIFontScale
	}
	if settings.CoreLogMinimumLevel == "" {
		settings.CoreLogMinimumLevel = current.CoreLogMinimumLevel
	}
	if err := settings.Validate(); err != nil {
		return core.PanelSettings{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	settings.UpdatedAt = time.Now().UTC()
	if ownerID := scopeForConfig(ctx).OwnerID; ownerID != "" {
		if expectedRevision == 0 {
			expectedRevision = current.Revision
		}
		return s.saveUserPanelSettings(ctx, ownerID, settings, expectedRevision)
	}
	where := "id=1"
	args := []any{
		settings.PanelName, settings.PanelDescription, settings.TimeZone, settings.TimeDisplay, settings.UIFontScale, settings.DefaultConfigEditor,
		settings.TaskPageSize, settings.TaskPollIntervalMS, settings.AgentHeartbeatIntervalSeconds, settings.AgentMetricsIntervalSeconds,
		settings.AgentOfflineThresholdSeconds, settings.TaskStaleTimeoutSeconds, settings.InstallTaskStaleTimeoutSeconds, settings.TaskMaxAttempts,
		settings.PublicIPProbeIntervalSeconds, settings.CoreLogMinimumLevel, settings.CoreLogRetentionDays, settings.AgentCoreLogMaxMiB,
		settings.AgentCoreLogRotateCount, settings.MetricRetentionDays, settings.AuditRetentionDays, settings.TaskRetentionDays,
		settings.ConfigRevisionRetention, settings.WebhookURL, settings.NotifyTaskFailed, settings.NotifyAgentOffline,
		settings.NotifyAgentOnline, settings.NotifyTrafficQuota, settings.KomariURL, settings.KomariAPIKey, settings.UpdatedAt,
		settings.DefaultAgentEngines,
	}
	if expectedRevision > 0 {
		where += fmt.Sprintf(" AND revision=$%d", len(args)+1)
		args = append(args, expectedRevision)
	}
	query := `UPDATE panel_settings SET revision=revision+1,
		panel_name=$1,panel_description=$2,time_zone=$3,time_display=$4,ui_font_scale=$5,default_config_editor=$6,
		task_page_size=$7,task_poll_interval_ms=$8,agent_heartbeat_interval_seconds=$9,agent_metrics_interval_seconds=$10,
		agent_offline_threshold_seconds=$11,task_stale_timeout_seconds=$12,install_task_stale_timeout_seconds=$13,task_max_attempts=$14,
		public_ip_probe_interval_seconds=$15,core_log_minimum_level=$16,core_log_retention_days=$17,agent_core_log_max_mib=$18,
		agent_core_log_rotate_count=$19,metric_retention_days=$20,audit_retention_days=$21,task_retention_days=$22,
		config_revision_retention=$23,webhook_url=$24,notify_task_failed=$25,notify_agent_offline=$26,
		notify_agent_online=$27,notify_traffic_quota=$28,komari_url=$29,komari_api_key=$30,updated_at=$31,default_agent_engines=$32 WHERE ` + where + ` RETURNING ` + panelSettingsColumns
	saved, err := scanPanelSettings(s.pool.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) && expectedRevision > 0 {
		return core.PanelSettings{}, fmt.Errorf("%w: settings were changed in another session", ErrConflict)
	}
	if err != nil {
		return core.PanelSettings{}, fmt.Errorf("save panel settings: %w", err)
	}
	return saved, nil
}

func (s *Store) saveUserPanelSettings(ctx context.Context, ownerID string, settings core.PanelSettings, revision int64) (core.PanelSettings, error) {
	payload, err := json.Marshal(settings)
	if err != nil {
		return core.PanelSettings{}, err
	}
	content, err := s.encryptContent(string(payload))
	if err != nil {
		return core.PanelSettings{}, err
	}
	// Only operational, non-secret values are available to background SQL.
	runtime := settings
	runtime.WebhookURL, runtime.KomariURL, runtime.KomariAPIKey = "", "", ""
	runtimeJSON, err := json.Marshal(runtime)
	if err != nil {
		return core.PanelSettings{}, err
	}
	err = s.pool.QueryRow(ctx, `INSERT INTO user_panel_settings(owner_id,content,runtime,revision,updated_at)
		SELECT $1,$2,$3,2,$5 WHERE $4=1 OR EXISTS(SELECT 1 FROM user_panel_settings WHERE owner_id=$1)
		ON CONFLICT(owner_id) DO UPDATE SET content=EXCLUDED.content,runtime=EXCLUDED.runtime,
			revision=user_panel_settings.revision+1,updated_at=EXCLUDED.updated_at
		WHERE user_panel_settings.revision=$4
		RETURNING revision,updated_at`, ownerID, content, runtimeJSON, revision, settings.UpdatedAt).Scan(&settings.Revision, &settings.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.PanelSettings{}, fmt.Errorf("%w: settings were changed in another session", ErrConflict)
	}
	return settings, err
}

// InitializeDefaultAgentEngines seeds installation preferences once. Subsequent
// starts and upgrades never overwrite a selection saved by the panel.
func (s *Store) InitializeDefaultAgentEngines(ctx context.Context, engines []core.Engine) error {
	if engines == nil {
		return fmt.Errorf("%w: explicit engine selection required", ErrInvalid)
	}
	if err := core.ValidateEngineCapabilities(engines); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	_, err := s.pool.Exec(ctx, `UPDATE panel_settings SET default_agent_engines=$1,
		revision=revision+1,updated_at=now() WHERE id=1 AND default_agent_engines IS NULL`, engines)
	return err
}
