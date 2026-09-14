package core

import (
	"time"
)

type EnrollRequest struct {
	Name         string            `json:"name"`
	Version      string            `json:"version,omitempty"`
	OS           string            `json:"os"`
	Arch         string            `json:"arch"`
	Capabilities []Engine          `json:"capabilities"`
	Features     []string          `json:"features,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	PublicKey    string            `json:"public_key"`
}

type EnrollResponse struct {
	AgentID string `json:"agent_id"`
}

type EnrollmentToken struct {
	ID          string     `json:"id"`
	AgentID     string     `json:"agent_id,omitempty"`
	Name        string     `json:"name"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	MaxUses     int        `json:"max_uses"`
	UsedCount   int        `json:"used_count"`
	Reusable    bool       `json:"reusable,omitempty"`
	Recoverable bool       `json:"command_available,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

type EnrollmentTokenRequest struct {
	Name       string `json:"name"`
	TTLMinutes int    `json:"ttl_minutes"`
	MaxUses    int    `json:"max_uses"`
	Reusable   bool   `json:"reusable,omitempty"`
	// AdminHidden is chosen when the node is added and copied to the Agent on
	// first enrollment. The owner can change it later from the node settings.
	AdminHidden bool `json:"admin_hidden,omitempty"`
}

type EnrollmentTokenCreated struct {
	EnrollmentToken
	Token string `json:"token"`
}
