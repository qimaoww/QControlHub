package api

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestClientConnectionsRejectInvalidQueries(t *testing.T) {
	token := strings.Repeat("a", 48)
	handler := New(nil, Config{AdminToken: token}).Handler()
	queries := []string{"group_by=invalid", "group_by=ip&before=1", "group_by=ip&cursor=bad", "cursor=bad", "locations=invalid", "include_non_public=invalid", "limit=201", "port=65536", "port=0", "before=-1", "engine=unknown", "transport=quic", "client_ip=bad", "since=bad", "bucket=week", "since=2026-01-01T00:00:00Z&until=2026-03-01T00:00:00Z"}
	for _, payload := range []string{`null`, `{}`, `{"scope":"engine_node_ip","endpoint":["xray"],"ip":"8.8.8.8"}`, `{"scope":"engine_node_ip","endpoint":["xray","A","agt_test"],"ip":"bad"}`, `{"endpoint":["xray","A","agt_test"],"ip":"8.8.8.8"}`, `{"scope":"engine_node_ip","endpoint":["xray","bad\u0000","agt_test"],"ip":"8.8.8.8"}`} {
		queries = append(queries, "group_by=ip&cursor="+base64.RawURLEncoding.EncodeToString([]byte(payload)))
	}
	for _, query := range queries {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/client-connections?"+query, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", query, response.Code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/client-connections", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", response.Code)
	}
}

func TestClientConnectionsAcceptCalendarMonths(t *testing.T) {
	for _, bounds := range [][2]string{
		{"2026-01-01T00:00:00+08:00", "2026-02-01T00:00:00+08:00"},
		{"2026-10-01T00:00:00+02:00", "2026-11-01T00:00:00+01:00"},
		{"2024-02-01T00:00:00Z", "2024-03-01T00:00:00Z"},
	} {
		q, err := parseClientConnectionQuery(url.Values{"since": {bounds[0]}, "until": {bounds[1]}, "bucket": {"day"}, "group_by": {"ip"}}, time.Now())
		if err != nil || !q.GroupByIP || q.Bucket != "day" {
			t.Fatalf("month query: %+v %v", q, err)
		}
	}
}
