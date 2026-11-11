package api

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestAccountUIPreferences(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	read := func(client configScopeAPIClient) store.UIPreferences {
		var p store.UIPreferences
		client.call("GET", "/preferences/ui", nil, 200, &p)
		return p
	}
	patch := func(client configScopeAPIClient, values any, revision int64, status int) store.UIPreferences {
		var p store.UIPreferences
		var result any
		if status == 200 {
			result = &p
		}
		client.call("PATCH", "/preferences/ui", map[string]any{"values": values, "revision": revision}, status, result)
		return p
	}
	if p := read(alice); p.Revision != 0 || len(p.Values) != 0 {
		t.Fatalf("default: %+v", p)
	}
	p := patch(alice, map[string]any{"logs": map[string]any{"q": "search", "automatic": false}, "traffic": map[string]any{"scope": "all"}, "live-config": map[string]any{"agent": "agt_0123456789abcdef"}}, 0, 200)
	if p.Revision != 1 || string(p.Values["logs"]["automatic"]) != "false" {
		t.Fatalf("save: %+v", p)
	}
	if p := read(bob); p.Revision != 0 || len(p.Values) != 0 {
		t.Fatal("cross-account leak")
	}
	device, _ := managementLogin(t, alice.handler, "alice", configScopeFixturePassword, 200)
	if string(read(device).Values["logs"]["q"]) != `"search"` {
		t.Fatal("another login did not restore")
	}
	patch(device, map[string]any{"logs": map[string]any{"level": "error"}}, 1, 200)
	patch(alice, map[string]any{"logs": map[string]any{"q": ""}}, 1, 409)
	p = patch(alice, map[string]any{"logs": map[string]any{"q": ""}}, 2, 200)
	if string(p.Values["logs"]["q"]) != `""` || string(p.Values["logs"]["level"]) != `"error"` || string(p.Values["traffic"]["scope"]) != `"all"` {
		t.Fatalf("field merge: %+v", p)
	}
	// Preferences cannot grant authority to referenced nodes or other users' configs.
	alice.call("GET", "/agents/agt_0123456789abcdef/configs/mihomo/workspace", nil, 404, nil)
	for _, values := range []any{
		map[string]any{"live-config": map[string]any{"engine": "mihomo"}},
		map[string]any{"live-config": map[string]any{"content": "secret"}},
		map[string]any{"logs": map[string]any{"q": nil}},
		map[string]any{"logs": map[string]any{"q": strings.Repeat("x", 121)}},
		map[string]any{"logs": map[string]any{"automatic": "false"}},
		map[string]any{"logs": map[string]any{"agent_id": "../other"}},
		map[string]any{"tasks": map[string]any{"limit": "501"}},
		map[string]any{"connections": map[string]any{"date": "2026-02-30"}},
		map[string]any{"connections": map[string]any{"client_ip": "invalid"}},
		map[string]any{"connections": map[string]any{"country_code": "ZZ"}},
		map[string]any{"unknown": map[string]any{"search": "test"}},
		map[string]any{},
	} {
		patch(alice, values, 3, 400)
	}
	alice.call("PATCH", "/preferences/ui", map[string]any{"values": map[string]any{"logs": map[string]any{"q": "x"}}, "revision": 3, "account_id": bob.userID}, 400, nil)
	alice.call("PATCH", "/preferences/ui", map[string]any{"values": map[string]any{"logs": map[string]any{"q": "x"}}}, 400, nil)
	// Owner, shared user, named admin and environment admin have distinct records.
	host := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, host, alice, 21901)
	grantConfigScopeAPIAgent(t, ctx, db, host, bob, 21902)
	patch(bob, map[string]any{"live-config": map[string]any{"agent": host.ID}}, 0, 200)
	patch(admin, map[string]any{"live-config": map[string]any{"agent": host.ID}}, 0, 200)
	workspaceKey, _ := testCreateBusinessKey(t, alice, []core.Permission{core.PermissionAgentConfigRead, core.PermissionPreferencesRead, core.PermissionPreferencesWrite})
	workspaceKey.call("GET", "/agents", nil, 403, nil)
	workspaceKey.call("GET", "/agents/"+host.ID+"/configs/mihomo/workspace", nil, 200, nil)
	if read(workspaceKey).Revision != 3 {
		t.Fatal("workspace-only credential lost account preferences")
	}
	var named core.User
	admin.call("POST", "/users", core.UserRequest{Username: "preferences-admin", Password: configScopeFixturePassword, Role: core.RoleAdmin}, 201, &named)
	namedClient, _ := managementLogin(t, admin.handler, named.Username, configScopeFixturePassword, 200)
	if read(namedClient).Revision != 0 {
		t.Fatal("admin role shared preferences")
	}
	// Delegated keys require their explicit capabilities and inherit the real owner.
	key, _ := testCreateBusinessKey(t, alice, []core.Permission{core.PermissionPreferencesRead, core.PermissionPreferencesWrite})
	if read(key).Revision != 3 {
		t.Fatal("key did not use owner identity")
	}
	patch(key, map[string]any{"nodes": map[string]any{"search": "owner-only"}}, 3, 200)
	if string(read(alice).Values["nodes"]["search"]) != `"owner-only"` {
		t.Fatal("key write missed owner")
	}
	weak, _ := testCreateBusinessKey(t, bob, []core.Permission{core.PermissionAgentsRead})
	weak.call("GET", "/preferences/ui", nil, 403, nil)
	weak.call("PATCH", "/preferences/ui", map[string]any{}, 403, nil)
	for _, method := range []string{"GET", "PATCH"} {
		req := httptest.NewRequest(method, "/api/v1/preferences/ui", nil)
		res := httptest.NewRecorder()
		admin.handler.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous: %d", res.Code)
		}
	}
}

func TestAccountUIPreferencesConcurrentFields(t *testing.T) {
	db, ctx, _, alice, _ := newConfigScopeAPIFixture(t)
	scoped := store.WithConfigScope(ctx, alice.userID, false)
	raw := func(value string) map[string]map[string]json.RawMessage {
		return map[string]map[string]json.RawMessage{"logs": {"q": json.RawMessage(strconv.Quote(value))}}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, value := range []string{"one", "two"} {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := db.PatchUIPreferences(scoped, raw(value), 0)
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	conflicts := 0
	for err := range results {
		if errors.Is(err, store.ErrUIPreferencesConflict) {
			conflicts++
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if conflicts != 1 {
		t.Fatalf("conflicts=%d", conflicts)
	}
	if _, err := db.ReadUIPreferences(ctx); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("unscoped: %v", err)
	}
}

// Opt-in evidence fixture uses the same real PostgreSQL, authentication and
// shared-instance helpers as the existing browser suites.
func TestUIPreferencesBrowserEvidence(t *testing.T) {
	if os.Getenv("QCH_UI_EVIDENCE") != "1" {
		t.Skip("interactive UI preference evidence")
	}
	runUIPreferencesBrowserFixture(t, "")
}

func TestUIPreferencesBrowserSessions(t *testing.T) {
	if os.Getenv("QCH_UI_BROWSER") != "1" {
		t.Skip("opt-in real Chromium sessions")
	}
	for _, binary := range []string{"node", "chromium"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Fatal(err)
		}
	}
	for _, mode := range []string{"ui-preferences-desktop", "ui-preferences-mobile"} {
		t.Run(mode, func(t *testing.T) { runUIPreferencesBrowserFixture(t, mode) })
	}
}

func runUIPreferencesBrowserFixture(t *testing.T, mode string) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixtureWithin(t, 60*time.Minute)
	host := enrollConfigScopeAPIAgent(t, ctx, db)
	own := enrollConfigScopeAPIAgent(t, store.WithConfigScope(ctx, alice.userID, false), db)
	grantConfigScopeAPIAgent(t, ctx, db, host, alice, 21801)
	grantConfigScopeAPIAgent(t, ctx, db, host, bob, 21802)
	for _, item := range []struct {
		client configScopeAPIClient
		agent  string
		port   int
	}{{alice, own.ID, 21803}, {alice, host.ID, 21801}, {bob, host.ID, 21802}} {
		item.client.call("PUT", "/agents/"+item.agent+"/configs/mihomo", configScopeAPIConfig(t, item.port), 200, nil)
	}
	var user core.User
	admin.call("POST", "/users", core.UserRequest{Username: "preferences-admin", Password: configScopeFixturePassword, Role: core.RoleAdmin}, 201, &user)
	var reader core.User
	admin.call("POST", "/users", core.UserRequest{Username: "workspace-reader", Password: configScopeFixturePassword, Role: core.RoleUser, Permissions: []core.Permission{core.PermissionAgentConfigRead, core.PermissionAgentConfigWrite}}, 201, &reader)
	readerClient, _ := managementLogin(t, admin.handler, reader.Username, configScopeFixturePassword, 200)
	readerClient.userID = reader.ID
	grantConfigScopeAPIAgent(t, ctx, db, host, readerClient, 21804)
	readerClient.call("PUT", "/agents/"+host.ID+"/configs/mihomo", configScopeAPIConfig(t, 21804), 200, nil)
	root := filepath.Join("..", "..", "frontend")
	mux := http.NewServeMux()
	mux.Handle("/api/", admin.handler)
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(root))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, filepath.Join(root, "index.html")) })
	address := "127.0.0.1:0"
	if mode == "" {
		address = "127.0.0.1:18589"
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go server.Serve(listener)
	base := "http://" + listener.Addr().String()
	info, _ := json.Marshal(map[string]string{"url": base, "shared": host.ID, "owned": own.ID, "alice": alice.userID, "bob": bob.userID})
	if mode != "" {
		command := exec.CommandContext(ctx, "node", filepath.Join(root, "core_logs_browser_session.mjs"), base, mode)
		command.Env = append(os.Environ(), "QCH_CORE_LOG_FIXTURE="+string(info))
		output, err := command.CombinedOutput()
		t.Log(string(output))
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("QCH_UI_ARTIFACT_DIR"), "fixture.json"), info, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("preference evidence ready: " + string(info))
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(os.Getenv("QCH_UI_ARTIFACT_DIR"), "stop-evidence")); err == nil {
				return
			}
		}
	}
}
