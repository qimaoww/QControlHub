package core

import "time"

const AgentFeatureManagedProxies = "managed-proxies-v1"

type ProxyStatus struct {
	ObservedAt      time.Time `json:"observed_at,omitzero"`
	NewerInstalled  bool      `json:"newer_installed,omitempty"`
	State           string    `json:"state"`
	Managed         bool      `json:"managed"`
	Installed       string    `json:"installed,omitempty"`
	Running         string    `json:"running,omitempty"`
	Latest          string    `json:"latest,omitempty"`
	CheckedAt       time.Time `json:"checked_at,omitzero"`
	Outcome         string    `json:"outcome,omitempty"`
	CheckRequestAt  time.Time `json:"check_request_at,omitzero"`
	UpdateRequestAt time.Time `json:"update_request_at,omitzero"`
	CheckAttemptAt  time.Time `json:"check_attempt_at,omitzero"`
	CheckError      string    `json:"check_error,omitempty"`
	UpdateStartedAt time.Time `json:"update_started_at,omitzero"`
	UpdateAt        time.Time `json:"update_at,omitzero"`
	UpdateState     string    `json:"update_state,omitempty"`
	UpdateError     string    `json:"update_error,omitempty"`
	Listeners       int       `json:"listeners"`
	SharedInstances int       `json:"shared_instances"`
}

// A node manager may inspect component activity without access to task logs.
type ProxyTaskState struct {
	Status     TaskStatus `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

func (a Action) ProxyComponent() string {
	switch a {
	case ActionCheckNginx, ActionUpdateNginx:
		return "nginx"
	case ActionCheckCaddy, ActionUpdateCaddy:
		return "caddy"
	}
	return ""
}
func (a Action) ChecksProxy() bool { return a == ActionCheckNginx || a == ActionCheckCaddy }
