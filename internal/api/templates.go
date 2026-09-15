package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) listTemplates(w http.ResponseWriter, request *http.Request) {
	templates, err := s.store.ListConfigTemplates(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

func (s *Server) createTemplate(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Name    string `json:"name"`
		Engine  string `json:"engine"`
		Content string `json:"content"`
	}
	if err := decodeJSON(w, request, &input, core.MaxConfigEnvelopeBytes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	template, err := s.store.CreateConfigTemplate(request.Context(), input.Name, input.Engine, input.Content)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "template.created", template.ID, template.Name)
	writeJSON(w, http.StatusCreated, template)
}

func (s *Server) deleteTemplate(w http.ResponseWriter, request *http.Request) {
	if err := s.store.DeleteConfigTemplate(request.Context(), request.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "template.deleted", request.PathValue("id"), "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) applyTemplate(w http.ResponseWriter, request *http.Request) {
	var input struct {
		AgentID string `json:"agent_id"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input.AgentID = strings.TrimSpace(input.AgentID)
	if input.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	// {{lan_ip}} is derived from host metrics. Reject it before rendering or
	// saving so a caller without metrics.read cannot copy a private interface
	// address into a configuration the API returns and persists.
	template, agent, rendered, err := s.store.RenderTemplateForAgent(request.Context(), request.PathValue("id"), input.AgentID,
		s.sessionAllows(request, core.PermissionMetricsRead))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	current, currentErr := s.store.AgentConfig(request.Context(), input.AgentID, template.Engine)
	expectedVersion := 0
	if currentErr == nil {
		expectedVersion = current.Version
	} else if !errors.Is(currentErr, store.ErrNotFound) {
		writeStoreError(w, currentErr)
		return
	}
	saved, err := s.store.SaveAgentConfig(request.Context(), core.Config{AgentID: input.AgentID, Name: template.Name + " · 模板", Description: "由配置模板渲染", Engine: template.Engine, Content: rendered}, expectedVersion)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.reconcileSavedShadowsocksRustPolicies(request.Context(), saved); err != nil {
		writeStoreError(w, err)
		return
	}
	s.refreshSavedAgentTrafficMonitoring(request.Context(), saved.AgentID)
	s.recordAudit(request, "template.applied", template.ID, agent.Name+" "+string(template.Engine))
	writeJSON(w, http.StatusOK, saved)
}
