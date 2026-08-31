package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"gopkg.in/yaml.v3"
)

const subStoreResponseLimit = 1 << 20

type subStoreSyncAddress struct {
	Address string `json:"address"`
	Source  string `json:"source"`
	Family  string `json:"family"`
	URI     string `json:"-"`
}

type subStoreSyncProfile struct {
	AgentID     string                `json:"agent_id"`
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
}

func (profile subStoreSyncProfile) key() string {
	return profile.AgentID + "\x00" + string(profile.Engine) + "\x00" + profile.ProfileTag
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

func newSubStoreHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: 12 * time.Second,
		Transport: &http.Transport{
			// The backend path is a credential. Never expose it to an ambient
			// HTTP proxy configured for the control-plane process.
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 8 * time.Second,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("Sub-Store redirects are not allowed")
		},
	}
}

func normalizeSubStoreEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || utf8.RuneCountInString(raw) > 1000 {
		return "", errors.New("Sub-Store 地址不能为空且不能超过 1000 个字符")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("Sub-Store 地址必须是包含路径口令的 HTTP(S) 绝对地址")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("Sub-Store 地址不能包含用户信息、查询参数或片段")
	}
	if strings.EqualFold(parsed.Hostname(), "sub.store") {
		return "", errors.New("sub.store 不是官方公网后端域名，请填写自建 Sub-Store 地址")
	}
	cleanPath := path.Clean(parsed.EscapedPath())
	if cleanPath == "." || cleanPath == "/" || strings.Contains(cleanPath, "%") {
		return "", errors.New("Sub-Store 地址必须包含未编码的后端路径口令")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(cleanPath, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("Sub-Store 后端路径口令无效")
		}
		for _, character := range segment {
			if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
				(character >= '0' && character <= '9') || strings.ContainsRune("-._~", character)) {
				return "", errors.New("Sub-Store 后端路径口令只能使用字母、数字、-、_、. 或 ~")
			}
		}
	}
	parsed.Path = cleanPath
	parsed.RawPath = ""
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func subStoreEndpointHint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return "已保护保存"
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	masked := make([]string, 0, len(segments))
	for range segments {
		masked = append(masked, "••••••")
	}
	return parsed.Scheme + "://" + parsed.Host + "/" + strings.Join(masked, "/")
}

func (s *Server) getSubStoreSync(w http.ResponseWriter, request *http.Request) {
	resource, err := s.subStoreSyncResource(request.Context(), request.URL.Query().Get("target_id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resource)
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

func (s *Server) availableSubStoreProfiles(ctx context.Context, selections []core.SubStoreSyncSelection) ([]subStoreSyncProfile, error) {
	entries, err := s.clientAccessEntries(ctx)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]core.SubStoreSyncSelection, len(selections))
	for _, selection := range selections {
		selected[selection.Key()] = selection
	}
	profiles := make([]subStoreSyncProfile, 0)
	available := make(map[string]struct{})
	for _, entry := range entries {
		displayName := entry.AgentName
		if strings.TrimSpace(entry.ClientName) != "" {
			displayName = entry.ClientName
		}
		for _, item := range entry.Profiles {
			if !item.Profile.SubscriptionCompatible {
				continue
			}
			profile := subStoreSyncProfile{
				AgentID: entry.AgentID, AgentName: displayName, AgentStatus: entry.AgentStatus, Engine: entry.Engine,
				ProfileTag: item.Tag, Protocol: item.Protocol, Port: item.Port, URI: item.Profile.URI, Available: true,
				AddressMode: core.SubStoreAddressModeAuto,
				DefaultName: strings.TrimSpace(displayName + " · " + item.Tag),
			}
			for _, option := range entry.AddressOptions {
				for _, candidate := range option.Profiles {
					if candidate.Tag == item.Tag {
						profile.Addresses = append(profile.Addresses, subStoreSyncAddress{
							Address: option.Address, Source: option.Source, Family: option.Family, URI: candidate.Profile.URI,
						})
						break
					}
				}
			}
			if len(profile.Addresses) == 0 {
				profile.Addresses = []subStoreSyncAddress{{
					Address: entry.Address, Source: entry.Source, Family: clientAddressFamily(entry.Address), URI: item.Profile.URI,
				}}
			}
			if selection, ok := selected[profile.key()]; ok {
				profile.Selected = true
				profile.CustomName = selection.CustomName
				profile.AddressMode, _ = core.NormalizeSubStoreAddressMode(selection.AddressMode)
			}
			available[profile.key()] = struct{}{}
			profiles = append(profiles, profile)
		}
	}
	for _, selection := range selections {
		if _, ok := available[selection.Key()]; ok {
			continue
		}
		profiles = append(profiles, subStoreSyncProfile{
			AgentID: selection.AgentID, Engine: selection.Engine, ProfileTag: selection.ProfileTag,
			DefaultName: selection.CustomName, CustomName: selection.CustomName, AddressMode: selection.AddressMode,
			Addresses: []subStoreSyncAddress{}, Selected: true, Available: false,
		})
	}
	sort.SliceStable(profiles, func(left, right int) bool {
		if profiles[left].AgentName != profiles[right].AgentName {
			return profiles[left].AgentName < profiles[right].AgentName
		}
		if profiles[left].Engine != profiles[right].Engine {
			return profiles[left].Engine < profiles[right].Engine
		}
		return profiles[left].ProfileTag < profiles[right].ProfileTag
	})
	return profiles, nil
}

func (s *Server) putSubStoreSettings(w http.ResponseWriter, request *http.Request) {
	var input subStoreSettingsRequest
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	current, err := s.store.SubStoreSyncSettings(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	endpoint := strings.TrimSpace(input.EndpointURL)
	if endpoint == "" && current.Configured {
		endpoint = current.EndpointURL
	}
	endpoint, err = normalizeSubStoreEndpoint(endpoint)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	settings, err := s.store.SaveSubStoreSyncSettings(request.Context(), endpoint)
	if err != nil {
		if errors.Is(err, store.ErrSecretUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "请先配置 QCH_CONFIG_ENCRYPTION_KEY，再保存 Sub-Store 地址")
			return
		}
		writeStoreError(w, err)
		return
	}
	settings.EndpointHint = subStoreEndpointHint(settings.EndpointURL)
	s.recordAudit(request, "substore.settings.updated", "Sub-Store", "Sub-Store connection settings updated")
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) createSubStoreTarget(w http.ResponseWriter, request *http.Request) {
	var input subStoreTargetRequest
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(input.DisplayName)
	if name == "" {
		name = input.SubscriptionName
	}
	mode := strings.TrimSpace(input.SyncMode)
	if mode == "" {
		mode = core.SubStoreSyncModeIncremental
	}
	target, err := s.store.CreateSubStoreSyncTarget(request.Context(), name, mode)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "substore.target.created", target.SubscriptionName, "Sub-Store sync target created")
	writeJSON(w, http.StatusCreated, target)
}

func (s *Server) updateSubStoreTarget(w http.ResponseWriter, request *http.Request) {
	var input subStoreTargetRequest
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, err := s.store.SubStoreSyncTarget(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = input.SubscriptionName
	}
	subscriptionName := target.SubscriptionName
	mode := strings.TrimSpace(input.SyncMode)
	if mode == "" {
		mode = target.SyncMode
	}
	if input.RenameRemote {
		subscriptionName = displayName
		settings, settingsErr := s.store.SubStoreSyncSettings(request.Context())
		if settingsErr != nil {
			writeStoreError(w, settingsErr)
			return
		}
		if !settings.Configured {
			writeError(w, http.StatusBadRequest, "Sub-Store 尚未配置，无法同时修改远端组名")
			return
		}
	}
	updated, err := s.store.UpdateSubStoreSyncTarget(request.Context(), target.ID, displayName, subscriptionName, mode)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if input.RenameRemote && subscriptionName != target.SubscriptionName {
		settings, settingsErr := s.store.SubStoreSyncSettings(request.Context())
		if settingsErr == nil {
			_, settingsErr = s.renameSubStoreSubscription(request.Context(), settings, target, subscriptionName)
		}
		if settingsErr != nil {
			if _, rollbackErr := s.store.UpdateSubStoreSyncTarget(request.Context(), target.ID, target.DisplayName, target.SubscriptionName, target.SyncMode); rollbackErr != nil {
				writeInternalError(w, fmt.Errorf("rename Sub-Store group: %v; roll back local target: %w", settingsErr, rollbackErr))
				return
			}
			writeError(w, http.StatusBadGateway, settingsErr.Error())
			return
		}
	}
	s.recordAudit(request, "substore.target.updated", updated.DisplayName, "Sub-Store sync target renamed")
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) listSubStoreRemoteTargets(w http.ResponseWriter, request *http.Request) {
	settings, err := s.store.SubStoreSyncSettings(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !settings.Configured {
		writeError(w, http.StatusBadRequest, "请先保存 Sub-Store 连接设置")
		return
	}
	var subscriptions []map[string]any
	if _, err := s.subStoreRequest(request.Context(), settings.EndpointURL, http.MethodGet, "/api/subs", nil, &subscriptions); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	targets, err := s.store.ListSubStoreSyncTargets(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	importedNames := make(map[string]struct{}, len(targets))
	importedOwners := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		importedNames[target.SubscriptionName] = struct{}{}
		importedOwners[target.IntegrationID] = struct{}{}
	}
	result := make([]subStoreRemoteTarget, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		name, _ := subscription["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		owner, _ := subscription["qcontrolhub_integration_id"].(string)
		_, importedByName := importedNames[name]
		_, importedByOwner := importedOwners[owner]
		content, _ := subscription["content"].(string)
		result = append(result, subStoreRemoteTarget{
			SubscriptionName: name,
			NodeCount:        subStoreContentNodeCount(content),
			Imported:         importedByName || (owner != "" && importedByOwner),
		})
	}
	sort.SliceStable(result, func(left, right int) bool { return result[left].SubscriptionName < result[right].SubscriptionName })
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) importSubStoreRemoteTarget(w http.ResponseWriter, request *http.Request) {
	var input subStoreImportTargetRequest
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	settings, err := s.store.SubStoreSyncSettings(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !settings.Configured {
		writeError(w, http.StatusBadRequest, "请先保存 Sub-Store 连接设置")
		return
	}
	name := strings.TrimSpace(input.SubscriptionName)
	if _, err := validateSubStoreImportName(name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = name
	}
	if _, err := validateSubStoreImportName(displayName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if displayName == "." || displayName == ".." {
		writeError(w, http.StatusBadRequest, "同步组名称无效")
		return
	}
	var subscriptions []map[string]any
	if _, err := s.subStoreRequest(request.Context(), settings.EndpointURL, http.MethodGet, "/api/subs", nil, &subscriptions); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	var remote map[string]any
	for _, subscription := range subscriptions {
		remoteName, _ := subscription["name"].(string)
		if remoteName == name {
			remote = subscription
			break
		}
	}
	if remote == nil {
		writeError(w, http.StatusNotFound, "Sub-Store 中没有找到该订阅组")
		return
	}
	integrationID, _ := remote["qcontrolhub_integration_id"].(string)
	integrationID = strings.TrimSpace(integrationID)
	originalIntegrationID := integrationID
	// A newly created local target must own the remote group with a fresh
	// identity. Reusing an identity left by another control plane would make the
	// two panels race on the same group and would also trust stale managed-node
	// metadata from that panel.
	claimRemote := true
	if claimRemote {
		integrationID, err = core.NewID("ssi")
		if err != nil {
			writeInternalError(w, err)
			return
		}
	}
	target, err := s.store.ImportSubStoreSyncTarget(request.Context(), name, integrationID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if claimRemote {
		payload := make(map[string]any, len(remote)+1)
		for key, value := range remote {
			payload[key] = value
		}
		payload["qcontrolhub_integration_id"] = integrationID
		payload["qcontrolhub_managed_nodes"] = []any{}
		if _, patchErr := s.subStoreRequest(request.Context(), settings.EndpointURL, http.MethodPatch, "/api/sub/"+name, payload, nil); patchErr != nil {
			_ = s.store.DeleteSubStoreSyncTarget(request.Context(), target.ID)
			writeError(w, http.StatusBadGateway, patchErr.Error())
			return
		}
	}
	if displayName != target.DisplayName {
		targetID := target.ID
		target, err = s.store.UpdateSubStoreSyncTarget(request.Context(), targetID, displayName, target.SubscriptionName, target.SyncMode)
		if err != nil {
			if claimRemote {
				rollback := cloneSubStoreSubscription(remote)
				rollback["qcontrolhub_integration_id"] = originalIntegrationID
				if _, restoreErr := s.subStoreRequest(request.Context(), settings.EndpointURL, http.MethodPatch, "/api/sub/"+name, rollback, nil); restoreErr != nil {
					writeInternalError(w, fmt.Errorf("save local Sub-Store target name: %v; restore ownership: %w", err, restoreErr))
					return
				}
			}
			_ = s.store.DeleteSubStoreSyncTarget(request.Context(), targetID)
			writeStoreError(w, err)
			return
		}
	}
	s.recordAudit(request, "substore.target.imported", target.DisplayName, "Existing Sub-Store group added to sync targets")
	writeJSON(w, http.StatusCreated, target)
}

func (s *Server) linkSubStoreRemoteTarget(w http.ResponseWriter, request *http.Request) {
	var input subStoreImportTargetRequest
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, err := s.store.SubStoreSyncTarget(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	settings, err := s.store.SubStoreSyncSettings(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !settings.Configured {
		writeError(w, http.StatusBadRequest, "请先保存 Sub-Store 连接设置")
		return
	}
	name, err := validateSubStoreImportName(input.SubscriptionName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = target.DisplayName
	}
	if _, err := validateSubStoreImportName(displayName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if displayName == "." || displayName == ".." {
		writeError(w, http.StatusBadRequest, "同步组名称无效")
		return
	}
	var subscriptions []map[string]any
	if _, err := s.subStoreRequest(request.Context(), settings.EndpointURL, http.MethodGet, "/api/subs", nil, &subscriptions); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	var remote map[string]any
	for _, subscription := range subscriptions {
		remoteName, _ := subscription["name"].(string)
		if strings.TrimSpace(remoteName) == name {
			remote = subscription
			break
		}
	}
	if remote == nil {
		writeError(w, http.StatusNotFound, "Sub-Store 中没有找到该订阅组")
		return
	}
	localTargets, err := s.store.ListSubStoreSyncTargets(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	remoteOwner, _ := remote["qcontrolhub_integration_id"].(string)
	remoteOwner = strings.TrimSpace(remoteOwner)
	for _, existing := range localTargets {
		if existing.ID == target.ID {
			continue
		}
		if existing.SubscriptionName == name {
			writeError(w, http.StatusConflict, "该 Sub-Store 组已关联其他同步组")
			return
		}
		if remoteOwner != "" && existing.IntegrationID == remoteOwner {
			writeError(w, http.StatusConflict, "该 Sub-Store 组已关联其他同步组")
			return
		}
	}
	if name == target.SubscriptionName && remoteOwner == target.IntegrationID {
		if displayName != target.DisplayName {
			updated, updateErr := s.store.UpdateSubStoreSyncTarget(request.Context(), target.ID, displayName, target.SubscriptionName, target.SyncMode)
			if updateErr != nil {
				writeStoreError(w, updateErr)
				return
			}
			target = updated
		}
		writeJSON(w, http.StatusOK, target)
		return
	}
	// Selecting an existing remote group is an explicit relink operation. A group
	// may still carry the integration identity of a previous control plane, so
	// claim it for this target instead of creating a duplicate local subscription.
	// The conflict check above still prevents two local targets from claiming the
	// same remote identity.
	integrationID := target.IntegrationID
	needsClaim := remoteOwner != integrationID
	type ownershipChange struct {
		name   string
		before map[string]any
	}
	changes := make([]ownershipChange, 0)
	for _, subscription := range subscriptions {
		oldName, _ := subscription["name"].(string)
		owner, _ := subscription["qcontrolhub_integration_id"].(string)
		if strings.TrimSpace(oldName) != "" && strings.TrimSpace(oldName) != name && strings.TrimSpace(owner) == target.IntegrationID {
			changes = append(changes, ownershipChange{name: oldName, before: cloneSubStoreSubscription(subscription)})
		}
	}
	restoreOwnership := func() error {
		var restoreErr error
		for _, change := range changes {
			if _, err := s.subStoreRequest(request.Context(), settings.EndpointURL, http.MethodPatch, "/api/sub/"+change.name, change.before, nil); err != nil && restoreErr == nil {
				restoreErr = err
			}
		}
		if needsClaim {
			if _, err := s.subStoreRequest(request.Context(), settings.EndpointURL, http.MethodPatch, "/api/sub/"+name, remote, nil); err != nil && restoreErr == nil {
				restoreErr = err
			}
		}
		return restoreErr
	}
	for _, change := range changes {
		released := cloneSubStoreSubscription(change.before)
		released["qcontrolhub_integration_id"] = ""
		released["qcontrolhub_managed_nodes"] = []any{}
		if _, err := s.subStoreRequest(request.Context(), settings.EndpointURL, http.MethodPatch, "/api/sub/"+change.name, released, nil); err != nil {
			if restoreErr := restoreOwnership(); restoreErr != nil {
				writeInternalError(w, fmt.Errorf("release old Sub-Store group: %v; restore ownership: %w", err, restoreErr))
				return
			}
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	if needsClaim {
		claimed := cloneSubStoreSubscription(remote)
		claimed["qcontrolhub_integration_id"] = integrationID
		// Ownership changed from another panel; its managed-node list is not
		// authoritative for this target. The next sync will seed this panel's
		// list without deleting the existing remote content.
		claimed["qcontrolhub_managed_nodes"] = []any{}
		if _, err := s.subStoreRequest(request.Context(), settings.EndpointURL, http.MethodPatch, "/api/sub/"+name, claimed, nil); err != nil {
			if restoreErr := restoreOwnership(); restoreErr != nil {
				writeInternalError(w, fmt.Errorf("claim Sub-Store group: %v; restore ownership: %w", err, restoreErr))
				return
			}
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	updated, updateErr := s.store.UpdateSubStoreSyncTarget(request.Context(), target.ID, displayName, name, target.SyncMode)
	if updateErr != nil {
		if restoreErr := restoreOwnership(); restoreErr != nil {
			writeInternalError(w, fmt.Errorf("link Sub-Store group: %v; restore ownership: %w", updateErr, restoreErr))
			return
		}
		writeStoreError(w, updateErr)
		return
	}
	s.recordAudit(request, "substore.target.linked", updated.DisplayName, "Existing Sub-Store group linked to sync target")
	writeJSON(w, http.StatusOK, updated)
}

func subStoreContentNodeCount(content string) int {
	count := 0
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func validateSubStoreImportName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 100 || strings.ContainsAny(name, "/\\?#\r\n") {
		return "", errors.New("Sub-Store 订阅组名称无效")
	}
	return name, nil
}

func (s *Server) deleteSubStoreTarget(w http.ResponseWriter, request *http.Request) {
	target, err := s.store.SubStoreSyncTarget(request.Context(), request.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.store.DeleteSubStoreSyncTarget(request.Context(), target.ID); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "substore.target.deleted", target.DisplayName, "Sub-Store sync target removed locally; remote group retained")
	w.WriteHeader(http.StatusNoContent)
}

func ownedSubStoreSubscription(subscriptions []map[string]any, integrationID string) map[string]any {
	for _, subscription := range subscriptions {
		owner, _ := subscription["qcontrolhub_integration_id"].(string)
		if owner == integrationID {
			return subscription
		}
	}
	return nil
}

// cloneSubStoreSubscription keeps fields that are owned by Sub-Store (for
// example custom remarks, filters, and processing options) when we update only
// the content and QControlHub ownership metadata. The API returns maps whose
// values are not mutated after this point, so a shallow copy is sufficient.
func cloneSubStoreSubscription(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func subStoreSubscriptionUpdate(existing, desired map[string]any) map[string]any {
	updated := cloneSubStoreSubscription(existing)
	for key, value := range desired {
		updated[key] = value
	}
	// These options are intentionally user-owned in Sub-Store. Keep them when
	// the control plane refreshes node content, while still updating the name,
	// source, content, and QControlHub ownership fields above.
	for _, key := range []string{"displayName", "display-name", "noFlow", "remark", "process"} {
		if value, ok := existing[key]; ok {
			updated[key] = value
		}
	}
	return updated
}

func subStoreSubscriptionPayload(target core.SubStoreSyncTarget, content string) map[string]any {
	managedNames, _ := subStoreNodeNames(content)
	return map[string]any{
		"name":                       target.SubscriptionName,
		"displayName":                target.SubscriptionName,
		"display-name":               target.SubscriptionName,
		"source":                     "local",
		"content":                    content,
		"noFlow":                     true,
		"remark":                     "Managed by QControlHub",
		"process":                    []any{},
		"qcontrolhub_integration_id": target.IntegrationID,
		"qcontrolhub_managed_nodes":  managedNames,
	}
}

func (s *Server) renameSubStoreSubscription(ctx context.Context, settings core.SubStoreSyncSettings, target core.SubStoreSyncTarget, name string) (bool, error) {
	var subscriptions []map[string]any
	if _, err := s.subStoreRequest(ctx, settings.EndpointURL, http.MethodGet, "/api/subs", nil, &subscriptions); err != nil {
		return false, err
	}
	owned := ownedSubStoreSubscription(subscriptions, target.IntegrationID)
	if owned == nil {
		return false, nil
	}
	for _, subscription := range subscriptions {
		existingName, _ := subscription["name"].(string)
		owner, _ := subscription["qcontrolhub_integration_id"].(string)
		if existingName == name && owner != target.IntegrationID {
			return false, errors.New("Sub-Store 中已存在同名订阅，请更换同步组名称")
		}
	}
	existingName, _ := owned["name"].(string)
	if existingName == "" {
		return false, errors.New("Sub-Store 返回的订阅缺少名称")
	}
	payload := make(map[string]any, len(owned))
	for key, value := range owned {
		payload[key] = value
	}
	payload["name"] = name
	payload["displayName"] = name
	payload["display-name"] = name
	payload["qcontrolhub_integration_id"] = target.IntegrationID
	_, err := s.subStoreRequest(ctx, settings.EndpointURL, http.MethodPatch, "/api/sub/"+existingName, payload, nil)
	return err == nil, err
}

func (s *Server) putSubStoreSelections(w http.ResponseWriter, request *http.Request) {
	var input subStoreSelectionsRequest
	if err := decodeJSON(w, request, &input, 128<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	profiles, err := s.availableSubStoreProfiles(request.Context(), nil)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	available := make(map[string]subStoreSyncProfile, len(profiles))
	for _, profile := range profiles {
		available[profile.key()] = profile
	}
	for index := range input.Selections {
		profile, ok := available[input.Selections[index].Key()]
		if !ok || !profile.Available {
			writeError(w, http.StatusBadRequest, "选择中包含已不可用的客户端节点，请刷新后重试")
			return
		}
		if strings.TrimSpace(input.Selections[index].CustomName) == "" {
			input.Selections[index].CustomName = profile.DefaultName
		}
		mode, valid := core.NormalizeSubStoreAddressMode(strings.TrimSpace(input.Selections[index].AddressMode))
		if !valid || !subStoreProfileSupportsMode(profile, mode) {
			writeError(w, http.StatusBadRequest, "所选客户端节点不支持该 IP 地址模式，请刷新后重试")
			return
		}
		input.Selections[index].AddressMode = mode
	}
	selections, err := s.store.ReplaceSubStoreSyncSelections(request.Context(), input.TargetID, input.Selections)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "substore.nodes.updated", input.TargetID, fmt.Sprintf("selected node count: %d", len(selections)))
	writeJSON(w, http.StatusOK, selections)
}

func (s *Server) testSubStoreConnection(w http.ResponseWriter, request *http.Request) {
	settings, err := s.store.SubStoreSyncSettings(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !settings.Configured {
		writeError(w, http.StatusBadRequest, "请先保存 Sub-Store 连接设置")
		return
	}
	var environment map[string]any
	if _, err := s.subStoreRequest(request.Context(), settings.EndpointURL, http.MethodGet, "/api/utils/env", nil, &environment); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "endpoint_hint": subStoreEndpointHint(settings.EndpointURL)})
}

func (s *Server) runSubStoreSync(w http.ResponseWriter, request *http.Request) {
	var input subStoreRunRequest
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	settings, err := s.store.SubStoreSyncSettings(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !settings.Configured {
		writeError(w, http.StatusBadRequest, "请先保存 Sub-Store 连接设置")
		return
	}
	target, err := s.store.SubStoreSyncTarget(request.Context(), input.TargetID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	selections, err := s.store.ListSubStoreSyncSelections(request.Context(), target.ID)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if len(selections) == 0 && target.LastSyncedAt == nil {
		writeError(w, http.StatusBadRequest, "请至少选择一个客户端节点")
		return
	}
	profiles, err := s.availableSubStoreProfiles(request.Context(), selections)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	byKey := make(map[string]subStoreSyncProfile, len(profiles))
	for _, profile := range profiles {
		if profile.Available {
			byKey[profile.key()] = profile
		}
	}
	content := make([]string, 0, len(selections))
	for _, selection := range selections {
		profile, ok := byKey[selection.Key()]
		if !ok {
			err = errors.New("已选客户端节点发生变化，请删除失效项或重新选择后再同步")
			_ = s.store.RecordSubStoreSyncResult(request.Context(), target.ID, err)
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		nodes, selectionErr := subStoreNodesForSelection(profile, selection)
		if selectionErr != nil {
			err = fmt.Errorf("客户端节点 %s 无法同步: %w", selection.CustomName, selectionErr)
			_ = s.store.RecordSubStoreSyncResult(request.Context(), target.ID, err)
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		content = append(content, nodes...)
	}
	created, syncErr := s.upsertSubStoreSubscription(request.Context(), settings, target, strings.Join(content, "\n"))
	if recordErr := s.store.RecordSubStoreSyncResult(request.Context(), target.ID, syncErr); recordErr != nil && syncErr == nil {
		syncErr = recordErr
	}
	if syncErr != nil {
		writeError(w, http.StatusBadGateway, syncErr.Error())
		return
	}
	result := subStoreSyncResult{SubscriptionName: target.SubscriptionName, NodeCount: len(content), Created: created, SyncedAt: time.Now().UTC()}
	s.recordAudit(request, "substore.synced", target.SubscriptionName, fmt.Sprintf("node count: %d", len(content)))
	writeJSON(w, http.StatusOK, result)
}

func subStoreProfileAddress(profile subStoreSyncProfile, family string) (subStoreSyncAddress, bool) {
	for _, address := range profile.Addresses {
		if address.Family == family {
			return address, true
		}
	}
	return subStoreSyncAddress{}, false
}

func subStoreProfileSupportsMode(profile subStoreSyncProfile, mode string) bool {
	switch mode {
	case core.SubStoreAddressModeAuto:
		return strings.TrimSpace(profile.URI) != ""
	case core.SubStoreAddressModeIPv4, core.SubStoreAddressModeIPv6:
		_, available := subStoreProfileAddress(profile, mode)
		return available
	case core.SubStoreAddressModeBoth:
		_, ipv4 := subStoreProfileAddress(profile, core.SubStoreAddressModeIPv4)
		_, ipv6 := subStoreProfileAddress(profile, core.SubStoreAddressModeIPv6)
		return ipv4 && ipv6
	default:
		return false
	}
}

func subStoreIPv6NodeName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasSuffix(strings.ToLower(name), " v6") {
		return name
	}
	const suffix = " v6"
	runes := []rune(name)
	if len(runes)+utf8.RuneCountInString(suffix) > 100 {
		runes = runes[:100-utf8.RuneCountInString(suffix)]
		name = strings.TrimSpace(string(runes))
	}
	return name + suffix
}

func subStoreNodesForSelection(profile subStoreSyncProfile, selection core.SubStoreSyncSelection) ([]string, error) {
	mode, valid := core.NormalizeSubStoreAddressMode(selection.AddressMode)
	if !valid || !subStoreProfileSupportsMode(profile, mode) {
		return nil, errors.New("所选 IP 地址已不可用，请重新选择地址模式")
	}
	type candidate struct {
		uri  string
		name string
	}
	candidates := make([]candidate, 0, 2)
	switch mode {
	case core.SubStoreAddressModeAuto:
		candidates = append(candidates, candidate{uri: profile.URI, name: selection.CustomName})
	case core.SubStoreAddressModeIPv4:
		address, _ := subStoreProfileAddress(profile, core.SubStoreAddressModeIPv4)
		candidates = append(candidates, candidate{uri: address.URI, name: selection.CustomName})
	case core.SubStoreAddressModeIPv6:
		address, _ := subStoreProfileAddress(profile, core.SubStoreAddressModeIPv6)
		candidates = append(candidates, candidate{uri: address.URI, name: subStoreIPv6NodeName(selection.CustomName)})
	case core.SubStoreAddressModeBoth:
		ipv4, _ := subStoreProfileAddress(profile, core.SubStoreAddressModeIPv4)
		ipv6, _ := subStoreProfileAddress(profile, core.SubStoreAddressModeIPv6)
		candidates = append(candidates,
			candidate{uri: ipv4.URI, name: selection.CustomName},
			candidate{uri: ipv6.URI, name: subStoreIPv6NodeName(selection.CustomName)},
		)
	}
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		renamed, err := renameSubStoreNode(candidate.uri, candidate.name)
		if err != nil {
			return nil, err
		}
		result = append(result, renamed)
	}
	return result, nil
}

func renameSubStoreNode(rawValue, name string) (string, error) {
	rawValue = strings.TrimSpace(rawValue)
	name = strings.TrimSpace(name)
	if rawValue == "" || strings.ContainsAny(rawValue, "\r\n") {
		return "", errors.New("客户端分享值无效")
	}
	if name == "" || utf8.RuneCountInString(name) > 100 || strings.ContainsAny(name, "#\r\n") {
		return "", errors.New("节点名称不能为空、不能超过 100 个字符且不能包含 #")
	}
	if config, ok := subStoreSurgeConfig(rawValue); ok {
		name = strings.TrimSpace(strings.NewReplacer("=", "", ",", "").Replace(name))
		if name == "" {
			return "", errors.New("Surge 节点名称删除保留符号后不能为空")
		}
		return name + " = " + config, nil
	}
	if document, nameNode, ok := subStoreMihomoNode(rawValue); ok {
		nameNode.Value = name
		nameNode.Tag = "!!str"
		setSubStoreYAMLFlowStyle(document)
		encoded, err := yaml.Marshal(document)
		if err != nil {
			return "", errors.New("无法编码 Mihomo 节点配置")
		}
		return strings.TrimSpace(string(encoded)), nil
	}
	parsed, err := url.Parse(rawValue)
	if err != nil || parsed.Scheme == "" {
		return "", errors.New("Sub-Store 无法识别客户端分享值")
	}
	if fragment := strings.IndexByte(rawValue, '#'); fragment >= 0 {
		rawValue = rawValue[:fragment]
	}
	return rawValue + "#" + name, nil
}

func subStoreSurgeConfig(rawValue string) (string, bool) {
	_, config, found := strings.Cut(rawValue, "=")
	if !found {
		return "", false
	}
	config = strings.TrimSpace(config)
	proxyType, _, found := strings.Cut(config, ",")
	return config, found && strings.EqualFold(strings.TrimSpace(proxyType), "snell")
}

func subStoreMihomoNode(rawValue string) (*yaml.Node, *yaml.Node, bool) {
	rawValue = strings.TrimSpace(rawValue)
	if len(rawValue) > 64<<10 || !strings.HasPrefix(rawValue, "{") || !strings.HasSuffix(rawValue, "}") {
		return nil, nil, false
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(rawValue), &document); err != nil || len(document.Content) != 1 {
		return nil, nil, false
	}
	mapping := document.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return nil, nil, false
	}
	var nameNode, typeNode *yaml.Node
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		switch mapping.Content[index].Value {
		case "name":
			nameNode = mapping.Content[index+1]
		case "type":
			typeNode = mapping.Content[index+1]
		}
	}
	if nameNode == nil || typeNode == nil || strings.TrimSpace(typeNode.Value) == "" {
		return nil, nil, false
	}
	return &document, nameNode, true
}

func setSubStoreYAMLFlowStyle(node *yaml.Node) {
	if node.Kind == yaml.MappingNode || node.Kind == yaml.SequenceNode {
		node.Style |= yaml.FlowStyle
	}
	for _, child := range node.Content {
		setSubStoreYAMLFlowStyle(child)
	}
}

func (s *Server) upsertSubStoreSubscription(ctx context.Context, settings core.SubStoreSyncSettings, target core.SubStoreSyncTarget, content string) (bool, error) {
	mode, valid := core.NormalizeSubStoreSyncMode(strings.TrimSpace(target.SyncMode))
	if !valid {
		return false, errors.New("Sub-Store 同步组模式无效，请重新保存组设置")
	}
	currentNames, err := subStoreNodeNames(content)
	if err != nil {
		return false, err
	}
	payload := subStoreSubscriptionPayload(target, content)
	var subscriptions []map[string]any
	if _, err := s.subStoreRequest(ctx, settings.EndpointURL, http.MethodGet, "/api/subs", nil, &subscriptions); err != nil {
		return false, err
	}
	var owned map[string]any
	var colliding map[string]any
	for _, subscription := range subscriptions {
		name, _ := subscription["name"].(string)
		owner, _ := subscription["qcontrolhub_integration_id"].(string)
		if owner == target.IntegrationID {
			owned = subscription
		}
		if name == target.SubscriptionName {
			colliding = subscription
		}
	}
	if owned == nil && colliding == nil {
		_, err := s.subStoreRequest(ctx, settings.EndpointURL, http.MethodPost, "/api/subs", payload, nil)
		return true, err
	}
	if owned == nil {
		collisionOwner, _ := colliding["qcontrolhub_integration_id"].(string)
		if strings.TrimSpace(collisionOwner) != "" {
			return false, errors.New("Sub-Store 中已存在同名订阅，但它属于其他 QControlHub；请更换订阅名称")
		}
		existingName, _ := colliding["name"].(string)
		if existingName == "" {
			return false, errors.New("Sub-Store 返回的同名订阅缺少名称")
		}
		if mode == core.SubStoreSyncModeIncremental {
			existingContent, _ := colliding["content"].(string)
			merged, mergeErr := mergeSubStoreContentByName(existingContent, content, nil)
			if mergeErr != nil {
				return false, mergeErr
			}
			payload["content"] = merged
		}
		payload["qcontrolhub_managed_nodes"] = currentNames
		// Preserve fields configured directly in Sub-Store while replacing only
		// values owned by QControlHub.
		payload = subStoreSubscriptionUpdate(colliding, payload)
		_, err := s.subStoreRequest(ctx, settings.EndpointURL, http.MethodPatch, "/api/sub/"+existingName, payload, nil)
		return false, err
	}
	existingName, _ := owned["name"].(string)
	if existingName == "" {
		return false, errors.New("Sub-Store 返回的订阅缺少名称")
	}
	if colliding != nil {
		collisionOwner, _ := colliding["qcontrolhub_integration_id"].(string)
		if collisionOwner != target.IntegrationID {
			return false, errors.New("Sub-Store 中已存在同名订阅，但它属于其他同步组；请更换订阅名称")
		}
	}
	if mode == core.SubStoreSyncModeIncremental {
		existingContent, _ := owned["content"].(string)
		// Once ownership metadata exists, remove nodes that this target managed in
		// the previous sync but are no longer selected. A legacy group without the
		// metadata is different: its existing nodes have unknown provenance, so the
		// first incremental sync must preserve all of them and only start tracking
		// the nodes written by this run.
		var previouslyManaged []string
		if _, metadataPresent := owned["qcontrolhub_managed_nodes"]; metadataPresent {
			previouslyManaged = subStoreManagedNames(owned["qcontrolhub_managed_nodes"])
		}
		merged, mergeErr := mergeSubStoreContentByName(existingContent, content, previouslyManaged)
		if mergeErr != nil {
			return false, mergeErr
		}
		payload["content"] = merged
	}
	payload["qcontrolhub_managed_nodes"] = currentNames
	// Preserve fields configured directly in Sub-Store while replacing only
	// values owned by QControlHub.
	payload = subStoreSubscriptionUpdate(owned, payload)
	_, err = s.subStoreRequest(ctx, settings.EndpointURL, http.MethodPatch, "/api/sub/"+existingName, payload, nil)
	return false, err
}

func subStoreNodeNames(content string) ([]string, error) {
	result := make([]string, 0)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name := subStoreNodeName(line)
		if name == "" {
			return nil, errors.New("Sub-Store 同步要求每个节点配置都包含名称")
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("Sub-Store 同步清单存在重名节点 %q，请先修改同步名称", name)
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result, nil
}

func subStoreNodeName(rawValue string) string {
	rawValue = strings.TrimSpace(rawValue)
	if _, ok := subStoreSurgeConfig(rawValue); ok {
		name, _, _ := strings.Cut(rawValue, "=")
		return strings.TrimSpace(name)
	}
	if _, nameNode, ok := subStoreMihomoNode(rawValue); ok {
		return strings.TrimSpace(nameNode.Value)
	}
	fragment := strings.LastIndexByte(rawValue, '#')
	if fragment < 0 || fragment == len(rawValue)-1 {
		return ""
	}
	name := strings.TrimSpace(rawValue[fragment+1:])
	if decoded, err := url.PathUnescape(name); err == nil {
		name = strings.TrimSpace(decoded)
	}
	return name
}

func subStoreManagedNames(value any) []string {
	result := make([]string, 0)
	switch names := value.(type) {
	case []any:
		for _, item := range names {
			if name, ok := item.(string); ok && strings.TrimSpace(name) != "" {
				result = append(result, strings.TrimSpace(name))
			}
		}
	case []string:
		for _, name := range names {
			if strings.TrimSpace(name) != "" {
				result = append(result, strings.TrimSpace(name))
			}
		}
	}
	return result
}

func mergeSubStoreContentByName(existingContent, desiredContent string, previouslyManaged []string) (string, error) {
	desiredNames, err := subStoreNodeNames(desiredContent)
	if err != nil {
		return "", err
	}
	desiredLines := make(map[string]string, len(desiredNames))
	for _, line := range strings.Split(strings.ReplaceAll(desiredContent, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			desiredLines[subStoreNodeName(line)] = line
		}
	}
	managed := make(map[string]struct{}, len(previouslyManaged))
	for _, name := range previouslyManaged {
		managed[name] = struct{}{}
	}
	used := make(map[string]struct{}, len(desiredNames))
	merged := make([]string, 0)
	for _, line := range strings.Split(strings.ReplaceAll(existingContent, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name := subStoreNodeName(line)
		if replacement, exists := desiredLines[name]; exists {
			if _, alreadyUsed := used[name]; !alreadyUsed {
				merged = append(merged, replacement)
				used[name] = struct{}{}
			}
			continue
		}
		if _, remove := managed[name]; remove {
			continue
		}
		merged = append(merged, line)
	}
	for _, name := range desiredNames {
		if _, exists := used[name]; !exists {
			merged = append(merged, desiredLines[name])
		}
	}
	return strings.Join(merged, "\n"), nil
}

func (s *Server) subStoreRequest(ctx context.Context, endpoint, method, route string, payload any, destination any) (int, error) {
	base, err := url.Parse(endpoint)
	if err != nil {
		return 0, errors.New("Sub-Store 地址无效")
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + route
	var body io.Reader
	if payload != nil {
		encoded, encodeErr := json.Marshal(payload)
		if encodeErr != nil {
			return 0, errors.New("无法编码 Sub-Store 同步请求")
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, base.String(), body)
	if err != nil {
		return 0, errors.New("无法创建 Sub-Store 请求")
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := s.subStoreHTTP
	if client == nil {
		client = newSubStoreHTTPClient()
	}
	response, err := client.Do(req)
	if err != nil {
		// url.Error includes the complete request URL. The Sub-Store backend
		// path is a credential, so never return or persist that error verbatim.
		return 0, errors.New("连接 Sub-Store 失败，请确认地址和网络")
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, subStoreResponseLimit+1)
	contents, err := io.ReadAll(limited)
	if err != nil {
		return response.StatusCode, errors.New("读取 Sub-Store 响应失败")
	}
	if len(contents) > subStoreResponseLimit {
		return response.StatusCode, errors.New("Sub-Store 响应超过安全大小限制")
	}
	var envelope subStoreEnvelope
	if len(contents) > 0 && json.Unmarshal(contents, &envelope) != nil {
		return response.StatusCode, fmt.Errorf("Sub-Store 返回了无效响应 (%s)", response.Status)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || envelope.Status != "success" {
		message := strings.TrimSpace(envelope.Error.Message)
		if message == "" {
			message = strings.TrimSpace(envelope.Error.Details)
		}
		message = strings.ReplaceAll(strings.ReplaceAll(message, "\r", " "), "\n", " ")
		if len(message) > 240 {
			message = message[:240]
		}
		if message == "" {
			message = response.Status
		}
		return response.StatusCode, fmt.Errorf("Sub-Store 请求失败: %s", message)
	}
	if destination != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, destination); err != nil {
			return response.StatusCode, errors.New("Sub-Store 数据响应格式无效")
		}
	}
	return response.StatusCode, nil
}
