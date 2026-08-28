package core

import "time"

// SubStoreSyncSettings is the public, credential-free view of the Sub-Store
// integration. EndpointURL is only used inside the control plane and is never
// serialized to the browser.
type SubStoreSyncSettings struct {
	Configured       bool       `json:"configured"`
	EndpointURL      string     `json:"-"`
	EndpointHint     string     `json:"endpoint_hint,omitempty"`
	SubscriptionName string     `json:"subscription_name"`
	IntegrationID    string     `json:"-"`
	LastSyncedAt     *time.Time `json:"last_synced_at,omitempty"`
	LastSyncStatus   string     `json:"last_sync_status"`
	LastSyncError    string     `json:"last_sync_error,omitempty"`
	UpdatedAt        *time.Time `json:"updated_at,omitempty"`
}

type SubStoreSyncSelection struct {
	AgentID    string    `json:"agent_id"`
	Engine     Engine    `json:"engine"`
	ProfileTag string    `json:"profile_tag"`
	CustomName string    `json:"custom_name"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

func (selection SubStoreSyncSelection) Key() string {
	return selection.AgentID + "\x00" + string(selection.Engine) + "\x00" + selection.ProfileTag
}
