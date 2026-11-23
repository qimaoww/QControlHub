package core

import (
	"errors"
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
	options := PanelSettingOptions()
	if !oneOf(int(policy.HeartbeatIntervalSeconds), options["agent_heartbeat_interval_seconds"]...) {
		return errors.New("unsupported heartbeat interval")
	}
	if !oneOf(int(policy.MetricsIntervalSeconds), options["agent_metrics_interval_seconds"]...) {
		return errors.New("unsupported metrics interval")
	}
	if !oneOf(int(policy.CoreLogMaxMiB), options["agent_core_log_max_mib"]...) {
		return errors.New("unsupported local core log capacity")
	}
	if !oneOf(int(policy.CoreLogRotateCount), options["agent_core_log_rotate_count"]...) {
		return errors.New("unsupported local core log rotation count")
	}
	return nil
}

// AgentPolicy derives node-local policy from its owner's panel settings.
func (settings PanelSettings) AgentPolicy() AgentPolicy {
	return AgentPolicy{
		HeartbeatIntervalSeconds: uint32(settings.AgentHeartbeatIntervalSeconds),
		MetricsIntervalSeconds:   uint32(settings.AgentMetricsIntervalSeconds),
		CoreLogMaxMiB:            uint32(settings.AgentCoreLogMaxMiB),
		CoreLogRotateCount:       uint32(settings.AgentCoreLogRotateCount),
	}
}
