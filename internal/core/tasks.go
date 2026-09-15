package core

import (
	"time"
)

func (s TaskStatus) Valid() bool {
	switch s {
	case TaskPending, TaskRunning, TaskSucceeded, TaskFailed, TaskCanceled:
		return true
	default:
		return false
	}
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
	ActionEnableBBR         Action = "enable-bbr"
	ActionDisableBBR        Action = "disable-bbr"
	ActionConfigureTCP      Action = "configure-tcp"
)

func (a Action) Valid() bool {
	switch a {
	case ActionValidate, ActionDeploy, ActionStart, ActionStop, ActionRestart, ActionStatus, ActionInstall, ActionReadConfig, ActionReadManagedConfig, ActionImportExisting, ActionUpgradeAgent, ActionEnableBBR, ActionDisableBBR, ActionConfigureTCP:
		return true
	default:
		return false
	}
}

func (a Action) SystemBBR() bool {
	return a == ActionEnableBBR || a == ActionDisableBBR || a == ActionConfigureTCP
}

func (a Action) AgentLevel() bool { return a == ActionUpgradeAgent || a.SystemBBR() }

type TaskStatus string

// CapabilityTransition exposes the last service action behind each node switch.
type CapabilityTransition struct {
	TaskID  string     `json:"task_id"`
	Status  TaskStatus `json:"status"`
	Enabled bool       `json:"enabled"`
}

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
	TaskCanceled  TaskStatus = "canceled"
)

type Task struct {
	CNIPSource             *CNIPSource            `json:"cnip_source,omitempty"`
	SharedTrafficID        string                 `json:"shared_traffic_id,omitempty"`
	TCPSettings            TCPSettings            `json:"tcp_settings,omitempty"`
	ID                     string                 `json:"id"`
	AgentID                string                 `json:"agent_id"`
	Action                 Action                 `json:"action"`
	Engine                 Engine                 `json:"engine"`
	ConfigID               string                 `json:"config_id,omitempty"`
	ConfigVersion          int                    `json:"config_version,omitempty"`
	ConfigContent          string                 `json:"config_content,omitempty"`
	MainlandAccessPolicies []MainlandAccessPolicy `json:"mainland_access_policies,omitempty"`
	CoreVersion            string                 `json:"core_version,omitempty"`
	CoreSource             string                 `json:"core_source,omitempty"`
	InstallIfMissing       bool                   `json:"install_if_missing,omitempty"`
	Status                 TaskStatus             `json:"status"`
	Attempt                int                    `json:"attempt"`
	LeaseID                string                 `json:"lease_id,omitempty"`
	Output                 string                 `json:"output,omitempty"`
	Error                  string                 `json:"error,omitempty"`
	Reused                 bool                   `json:"reused,omitempty"`
	CreatedAt              time.Time              `json:"created_at"`
	StartedAt              *time.Time             `json:"started_at,omitempty"`
	FinishedAt             *time.Time             `json:"finished_at,omitempty"`
}

type TaskRequest struct {
	// Only the atomic inbound mutation and its retries may request installation.
	InstallIfMissing      bool        `json:"-"`
	ExpectedConfigVersion int         `json:"expected_config_version,omitempty"`
	TCPSettings           TCPSettings `json:"tcp_settings,omitempty"`
	AgentID               string      `json:"agent_id"`
	Action                Action      `json:"action"`
	Engine                Engine      `json:"engine"`
	ConfigID              string      `json:"config_id,omitempty"`
	CoreVersion           string      `json:"core_version,omitempty"`
	CoreSource            string      `json:"core_source,omitempty"`
}

type TaskResultRequest struct {
	LeaseID        string `json:"lease_id"`
	Success        bool   `json:"success"`
	Output         string `json:"output,omitempty"`
	Error          string `json:"error,omitempty"`
	TrafficSettled bool   `json:"traffic_settled,omitempty"`
}
