package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

const subStoreResponseLimit = 1 << 20

type subStoreSyncAddress struct {
	Address string `json:"address"`
	Source  string `json:"source"`
	Family  string `json:"family"`
	URI     string `json:"-"`
	Mihomo  string `json:"-"`
}

type subStoreSyncProfile struct {
	AgentID     string                `json:"agent_id"`
	ConfigID    string                `json:"config_id"`
	AgentName   string                `json:"agent_name"`
	AgentStatus string                `json:"agent_status"`
	Engine      core.Engine           `json:"engine"`
	ProfileTag  string                `json:"profile_tag"`
	Protocol    string                `json:"protocol"`
	Port        int                   `json:"port"`
	DefaultName string                `json:"default_name"`
	CustomName  string                `json:"custom_name,omitempty"`
	AddressMode string                `json:"address_mode"`
	Addresses   []subStoreSyncAddress `json:"addresses"`
	Selected    bool                  `json:"selected"`
	Available   bool                  `json:"available"`
	URI         string                `json:"-"`
	Mihomo      string                `json:"-"`
	MihomoError string                `json:"mihomo_error,omitempty"`
}

func (profile subStoreSyncProfile) key() string {
	return profile.AgentID + "\x00" + string(profile.Engine) + "\x00" + profile.ProfileTag + "\x00" + profile.ConfigID
}

type subStoreSyncResource struct {
	Settings   core.SubStoreSyncSettings    `json:"settings"`
	Targets    []core.SubStoreSyncTarget    `json:"targets"`
	TargetID   string                       `json:"target_id,omitempty"`
	Profiles   []subStoreSyncProfile        `json:"profiles"`
	Selections []core.SubStoreSyncSelection `json:"selections"`
}

type subStoreSettingsRequest struct {
	EndpointURL string `json:"endpoint_url"`
}

type subStoreSelectionsRequest struct {
	TargetID   string                       `json:"target_id"`
	Selections []core.SubStoreSyncSelection `json:"selections"`
}

type subStoreTargetRequest struct {
	DisplayName      string `json:"display_name"`
	SubscriptionName string `json:"subscription_name"`
	SyncMode         string `json:"sync_mode"`
	SyncFormat       string `json:"sync_format"`
	RenameRemote     bool   `json:"rename_remote"`
}

type subStoreRunRequest struct {
	TargetID string `json:"target_id"`
}

type subStoreSyncResult struct {
	SubscriptionName string    `json:"subscription_name"`
	NodeCount        int       `json:"node_count"`
	Created          bool      `json:"created"`
	SyncedAt         time.Time `json:"synced_at"`
}

type subStoreImportTargetRequest struct {
	SubscriptionName string `json:"subscription_name"`
	DisplayName      string `json:"display_name"`
	SyncFormat       string `json:"sync_format"`
}

type subStoreRemoteTarget struct {
	SubscriptionName string `json:"subscription_name"`
	NodeCount        int    `json:"node_count"`
	Imported         bool   `json:"imported"`
}

type subStoreEnvelope struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Error  struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details string `json:"details"`
	} `json:"error"`
}

func (s *Server) getSubStoreSync(w http.ResponseWriter, request *http.Request) {
	resource, err := s.subStoreSyncResource(request.Context(), request.URL.Query().Get("target_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resource)
}

func (s *Server) subStoreMutation(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, request *http.Request) {
		release, err := s.store.TryLockSubStoreOperation(request.Context())
		if err != nil {
			writeStoreError(w, err)
			return
		}
		defer release()
		next(w, request)
	}
}

func (s *Server) subStoreSyncResource(ctx context.Context, targetID string) (subStoreSyncResource, error) {
	settings, err := s.store.SubStoreSyncSettings(ctx)
	if err != nil {
		return subStoreSyncResource{}, err
	}
	if settings.Configured {
		settings.EndpointHint = subStoreEndpointHint(settings.EndpointURL)
	}
	targets, err := s.store.ListSubStoreSyncTargets(ctx)
	if err != nil {
		return subStoreSyncResource{}, err
	}
	targetID = strings.TrimSpace(targetID)
	if targetID == "" && len(targets) > 0 {
		targetID = targets[0].ID
	}
	if targetID != "" {
		found := false
		for _, target := range targets {
			if target.ID == targetID {
				found = true
				break
			}
		}
		if !found {
			return subStoreSyncResource{}, store.ErrNotFound
		}
	}
	selections := make([]core.SubStoreSyncSelection, 0)
	if targetID != "" {
		selections, err = s.store.ListSubStoreSyncSelections(ctx, targetID)
		if err != nil {
			return subStoreSyncResource{}, err
		}
	}
	profiles, err := s.availableSubStoreProfiles(ctx, selections)
	if err != nil {
		return subStoreSyncResource{}, err
	}
	return subStoreSyncResource{Settings: settings, Targets: targets, TargetID: targetID, Profiles: profiles, Selections: selections}, nil
}
