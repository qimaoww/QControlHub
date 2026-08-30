package core

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type Engine string

// AgentFeatureSelfUpgrade identifies the signed task protocol needed for a
// running Agent to replace its own executable and reconnect. Older Agents do
// not advertise this feature and must be reinstalled once before remote
// upgrades can be used.
const AgentFeatureSelfUpgrade = "agent-self-upgrade-v1"

// AgentFeaturePortTraffic identifies Agents that can account and enforce
// per-port traffic quotas independently of the managed proxy engine.
const AgentFeaturePortTraffic = "port-traffic-v1"

// AgentFeatureCoreLogs identifies Agents that stream managed core logs to the
// control plane without persisting them on the node.
const AgentFeatureCoreLogs = "core-logs-v1"

// AgentFeatureCoreLogStatus identifies Agents that report the per-engine
// health of their managed log source. Older Agents continue to stream logs,
// but the panel cannot distinguish an idle source from a failed collector.
const AgentFeatureCoreLogStatus = "core-log-status-v1"

// AgentFeatureMihomoDevelopmentSource identifies Agents that understand the
// negotiated Mihomo development source. Older Agents ignore an unknown
// core_source field during JSON/WSS decoding and would silently fall back to
// the official repository, so the control plane must refuse a mirror task for
// an Agent that does not advertise this feature.
const AgentFeatureMihomoDevelopmentSource = "mihomo-development-source-v1"

// AgentFeaturePublicIPProbe identifies Agents that actively probe their public
// IPv4 and IPv6 egress addresses and report both families in metrics.
const AgentFeaturePublicIPProbe = "public-ip-probe-v1"

// AgentFeatureManagedPublicIPProbe identifies Agents that can receive an
// operator-supplied probe configuration over the authenticated WSS session.
// The control plane sends that configuration only after the current session's
// first complete heartbeat advertises this feature.
const AgentFeatureManagedPublicIPProbe = "managed-public-ip-probe-v1"

// AgentFeatureManagedPolicy identifies Agents that can apply reporting and
// local core-log retention policy updates without a reinstall or restart.
const AgentFeatureManagedPolicy = "managed-agent-policy-v1"

// AgentFeatureManagedConfigRead identifies Agents that can independently read
// the QAgent-managed configuration while an external service is also exposed
// as an optional import source.
const AgentFeatureManagedConfigRead = "managed-config-read-v1"

const (
	PublicIPProbeSourceAgent         = "agent-config"
	PublicIPProbeSourceControlPlane  = "control-plane-config"
	DefaultPublicIPProbeIPv4Endpoint = "https://api.ipify.org/"
	DefaultPublicIPProbeIPv4Fallback = "https://4.ident.me"
	DefaultPublicIPProbeIPv6Endpoint = "https://api6.ipify.org"
	DefaultPublicIPProbeIPv6Fallback = "https://6.ident.me"
	publicIPProbeEndpointMaxBytes    = 2048
)

// PublicIPProbeConfig is an operator-controlled, per-family direct egress
// probe configuration. Managed defaults may carry one approved same-family
// fallback after the ipify primary; an explicit operator endpoint replaces its
// complete family chain and never inherits a hidden public fallback.
type PublicIPProbeConfig struct {
	IPv4Endpoint         string `json:"ipv4_endpoint,omitempty"`
	IPv4FallbackEndpoint string `json:"ipv4_fallback_endpoint,omitempty"`
	IPv6Endpoint         string `json:"ipv6_endpoint,omitempty"`
	IPv6FallbackEndpoint string `json:"ipv6_fallback_endpoint,omitempty"`
	IntervalSeconds      uint32 `json:"interval_seconds,omitempty"`
}

func (config PublicIPProbeConfig) Validate() error {
	if config.IntervalSeconds != 0 && (config.IntervalSeconds < 60 || config.IntervalSeconds > 24*60*60) {
		return errors.New("public IP probe interval must be between 60 and 86400 seconds")
	}
	for index, endpoint := range []string{config.IPv4Endpoint, config.IPv4FallbackEndpoint, config.IPv6Endpoint, config.IPv6FallbackEndpoint} {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint == "" {
			continue
		}
		if len(endpoint) > publicIPProbeEndpointMaxBytes {
			return errors.New("public IP probe endpoint exceeds the length limit")
		}
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("public IP probe endpoint must be an absolute HTTPS URL without credentials, query, or fragment")
		}
		if index == 1 && endpoint != DefaultPublicIPProbeIPv4Fallback || index == 3 && endpoint != DefaultPublicIPProbeIPv6Fallback {
			return errors.New("public IP probe fallback endpoint is not an approved same-family endpoint")
		}
	}
	if fallback := strings.TrimSpace(config.IPv4FallbackEndpoint); fallback != "" && strings.TrimSpace(config.IPv4Endpoint) != DefaultPublicIPProbeIPv4Endpoint {
		return errors.New("public IP probe IPv4 fallback requires the approved ipify primary")
	}
	if fallback := strings.TrimSpace(config.IPv6FallbackEndpoint); fallback != "" && strings.TrimSpace(config.IPv6Endpoint) != DefaultPublicIPProbeIPv6Endpoint {
		return errors.New("public IP probe IPv6 fallback requires the approved ipify primary")
	}
	return nil
}

// Role identifies the account class. Fine-grained access is carried by the
// explicit Permissions field on a user; only admin/user are persisted.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
	// Deprecated aliases keep older integrations compiling. They serialize as
	// "user" and are not accepted as separate persisted identities.
	RoleOperator Role = RoleUser
	RoleAuditor  Role = RoleUser
	RoleReadonly Role = RoleUser
)

// AtLeast reports whether the role grants at least the given privilege level.
func (role Role) AtLeast(minimum Role) bool {
	rank := func(value Role) int {
		switch value {
		case RoleAdmin:
			return 3
		case RoleUser:
			return 1
		default:
			return 1
		}
	}
	return rank(role) >= rank(minimum)
}

func (role Role) Valid() bool {
	switch role {
	case RoleAdmin, RoleUser:
		return true
	default:
		return false
	}
}

// User is a durable panel login identity. Password hashes are intentionally
// never included in this public model.
type User struct {
	ID          string       `json:"id"`
	Username    string       `json:"username"`
	DisplayName string       `json:"display_name,omitempty"`
	Role        Role         `json:"role"`
	Permissions []Permission `json:"permissions,omitempty"`
	Disabled    bool         `json:"disabled"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	LastLoginAt *time.Time   `json:"last_login_at,omitempty"`
}

type UserRequest struct {
	Username    string       `json:"username"`
	DisplayName string       `json:"display_name"`
	Role        Role         `json:"role"`
	Password    string       `json:"password"`
	Permissions []Permission `json:"permissions,omitempty"`
}

type UserUpdate struct {
	DisplayName *string       `json:"display_name"`
	Role        *Role         `json:"role"`
	Password    *string       `json:"password"`
	Disabled    *bool         `json:"disabled"`
	Permissions *[]Permission `json:"permissions"`
}

const (
	EngineMihomo          Engine = "mihomo"
	EngineXray            Engine = "xray"
	EngineSingBox         Engine = "sing-box"
	EngineShadowsocksRust Engine = "ss-rust"
)

func (s TaskStatus) Valid() bool {
	switch s {
	case TaskPending, TaskRunning, TaskSucceeded, TaskFailed, TaskCanceled:
		return true
	default:
		return false
	}
}

func (e Engine) Valid() bool {
	switch e {
	case EngineMihomo, EngineXray, EngineSingBox, EngineShadowsocksRust:
		return true
	default:
		return false
	}
}

func ParseEngine(value string) (Engine, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "singbox" {
		value = string(EngineSingBox)
	}
	if value == "ssrust" || value == "shadowsocks-rust" || value == "shadowsocksrust" {
		value = string(EngineShadowsocksRust)
	}
	engine := Engine(value)
	if !engine.Valid() {
		return "", fmt.Errorf("unsupported engine %q", value)
	}
	return engine, nil
}

type Action string

const (
	ActionValidate          Action = "validate"
	ActionDeploy            Action = "deploy"
	ActionStart             Action = "start"
	ActionStop              Action = "stop"
	ActionRestart           Action = "restart"
	ActionStatus            Action = "status"
	ActionInstall           Action = "install"
	ActionReadConfig        Action = "read-config"
	ActionReadManagedConfig Action = "read-managed-config"
	ActionImportExisting    Action = "import-existing"
	ActionUpgradeAgent      Action = "upgrade-agent"
)

func (a Action) Valid() bool {
	switch a {
	case ActionValidate, ActionDeploy, ActionStart, ActionStop, ActionRestart, ActionStatus, ActionInstall, ActionReadConfig, ActionReadManagedConfig, ActionImportExisting, ActionUpgradeAgent:
		return true
	default:
		return false
	}
}

type RuntimeState struct {
	Installed                       bool   `json:"installed"`
	Version                         string `json:"version,omitempty"`
	ServiceStatus                   string `json:"service_status,omitempty"`
	ExistingConfigAvailable         bool   `json:"existing_config_available,omitempty"`
	ExistingConfigUnsupportedReason string `json:"existing_config_unsupported_reason,omitempty"`
	CoreLogStatus                   string `json:"core_log_status,omitempty"`
	CoreLogError                    string `json:"core_log_error,omitempty"`
}

type HostMetrics struct {
	CollectedAt       time.Time              `json:"collected_at"`
	CPUAvailable      bool                   `json:"cpu_available"`
	CPUPercent        float64                `json:"cpu_percent"`
	MemoryAvailable   bool                   `json:"memory_available"`
	MemoryUsedBytes   uint64                 `json:"memory_used_bytes"`
	MemoryTotalBytes  uint64                 `json:"memory_total_bytes"`
	DiskAvailable     bool                   `json:"disk_available"`
	DiskUsedBytes     uint64                 `json:"disk_used_bytes"`
	DiskTotalBytes    uint64                 `json:"disk_total_bytes"`
	NetworkAvailable  bool                   `json:"network_available"`
	NetworkRXBytes    uint64                 `json:"network_rx_bytes"`
	NetworkTXBytes    uint64                 `json:"network_tx_bytes"`
	NetworkRXBPS      uint64                 `json:"network_rx_bps"`
	NetworkTXBPS      uint64                 `json:"network_tx_bps"`
	NetworkInterfaces []HostNetworkInterface `json:"network_interfaces,omitempty"`
	// ObservedPublicIP is assigned by the control plane from the authenticated
	// WSS source. Agent-provided values are never trusted. It only ever
	// reflects the address family the WSS connection itself used.
	ObservedPublicIP string `json:"observed_public_ip,omitempty"`
	// PublicIPv4 and PublicIPv6 are the egress addresses the Agent probed
	// outbound over each family. Together with ObservedPublicIP they close the
	// single-family blind spot: a node that connects over IPv4 still reports
	// its routable IPv6, and vice versa.
	PublicIPv4 string `json:"public_ipv4,omitempty"`
	PublicIPv6 string `json:"public_ipv6,omitempty"`
	// Probe sources are fixed audit enums and never contain endpoint URLs.
	PublicIPv4Source string `json:"public_ipv4_source,omitempty"`
	PublicIPv6Source string `json:"public_ipv6_source,omitempty"`
}

// HostNetworkInterface describes addresses assigned to an interface that
// carries a default route. Agents only report usable unicast addresses; the
// control plane never needs wildcard, loopback, multicast or link-local
// addresses to generate client connection details.
type HostNetworkInterface struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses,omitempty"`
}

type Agent struct {
	ID                         string                  `json:"id"`
	Name                       string                  `json:"name"`
	Version                    string                  `json:"version,omitempty"`
	OS                         string                  `json:"os"`
	Arch                       string                  `json:"arch"`
	Capabilities               []Engine                `json:"capabilities"`
	Features                   []string                `json:"features,omitempty"`
	Labels                     map[string]string       `json:"labels,omitempty"`
	Runtime                    map[Engine]RuntimeState `json:"runtime,omitempty"`
	Metrics                    HostMetrics             `json:"metrics,omitempty"`
	LastSeen                   time.Time               `json:"last_seen"`
	EnrolledAt                 time.Time               `json:"enrolled_at"`
	PublicKey                  []byte                  `json:"-"`
	Status                     string                  `json:"status,omitempty"`
	EnrollmentCommandAvailable bool                    `json:"enrollment_command_available,omitempty"`
	Reinstalled                bool                    `json:"-"`
}

// KomariNode is the read-only billing and traffic configuration returned by a
// linked Komari monitor. Traffic values are kept in Komari's native byte unit.
type KomariNode struct {
	UUID                           string `json:"uuid"`
	Name                           string `json:"name,omitempty"`
	BillingCycle                   int64  `json:"billing_cycle"`
	TrafficLimit                   int64  `json:"traffic_limit"`
	TrafficLimitType               string `json:"traffic_limit_type,omitempty"`
	EffectiveTrafficLimit          int64  `json:"effective_traffic_limit"`
	EffectiveTrafficType           string `json:"effective_traffic_type,omitempty"`
	EffectiveTrafficLimitAvailable bool   `json:"effective_traffic_limit_available,omitempty"`
	EffectiveTrafficTypeAvailable  bool   `json:"effective_traffic_type_available,omitempty"`
	TrafficResetDay                int64  `json:"traffic_reset_day,omitempty"`
	TrafficUsed                    int64  `json:"traffic_used"`
	TrafficUsedAvailable           bool   `json:"traffic_used_available,omitempty"`
	ExpiredAt                      string `json:"expired_at,omitempty"`
	UpdatedAt                      string `json:"updated_at,omitempty"`
}

// KomariLink is returned by the node settings endpoint. Server is omitted
// when the UUID is not configured or when Komari is unavailable.
type KomariLink struct {
	UUID   string      `json:"uuid,omitempty"`
	Server *KomariNode `json:"server,omitempty"`
}

type Config struct {
	ID          string    `json:"id"`
	AgentID     string    `json:"agent_id,omitempty"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Engine      Engine    `json:"engine"`
	Content     string    `json:"content"`
	Version     int       `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
	TaskCanceled  TaskStatus = "canceled"
)

type Task struct {
	ID            string     `json:"id"`
	AgentID       string     `json:"agent_id"`
	Action        Action     `json:"action"`
	Engine        Engine     `json:"engine"`
	ConfigID      string     `json:"config_id,omitempty"`
	ConfigVersion int        `json:"config_version,omitempty"`
	ConfigContent string     `json:"config_content,omitempty"`
	CoreVersion   string     `json:"core_version,omitempty"`
	CoreSource    string     `json:"core_source,omitempty"`
	Status        TaskStatus `json:"status"`
	Attempt       int        `json:"attempt"`
	LeaseID       string     `json:"lease_id,omitempty"`
	Output        string     `json:"output,omitempty"`
	Error         string     `json:"error,omitempty"`
	Reused        bool       `json:"reused,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

type TaskRequest struct {
	AgentID     string `json:"agent_id"`
	Action      Action `json:"action"`
	Engine      Engine `json:"engine"`
	ConfigID    string `json:"config_id,omitempty"`
	CoreVersion string `json:"core_version,omitempty"`
	CoreSource  string `json:"core_source,omitempty"`
}

type Deployment struct {
	AgentID       string    `json:"agent_id"`
	Engine        Engine    `json:"engine"`
	ConfigID      string    `json:"config_id"`
	ConfigVersion int       `json:"config_version"`
	DeployedAt    time.Time `json:"deployed_at"`
}

type EnrollRequest struct {
	Name         string            `json:"name"`
	Version      string            `json:"version,omitempty"`
	OS           string            `json:"os"`
	Arch         string            `json:"arch"`
	Capabilities []Engine          `json:"capabilities"`
	Features     []string          `json:"features,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	PublicKey    string            `json:"public_key"`
}

type EnrollResponse struct {
	AgentID string `json:"agent_id"`
}

type EnrollmentToken struct {
	ID          string     `json:"id"`
	AgentID     string     `json:"agent_id,omitempty"`
	Name        string     `json:"name"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	MaxUses     int        `json:"max_uses"`
	UsedCount   int        `json:"used_count"`
	Reusable    bool       `json:"reusable,omitempty"`
	Recoverable bool       `json:"command_available,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

type EnrollmentTokenRequest struct {
	Name       string `json:"name"`
	TTLMinutes int    `json:"ttl_minutes"`
	MaxUses    int    `json:"max_uses"`
	Reusable   bool   `json:"reusable,omitempty"`
}

type EnrollmentTokenCreated struct {
	EnrollmentToken
	Token string `json:"token"`
}

type HeartbeatRequest struct {
	Version      string                  `json:"version,omitempty"`
	OS           string                  `json:"os,omitempty"`
	Arch         string                  `json:"arch,omitempty"`
	Runtime      map[Engine]RuntimeState `json:"runtime,omitempty"`
	Metrics      *HostMetrics            `json:"metrics,omitempty"`
	TrafficUsage []PortTrafficUsage      `json:"traffic_usage,omitempty"`
	Features     []string                `json:"features,omitempty"`
}

type TaskResultRequest struct {
	LeaseID string `json:"lease_id"`
	Success bool   `json:"success"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

const (
	WireHello         = "hello"
	WirePublicIPProbe = "public_ip_probe"
	WireAgentPolicy   = "agent_policy"
	WireHeartbeat     = "heartbeat"
	WireMetrics       = "metrics"
	WireTask          = "task"
	WireResult        = "result"
	WireResultAck     = "result_ack"
	WireCoreLogs      = "core_logs"
	WireCoreLogsAck   = "core_logs_ack"
	WireError         = "error"
)

// AgentPolicy contains the node-local settings controlled by the panel. The
// limits intentionally stay small because they affect every connected node.
type AgentPolicy struct {
	HeartbeatIntervalSeconds uint32 `json:"heartbeat_interval_seconds"`
	MetricsIntervalSeconds   uint32 `json:"metrics_interval_seconds"`
	CoreLogMaxMiB            uint32 `json:"core_log_max_mib"`
	CoreLogRotateCount       uint32 `json:"core_log_rotate_count"`
}

func (policy AgentPolicy) Validate() error {
	if !oneOf(int(policy.HeartbeatIntervalSeconds), 10, 15, 30) {
		return errors.New("unsupported heartbeat interval")
	}
	if !oneOf(int(policy.MetricsIntervalSeconds), 1, 5, 15, 30) {
		return errors.New("unsupported metrics interval")
	}
	if !oneOf(int(policy.CoreLogMaxMiB), 1, 2, 4, 8, 16, 32, 64, 128) {
		return errors.New("unsupported local core log capacity")
	}
	if !oneOf(int(policy.CoreLogRotateCount), 0, 1, 2, 3, 5) {
		return errors.New("unsupported local core log rotation count")
	}
	return nil
}

const (
	MaxCoreLogBatchEntries = 32
	MaxCoreLogMessageBytes = 4096
	// JSON escaping can expand a batch of control-character-heavy messages to
	// nearly six times the raw message size.
	MaxCoreLogWireBytes = 1 << 20
)

// CoreLogEntry is one line emitted by a managed proxy core. AgentID and ID are
// assigned by the control plane; Agents only submit the remaining fields.
type CoreLogEntry struct {
	ID         int64     `json:"id,omitempty"`
	AgentID    string    `json:"agent_id,omitempty"`
	Engine     Engine    `json:"engine"`
	Level      string    `json:"level"`
	Message    string    `json:"message"`
	LoggedAt   time.Time `json:"logged_at"`
	ReceivedAt time.Time `json:"received_at,omitempty"`
}

type CoreLogBatch struct {
	ID      string         `json:"id"`
	Entries []CoreLogEntry `json:"entries"`
}

type TaskResultEnvelope struct {
	TaskID string            `json:"task_id"`
	Result TaskResultRequest `json:"result"`
}

type WireMessage struct {
	Type            string               `json:"type"`
	Heartbeat       *HeartbeatRequest    `json:"heartbeat,omitempty"`
	Metrics         *HostMetrics         `json:"metrics,omitempty"`
	TrafficUsage    []PortTrafficUsage   `json:"traffic_usage,omitempty"`
	Task            *Task                `json:"task,omitempty"`
	Result          *TaskResultEnvelope  `json:"result,omitempty"`
	CoreLogs        *CoreLogBatch        `json:"core_logs,omitempty"`
	TrafficPolicies []PortTrafficPolicy  `json:"traffic_policies,omitempty"`
	PublicIPProbe   *PublicIPProbeConfig `json:"public_ip_probe,omitempty"`
	AgentPolicy     *AgentPolicy         `json:"agent_policy,omitempty"`
	TaskID          string               `json:"task_id,omitempty"`
	BatchID         string               `json:"batch_id,omitempty"`
	Error           string               `json:"error,omitempty"`
}

type Overview struct {
	Agents       int `json:"agents"`
	AgentsOnline int `json:"agents_online"`
	Configs      int `json:"configs"`
	NodeConfigs  int `json:"node_configs"`
	TasksPending int `json:"tasks_pending"`
	TasksQueued  int `json:"tasks_queued"`
	TasksRunning int `json:"tasks_running"`
	TasksFailed  int `json:"tasks_failed"`
}

// MetricSample is one historical resource sample recorded for an agent.
// Rates are stored in bytes per second and percentages as 0-100 values.
type MetricSample struct {
	SampledAt     time.Time `json:"sampled_at"`
	CPUPercent    float64   `json:"cpu_percent"`
	MemoryPercent float64   `json:"memory_percent"`
	RXRateBPS     uint64    `json:"rx_rate_bps"`
	TXRateBPS     uint64    `json:"tx_rate_bps"`
}

// AuditLogEntry is one administrative action recorded for the audit trail.
type AuditLogEntry struct {
	ID       int64     `json:"id"`
	ActedAt  time.Time `json:"acted_at"`
	Actor    string    `json:"actor"`
	Action   string    `json:"action"`
	Target   string    `json:"target,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	RemoteIP string    `json:"remote_ip,omitempty"`
}

// ConfigTemplate is a reusable configuration body with {{variable}} placeholders
// that are rendered per node when the template is applied.
type ConfigTemplate struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Engine    Engine    `json:"engine"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type PanelSettings struct {
	Revision                       int64     `json:"revision"`
	PanelName                      string    `json:"panel_name"`
	PanelDescription               string    `json:"panel_description"`
	TimeZone                       string    `json:"time_zone"`
	TimeDisplay                    string    `json:"time_display"`
	UIFontScale                    int       `json:"ui_font_scale"`
	DefaultConfigEditor            string    `json:"default_config_editor"`
	TaskPageSize                   int       `json:"task_page_size"`
	TaskPollIntervalMS             int       `json:"task_poll_interval_ms"`
	AgentHeartbeatIntervalSeconds  int       `json:"agent_heartbeat_interval_seconds"`
	AgentMetricsIntervalSeconds    int       `json:"agent_metrics_interval_seconds"`
	AgentOfflineThresholdSeconds   int       `json:"agent_offline_threshold_seconds"`
	TaskStaleTimeoutSeconds        int       `json:"task_stale_timeout_seconds"`
	InstallTaskStaleTimeoutSeconds int       `json:"install_task_stale_timeout_seconds"`
	TaskMaxAttempts                int       `json:"task_max_attempts"`
	PublicIPProbeIntervalSeconds   int       `json:"public_ip_probe_interval_seconds"`
	CoreLogMinimumLevel            string    `json:"core_log_minimum_level"`
	CoreLogRetentionDays           int       `json:"core_log_retention_days"`
	AgentCoreLogMaxMiB             int       `json:"agent_core_log_max_mib"`
	AgentCoreLogRotateCount        int       `json:"agent_core_log_rotate_count"`
	MetricRetentionDays            int       `json:"metric_retention_days"`
	AuditRetentionDays             int       `json:"audit_retention_days"`
	TaskRetentionDays              int       `json:"task_retention_days"`
	ConfigRevisionRetention        int       `json:"config_revision_retention"`
	WebhookURL                     string    `json:"webhook_url"`
	NotifyTaskFailed               bool      `json:"notify_task_failed"`
	NotifyAgentOffline             bool      `json:"notify_agent_offline"`
	NotifyAgentOnline              bool      `json:"notify_agent_online"`
	NotifyTrafficQuota             bool      `json:"notify_traffic_quota"`
	KomariURL                      string    `json:"komari_url"`
	KomariAPIKey                   string    `json:"komari_api_key"`
	UpdatedAt                      time.Time `json:"updated_at"`
}

func DefaultPanelSettings() PanelSettings {
	return PanelSettings{
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

func NewID(prefix string) (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(buf), nil
}

func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
