package core

import "time"

// Only explicitly provided common quota fields are changed by a batch.
type TrafficQuotaPatch struct {
	Cycle       *TrafficCycle `json:"cycle,omitempty"`
	CycleAnchor *time.Time    `json:"cycle_anchor,omitempty"`
	LimitBytes  *uint64       `json:"limit_bytes,omitempty"`
	AutoBlock   *bool         `json:"auto_block,omitempty"`
}

type TrafficBatchRequest struct {
	RequestID string             `json:"request_id"`
	Action    string             `json:"action"`
	IDs       []string           `json:"ids"`
	Quota     *TrafficQuotaPatch `json:"quota,omitempty"`
}

type TrafficBatchResult struct {
	ID      string `json:"id"`
	Status  string `json:"status"` // succeeded, failed, not_executed
	Message string `json:"message,omitempty"`
	AgentID string `json:"-"`
}
