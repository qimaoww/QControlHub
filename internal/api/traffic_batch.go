package api

import (
	"net/http"
	"strconv"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (s *Server) batchPortTraffic(w http.ResponseWriter, r *http.Request) {
	var input core.TrafficBatchRequest
	if err := decodeJSON(w, r, &input, 512<<10); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	results, err := s.store.BatchPortTraffic(r.Context(), input)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	changed := map[string]bool{}
	for _, result := range results {
		if result.Status == "succeeded" && result.AgentID != "" {
			changed[result.AgentID] = true
			s.recordAudit(r, "traffic_batch."+input.Action, result.ID, input.RequestID)
		}
	}
	for id := range changed {
		s.DisconnectAgent(id)
	}
	w.Header().Set("Cache-Control", "no-store")
	s.recordAudit(r, "traffic_batch.completed", input.RequestID, input.Action+" records: "+strconv.Itoa(len(input.IDs)))
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
