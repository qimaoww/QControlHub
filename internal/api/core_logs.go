package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// Include authentication, pool acquisition and row reads in the same deadline.
// The browser's longer deadline also bounds transfer and JSON parsing.
func coreLogReadDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeCoreLogReadError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() == context.DeadlineExceeded {
		writeError(w, http.StatusGatewayTimeout, "core log read timed out")
		return
	}
	writeStoreError(w, err)
}

func (s *Server) listCoreLogs(w http.ResponseWriter, request *http.Request) {
	values := request.URL.Query()
	query := store.CoreLogQuery{
		AgentID:    strings.TrimSpace(values.Get("agent_id")),
		InstanceID: strings.TrimSpace(values.Get("instance_id")),
		UserID:     strings.TrimSpace(values.Get("user_id")),
		Level:      strings.TrimSpace(values.Get("level")),
		Search:     strings.TrimSpace(values.Get("q")),
		Limit:      1000,
	}
	validUser := query.UserID == "" || (strings.HasPrefix(query.UserID, "usr_") && validAgentID("agt_"+strings.TrimPrefix(query.UserID, "usr_")))
	if (query.InstanceID != "" && query.InstanceID != "main" && !core.ValidAgentShareID(query.InstanceID)) || !validUser || (query.AgentID != "" && !validAgentID(query.AgentID)) {
		writeError(w, http.StatusBadRequest, "invalid log resource filter")
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
		writeCoreLogReadError(w, request, err)
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
	// Hash the same bytes incrementally. Concatenating the scope and payload
	// copied the complete response once more for every conditional poll.
	digest := sha256.New()
	_, _ = digest.Write([]byte(request.URL.RawQuery + "\x00" + request.Header.Get("Authorization") + request.Header.Get("Cookie") + "\x00"))
	_, _ = digest.Write(payload)
	etag := `W/"` + hex.EncodeToString(digest.Sum(nil)) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Vary", "Cookie, Authorization")
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

func (s *Server) listCoreLogSources(w http.ResponseWriter, r *http.Request) {
	sources, err := s.store.ListCoreLogSources(r.Context())
	if err != nil {
		writeCoreLogReadError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, sources)
}
