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
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func parseClientConnectionQuery(values url.Values, now time.Time) (store.ClientConnectionQuery, error) {
	q := store.ClientConnectionQuery{AgentID: values.Get("agent_id"), Engine: core.Engine(values.Get("engine")), Protocol: values.Get("protocol"), Inbound: values.Get("inbound"), Transport: values.Get("transport"), ClientIP: values.Get("client_ip"), Since: now.Add(-24 * time.Hour), Until: now, Limit: 100, Bucket: "hour"}
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

func (s *Server) listClientConnections(w http.ResponseWriter, request *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if value := request.URL.Query().Get("scope_only"); value != "" {
		if value != "true" {
			writeError(w, http.StatusBadRequest, "invalid scope_only")
			return
		}
		scope, err := s.clientConnectionScope(request)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"scope": scope})
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
	scope, err := s.clientConnectionScope(request)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	history, err := s.store.ClientConnectionHistory(request.Context(), query)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.resolveClientConnectionLocations(request.Context(), history.Records, locationMode == "cached")
	// User disablement, role/permission changes and session expiry can happen
	// while the history or deferred geography read is in progress.
	// Drop the middleware's per-request session snapshot before revalidating.
	request = request.WithContext(context.WithValue(request.Context(), validatedSessionKey{}, nil))
	request, valid := s.validateSession(w, request)
	if !valid {
		return
	}
	latest, err := s.clientConnectionScope(request)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if latest != scope {
		writeError(w, http.StatusConflict, "访问范围已变化，请重试。")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		core.ClientConnectionHistory
		CacheScope string `json:"cache_scope"`
	}{history, scope})
}

func (s *Server) clientConnectionScope(request *http.Request) (string, error) {
	// Browser persistence requires an authenticated login session. Bearer API
	// clients continue to read history but never receive a restorable scope.
	session, ok := s.sessionForRequest(request)
	if !ok {
		return "", nil
	}
	nodes, err := s.store.ClientConnectionScope(request.Context())
	if err != nil {
		return "", err
	}
	// CSRF is an opaque per-login nonce. Only its digest leaves this function;
	// no credential is put into browser storage. A new login or deployment
	// cannot inherit another session's history, even with the same username.
	payload, err := json.Marshal([]any{"connections-v1", session.CSRF, session.UserID, session.ConfigOwnerID, session.Role, session.Permissions, nodes})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:]), nil
}
