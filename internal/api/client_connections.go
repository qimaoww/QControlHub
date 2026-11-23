package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func parseClientConnectionQuery(values url.Values, now time.Time) (store.ClientConnectionQuery, error) {
	q := store.ClientConnectionQuery{AgentID: values.Get("agent_id"), Engine: core.Engine(values.Get("engine")), Protocol: values.Get("protocol"), Inbound: values.Get("inbound"), Transport: values.Get("transport"), ClientIP: values.Get("client_ip"), Since: now.Add(-24 * time.Hour), Until: now, Limit: 100, Bucket: "hour"}
	q.CountryCode = strings.ToUpper(strings.TrimSpace(values.Get("country_code")))
	if group := values.Get("group_by"); group != "" {
		if group != "ip" {
			return q, fmt.Errorf("invalid group_by")
		}
		q.GroupByIP = true
	}
	q.Cursor = values.Get("cursor")
	q.NodeOrder = values["node_order"]
	if value := values.Get("include_non_public"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return q, fmt.Errorf("invalid include_non_public")
		}
		q.IncludeNonPublic = parsed
	}
	if value := values.Get("timeline"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return q, fmt.Errorf("invalid timeline")
		}
		q.SkipTimeline = !parsed
	}
	if q.AgentID != "" && !validAgentID(q.AgentID) {
		return q, fmt.Errorf("invalid agent_id")
	}
	for key, target := range map[string]*time.Time{"since": &q.Since, "until": &q.Until} {
		if value := values.Get(key); value != "" {
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				return q, fmt.Errorf("%s must be RFC3339", key)
			}
			*target = parsed
		}
	}
	for key, target := range map[string]*int{"port": &q.Port, "limit": &q.Limit} {
		if value := values.Get(key); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 {
				return q, fmt.Errorf("invalid %s", key)
			}
			*target = parsed
		}
	}
	if value := values.Get("before"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 1 {
			return q, fmt.Errorf("invalid before")
		}
		q.Before = parsed
	}
	if value := values.Get("bucket"); value != "" {
		q.Bucket = value
	}
	return q, q.Validate()
}

// Hold the slot until the response has been encoded and written, so slow
// receivers cannot keep arbitrarily many bounded pages alive in the panel.
const clientConnectionConcurrentReads = 4

func (s *Server) listClientConnections(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.clientConnectionReads.Add(1) > clientConnectionConcurrentReads {
		s.clientConnectionReads.Add(-1)
		writeError(w, http.StatusTooManyRequests, "客户端连接查询繁忙，请稍后重试。")
		return
	}
	defer s.clientConnectionReads.Add(-1)
	initialSession, hadSession := s.sessionForRequest(request)
	if bearerToken(request) == "" && !hadSession {
		writeError(w, http.StatusUnauthorized, "session expired")
		return
	}
	if value := request.URL.Query().Get("scope_only"); value != "" {
		if value != "true" {
			writeError(w, http.StatusBadRequest, "invalid scope_only")
			return
		}
		scope, revision, err := s.clientConnectionScope(request, initialSession, hadSession)
		if err != nil {
			writeCoreLogReadError(w, request, err)
			return
		}
		if !s.validateClientConnectionAuthority(w, request, initialSession, hadSession, revision) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"scope": scope})
		return
	}
	if value := request.URL.Query().Get("sync"); value != "" && value != "true" || len(request.URL.Query().Get("sync_token")) > 128 {
		writeError(w, http.StatusBadRequest, "请选择有效的连接同步参数。")
		return
	}
	locationMode := request.URL.Query().Get("locations")
	if locationMode != "" && locationMode != "cached" && locationMode != "only" {
		writeError(w, http.StatusBadRequest, "请选择有效的 IP 属地查询模式。")
		return
	}
	query, err := parseClientConnectionQuery(request.URL.Query(), time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	query.RecordsOnly = locationMode == "only"
	// Bind a snapshot to the authority that produced it. A concurrent node
	// revocation must not stamp old rows with the new, narrower authority.
	scope, authorityRevision, err := s.clientConnectionScope(request, initialSession, hadSession)
	if err != nil {
		writeCoreLogReadError(w, request, err)
		return
	}
	var history core.ClientConnectionHistory
	var synced bool
	if scope != "" && request.URL.Query().Get("sync") == "true" {
		token := request.URL.Query().Get("sync_token")
		if query.RecordsOnly {
			token = ""
		}
		history, synced, err = s.store.SyncClientConnections(request.Context(), query, connectionScopeDigest([]any{scope, authorityRevision}), token)
	}
	if !synced && err == nil {
		history, err = s.store.ClientConnectionHistory(request.Context(), query)
	}
	if err != nil {
		writeCoreLogReadError(w, request, err)
		return
	}
	if query.RecordsOnly && synced {
		history = core.ClientConnectionHistory{Records: history.Records, NextCursor: history.NextCursor, PageCursor: history.PageCursor, Sources: []core.ClientConnectionSource{}, Timeline: []core.ClientConnectionBucket{}, CountryCodes: []string{}}
	}
	if !synced || query.RecordsOnly {
		s.resolveClientConnectionLocations(request.Context(), history.Records, locationMode == "cached")
	}
	if synced && !query.RecordsOnly {
		s.clientConnectionRetryTimes(request.Context(), history.Records)
	}
	if query.CountryCode != "" {
		// A deferred lookup may reclassify an IP for later queries. This response
		// still describes the filtered database snapshot used for its totals.
		for i := range history.Records {
			if history.Records[i].Location.CountryCode != query.CountryCode {
				history.Records[i].Location = core.ClientIPLocation{CountryCode: query.CountryCode}
			}
		}
	}
	if !s.validateClientConnectionAuthority(w, request, initialSession, hadSession, authorityRevision) {
		return
	}
	if history.SyncToken != "" {
		// The token covers the full projected page; sources have an independent
		// lifecycle. Only validate after the live session and grants are checked.
		tag := connectionScopeDigest([]any{scope, history.SyncToken, history.Sources})
		etag := `W/"` + tag + `"`
		w.Header().Set("ETag", etag)
		if history.Delta && history.SyncToken == request.URL.Query().Get("sync_token") && len(history.Records) == 0 && coreLogETagMatches(request.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	writeJSON(w, http.StatusOK, struct {
		core.ClientConnectionHistory
		CacheScope string `json:"cache_scope"`
	}{history, scope})
}

func (s *Server) validateClientConnectionAuthority(w http.ResponseWriter, request *http.Request, initialSession apiSession, hadSession bool, authorityRevision int64) bool {
	// User disablement, role/permission changes and session expiry can happen
	// while the history or deferred geography read is in progress.
	// Drop the middleware's per-request session snapshot before revalidating.
	request = request.WithContext(context.WithValue(request.Context(), validatedSessionKey{}, nil))
	request, valid := s.validateSession(w, request)
	if !valid {
		return false
	}
	latestSession, present := s.sessionForRequest(request)
	if hadSession && (!present || connectionSessionAuthority(initialSession) != connectionSessionAuthority(latestSession)) {
		writeError(w, http.StatusConflict, "访问范围已变化，请重试。")
		return false
	}
	latest, err := s.store.ClientConnectionAuthorityRevision(request.Context())
	if err != nil {
		writeCoreLogReadError(w, request, err)
		return false
	}
	if latest != authorityRevision {
		writeError(w, http.StatusConflict, "访问范围已变化，请重试。")
		return false
	}
	return true
}

func (s *Server) clientConnectionScope(request *http.Request, session apiSession, hasSession bool) (string, int64, error) {
	// Bearer clients remain supported without a restorable browser scope.
	if hasSession && bearerToken(request) == "" {
		nodes, revision, err := s.store.ClientConnectionAuthority(request.Context())
		if err != nil {
			return "", 0, err
		}
		return connectionScopeDigest([]any{"connections-v2", connectionSessionAuthority(session), nodes}), revision, nil
	}
	revision, err := s.store.ClientConnectionAuthorityRevision(request.Context())
	return "", revision, err
}

func connectionSessionAuthority(session apiSession) string {
	return connectionScopeDigest([]any{session.UserID, session.ConfigOwnerID, session.EnvironmentID, session.AuthRevision, session.Role, session.Permissions})
}

func connectionScopeDigest(value any) string {
	payload, _ := json.Marshal(value)
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}
