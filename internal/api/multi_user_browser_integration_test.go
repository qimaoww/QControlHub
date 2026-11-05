package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
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
			var analyticsUser core.User
			admin.call("POST", "/users", core.UserRequest{Username: "analytics-admin", Role: core.RoleAdmin, Password: "config scope password fixture"}, http.StatusCreated, &analyticsUser)
			analyticsAgent := enrollConfigScopeAPIAgent(t, store.WithConfigScope(ctx, analyticsUser.ID, false), db)
			analyticsScope := store.WithConfigScope(ctx, analyticsUser.ID, false)
			// This browser fixture declares the current Agent traffic capability.
			// Runtime/feature state is separate from the metrics sample below.
			analyticsAgent.Features = append(analyticsAgent.Features, "port-traffic-v1")
			if err := db.Heartbeat(analyticsScope, analyticsAgent.ID, core.HeartbeatRequest{Features: analyticsAgent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true}}}); err != nil {
				t.Fatal(err)
			}
			if err := db.UpdateAgentMetrics(analyticsScope, analyticsAgent.ID, core.HostMetrics{
				PublicIPv4: "93.184.216.34", PublicIPv6: "2606:2800:220:1:248:1893:25c8:1946",
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.SetAgentRegionCode(analyticsScope, analyticsAgent.ID, "JP"); err != nil {
				t.Fatal(err)
			}
			analyticsConfig := configScopeAPIConfig(t, 23475)
			analyticsConfig.AgentID = analyticsAgent.ID
			analyticsConfig, err := db.SaveAgentConfig(analyticsScope, analyticsConfig, 0)
			if err != nil {
				t.Fatal(err)
			}
			analyticsDeploy, err := db.CreateTask(analyticsScope, core.TaskRequest{AgentID: analyticsAgent.ID, Engine: analyticsConfig.Engine, Action: core.ActionDeploy, ConfigID: analyticsConfig.ID})
			if err != nil {
				t.Fatal(err)
			}
			completeConfigScopeAPITask(t, analyticsScope, db, analyticsDeploy)
			analyticsXray := enrollAnalyticsXray(t, analyticsScope, db)
			admin.call("POST", "/users", core.UserRequest{Username: "browser-admin", Role: core.RoleAdmin, Password: "config scope password fixture"}, http.StatusCreated, nil)
			admin.call("POST", "/users", core.UserRequest{Username: "no-permissions", Role: core.RoleUser, Password: "config scope password fixture"}, http.StatusCreated, nil)
			owned := map[string]string{}
			for name, permission := range map[string]core.Permission{"task-reader": core.PermissionTasksRead, "traffic-reader": core.PermissionTrafficRead} {
				var user core.User
				admin.call("POST", "/users", core.UserRequest{Username: name, Role: core.RoleUser, Password: "config scope password fixture", Permissions: []core.Permission{permission}}, http.StatusCreated, &user)
				agent := enrollConfigScopeAPIAgent(t, store.WithConfigScope(ctx, user.ID, false), db)
				owned[name] = agent.ID
				if name == "traffic-reader" {
					scope := store.WithConfigScope(ctx, user.ID, false)
					config := configScopeAPIConfig(t, 23476)
					config.AgentID = agent.ID
					config, err = db.SaveAgentConfig(scope, config, 0)
					if err != nil {
						t.Fatal(err)
					}
					deploy, err := db.CreateTask(store.WithConfigScope(ctx, admin.userID, true), core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionDeploy, ConfigID: config.ID})
					if err != nil {
						t.Fatal(err)
					}
					completeConfigScopeAPITask(t, store.WithConfigScope(ctx, admin.userID, true), db, deploy)
				}
			}
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
			fixture := map[string]any{"owned": owned, "analyticsAgent": analyticsAgent.ID, "analyticsXray": analyticsXray.ID, "shared": shared.ID, "alice": alice.userID, "bob": bob.userID,
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

// A saved fixture exercises analytics editing without claiming a live kernel.
func enrollAnalyticsXray(t *testing.T, ctx context.Context, db *store.Store) core.Agent {
	t.Helper()
	token, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "analytics-xray"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "analytics-xray", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineXray}, Features: []string{core.AgentFeatureManagedConfigRead, core.AgentFeatureIndependentEgress}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, token.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	content, err := serverconfig.Generate(core.EngineXray, serverconfig.Input{Protocol: serverconfig.ProtocolVLESS, Tag: "theme-xray", Listen: "0.0.0.0", Port: 23479, Username: "theme-user", Credential: "123e4567-e89b-42d3-a456-426614174000"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Name: "analytics-xray", Engine: core.EngineXray, Content: content}, 0); err != nil {
		t.Fatal(err)
	}
	return agent
}
