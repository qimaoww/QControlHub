package api

import (
	"net/http"
	"strconv"
	"strings"
)

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
