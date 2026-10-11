package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCoreLogConditionalReadRechecksOwnership(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "conditional-owner")
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21001)
	if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: "log_abcdefabcdef1234", Entries: []core.CoreLogEntry{
		{Engine: core.EngineMihomo, Level: "warning", Message: "owner-only diagnostic"},
	}}); err != nil {
		t.Fatal(err)
	}
	fetch := func(client configScopeAPIClient, query, tag string, status int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/core-logs"+query, nil)
		request.AddCookie(client.cookie)
		request.Header.Set("If-None-Match", tag)
		response := httptest.NewRecorder()
		client.handler.ServeHTTP(response, request)
		if response.Code != status {
			t.Fatalf("%s: status=%d, body=%s", query, response.Code, response.Body.String())
		}
		return response
	}
	query := "?agent_id=" + agent.ID
	tag := fetch(alice, query, "", http.StatusOK).Header().Get("ETag")
	fetch(alice, query, tag, http.StatusNotModified)
	fetch(bob, query, tag, http.StatusForbidden)
	// Aggregate reads must also evaluate ownership before comparing the tag.
	fetch(bob, "", tag, http.StatusOK)
	alice.call(http.MethodDelete, "/agents/"+agent.ID, nil, http.StatusNoContent, nil)
	fetch(alice, query, tag, http.StatusNotFound)
}
