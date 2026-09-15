package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
		if input.Selections[index].ConfigID == "" {
			// Older clients omit config_id. Resolve it only from the currently
			// visible deployment, then persist the exact identity.
			for _, profile := range profiles {
				if profile.AgentID == input.Selections[index].AgentID && profile.Engine == input.Selections[index].Engine && profile.ProfileTag == input.Selections[index].ProfileTag {
					input.Selections[index].ConfigID = profile.ConfigID
					break
				}
			}
		}
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
		nodes, selectionErr := subStoreNodesForSelection(profile, selection, target.SyncFormat)
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
