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
// assigned by the control plane; Agents only submit the remaining fields.
type CoreLogEntry struct {
	ID         int64     `json:"id,omitempty"`
	AgentID    string    `json:"agent_id,omitempty"`
	Engine     Engine    `json:"engine"`
	Level      string    `json:"level"`
	Message    string    `json:"message"`
	LoggedAt   time.Time `json:"logged_at"`
	ReceivedAt time.Time `json:"received_at,omitempty"`
}

type CoreLogBatch struct {
	ID      string         `json:"id"`
	Entries []CoreLogEntry `json:"entries"`
}
