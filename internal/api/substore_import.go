package api

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
	visibleTargets := make(map[string]bool, len(targets))
	for _, target := range targets {
		visibleTargets[target.ID] = true
		importedNames[target.SubscriptionName] = struct{}{}
		importedOwners[target.IntegrationID] = struct{}{}
	}
	claims, err := s.store.SubStoreTargetClaims(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	result := make([]subStoreRemoteTarget, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		name, _ := subscription["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		owner, _ := subscription["qcontrolhub_integration_id"].(string)
		hidden := false
		for _, claim := range claims {
			if !visibleTargets[claim.ID] && (claim.SubscriptionName == name || (owner != "" && claim.IntegrationID == owner)) {
				hidden = true
				break
			}
		}
		if hidden {
			continue
		}
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
	claims, err := s.store.SubStoreTargetClaims(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	for _, claim := range claims {
		if claim.SubscriptionName == name || (integrationID != "" && claim.IntegrationID == integrationID) {
			writeError(w, http.StatusConflict, "该 Sub-Store 组已关联其他同步组")
			return
		}
	}
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
	target, err := s.store.ImportSubStoreSyncTarget(request.Context(), name, integrationID, input.SyncFormat)
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
	format := target.SyncFormat
	if strings.TrimSpace(input.SyncFormat) != "" {
		var valid bool
		format, valid = core.NormalizeSubStoreSyncFormat(strings.TrimSpace(input.SyncFormat))
		if !valid {
			writeError(w, http.StatusBadRequest, "Sub-Store 同步格式必须是 url 或 mihomo")
			return
		}
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
	localTargets, err := s.store.SubStoreTargetClaims(request.Context())
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
		if displayName != target.DisplayName || format != target.SyncFormat {
			updated, updateErr := s.store.UpdateSubStoreSyncTarget(request.Context(), target.ID, displayName, target.SubscriptionName, target.SyncMode, format)
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
	updated, updateErr := s.store.UpdateSubStoreSyncTarget(request.Context(), target.ID, displayName, name, target.SyncMode, format)
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
