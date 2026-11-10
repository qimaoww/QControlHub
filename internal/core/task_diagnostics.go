package core

import "time"

// Limits apply to acquisition and transfer, never to the short list summary.
const MaxTaskDiagnosticBytes = 8 << 20
const TaskDiagnosticChunkBytes = 32 << 10
const MaxEncodedTaskDiagnosticBytes = MaxTaskDiagnosticBytes*6 + 4096
const MaxTaskDiagnosticChunks = MaxEncodedTaskDiagnosticBytes/TaskDiagnosticChunkBytes + 1

const ActionPanelFailure Action = "panel-failure"

type PanelFailure struct {
	Resource   string    `json:"resource,omitempty"`
	Operation  string    `json:"operation"`
	Stage      string    `json:"stage"`
	Summary    string    `json:"summary"`
	Detail     string    `json:"detail"`
	RequestID  string    `json:"request_id,omitempty"`
	HTTPStatus int       `json:"http_status,omitempty"`
	Code       string    `json:"code,omitempty"`
	Field      string    `json:"field,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
	AgentID    string    `json:"agent_id,omitempty"`
	Engine     Engine    `json:"engine,omitempty"`
	InstanceID string    `json:"instance_id,omitempty"`
}

type TaskDiagnosticChunk struct {
	LeaseID string `json:"lease_id"`
	Index   int    `json:"index"`
	Count   int    `json:"count"`
	Content string `json:"content"`
	Digest  string `json:"digest"`
}

type DiagnosticAttempt struct {
	ExecutedAttempt int    `json:"executed_attempt,omitempty"`
	Attempt         int    `json:"attempt"`
	Output          string `json:"output,omitempty"`
	Error           string `json:"error,omitempty"`
	Source          string `json:"source"`
}

type TaskDiagnostic struct {
	Attempts      []DiagnosticAttempt `json:"attempts,omitempty"`
	Source        string              `json:"source,omitempty"`
	Occurrences   int                 `json:"occurrences,omitempty"`
	LastFailureAt *time.Time          `json:"last_failure_at,omitempty"`
	RetryOf       string              `json:"retry_of,omitempty"`
	Output        string              `json:"output"`
	Error         string              `json:"error"`
}
