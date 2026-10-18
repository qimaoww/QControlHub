package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowserReadsStayBoundToTheirSessionAfterCookieReplacement(t *testing.T) {
	_, _, admin, alice, bob := newConfigScopeAPIFixture(t)
	for _, path := range []string{"/overview", "/auth/session", "/agent-access", "/core-log-sources", "/preferences/node-order"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1"+path, nil)
		request.AddCookie(bob.cookie)              // Another tab has signed in as B.
		request.Header.Set(csrfHeader, alice.csrf) // This page still belongs to A.
		response := httptest.NewRecorder()
		alice.handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s returned %d to the previous account: %s", path, response.Code, response.Body.String())
		}
	}
	// Rejection of A's old page must not revoke B's valid login or affect a
	// token client. Reads without the optional binding retain compatibility.
	bob.call("GET", "/overview", nil, http.StatusOK, nil)
	admin.call("GET", "/overview", nil, http.StatusOK, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	request.AddCookie(bob.cookie)
	response := httptest.NewRecorder()
	bob.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unbound API client: %d %s", response.Code, response.Body.String())
	}
}
