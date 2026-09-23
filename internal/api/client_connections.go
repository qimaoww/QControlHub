package api

import (
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
	history, err := s.store.ClientConnectionHistory(request.Context(), query)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.resolveClientConnectionLocations(request.Context(), history.Records, locationMode == "cached")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, history)
}
