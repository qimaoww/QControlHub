package core

import (
	"math"
	"strings"
	"time"
)

type AgentShareStatus string

const (
	AgentSharePending  AgentShareStatus = "pending"
	AgentShareAccepted AgentShareStatus = "accepted"
	AgentShareRejected AgentShareStatus = "rejected"
)

// AgentShare is a durable per-user allocation. Access requires both Enabled
// and recipient acceptance. Revocation or reinvitation never resets usage.
type AgentShare struct {
	ID                 string           `json:"id"`
	UserID             string           `json:"user_id"`
	Username           string           `json:"username,omitempty"`
	DisplayName        string           `json:"display_name,omitempty"`
	AgentID            string           `json:"agent_id"`
	AgentName          string           `json:"agent_name"`
	OwnerUsername      string           `json:"owner_username,omitempty"`
	Enabled            bool             `json:"enabled"`
	Status             AgentShareStatus `json:"status"`
	InvitationRevision int64            `json:"invitation_revision"`
	Engines            []Engine         `json:"engines"`
	Ports              []int            `json:"ports"`
	LimitBytes         uint64           `json:"limit_bytes"` // Zero is unlimited.
	UsedBytes          uint64           `json:"used_bytes"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
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
	Username   string   `json:"username"`
	Engines    []Engine `json:"engines"`
	LimitBytes uint64   `json:"limit_bytes"`
	Ports      []int    `json:"ports"`
	Enabled    *bool    `json:"enabled,omitempty"`
	Reinvite   bool     `json:"reinvite,omitempty"`
}

type AgentShareRequest struct {
	AgentID    string   `json:"agent_id"`
	Engines    []Engine `json:"engines"`
	LimitBytes uint64   `json:"limit_bytes"`
	Ports      []int    `json:"ports"`
	Enabled    *bool    `json:"enabled,omitempty"`
	Reinvite   bool     `json:"reinvite,omitempty"`
}

type AgentShareResponseRequest struct {
	Revision int64  `json:"revision"`
	Decision string `json:"decision"` // accept or reject
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
	ID            string   `json:"id"`
	LimitBytes    uint64   `json:"limit_bytes"`
	UsedBytes     uint64   `json:"used_bytes"`
	PortUsedBytes uint64   `json:"port_used_bytes"`
	Revoked       bool     `json:"revoked"`
	Engines       []Engine `json:"engines"`
}

const AgentFeatureSharedTraffic = "shared-traffic-v1"
const AgentFeatureSharedEngines = "shared-engines-v1"

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
		quota.PortUsedBytes <= quota.UsedBytes && ValidateEngineCapabilities(quota.Engines) == nil &&
		(quota.Revoked || len(quota.Engines) > 0))
}

func (quota *SharedTrafficQuota) AllowsEngine(engine Engine) bool {
	if quota == nil || quota.Revoked {
		return false
	}
	for _, allowed := range quota.Engines {
		if engine == allowed {
			return true
		}
	}
	return false
}
