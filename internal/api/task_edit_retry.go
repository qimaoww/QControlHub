package api

import (
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Server) authorizeRetryConfig(w http.ResponseWriter, r *http.Request, config core.Config, write bool) bool {
	permission := core.PermissionConfigsRead
	if config.AgentID != "" {
		permission = core.PermissionAgentConfigRead
	}
	if write {
		permission = core.PermissionConfigsWrite
		if config.AgentID != "" {
			permission = core.PermissionAgentConfigWrite
		}
	}
	role, _ := s.sessionRole(r)
	granted, _ := s.sessionPermissions(r)
	if !role.Allows(permission) && !core.HasPermission(granted, permission) {
		writeError(w, http.StatusForbidden, "没有此配置的读写权限")
		return false
	}
	return true
}
func (s *Server) taskRetryEditor(w http.ResponseWriter, r *http.Request) {
	task, config, err := s.store.RetryEditor(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !s.authorizeRetryConfig(w, r, config, false) || !s.authorizeRetryConfig(w, r, config, true) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task, "config": config})
}
func (s *Server) editRetryTask(w http.ResponseWriter, r *http.Request) {
	_, config, err := s.store.RetryEditor(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !s.authorizeRetryConfig(w, r, config, false) || !s.authorizeRetryConfig(w, r, config, true) {
		return
	}
	var input struct {
		Content string `json:"content"`
		Version int    `json:"expected_version"`
	}
	if err := decodeJSON(w, r, &input, 2<<20); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, task, err := s.store.SaveEditedRetry(r.Context(), r.PathValue("id"), input.Content, input.Version)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task.ConfigContent = ""
	task.TLSFrontends = nil
	s.recordAudit(r, "task.edited-retry", task.ID, "")
	writeJSON(w, http.StatusCreated, map[string]any{"config": saved, "task": task})
}
