package api

import (
	"net/http"
	"strconv"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Server) getAgentConfig(w http.ResponseWriter, request *http.Request) {
	engine, err := core.ParseEngine(request.PathValue("engine"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.store.AgentConfig(request.Context(), request.PathValue("id"), engine)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) putAgentConfig(w http.ResponseWriter, request *http.Request) {
	engine, err := core.ParseEngine(request.PathValue("engine"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var input core.Config
	if err := decodeJSON(w, request, &input, core.MaxConfigEnvelopeBytes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Engine != "" && input.Engine != engine {
		writeError(w, http.StatusBadRequest, "configuration engine does not match the URL")
		return
	}
	input.AgentID = request.PathValue("id")
	input.Engine = engine
	s.saveAgentConfigResponse(w, request, input)
}

func (s *Server) saveAgentConfigResponse(w http.ResponseWriter, request *http.Request, input core.Config) {
	config, err := s.store.SaveAgentConfig(request.Context(), input, input.Version)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.reconcileSavedShadowsocksRustPolicies(request.Context(), config); err != nil {
		writeStoreError(w, err)
		return
	}
	s.refreshSavedAgentTrafficMonitoring(request.Context(), config.AgentID)
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) listConfigs(w http.ResponseWriter, request *http.Request) {
	configs, err := s.store.ListConfigs(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, configs)
}

func (s *Server) createConfig(w http.ResponseWriter, request *http.Request) {
	var input core.Config
	if err := decodeJSON(w, request, &input, core.MaxConfigEnvelopeBytes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.store.CreateConfig(request.Context(), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "config.created", config.ID, config.Name+" ("+string(config.Engine)+")")
	writeJSON(w, http.StatusCreated, config)
}

func (s *Server) updateConfig(w http.ResponseWriter, request *http.Request) {
	var input core.Config
	if err := decodeJSON(w, request, &input, core.MaxConfigEnvelopeBytes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.store.UpdateConfig(request.Context(), request.PathValue("id"), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "config.updated", config.ID, "v"+strconv.Itoa(config.Version)+" "+config.Name)
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) deleteConfig(w http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteConfig(request.Context(), request.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "config.deleted", request.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listConfigRevisions(w http.ResponseWriter, request *http.Request) {
	limit := 20
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "revision limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	revisions, err := s.store.ListConfigRevisions(request.Context(), request.PathValue("id"), limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, revisions)
}

func (s *Server) getConfigRevision(w http.ResponseWriter, request *http.Request) {
	version, err := strconv.Atoi(request.PathValue("version"))
	if err != nil || version < 1 {
		writeError(w, http.StatusBadRequest, "revision version must be positive")
		return
	}
	revision, err := s.store.ConfigRevision(request.Context(), request.PathValue("id"), version)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, revision)
}

func (s *Server) restoreConfigRevision(w http.ResponseWriter, request *http.Request) {
	version, err := strconv.Atoi(request.PathValue("version"))
	if err != nil || version < 1 {
		writeError(w, http.StatusBadRequest, "revision version must be positive")
		return
	}
	var input struct {
		ExpectedVersion int `json:"expected_version"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.ExpectedVersion < 1 {
		writeError(w, http.StatusBadRequest, "expected_version must be positive")
		return
	}
	restored, err := s.store.RestoreConfigRevision(request.Context(), request.PathValue("id"), version, input.ExpectedVersion)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "config.restored", restored.ID, "v"+strconv.Itoa(version)+" -> v"+strconv.Itoa(restored.Version))
	writeJSON(w, http.StatusOK, restored)
}
