package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestClientProfileNamesAreScopedToDeployedPorts(t *testing.T) {
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
	db, err := store.OpenWithConfigKey(ctx, schema.URL, true, strings.Repeat("p", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	credential, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "ports"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "ports", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineShadowsocksRust, core.EngineXray}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Runtime: map[core.Engine]core.RuntimeState{core.EngineShadowsocksRust: {Installed: true}, core.EngineXray: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	address, legacyName := "edge.example.com", "Legacy node name"
	if err := db.SetAgentClientDetails(ctx, agent.ID, &address, &legacyName); err != nil {
		t.Fatal(err)
	}
	const content = `{"dns":"9.9.9.9","servers":[{"id":"one","server":"::","server_port":20001,"method":"aes-256-gcm","password":"password-one-long"},{"id":"two","server":"::","server_port":20002,"method":"aes-256-gcm","password":"password-two-long"}]}`
	config, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Name: "ports", Engine: core.EngineShadowsocksRust, Content: content}, 0)
	if err != nil {
		t.Fatal(err)
	}
	deploy := func(c core.Config) {
		t.Helper()
		if _, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: c.Engine, Action: core.ActionDeploy, ConfigID: c.ID}); err != nil {
			t.Fatal(err)
		}
		claimed, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || claimed == nil {
			t.Fatalf("claim: %+v %v", claimed, err)
		}
		if err := db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
			t.Fatal(err)
		}
	}
	deploy(config)
	xrayContent, err := serverconfig.Generate(core.EngineXray, serverconfig.Input{Protocol: serverconfig.ProtocolVLESS, Tag: "one", Listen: "::", Port: 20001, Username: "test", Credential: "123e4567-e89b-42d3-a456-426614174000"})
	if err != nil {
		t.Fatal(err)
	}
	xray, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Name: "other engine", Engine: core.EngineXray, Content: xrayContent}, 0)
	if err != nil {
		t.Fatal(err)
	}
	deploy(xray)
	admin := strings.Repeat("a", 48)
	reader := strings.Repeat("r", 48)
	s := New(db, Config{AdminToken: admin, ReadonlyTokens: []string{reader}})
	handler := s.Handler()
	const protectedBody = `{"profile":{"engine":"ss-rust","tag":"one","port":20001},"name":"blocked"}`
	endpoint := "/api/v1/agents/" + agent.ID + "/client-address"
	for _, token := range []string{"", reader} {
		r := httptest.NewRequest(http.MethodPut, endpoint, strings.NewReader(protectedBody))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := http.StatusUnauthorized
		if token != "" {
			want = http.StatusForbidden
		}
		if w.Code != want {
			t.Fatalf("unauthorized profile update: %d", w.Code)
		}
	}
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"token":"`+admin+`"}`))
	login.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	if loginResponse.Code != http.StatusOK || json.Unmarshal(loginResponse.Body.Bytes(), &session) != nil {
		t.Fatal("browser login failed")
	}
	for _, csrf := range []string{"", session.CSRFToken} {
		r := httptest.NewRequest(http.MethodPut, endpoint, strings.NewReader(protectedBody))
		r.AddCookie(loginResponse.Result().Cookies()[0])
		r.Header.Set("Content-Type", "application/json")
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
			t.Fatalf("CSRF scoped rename: %d %s", w.Code, w.Body.String())
		}
	}
	request := func(body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agent.ID+"/client-address", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+admin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	selector := func(tag string, port int) map[string]any {
		return map[string]any{"engine": "ss-rust", "tag": tag, "port": port}
	}
	setName := func(tag string, port int, name string) {
		t.Helper()
		w := request(map[string]any{"profile": selector(tag, port), "name": name})
		if w.Code != http.StatusOK {
			t.Fatalf("save name: %d %s", w.Code, w.Body.String())
		}
	}
	checkNames := func(first, second, other string) {
		t.Helper()
		entries, err := s.clientAccessEntries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, entry := range entries {
			for _, p := range entry.Profiles {
				u, err := url.Parse(p.Profile.URI)
				if err != nil {
					t.Fatal(err)
				}
				got[string(entry.Engine)+"/"+p.Tag] = u.Fragment
				for _, option := range entry.AddressOptions {
					for _, variant := range option.Profiles {
						if variant.Tag == p.Tag {
							v, _ := url.Parse(variant.Profile.URI)
							if v.Fragment != u.Fragment {
								t.Fatal("address variant lost name")
							}
						}
					}
				}
			}
		}
		if got["ss-rust/one"] != first || got["ss-rust/two"] != second || got["xray/one"] != other {
			t.Fatalf("names: %+v", got)
		}
	}
	setName("one", 20001, "香港 & ATT <edge>")
	checkNames("香港 & ATT <edge>", "two", "one")
	setName("two", 20002, "另一个端口")
	checkNames("香港 & ATT <edge>", "另一个端口", "one")
	stored, err := db.GetAgent(ctx, agent.ID)
	if err != nil || stored.Labels["client_address"] != address || stored.Labels["client_name"] != legacyName {
		t.Fatalf("node defaults changed: %+v %v", stored, err)
	}
	unchanged, err := db.AgentConfig(ctx, agent.ID, core.EngineShadowsocksRust)
	if err != nil || unchanged.Version != config.Version || unchanged.Content != config.Content {
		t.Fatal("display rename mutated core configuration")
	}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Version: "refresh"}); err != nil {
		t.Fatal(err)
	}
	checkNames("香港 & ATT <edge>", "另一个端口", "one")
	setName("one", 20001, "")
	checkNames("one", "另一个端口", "one")
	setName("one", 20001, "香港 & ATT <edge>")
	for _, body := range []any{
		map[string]any{"profile": selector("missing", 20001), "name": "bad"},
		map[string]any{"profile": selector("one", 20002), "name": "bad"},
		map[string]any{"profile": map[string]any{}, "name": "bad"},
		map[string]any{"profile": selector("one", 20001), "name": "bad\u0000name", "address": "changed.example.com"},
		map[string]any{"profile": selector("one", 20001), "name": strings.Repeat("名", 101)},
	} {
		if w := request(body); w.Code < 400 {
			t.Fatalf("invalid selection accepted: %v", body)
		}
	}
	stored, _ = db.GetAgent(ctx, agent.ID)
	if stored.Labels["client_address"] != address {
		t.Fatal("invalid rename partially changed address")
	}
	profiles, err := s.availableSubStoreProfiles(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		if p.Engine == core.EngineShadowsocksRust && p.ProfileTag == "one" && p.DefaultName != "香港 & ATT <edge>" {
			t.Fatalf("subscription name mismatch: %+v", p)
		}
	}
	// Saving a renamed server draft must not disturb names for the still-live revision.
	plan := serverconfig.ParseAll(core.EngineShadowsocksRust, config.Content)[0]
	plan.Tag = "香港-入站"
	generated, err := serverconfig.Generate(core.EngineShadowsocksRust, plan)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := serverconfig.MutateSSRustPort(config.Content, generated, "one", "modify")
	if err != nil {
		t.Fatal(err)
	}
	config, err = db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Name: config.Name, Engine: config.Engine, Content: changed}, config.Version)
	if err != nil {
		t.Fatal(err)
	}
	checkNames("香港 & ATT <edge>", "另一个端口", "one")
	deploy(config)
	entries, err := s.clientAccessEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Engine == core.EngineShadowsocksRust {
			if entry.Profiles[0].Tag != plan.Tag || entry.Profiles[0].ClientName != "香港 & ATT <edge>" {
				t.Fatalf("server rename lost client name: %+v", entry.Profiles)
			}
		}
	}
	if w := request(map[string]any{"profile": selector("one", 20001), "name": "stale"}); w.Code != http.StatusNotFound {
		t.Fatalf("stale selector accepted: %d", w.Code)
	}
	setName(plan.Tag, 20001, "部署后仍可改名")
}

func TestClientProfileDisplayParametersAreScopedToDeployedPorts(t *testing.T) {
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
	db, err := store.OpenWithConfigKey(ctx, schema.URL, true, strings.Repeat("p", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	credential, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "display"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "display", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineShadowsocksRust}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, credential.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Runtime: map[core.Engine]core.RuntimeState{core.EngineShadowsocksRust: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	address, mode := "edge.example.com", core.SubStoreAddressModeIPv4
	if err := db.SetAgentClientPreferences(ctx, agent.ID, &address, nil, &mode); err != nil {
		t.Fatal(err)
	}
	const content = `{"dns":"9.9.9.9","servers":[{"id":"one","server":"::","server_port":20001,"method":"aes-256-gcm","password":"password-one-long"},{"id":"two","server":"::","server_port":20002,"method":"aes-256-gcm","password":"password-two-long"}]}`
	config, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Name: "display", Engine: core.EngineShadowsocksRust, Content: content}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionDeploy, ConfigID: config.ID}); err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if err := db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	admin := strings.Repeat("a", 48)
	s := New(db, Config{AdminToken: admin})
	handler := s.Handler()
	request := func(body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+agent.ID+"/client-address", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+admin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	selector := func(tag string, port int) map[string]any {
		return map[string]any{"engine": "ss-rust", "tag": tag, "port": port}
	}
	type displayProfile struct {
		address    string
		mode       string
		overridden bool
		host       string
	}
	check := func(want map[string]displayProfile) {
		t.Helper()
		entries, err := s.clientAccessEntries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]displayProfile{}
		for _, entry := range entries {
			if entry.Engine != core.EngineShadowsocksRust {
				continue
			}
			for _, profile := range entry.Profiles {
				parsed, err := url.Parse(profile.Profile.URI)
				if err != nil {
					t.Fatal(err)
				}
				got[profile.Tag] = displayProfile{address: profile.Address, mode: profile.AddressMode, overridden: profile.AddressOverridden, host: parsed.Hostname()}
			}
		}
		for tag, expected := range want {
			if got[tag] != expected {
				t.Fatalf("profile %s = %+v, want %+v (all: %+v)", tag, got[tag], expected, got)
			}
		}
	}
	check(map[string]displayProfile{
		"one": {address: address, mode: core.SubStoreAddressModeAuto, host: address},
		"two": {address: address, mode: core.SubStoreAddressModeAuto, host: address},
	})
	if w := request(map[string]any{"profile": selector("one", 20001), "address": "one.example.com", "address_mode": core.SubStoreAddressModeIPv6}); w.Code != http.StatusOK {
		t.Fatalf("save profile one: %d %s", w.Code, w.Body.String())
	}
	check(map[string]displayProfile{
		"one": {address: "one.example.com", mode: core.SubStoreAddressModeIPv6, overridden: true, host: "one.example.com"},
		"two": {address: address, mode: core.SubStoreAddressModeAuto, host: address},
	})
	if w := request(map[string]any{"profile": selector("two", 20002), "address_mode": core.SubStoreAddressModeAuto}); w.Code != http.StatusOK {
		t.Fatalf("save profile two mode: %d %s", w.Code, w.Body.String())
	}
	check(map[string]displayProfile{
		"one": {address: "one.example.com", mode: core.SubStoreAddressModeIPv6, overridden: true, host: "one.example.com"},
		"two": {address: address, mode: core.SubStoreAddressModeAuto, host: address},
	})
	if w := request(map[string]any{"profile": selector("one", 20001), "address": ""}); w.Code != http.StatusOK {
		t.Fatalf("clear profile one address: %d %s", w.Code, w.Body.String())
	}
	check(map[string]displayProfile{
		"one": {address: address, mode: core.SubStoreAddressModeIPv6, host: address},
		"two": {address: address, mode: core.SubStoreAddressModeAuto, host: address},
	})
	stored, err := db.GetAgent(ctx, agent.ID)
	if err != nil || stored.Labels["client_address"] != address || stored.Labels["client_address_mode"] != core.SubStoreAddressModeIPv4 {
		t.Fatalf("node defaults changed: %+v %v", stored.Labels, err)
	}
}
