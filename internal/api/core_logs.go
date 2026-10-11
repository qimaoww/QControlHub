package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func (s *Server) listCoreLogs(w http.ResponseWriter, request *http.Request) {
	values := request.URL.Query()
	query := store.CoreLogQuery{
		AgentID: strings.TrimSpace(values.Get("agent_id")),
		Level:   strings.TrimSpace(values.Get("level")),
		Search:  strings.TrimSpace(values.Get("q")),
		Limit:   1000,
	}
	if query.AgentID != "" && !validAgentID(query.AgentID) {
		writeError(w, http.StatusBadRequest, "invalid agent_id filter")
		return
	}
	if raw := strings.TrimSpace(values.Get("engine")); raw != "" {
		engine, err := core.ParseEngine(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid engine filter")
			return
		}
		query.Engine = engine
	}
	if query.Level != "" && query.Level != "debug" && query.Level != "info" && query.Level != "warning" && query.Level != "error" && query.Level != "critical" {
		writeError(w, http.StatusBadRequest, "invalid level filter")
		return
	}
	if raw := strings.TrimSpace(values.Get("before")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "before must be a positive integer")
			return
		}
		query.Before = parsed
	}
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 2000 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 2000 per engine")
			return
		}
		query.Limit = parsed
	}
	entries, err := s.store.ListCoreLogs(request.Context(), query)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	// Hash the actual representation after permission filtering and sanitizing.
	// A newest-ID watermark misses retention/deletion and corrections to existing
	// rows. Reading xmin would also defeat the store's covering index. Reusing
	// this encoding keeps the original bounded, index-only query and suppresses
	// compression, transfer and browser parsing when the window is unchanged.
	payload, err := json.Marshal(entries)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	payload = append(payload, '\n')
	digest := sha256.Sum256(payload)
	etag := `W/"` + hex.EncodeToString(digest[:]) + `"`
	w.Header().Set("ETag", etag)
	if coreLogETagMatches(request.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func coreLogETagMatches(condition, etag string) bool {
	// Generated tags contain only a SHA-256 hexadecimal digest, so a comma
	// cannot be part of a matching tag. GET uses weak comparison (RFC 9110).
	for _, candidate := range strings.Split(condition, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}
