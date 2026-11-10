package core

import (
	"time"
)

type RuntimeState struct {
	ManagedHostBlockedReason        string                 `json:"managed_host_blocked_reason,omitempty"`
	ManagedOperationBlockedReason   string                 `json:"managed_operation_blocked_reason,omitempty"`
	Instance                        *SharedInstanceRuntime `json:"instance,omitempty"`
	Installed                       bool                   `json:"installed"`
	Version                         string                 `json:"version,omitempty"`
	ServiceStatus                   string                 `json:"service_status,omitempty"`
	ExistingConfigAvailable         bool                   `json:"existing_config_available,omitempty"`
	ExistingConfigUnsupportedReason string                 `json:"existing_config_unsupported_reason,omitempty"`
	CoreLogStatus                   string                 `json:"core_log_status,omitempty"`
	CoreLogError                    string                 `json:"core_log_error,omitempty"`
	// CoreLogSource names the transport behind CoreLogStatus and CoreLogLastAt
	// is the last line the Agent delivered from it, which separates "quiet" from
	// "never delivered".
	CoreLogSource string     `json:"core_log_source,omitempty"`
	CoreLogLastAt *time.Time `json:"core_log_last_at,omitempty"`
}

// Configuration metadata is projected without reading or decrypting bodies.
// An allocated identity can exist before its service or configuration exists.
type SharedInstanceRuntime struct {
	ID               string     `json:"id"`
	Created          bool       `json:"created"`
	SavedConfigID    string     `json:"saved_config_id,omitempty"`
	SavedVersion     int        `json:"saved_version,omitempty"`
	DeployedConfigID string     `json:"deployed_config_id,omitempty"`
	DeployedVersion  int        `json:"deployed_version,omitempty"`
	DeployedAt       *time.Time `json:"deployed_at,omitempty"`
	ObservedAt       *time.Time `json:"observed_at,omitempty"`
	ConfigUncertain  bool       `json:"config_uncertain"`
	Reason           string     `json:"reason,omitempty"`
	Restriction      string     `json:"restriction,omitempty"`
	UpgradeRequired  bool       `json:"upgrade_required"`
	Operation        *Task      `json:"operation,omitempty"`
}

type Agent struct {
	ID                         string                          `json:"id"`
	OwnerID                    string                          `json:"-"`
	CanManage                  bool                            `json:"can_manage"`
	SharedEngines              []Engine                        `json:"shared_engines,omitempty"`
	SharedAllocation           *AgentShare                     `json:"shared_allocation,omitempty"`
	Name                       string                          `json:"name"`
	Version                    string                          `json:"version,omitempty"`
	OS                         string                          `json:"os"`
	Arch                       string                          `json:"arch"`
	Capabilities               []Engine                        `json:"capabilities"`
	SupportedCapabilities      []Engine                        `json:"supported_capabilities"`
	CapabilityTransitions      map[Engine]CapabilityTransition `json:"capability_transitions,omitempty"`
	Features                   []string                        `json:"features,omitempty"`
	Labels                     map[string]string               `json:"labels,omitempty"`
	Runtime                    map[Engine]RuntimeState         `json:"runtime,omitempty"`
	Metrics                    HostMetrics                     `json:"metrics,omitempty"`
	TimeZone                   AgentTimeZoneState              `json:"time_zone"`
	LastSeen                   time.Time                       `json:"last_seen"`
	EnrolledAt                 time.Time                       `json:"enrolled_at"`
	PublicKey                  []byte                          `json:"-"`
	Status                     string                          `json:"status,omitempty"`
	EnrollmentCommandAvailable bool                            `json:"enrollment_command_available,omitempty"`
	// RegionCode is the node's resolved country/region: the owner's manual
	// preference when one is set, otherwise the control plane's GeoIP result.
	// List views render the flag from this instead of one request per node.
	RegionCode string `json:"region_code,omitempty"`
	// AdminHidden lets the owner keep a node out of every administrator view
	// while the node keeps running and accounting. Only the owner and explicit
	// share recipients can see or manage it.
	AdminHidden bool `json:"admin_hidden,omitempty"`
	// CanHide tells the UI whether this principal may toggle AdminHidden. Only
	// the node owner can, so administrators and compatibility tokens never see
	// the control for someone else's node.
	CanHide     bool `json:"can_hide,omitempty"`
	Reinstalled bool `json:"-"`
}

// AgentTimeZoneState is scoped to one registered Agent. AutoAddressHash is
// retained only as a non-reversible change detector and is never sent to the
// panel client.
type AgentTimeZoneState struct {
	Mode            string `json:"mode,omitempty"`
	TimeZone        string `json:"time_zone,omitempty"`
	AppliedTimeZone string `json:"applied_time_zone,omitempty"`
	Status          string `json:"status,omitempty"`
	Error           string `json:"error,omitempty"`
	Revision        int64  `json:"revision,omitempty"`
	AutoAddressHash string `json:"-"`
}

// AgentDirectoryEntry is the read-only cross-account summary behind the
// "other users' nodes" dialog. It deliberately excludes configuration bodies,
// logs, metrics and tasks, and remains readable for hidden nodes so an
// operator can still confirm that a node exists without managing it.
type AgentDirectoryEntry struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	OwnerID       string    `json:"owner_id"`
	OwnerUsername string    `json:"owner_username"`
	Status        string    `json:"status"`
	Capabilities  []Engine  `json:"capabilities"`
	Ports         []int     `json:"ports"`
	AdminHidden   bool      `json:"admin_hidden"`
	CanManage     bool      `json:"can_manage"`
	EnrolledAt    time.Time `json:"enrolled_at"`
	LastSeen      time.Time `json:"last_seen"`
}

// ManagedBlockReason keeps older Agents fail closed when the scope of a
// discovery issue cannot be established. No policy depends on error text.
func (s RuntimeState) ManagedBlockReason(features []string, shared ...bool) string {
	if s.ManagedOperationBlockedReason != "" {
		return s.ManagedOperationBlockedReason
	}
	if (len(shared) == 0 || !shared[0]) && s.ManagedHostBlockedReason != "" {
		return s.ManagedHostBlockedReason
	}
	isolated := false
	for _, feature := range features {
		if feature == AgentFeatureImportIsolation {
			isolated = true
		}
	}
	if !isolated && s.ExistingConfigUnsupportedReason != "" {
		return "请先升级 Agent，以区分外部导入限制和托管目标安全状态：" + s.ExistingConfigUnsupportedReason
	}
	return ""
}
