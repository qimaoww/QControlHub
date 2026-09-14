package core

import (
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type PanelSettings struct {
	CNIPSource                     *CNIPSource `json:"cnip_source"`
	DefaultAgentEngines            []Engine    `json:"default_agent_engines"`
	Revision                       int64       `json:"revision"`
	PanelName                      string      `json:"panel_name"`
	PanelDescription               string      `json:"panel_description"`
	TimeZone                       string      `json:"time_zone"`
	TimeDisplay                    string      `json:"time_display"`
	UIFontScale                    int         `json:"ui_font_scale"`
	DefaultConfigEditor            string      `json:"default_config_editor"`
	TaskPageSize                   int         `json:"task_page_size"`
	TaskPollIntervalMS             int         `json:"task_poll_interval_ms"`
	AgentHeartbeatIntervalSeconds  int         `json:"agent_heartbeat_interval_seconds"`
	AgentMetricsIntervalSeconds    int         `json:"agent_metrics_interval_seconds"`
	AgentOfflineThresholdSeconds   int         `json:"agent_offline_threshold_seconds"`
	TaskStaleTimeoutSeconds        int         `json:"task_stale_timeout_seconds"`
	InstallTaskStaleTimeoutSeconds int         `json:"install_task_stale_timeout_seconds"`
	TaskMaxAttempts                int         `json:"task_max_attempts"`
	PublicIPProbeIntervalSeconds   int         `json:"public_ip_probe_interval_seconds"`
	CoreLogMinimumLevel            string      `json:"core_log_minimum_level"`
	CoreLogRetentionDays           int         `json:"core_log_retention_days"`
	AgentCoreLogMaxMiB             int         `json:"agent_core_log_max_mib"`
	AgentCoreLogRotateCount        int         `json:"agent_core_log_rotate_count"`
	MetricRetentionDays            int         `json:"metric_retention_days"`
	AuditRetentionDays             int         `json:"audit_retention_days"`
	TaskRetentionDays              int         `json:"task_retention_days"`
	ConfigRevisionRetention        int         `json:"config_revision_retention"`
	WebhookURL                     string      `json:"webhook_url"`
	NotifyTaskFailed               bool        `json:"notify_task_failed"`
	NotifyAgentOffline             bool        `json:"notify_agent_offline"`
	NotifyAgentOnline              bool        `json:"notify_agent_online"`
	NotifyTrafficQuota             bool        `json:"notify_traffic_quota"`
	KomariURL                      string      `json:"komari_url"`
	KomariAPIKey                   string      `json:"komari_api_key"`
	UpdatedAt                      time.Time   `json:"updated_at"`
}

func DefaultPanelSettings() PanelSettings {
	return PanelSettings{
		DefaultAgentEngines:            AllEngines(),
		Revision:                       1,
		PanelName:                      "QControlHub",
		PanelDescription:               "可信远程编排",
		TimeZone:                       "browser",
		TimeDisplay:                    "absolute-relative",
		UIFontScale:                    100,
		DefaultConfigEditor:            "structured",
		TaskPageSize:                   100,
		TaskPollIntervalMS:             600,
		AgentHeartbeatIntervalSeconds:  15,
		AgentMetricsIntervalSeconds:    1,
		AgentOfflineThresholdSeconds:   45,
		TaskStaleTimeoutSeconds:        120,
		InstallTaskStaleTimeoutSeconds: 360,
		TaskMaxAttempts:                3,
		PublicIPProbeIntervalSeconds:   300,
		CoreLogMinimumLevel:            "debug",
		CoreLogRetentionDays:           7,
		AgentCoreLogMaxMiB:             16,
		AgentCoreLogRotateCount:        1,
		MetricRetentionDays:            7,
		AuditRetentionDays:             90,
		NotifyTaskFailed:               true,
		NotifyAgentOffline:             true,
		NotifyAgentOnline:              true,
		NotifyTrafficQuota:             true,
		KomariURL:                      "",
		KomariAPIKey:                   "",
	}
}

func (settings PanelSettings) Validate() error {
	if settings.CNIPSource != nil {
		if err := settings.CNIPSource.Validate(); err != nil {
			return err
		}
	}
	if err := ValidateEngineCapabilities(settings.DefaultAgentEngines); err != nil {
		return err
	}
	if settings.PanelName == "" {
		return errors.New("panel name is required")
	}
	if utf8.RuneCountInString(settings.KomariURL) > 500 || utf8.RuneCountInString(settings.KomariAPIKey) > 500 {
		return errors.New("Komari settings must not exceed 500 characters")
	}
	if strings.ContainsAny(settings.KomariAPIKey, "\r\n") {
		return errors.New("Komari API Key contains invalid characters")
	}
	if strings.TrimSpace(settings.KomariURL) != "" {
		parsed, err := url.Parse(strings.TrimSpace(settings.KomariURL))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("Komari URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
		}
	}
	if utf8.RuneCountInString(settings.PanelName) > 40 {
		return errors.New("panel name must not exceed 40 characters")
	}
	if utf8.RuneCountInString(settings.PanelDescription) > 120 {
		return errors.New("panel description must not exceed 120 characters")
	}
	if !oneOfString(settings.TimeZone, "browser", "Asia/Shanghai", "UTC") {
		return errors.New("unsupported time zone")
	}
	if !oneOfString(settings.TimeDisplay, "absolute-relative", "absolute") {
		return errors.New("unsupported time display")
	}
	if !oneOf(settings.UIFontScale, 90, 100, 110) {
		return errors.New("unsupported UI font scale")
	}
	if !oneOfString(settings.DefaultConfigEditor, "structured", "source") {
		return errors.New("unsupported default config editor")
	}
	if !oneOf(settings.TaskPageSize, 50, 100, 500) {
		return errors.New("unsupported task page size")
	}
	if !oneOf(settings.TaskPollIntervalMS, 600, 1000, 2000, 5000) {
		return errors.New("unsupported task polling interval")
	}
	if !oneOf(settings.AgentHeartbeatIntervalSeconds, 10, 15, 30) || !oneOf(settings.AgentMetricsIntervalSeconds, 1, 5, 15, 30) {
		return errors.New("unsupported agent reporting interval")
	}
	if !oneOf(settings.AgentOfflineThresholdSeconds, 45, 60, 90, 180) || settings.AgentOfflineThresholdSeconds < settings.AgentHeartbeatIntervalSeconds*3 {
		return errors.New("offline threshold must be at least three heartbeat intervals")
	}
	if !oneOf(settings.TaskStaleTimeoutSeconds, 60, 120, 300, 600) || !oneOf(settings.InstallTaskStaleTimeoutSeconds, 180, 360, 600, 900) || !oneOf(settings.TaskMaxAttempts, 1, 3, 5) {
		return errors.New("unsupported task recovery policy")
	}
	if !oneOf(settings.PublicIPProbeIntervalSeconds, 300, 900, 3600) {
		return errors.New("unsupported public IP probe interval")
	}
	if !oneOfString(settings.CoreLogMinimumLevel, "debug", "info", "warning", "error", "critical", "off") {
		return errors.New("unsupported core log minimum level")
	}
	if !oneOf(settings.CoreLogRetentionDays, 1, 3, 7, 14, 30) || !oneOf(settings.AgentCoreLogMaxMiB, 1, 2, 4, 8, 16, 32, 64, 128) || !oneOf(settings.AgentCoreLogRotateCount, 0, 1, 2, 3, 5) {
		return errors.New("unsupported core log retention policy")
	}
	if !oneOf(settings.MetricRetentionDays, 7, 14, 30) || !oneOf(settings.AuditRetentionDays, 0, 30, 90, 180) || !oneOf(settings.TaskRetentionDays, 0, 30, 90, 180) || !oneOf(settings.ConfigRevisionRetention, 0, 50, 100) {
		return errors.New("unsupported database retention policy")
	}
	settings.WebhookURL = strings.TrimSpace(settings.WebhookURL)
	if settings.WebhookURL != "" {
		if utf8.RuneCountInString(settings.WebhookURL) > 500 {
			return errors.New("webhook URL must not exceed 500 characters")
		}
		parsed, err := url.Parse(settings.WebhookURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return errors.New("webhook URL must be an absolute http(s) URL")
		}
	}
	return nil
}

func oneOf(value int, allowed ...int) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func oneOfString(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
