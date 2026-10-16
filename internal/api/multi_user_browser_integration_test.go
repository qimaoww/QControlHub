package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestMultiUserBrowserSessions(t *testing.T) {
	if os.Getenv("QCH_MULTI_USER_BROWSER") != "1" {
		t.Skip("enable with QCH_MULTI_USER_BROWSER=1")
	}
	for _, mobile := range []bool{false, true} {
		name := "desktop"
		if mobile {
			name = "mobile"
		}
		t.Run(name, func(t *testing.T) {
			db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
			admin.call("POST", "/users", core.UserRequest{Username: "browser-admin", Role: core.RoleAdmin, Password: "config scope password fixture"}, http.StatusCreated, nil)
			owned := map[string]string{}
			for username, client := range map[string]configScopeAPIClient{"alice": alice, "bob": bob} {
				token, err := db.CreateEnrollmentToken(store.WithConfigScope(ctx, client.userID, false), core.EnrollmentTokenRequest{Name: username + " node"})
				if err != nil {
					t.Fatal(err)
				}
				agent := enrollTaskAPIAgent(t, ctx, db, token.Token, username+" node")
				owned[username] = agent.ID
			}
			shared := enrollConfigScopeAPIAgent(t, ctx, db)
			features := append(shared.Features, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCorePrepare)
			if err := db.Heartbeat(ctx, shared.ID, core.HeartbeatRequest{Features: features}); err != nil {
				t.Fatal(err)
			}
			for index, client := range []configScopeAPIClient{alice, bob} {
				if _, err := db.SetUserAgentAccess(ctx, client.userID, core.AgentAccessRequest{Isolated: true, Shares: []core.AgentShareRequest{{AgentID: shared.ID, Engines: shared.Capabilities, Ports: []int{21001 + index}}}}); err != nil {
					t.Fatal(err)
				}
			}
			fixture := map[string]any{"owned": owned, "shared": shared.ID, "alice": alice.userID, "bob": bob.userID,
				"contentA": configScopeAPIConfig(t, 21001).Content, "contentB": configScopeAPIConfig(t, 21002).Content}
			frontendRoot := filepath.Join("..", "..", "frontend")
			serve := func(root string) *httptest.Server {
				mux := http.NewServeMux()
				mux.Handle("/api/", alice.handler)
				mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(root))))
				mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Cache-Control", "no-store")
					http.ServeFile(w, r, filepath.Join(root, "index.html"))
				})
				return httptest.NewServer(mux)
			}
			server := serve(frontendRoot)
			defer server.Close()
			arguments := []string{filepath.Join(frontendRoot, "multi_user_browser_session.mjs"), server.URL, name}
			// Local evidence may compare main's exact assets against the same
			// API, account, database records and viewport. CI needs no git refs.
			if root := os.Getenv("QCH_MULTI_USER_BASELINE_ROOT"); root != "" {
				baseline := serve(root)
				defer baseline.Close()
				arguments = append(arguments, baseline.URL)
			}
			command := exec.CommandContext(ctx, "node", arguments...)
			encoded, err := json.Marshal(fixture)
			if err != nil {
				t.Fatal(err)
			}
			command.Env = append(os.Environ(), "QCH_MULTI_USER_FIXTURE="+string(encoded))
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("multi-user browser: %v\n%s", err, output)
			}
			t.Log(string(output))
			// The browser intentionally leaves A revoked and B pending. Inspect
			// committed rows as well as browser responses.
			if task, err := db.ClaimTask(ctx, shared.ID); err != nil || task == nil || !task.SharedInstance {
				t.Fatalf("unaffected B task was not claimable: %+v %v", task, err)
			} else if err := db.CompleteTask(ctx, shared.ID, task.ID, core.TaskResultRequest{LeaseID: task.LeaseID, Success: true, TrafficSettled: true}); err != nil {
				t.Fatal(err)
			}
			if task, err := db.ClaimTask(ctx, shared.ID); err != nil || task != nil {
				t.Fatalf("revoked A work survived: %+v %v", task, err)
			}
		})
	}
}
