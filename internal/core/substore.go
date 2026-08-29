package core

import "time"

const (
	SubStoreAddressModeAuto = "auto"
	SubStoreAddressModeIPv4 = "ipv4"
	SubStoreAddressModeIPv6 = "ipv6"
	SubStoreAddressModeBoth = "both"
)

func NormalizeSubStoreAddressMode(value string) (string, bool) {
	switch value {
	case "", SubStoreAddressModeAuto:
		return SubStoreAddressModeAuto, true
	case SubStoreAddressModeIPv4, SubStoreAddressModeIPv6, SubStoreAddressModeBoth:
		return value, true
	default:
		return "", false
	}
}

// SubStoreSyncSettings is the public, credential-free view of the Sub-Store
// integration. EndpointURL is only used inside the control plane and is never
// serialized to the browser.
type SubStoreSyncSettings struct {
	Configured   bool       `json:"configured"`
	EndpointURL  string     `json:"-"`
	EndpointHint string     `json:"endpoint_hint,omitempty"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
}

type SubStoreSyncTarget struct {
	ID               string     `json:"id"`
	DisplayName      string     `json:"display_name"`
	SubscriptionName string     `json:"subscription_name"`
	IntegrationID    string     `json:"-"`
	LastSyncedAt     *time.Time `json:"last_synced_at,omitempty"`
	LastSyncStatus   string     `json:"last_sync_status"`
	LastSyncError    string     `json:"last_sync_error,omitempty"`
	SelectionCount   int        `json:"selection_count"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type SubStoreSyncSelection struct {
	TargetID    string    `json:"target_id,omitempty"`
	AgentID     string    `json:"agent_id"`
	Engine      Engine    `json:"engine"`
	ProfileTag  string    `json:"profile_tag"`
	CustomName  string    `json:"custom_name"`
	AddressMode string    `json:"address_mode"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

func (selection SubStoreSyncSelection) Key() string {
	return selection.AgentID + "\x00" + string(selection.Engine) + "\x00" + selection.ProfileTag
}
