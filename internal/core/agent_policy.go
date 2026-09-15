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
