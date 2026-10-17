package core

import "time"

const AgentFeatureCertificates = "panel-certificates-v1"
const PanelCertificatePrefix = "/etc/qcontrolhub/panel-certificates/"

type Certificate struct {
	ID           string     `json:"id"`
	BindingCount int        `json:"binding_count"`
	PendingCount int        `json:"pending_count"`
	OwnerID      string     `json:"owner_id"`
	Name         string     `json:"name"`
	Domains      []string   `json:"domains"`
	Source       string     `json:"source"`
	Method       string     `json:"method,omitempty"`
	CA           string     `json:"ca,omitempty"`
	KeyType      string     `json:"key_type"`
	AccountID    string     `json:"account_id,omitempty"`
	DNSAccountID string     `json:"dns_account_id,omitempty"`
	AgentID      string     `json:"agent_id,omitempty"`
	AutoRenew    bool       `json:"auto_renew"`
	Version      int        `json:"version"`
	Fingerprint  string     `json:"fingerprint,omitempty"`
	NotBefore    *time.Time `json:"not_before,omitempty"`
	NotAfter     *time.Time `json:"not_after,omitempty"`
	Status       string     `json:"status"`
	Error        string     `json:"error,omitempty"`
	Owned        bool       `json:"owned"`
	CreatedAt    time.Time  `json:"created_at"`
	NextAttempt  time.Time  `json:"next_attempt"`
}
type CertificateAccount struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	CA       string `json:"ca,omitempty"`
	Email    string `json:"email,omitempty"`
	Provider string `json:"provider,omitempty"`
	// All credential and account key fields live only in the encrypted payload.
}
type CertificateGrant struct {
	UserID        string `json:"user_id"`
	Username      string `json:"username"`
	CertificateID string `json:"certificate_id"`
}
type CertificateBinding struct {
	CertificateID string `json:"certificate_id"`
	ConfigID      string `json:"config_id"`
	AgentID       string `json:"agent_id"`
	OwnerID       string `json:"owner_id"`
	Engine        Engine `json:"engine"`
	Version       int    `json:"version"`
	TaskID        string `json:"task_id,omitempty"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	Authorized    bool   `json:"authorized"`
}
type CertificateChallenge struct {
	Domain   string `json:"domain"`
	Token    string `json:"token,omitempty"`
	Value    string `json:"value"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	URL      string `json:"-"`
	Status   string `json:"status"`
	RecordID string `json:"-"`
	ZoneID   string `json:"-"`
}
type CertificateOrder struct {
	ID            string                 `json:"id"`
	CertificateID string                 `json:"certificate_id"`
	Status        string                 `json:"status"`
	Stage         string                 `json:"stage"`
	Error         string                 `json:"error,omitempty"`
	Challenges    []CertificateChallenge `json:"challenges"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
	Attempt       int                    `json:"attempt"`
}

// Only the authenticated Agent execution channel receives this model.
type DeploymentCertificate struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
}
type HTTPChallenge struct {
	OrderID   string    `json:"order_id"`
	Domain    string    `json:"domain"`
	Token     string    `json:"token"`
	Value     string    `json:"value"`
	ExpiresAt time.Time `json:"expires_at"`
}
type HTTPChallengeGrant struct {
	Username string `json:"username,omitempty"`
	AgentID  string `json:"agent_id"`
	UserID   string `json:"user_id"`
	Domain   string `json:"domain"`
}
