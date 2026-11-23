package api

import (
	"net/http"
	"strconv"

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
	if op.State == "pending" || op.State == "running" || op.State == "verifying" || op.State == "rolling_back" {
		w.Header().Set("Location", "/api/v1/agents/"+agent.ID+"/tls-migrations/"+op.TaskID)
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

func (s *Server) getTLSPortOccupancy(w http.ResponseWriter, r *http.Request) {
	agent, err := s.store.GetAgent(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil {
		writeStoreError(w, store.ErrInvalid)
		return
	}
	summary, err := s.store.TLSPortOccupancy(r.Context(), agent, port)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}
