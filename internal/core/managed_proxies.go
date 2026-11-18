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
	Listeners       int       `json:"listeners"`
	SharedInstances int       `json:"shared_instances"`
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
