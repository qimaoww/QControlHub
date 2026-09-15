package core

type HeartbeatRequest struct {
	Version      string                  `json:"version,omitempty"`
	OS           string                  `json:"os,omitempty"`
	Arch         string                  `json:"arch,omitempty"`
	Runtime      map[Engine]RuntimeState `json:"runtime,omitempty"`
	Metrics      *HostMetrics            `json:"metrics,omitempty"`
	TrafficUsage []PortTrafficUsage      `json:"traffic_usage,omitempty"`
	Features     []string                `json:"features,omitempty"`
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
