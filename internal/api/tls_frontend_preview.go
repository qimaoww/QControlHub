package api

import (
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) previewTLSFrontend(w http.ResponseWriter, r *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, r)
	if !ok {
		return
	}
	var input store.TLSFrontendSelection
	if err := decodeJSON(w, r, &input, 4096); err != nil {
		writeError(w, 400, "TLS 前端预览参数无效")
		return
	}
	preview, err := s.store.PreviewTLSFrontend(r.Context(), agent, engine, r.PathValue("tag"), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) submitTLSMigration(w http.ResponseWriter, r *http.Request) {
	engine, agent, ok := s.agentEngineFromPath(w, r)
	if !ok {
		return
	}
	var input struct {
		store.TLSFrontendSelection
		PlanID string `json:"plan_id"`
		TaskID string `json:"task_id"`
	}
	if err := decodeJSON(w, r, &input, 4096); err != nil {
		writeError(w, 400, "迁移参数无效")
		return
	}
	op, err := s.store.SubmitTLSMigration(r.Context(), agent, engine, r.PathValue("tag"), input.TLSFrontendSelection, input.PlanID, input.TaskID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if op.State == "pending" || op.State == "running" {
		writeJSON(w, http.StatusAccepted, op)
		return
	}
	writeJSON(w, http.StatusOK, op)
}
func (s *Server) getTLSMigration(w http.ResponseWriter, r *http.Request) {
	op, err := s.store.TLSMigrationStatus(r.Context(), r.PathValue("id"), r.PathValue("task"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, op)
}
