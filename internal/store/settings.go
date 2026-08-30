package store

import (
	"context"
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
	webhook_url,notify_task_failed,notify_agent_offline,notify_agent_online,notify_traffic_quota,updated_at`

func scanPanelSettings(row pgx.Row) (core.PanelSettings, error) {
	var value core.PanelSettings
	err := row.Scan(
		&value.Revision, &value.PanelName, &value.PanelDescription, &value.TimeZone, &value.TimeDisplay, &value.UIFontScale, &value.DefaultConfigEditor,
		&value.TaskPageSize, &value.TaskPollIntervalMS, &value.AgentHeartbeatIntervalSeconds, &value.AgentMetricsIntervalSeconds,
		&value.AgentOfflineThresholdSeconds, &value.TaskStaleTimeoutSeconds, &value.InstallTaskStaleTimeoutSeconds, &value.TaskMaxAttempts,
		&value.PublicIPProbeIntervalSeconds, &value.CoreLogMinimumLevel, &value.CoreLogRetentionDays, &value.AgentCoreLogMaxMiB,
		&value.AgentCoreLogRotateCount, &value.MetricRetentionDays, &value.AuditRetentionDays, &value.TaskRetentionDays, &value.ConfigRevisionRetention,
		&value.WebhookURL, &value.NotifyTaskFailed, &value.NotifyAgentOffline, &value.NotifyAgentOnline, &value.NotifyTrafficQuota, &value.UpdatedAt,
	)
	return value, err
}

func (s *Store) PanelSettings(ctx context.Context) (core.PanelSettings, error) {
	settings, err := scanPanelSettings(s.pool.QueryRow(ctx, `SELECT `+panelSettingsColumns+` FROM panel_settings WHERE id=1`))
	if err != nil {
		return core.PanelSettings{}, fmt.Errorf("read panel settings: %w", err)
	}
	return settings, nil
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
	settings.PanelName = strings.TrimSpace(settings.PanelName)
	settings.PanelDescription = strings.TrimSpace(settings.PanelDescription)
	settings.CoreLogMinimumLevel = strings.ToLower(strings.TrimSpace(settings.CoreLogMinimumLevel))
	settings.WebhookURL = strings.TrimSpace(settings.WebhookURL)
	current, err := s.PanelSettings(ctx)
	if err != nil {
		return core.PanelSettings{}, err
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
	// The v36 frontend sends a revision and all operational fields but does not
	// know about the v37 typography setting. Preserve the stored value when
	// that field is absent so an older tab can still save unrelated changes.
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
	where := "id=1"
	args := []any{
		settings.PanelName, settings.PanelDescription, settings.TimeZone, settings.TimeDisplay, settings.UIFontScale, settings.DefaultConfigEditor,
		settings.TaskPageSize, settings.TaskPollIntervalMS, settings.AgentHeartbeatIntervalSeconds, settings.AgentMetricsIntervalSeconds,
		settings.AgentOfflineThresholdSeconds, settings.TaskStaleTimeoutSeconds, settings.InstallTaskStaleTimeoutSeconds, settings.TaskMaxAttempts,
		settings.PublicIPProbeIntervalSeconds, settings.CoreLogMinimumLevel, settings.CoreLogRetentionDays, settings.AgentCoreLogMaxMiB,
		settings.AgentCoreLogRotateCount, settings.MetricRetentionDays, settings.AuditRetentionDays, settings.TaskRetentionDays,
		settings.ConfigRevisionRetention, settings.WebhookURL, settings.NotifyTaskFailed, settings.NotifyAgentOffline,
		settings.NotifyAgentOnline, settings.NotifyTrafficQuota, settings.UpdatedAt,
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
		notify_agent_online=$27,notify_traffic_quota=$28,updated_at=$29 WHERE ` + where + ` RETURNING ` + panelSettingsColumns
	saved, err := scanPanelSettings(s.pool.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) && expectedRevision > 0 {
		return core.PanelSettings{}, fmt.Errorf("%w: settings were changed in another session", ErrConflict)
	}
	if err != nil {
		return core.PanelSettings{}, fmt.Errorf("save panel settings: %w", err)
	}
	return saved, nil
}
