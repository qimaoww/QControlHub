package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestAgentRenamePreservesIdentityAndEnrollment(t *testing.T) {
	database := os.Getenv("QCH_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx := context.Background()
	schema, err := testdb.IsolatePostgres(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(ctx)
	dataStore, err := store.OpenWithConfigKey(ctx, schema.URL, true, strings.Repeat("n", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	credential, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "original-node", Reusable: true})
	if err != nil {
		t.Fatal(err)
	}
	input := core.EnrollRequest{Name: "original-node", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineShadowsocksRust}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}
	agent, err := dataStore.EnrollAgent(ctx, input, credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	config, err := dataStore.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Name: "config", Engine: core.EngineShadowsocksRust, Content: `{"server":"127.0.0.1","server_port":20001,"method":"aes-256-gcm","password":"test-password"}`}, 0)
	if err != nil {
		t.Fatal(err)
	}
	adminToken, readerToken := strings.Repeat("a", 48), strings.Repeat("r", 48)
	server := New(dataStore, Config{AdminToken: adminToken})
	server.roleTokens[sha256.Sum256([]byte(readerToken))] = tokenPrincipal{Role: core.RoleUser, Permissions: []core.Permission{core.PermissionAgentsRead}}
	handler := server.Handler()
	request := func(id, token string, body any) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id+"/name", bytes.NewReader(payload))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request(agent.ID, readerToken, map[string]string{"name": "denied"}); got.Code != http.StatusForbidden {
		t.Fatalf("read-only rename: %d %s", got.Code, got.Body.String())
	}
	if got := request(agent.ID, "", map[string]string{"name": "denied"}); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous rename: %d", got.Code)
	}
	for _, body := range []any{map[string]string{}, map[string]string{"name": "   "}, map[string]string{"name": strings.Repeat("名", 101)}, map[string]string{"name": "bad\nname"}, map[string]string{"name": "bad\u0000name"}, map[string]any{"name": 1}} {
		if got := request(agent.ID, adminToken, body); got.Code != http.StatusBadRequest {
			t.Fatalf("invalid name %v: %d %s", body, got.Code, got.Body.String())
		}
	}
	const name = "香港 & Tokyo <edge>"
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"token":"`+adminToken+`"}`))
	login.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	if loginResponse.Code != http.StatusOK || json.NewDecoder(loginResponse.Body).Decode(&session) != nil || session.CSRFToken == "" {
		t.Fatal("browser login failed")
	}
	for _, csrf := range []string{"", session.CSRFToken} {
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agent.ID+"/name", strings.NewReader(`{"name":"browser-name"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(loginResponse.Result().Cookies()[0])
		if csrf != "" {
			r.Header.Set(csrfHeader, csrf)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := http.StatusForbidden
		if csrf != "" {
			want = http.StatusOK
		}
		if w.Code != want {
			t.Fatalf("browser CSRF=%t: %d %s", csrf != "", w.Code, w.Body.String())
		}
	}
	got := request(agent.ID, adminToken, map[string]string{"name": "  " + name + "  "})
	if got.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", got.Code, got.Body.String())
	}
	stored, err := dataStore.GetAgent(ctx, agent.ID)
	if err != nil || stored.Name != name || stored.ID != agent.ID {
		t.Fatalf("renamed agent: %+v %v", stored, err)
	}
	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Version: "after-rename"}); err != nil {
		t.Fatal(err)
	}
	stored, err = dataStore.GetAgent(ctx, agent.ID)
	if err != nil || stored.Name != name {
		t.Fatalf("heartbeat reverted rename: %+v %v", stored, err)
	}
	key, err := dataStore.AgentPublicKey(ctx, agent.ID)
	if err != nil || authn.EncodePublicKey(key) != input.PublicKey {
		t.Fatal("rename changed authentication key")
	}
	// The old installation command still authenticates with its original name.
	input.PublicKey = authn.EncodePublicKey(randomEnrollmentKey(t))
	reinstalled, err := dataStore.EnrollAgent(ctx, input, credential.Token)
	if err != nil || reinstalled.ID != agent.ID || reinstalled.Name != name || !reinstalled.Reinstalled {
		t.Fatalf("reinstall after rename: %+v %v", reinstalled, err)
	}
	kept, err := dataStore.AgentConfig(ctx, agent.ID, core.EngineShadowsocksRust)
	if err != nil || kept.ID != config.ID || kept.Version != config.Version {
		t.Fatalf("rename/reinstall changed config: %+v %v", kept, err)
	}
	if got := request("missing", adminToken, map[string]string{"name": name}); got.Code != http.StatusNotFound {
		t.Fatalf("missing agent: %d", got.Code)
	}
	if err := dataStore.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if got := request(agent.ID, adminToken, map[string]string{"name": name}); got.Code != http.StatusNotFound {
		t.Fatalf("revoked agent rename: %d", got.Code)
	}
}
