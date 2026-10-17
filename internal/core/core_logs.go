package core

import (
	"time"
)

const (
	MaxCoreLogBatchEntries = 32
	MaxCoreLogMessageBytes = 4096
	// JSON escaping can expand a batch of control-character-heavy messages to
	// nearly six times the raw message size.
	MaxCoreLogWireBytes = 1 << 20
)

// CoreLogEntry is one line emitted by a managed proxy core. AgentID and ID are
// assigned by the control plane, as are OwnerID and NodeOwnerID. Agents submit
// the validated engine/service/instance identity and content.
type CoreLogEntry struct {
	ID          int64     `json:"id,omitempty"`
	AgentID     string    `json:"agent_id,omitempty"`
	InstanceID  string    `json:"instance_id,omitempty"`
	Service     string    `json:"service,omitempty"`
	OwnerID     string    `json:"owner_id,omitempty"`
	NodeOwnerID string    `json:"-"`
	Engine      Engine    `json:"engine"`
	Level       string    `json:"level"`
	Message     string    `json:"message"`
	LoggedAt    time.Time `json:"logged_at"`
	ReceivedAt  time.Time `json:"received_at,omitempty"`
}

type CoreLogBatch struct {
	ID      string         `json:"id"`
	Entries []CoreLogEntry `json:"entries"`
}
