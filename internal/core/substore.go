package core

import "time"

const (
	SubStoreAddressModeAuto     = "auto"
	SubStoreAddressModeIPv4     = "ipv4"
	SubStoreAddressModeIPv6     = "ipv6"
	SubStoreAddressModeBoth     = "both"
	SubStoreSyncModeIncremental = "incremental"
	SubStoreSyncModeManaged     = "managed"
	SubStoreSyncFormatURL       = "url"
	SubStoreSyncFormatMihomo    = "mihomo"
)

func NormalizeSubStoreSyncFormat(value string) (string, bool) {
	switch value {
	case "", SubStoreSyncFormatURL:
		return SubStoreSyncFormatURL, true
	case SubStoreSyncFormatMihomo:
		return value, true
	default:
		return "", false
	}
}

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

func NormalizeSubStoreSyncMode(value string) (string, bool) {
	switch value {
	case SubStoreSyncModeIncremental, SubStoreSyncModeManaged:
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
	OwnerID          string     `json:"owner_id,omitempty"`
	DisplayName      string     `json:"display_name"`
	SubscriptionName string     `json:"subscription_name"`
	IntegrationID    string     `json:"-"`
	SyncMode         string     `json:"sync_mode"`
	SyncFormat       string     `json:"sync_format"`
	LastSyncedAt     *time.Time `json:"last_synced_at,omitempty"`
	LastSyncStatus   string     `json:"last_sync_status"`
	LastSyncError    string     `json:"last_sync_error,omitempty"`
	SelectionCount   int        `json:"selection_count"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type SubStoreSyncSelection struct {
	TargetID    string    `json:"target_id,omitempty"`
	ConfigID    string    `json:"config_id,omitempty"`
	AgentID     string    `json:"agent_id"`
	Engine      Engine    `json:"engine"`
	ProfileTag  string    `json:"profile_tag"`
	CustomName  string    `json:"custom_name"`
	AddressMode string    `json:"address_mode"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

func (selection SubStoreSyncSelection) Key() string {
	return selection.AgentID + "\x00" + string(selection.Engine) + "\x00" + selection.ProfileTag + "\x00" + selection.ConfigID
}
