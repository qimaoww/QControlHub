package api

import (
	"bytes"
	"context"
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

func TestSSRustScopedConfigAPIWithPostgreSQL(t *testing.T) {
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
	dataStore, err := store.OpenWithConfigKey(ctx, schema.URL, true, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	enrollment, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "ss-rust scopes test"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{Name: "scopes", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineShadowsocksRust}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, enrollment.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := dataStore.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Runtime: map[core.Engine]core.RuntimeState{core.EngineShadowsocksRust: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	const initial = `{"dns":"1.1.1.1","mode":"tcp_and_udp","timeout":75,"servers":[{"id":"one","server":"::","server_port":20001,"method":"aes-256-gcm","password":"password-one-long","mode":"tcp_only"},{"id":"two","server":"::","server_port":20002,"method":"aes-256-gcm","password":"password-two-long","mode":"udp_only"}]}`
	saved, err := dataStore.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Name: "test", Engine: core.EngineShadowsocksRust, Content: initial}, 0)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 48)
	handler := New(dataStore, Config{AdminToken: token}).Handler()
	base := "/api/v1/agents/" + agent.ID + "/configs/ss-rust"
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, base+path, bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	body := map[string]any{"mutation": "delete", "expected_version": saved.Version, "intent": "validate"}
	response := request(http.MethodPost, "/fields/mode?inbound=one", body)
	if response.Code != http.StatusOK {
		t.Fatalf("port deletion: %d %s", response.Code, response.Body.String())
	}
	response = request(http.MethodGet, "/fields/mode?inbound=one", nil)
	var fragment struct {
		Present   bool   `json:"present"`
		Inherited string `json:"inherited_fragment"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &fragment); err != nil || fragment.Present || fragment.Inherited != `"tcp_and_udp"` {
		t.Fatalf("inherited value: %s / %v", response.Body.String(), err)
	}
	response = request(http.MethodPost, "/fields/dns", map[string]any{"mutation": "modify", "fragment": `"9.9.9.9"`, "expected_version": saved.Version, "intent": "validate"})
	if response.Code != http.StatusConflict {
		t.Fatalf("stale version: %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{"/fields/mode?inbound=", "/fields/dns?inbound=one", "/fields/mode?inbound=missing"} {
		response = request(http.MethodPost, path, body)
		if response.Code < 400 {
			t.Fatalf("ambiguous scope accepted: %s", path)
		}
	}
	saved, err = dataStore.AgentConfig(ctx, agent.ID, core.EngineShadowsocksRust)
	if err != nil {
		t.Fatal(err)
	}
	response = request(http.MethodPost, "/server-inbounds", map[string]any{"operation": "modify", "original_tag": "one", "expected_version": saved.Version, "intent": "validate", "preserve_ss_rust_globals": true,
		"input": map[string]any{"protocol": "shadowsocks", "tag": "one", "listen": "::", "port": 20003, "method": "aes-256-gcm", "credential": "password-one-long", "username": "default"}})
	if response.Code != http.StatusOK {
		t.Fatalf("scoped preset: %d %s", response.Code, response.Body.String())
	}
	saved, err = dataStore.AgentConfig(ctx, agent.ID, core.EngineShadowsocksRust)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = json.Unmarshal([]byte(saved.Content), &root)
	entries := root["servers"].([]any)
	first, second := entries[0].(map[string]any), entries[1].(map[string]any)
	if root["dns"] != "1.1.1.1" || root["mode"] != "tcp_and_udp" || root["timeout"] != float64(75) || first["mode"] != nil || first["server_port"] != float64(20003) || second["mode"] != "udp_only" || second["server_port"] != float64(20002) {
		t.Fatalf("scope isolation lost: %s", saved.Content)
	}
}
