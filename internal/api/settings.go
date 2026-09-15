package api

import (
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const komariAPIKeyMask = "••••••••"

func panelSettingsResponse(settings core.PanelSettings) core.PanelSettings {
	if settings.KomariAPIKey != "" {
		settings.KomariAPIKey = komariAPIKeyMask
	}
	return settings
}

func (s *Server) getSettings(w http.ResponseWriter, request *http.Request) {
	settings, err := s.store.PanelSettings(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, panelSettingsResponse(settings))
}

func (s *Server) putSettings(w http.ResponseWriter, request *http.Request) {
	previous, previousErr := s.store.PanelSettings(request.Context())
	if previousErr != nil {
		writeInternalError(w, previousErr)
		return
	}
	var input struct {
		core.PanelSettings
		KomariURL         *string `json:"komari_url"`
		KomariAPIKey      *string `json:"komari_api_key"`
		ClearKomariAPIKey bool    `json:"clear_komari_api_key"`
	}
	if err := decodeJSON(w, request, &input, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	settings := input.PanelSettings
	// Pointer fields distinguish a legacy client that does not know about
	// Komari from an explicit empty value used to clear the URL.
	if input.KomariURL == nil {
		settings.KomariURL = previous.KomariURL
	} else {
		settings.KomariURL = *input.KomariURL
	}
	preserveKomariAPIKey := input.KomariAPIKey == nil
	if !preserveKomariAPIKey && !input.ClearKomariAPIKey {
		preserveKomariAPIKey = *input.KomariAPIKey == "" || *input.KomariAPIKey == komariAPIKeyMask
	}
	if preserveKomariAPIKey {
		settings.KomariAPIKey = previous.KomariAPIKey
	} else {
		settings.KomariAPIKey = *input.KomariAPIKey
	}
	// The browser never receives the actual key. An empty or masked value means
	// keep the currently saved key; an explicit clear flag removes it.
	ids, err := s.store.OwnedAgentIDs(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	expectedRevision := settings.Revision
	var saved core.PanelSettings
	if expectedRevision > 0 {
		saved, err = s.store.SavePanelSettingsRevision(request.Context(), settings, expectedRevision)
	} else {
		// Cached pre-v37 frontends do not send revision or any newly introduced
		// policy fields. The store preserves those fields as one compatibility
		// unit while the new UI gets optimistic concurrency protection.
		saved, err = s.store.SavePanelSettings(request.Context(), settings)
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "settings.saved", "", "api")
	if agentPolicyChanged(previous, saved) {
		for _, id := range ids {
			s.DisconnectAgent(id)
		}
	}
	writeJSON(w, http.StatusOK, panelSettingsResponse(saved))
}

func agentPolicyChanged(left, right core.PanelSettings) bool {
	return left.AgentHeartbeatIntervalSeconds != right.AgentHeartbeatIntervalSeconds ||
		left.AgentMetricsIntervalSeconds != right.AgentMetricsIntervalSeconds ||
		left.AgentOfflineThresholdSeconds != right.AgentOfflineThresholdSeconds ||
		left.PublicIPProbeIntervalSeconds != right.PublicIPProbeIntervalSeconds ||
		left.AgentCoreLogMaxMiB != right.AgentCoreLogMaxMiB ||
		left.AgentCoreLogRotateCount != right.AgentCoreLogRotateCount
}

func (s *Server) getDeploymentSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"control_plane_version":        s.controlPlaneVersion,
		"agent_package_version":        s.agentVersion,
		"secure_transport":             s.secureTransport,
		"database_tls_verified":        s.databaseTLSVerified,
		"config_encryption_configured": s.configEncryptionConfigured,
		"webhook_signing_configured":   s.webhookSigningConfigured,
		"trusted_proxy_count":          len(s.trustedProxies),
	})
}
