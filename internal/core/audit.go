package core

import (
	"time"
)

// AuditLogEntry is one administrative action recorded for the audit trail.
type AuditLogEntry struct {
	ID       int64     `json:"id"`
	ActedAt  time.Time `json:"acted_at"`
	Actor    string    `json:"actor"`
	Action   string    `json:"action"`
	Target   string    `json:"target,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	RemoteIP string    `json:"remote_ip,omitempty"`
}
