package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

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
