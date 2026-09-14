package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) putSubStoreSettings(w http.ResponseWriter, request *http.Request) {
	var input subStoreSettingsRequest
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Serialize both the account and the backend being joined. Locking only
	// the previous backend lets a concurrent relink race a credential change.
	endpoint := strings.TrimSpace(input.EndpointURL)
	if endpoint != "" {
		var err error
		endpoint, err = normalizeSubStoreEndpoint(endpoint)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	release, err := s.store.TryLockSubStoreOperation(request.Context(), endpoint)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	defer release()
	current, err := s.store.SubStoreSyncSettings(request.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
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
	target, err := s.store.CreateSubStoreSyncTarget(request.Context(), name, mode, input.SyncFormat)
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
	format := strings.TrimSpace(input.SyncFormat)
	if format == "" {
		format = target.SyncFormat
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
	updated, err := s.store.UpdateSubStoreSyncTarget(request.Context(), target.ID, displayName, subscriptionName, mode, format)
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
			if _, rollbackErr := s.store.UpdateSubStoreSyncTarget(request.Context(), target.ID, target.DisplayName, target.SubscriptionName, target.SyncMode, target.SyncFormat); rollbackErr != nil {
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
