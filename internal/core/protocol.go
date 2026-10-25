package core

import "time"

type HeartbeatRequest struct {
	Version         string                  `json:"version,omitempty"`
	OS              string                  `json:"os,omitempty"`
	Arch            string                  `json:"arch,omitempty"`
	Runtime         map[Engine]RuntimeState `json:"runtime,omitempty"`
	Metrics         *HostMetrics            `json:"metrics,omitempty"`
	TrafficUsage    []PortTrafficUsage      `json:"traffic_usage,omitempty"`
	SharedInstances []SharedInstanceStatus  `json:"shared_instances,omitempty"`
	Features        []string                `json:"features,omitempty"`
}

type SharedInstanceStatus struct {
	ConfigID      string    `json:"config_id,omitempty"`
	ConfigVersion int       `json:"config_version,omitempty"`
	AppliedTaskID string    `json:"applied_task_id,omitempty"`
	TaskID        string    `json:"task_id,omitempty"`
	ObservedAt    time.Time `json:"observed_at,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	Engine        Engine    `json:"engine"`
	ShareID       string    `json:"share_id"`
	Status        string    `json:"status"`
	CoreLogStatus string    `json:"core_log_status,omitempty"`
	CoreLogError  string    `json:"core_log_error,omitempty"`
	// CoreLogSource and CoreLogLastAt report the shared instance's transport and
	// its last delivered line, so per-instance health is attributable.
	CoreLogSource string     `json:"core_log_source,omitempty"`
	CoreLogLastAt *time.Time `json:"core_log_last_at,omitempty"`
}

const (
	WireHello                     = "hello"
	WireManagedCertificateSources = "managed_certificate_sources"
	WireManagedCertificateReport  = "managed_certificate_report"
	WireCertificateChallenges     = "certificate_challenges"
	WirePublicIPProbe             = "public_ip_probe"
	WireAgentPolicy               = "agent_policy"
	WireHeartbeat                 = "heartbeat"
	WireMetrics                   = "metrics"
	WireTask                      = "task"
	WireResult                    = "result"
	WireResultAck                 = "result_ack"
	WireCoreLogs                  = "core_logs"
	WireCoreLogsAck               = "core_logs_ack"
	WireError                     = "error"
)

type TaskResultEnvelope struct {
	TaskID string            `json:"task_id"`
	Result TaskResultRequest `json:"result"`
}

type WireMessage struct {
	ManagedCertificateSources []ManagedCertificateSource `json:"managed_certificate_sources,omitempty"`
	ManagedCertificateReport  *ManagedCertificateReport  `json:"managed_certificate_report,omitempty"`
	CertificateChallenges     []HTTPChallenge            `json:"certificate_challenges,omitempty"`
	Type                      string                     `json:"type"`
	Heartbeat                 *HeartbeatRequest          `json:"heartbeat,omitempty"`
	Metrics                   *HostMetrics               `json:"metrics,omitempty"`
	TrafficUsage              []PortTrafficUsage         `json:"traffic_usage,omitempty"`
	Task                      *Task                      `json:"task,omitempty"`
	Result                    *TaskResultEnvelope        `json:"result,omitempty"`
	CoreLogs                  *CoreLogBatch              `json:"core_logs,omitempty"`
	TrafficPolicies           []PortTrafficPolicy        `json:"traffic_policies,omitempty"`
	// Preserve verified local enforcement until the initial heartbeat arrives.
	TrafficPoliciesPending bool                 `json:"traffic_policies_pending,omitempty"`
	PublicIPProbe          *PublicIPProbeConfig `json:"public_ip_probe,omitempty"`
	AgentPolicy            *AgentPolicy         `json:"agent_policy,omitempty"`
	TaskID                 string               `json:"task_id,omitempty"`
	BatchID                string               `json:"batch_id,omitempty"`
	Error                  string               `json:"error,omitempty"`
}
