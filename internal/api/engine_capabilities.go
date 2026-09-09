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
	change, err := s.store.ChangeAgentEngineCapability(request.Context(), id, engine, *input.Enabled)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	status := http.StatusOK
	event := "agent.capability.updated"
	if change.TaskID != "" {
		status = http.StatusAccepted
		event = "agent.capability.requested"
	}
	s.recordAudit(request, event, id, fmt.Sprintf("%s=%t task=%s", engine, *input.Enabled, change.TaskID))
	writeJSON(w, status, change)
}
