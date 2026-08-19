package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) getSettings(w http.ResponseWriter, request *http.Request) {
	settings, err := s.store.PanelSettings(request.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) putSettings(w http.ResponseWriter, request *http.Request) {
	var settings core.PanelSettings
	if err := decodeJSON(w, request, &settings, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.store.SavePanelSettings(request.Context(), settings)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "settings.saved", "", "api")
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) listAudit(w http.ResponseWriter, request *http.Request) {
	limit := 50
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 200")
			return
		}
		limit = parsed
	}
	entries, err := s.store.ListAuditLogs(request.Context(), limit)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) metricSamples(w http.ResponseWriter, request *http.Request) {
	since := time.Now().UTC().Add(-24 * time.Hour)
	if raw := strings.TrimSpace(request.URL.Query().Get("since")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be RFC3339")
			return
		}
		since = parsed
	}
	limit := 1500
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 5000 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 5000")
			return
		}
		limit = parsed
	}
	samples, err := s.store.MetricSamples(request.Context(), request.PathValue("id"), since, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, samples)
}

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
	template, agent, rendered, err := s.store.RenderTemplateForAgent(request.Context(), request.PathValue("id"), input.AgentID)
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
	s.recordAudit(request, "template.applied", template.ID, agent.Name+" "+string(template.Engine))
	writeJSON(w, http.StatusOK, saved)
}
