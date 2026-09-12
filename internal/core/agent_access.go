package core

import (
	"math"
	"strings"
	"time"
)

// AgentShare is a durable per-user allocation. Disabling and re-enabling a
// share never resets its cumulative usage.
type AgentShare struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Username    string    `json:"username,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	AgentID     string    `json:"agent_id"`
	AgentName   string    `json:"agent_name"`
	Enabled     bool      `json:"enabled"`
	Ports       []int     `json:"ports"`
	LimitBytes  uint64    `json:"limit_bytes"` // Zero is unlimited.
	UsedBytes   uint64    `json:"used_bytes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type AgentAccess struct {
	Isolated      bool         `json:"isolated"`
	Shares        []AgentShare `json:"shares"`
	Revision      int64        `json:"revision"`
	OwnedAgentIDs []string     `json:"owned_agent_ids"`
}

type AgentSharing struct {
	Revision int64        `json:"revision"`
	Shares   []AgentShare `json:"shares"`
}

type AgentSharingRequest struct {
	Revision int64                   `json:"revision"`
	Shares   []AgentSharingRecipient `json:"shares"`
}

type AgentSharingRecipient struct {
	Username   string `json:"username"`
	LimitBytes uint64 `json:"limit_bytes"`
	Ports      []int  `json:"ports"`
	Enabled    *bool  `json:"enabled,omitempty"`
}

type AgentShareRequest struct {
	AgentID    string `json:"agent_id"`
	LimitBytes uint64 `json:"limit_bytes"`
	Ports      []int  `json:"ports"`
	Enabled    *bool  `json:"enabled,omitempty"`
}

type AgentAccessRequest struct {
	Isolated bool                `json:"isolated"`
	Shares   []AgentShareRequest `json:"shares"`
	Revision int64               `json:"revision"`
}

// SharedTrafficQuota accompanies every reserved port on the Agent. UsedBytes
// is the server's durable total across all ports; PortUsedBytes acknowledges
// this port's contribution. The Agent adds unreported local increments to the
// total, so the combined allowance is enforced even during a disconnection.
type SharedTrafficQuota struct {
	ID            string `json:"id"`
	LimitBytes    uint64 `json:"limit_bytes"`
	UsedBytes     uint64 `json:"used_bytes"`
	PortUsedBytes uint64 `json:"port_used_bytes"`
	Revoked       bool   `json:"revoked"`
}

const AgentFeatureSharedTraffic = "shared-traffic-v1"

func ValidAgentShareID(id string) bool {
	if len(id) != 20 || !strings.HasPrefix(id, "shr_") {
		return false
	}
	for _, c := range id[4:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (quota *SharedTrafficQuota) Valid() bool {
	return quota == nil || (ValidAgentShareID(quota.ID) &&
		quota.LimitBytes <= math.MaxInt64 && quota.UsedBytes <= math.MaxInt64 &&
		quota.PortUsedBytes <= quota.UsedBytes)
}
