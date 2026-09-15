package core

import (
	"time"
)

type RuntimeState struct {
	Installed                       bool   `json:"installed"`
	Version                         string `json:"version,omitempty"`
	ServiceStatus                   string `json:"service_status,omitempty"`
	ExistingConfigAvailable         bool   `json:"existing_config_available,omitempty"`
	ExistingConfigUnsupportedReason string `json:"existing_config_unsupported_reason,omitempty"`
	CoreLogStatus                   string `json:"core_log_status,omitempty"`
	CoreLogError                    string `json:"core_log_error,omitempty"`
}

type Agent struct {
	ID                         string                          `json:"id"`
	OwnerID                    string                          `json:"-"`
	CanManage                  bool                            `json:"can_manage"`
	SharedEngines              []Engine                        `json:"shared_engines,omitempty"`
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
	LastSeen                   time.Time                       `json:"last_seen"`
	EnrolledAt                 time.Time                       `json:"enrolled_at"`
	PublicKey                  []byte                          `json:"-"`
	Status                     string                          `json:"status,omitempty"`
	EnrollmentCommandAvailable bool                            `json:"enrollment_command_available,omitempty"`
	// RegionCode is the node's resolved country/region: the owner's manual
	// preference when one is set, otherwise the control plane's GeoIP result.
	// List views render the flag from this instead of one request per node.
	RegionCode string `json:"region_code,omitempty"`
	// Komari carries the cached monthly traffic of a bound node so a list view
	// renders it without one request per card. The node settings endpoint stays
	// the authoritative read.
	Komari *KomariNode `json:"komari,omitempty"`
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
