package api

import (
	"fmt"
	"net/http"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Server) putAgentEngineCapability(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(w, request, &input, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required")
		return
	}
	id, engine := request.PathValue("id"), core.Engine(request.PathValue("engine"))
	if err := s.store.SetAgentEngineCapability(request.Context(), id, engine, *input.Enabled); err != nil {
		writeStoreError(w, err)
		return
	}
	s.recordAudit(request, "agent.capability.updated", id, fmt.Sprintf("%s=%t", engine, *input.Enabled))
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": *input.Enabled})
}
