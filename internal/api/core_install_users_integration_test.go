package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// Use password-authenticated panel users, including their real session and
// CSRF middleware, rather than compatibility operator tokens.
func TestCoreFirstInstallUserSessions(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo, core.EngineShadowsocksRust} {
		t.Run(string(engine), func(t *testing.T) {
			ownerCtx := store.WithConfigScope(ctx, alice.userID, false)
			token, err := db.CreateEnrollmentToken(ownerCtx, core.EnrollmentTokenRequest{Name: "first-install"})
			if err != nil {
				t.Fatal(err)
			}
			features := []string{core.AgentFeatureIndependentEgress, core.AgentFeaturePresetAutoInstall,
				core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureSharedCoreInstances,
				core.AgentFeatureSharedCorePrepare, core.AgentFeatureSharedCoreLifecycle}
			agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "first-install", OS: "linux", Arch: "amd64",
				Capabilities: []core.Engine{engine}, Features: features,
				PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, token.Token)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: features,
				Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: false}}}); err != nil {
				t.Fatal(err)
			}
			var agents []core.Agent
			alice.call("GET", "/agents", nil, http.StatusOK, &agents)
			var visible core.Agent
			for _, item := range agents {
				if item.ID == agent.ID {
					visible = item
				}
			}
			if !visible.CanManage {
				t.Fatalf("incorrect owner scope: %+v", visible)
			}
			var task core.Task
			alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: engine,
				Action: core.ActionInstall, CoreVersion: "stable"}, http.StatusCreated, &task)
			completeConfigScopeAPITask(t, ctx, db, task)
			// The owner can already install manually on main. Keep that contract.
			admin.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: engine,
				Action: core.ActionInstall, CoreVersion: "stable"}, http.StatusCreated, &task)
			completeConfigScopeAPITask(t, ctx, db, task)
			input, err := serverconfig.NewPlan(serverconfig.Protocols(engine)[0])
			if err != nil {
				t.Fatal(err)
			}
			input.Port = 21000
			var ownerResult configMutationResult
			alice.call("POST", "/agents/"+agent.ID+"/configs/"+string(engine)+"/server-inbounds", map[string]any{
				"operation": "add", "intent": "validate", "install_if_missing": true, "input": input,
			}, http.StatusOK, &ownerResult)
			if !ownerResult.Task.InstallIfMissing || ownerResult.Task.SharedInstance || ownerResult.Task.ConfigVersion != ownerResult.Config.Version {
				t.Fatalf("lost owner preparation: %+v", ownerResult.Task)
			}
			completeConfigScopeAPITask(t, ctx, db, ownerResult.Task)
			grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21001)
			bob.call("GET", "/agents", nil, http.StatusOK, &agents)
			for _, item := range agents {
				if item.ID == agent.ID {
					visible = item
				}
			}
			if visible.CanManage {
				t.Fatal("share granted host administration")
			}
			bob.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: engine,
				Action: core.ActionInstall, CoreVersion: "stable"}, http.StatusCreated, &task)
			if !task.SharedInstance || task.SharedTrafficID == "" {
				t.Fatal("manual preparation lost share identity")
			}
			completeConfigScopeAPITask(t, ctx, db, task)
			bob.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: engine,
				Action: core.ActionInstall, CoreVersion: "v1.19.29"}, http.StatusForbidden, nil)
			bob.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: engine, Action: core.ActionUninstall}, http.StatusForbidden, nil)
			for _, action := range []core.Action{core.ActionStop, core.ActionStart, core.ActionRestart} {
				bob.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: engine, Action: action}, http.StatusConflict, nil)
			}
			input, err = serverconfig.NewPlan(serverconfig.Protocols(engine)[0])
			if err != nil {
				t.Fatal(err)
			}
			input.Port = 21001
			var result configMutationResult
			bob.call("POST", "/agents/"+agent.ID+"/configs/"+string(engine)+"/server-inbounds", map[string]any{
				"operation": "add", "intent": "validate", "install_if_missing": true, "input": input,
			}, http.StatusOK, &result)
			if !result.Task.InstallIfMissing || !result.Task.SharedInstance || result.Task.SharedTrafficID == "" ||
				result.Task.ConfigVersion != result.Config.Version {
				t.Fatalf("lost isolated preparation: %+v", result.Task)
			}
			completeConfigScopeAPITask(t, ctx, db, result.Task)
			bob.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: engine, Action: core.ActionDeploy,
				ConfigID: result.Config.ID, ExpectedConfigVersion: result.Config.Version, InstallIfMissing: true}, http.StatusCreated, &task)
			if !task.SharedInstance || task.SharedTrafficID != result.Task.SharedTrafficID || task.ConfigContent != result.Task.ConfigContent {
				t.Fatal("private deployment lost validated snapshot or instance")
			}
			completeConfigScopeAPITask(t, ctx, db, task)
		})
	}
}

func TestCoreInstallExecuteAndReadPermissionsAreIndependent(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	token, err := db.CreateEnrollmentToken(store.WithConfigScope(ctx, alice.userID, false), core.EnrollmentTokenRequest{Name: "permission matrix"})
	if err != nil {
		t.Fatal(err)
	}
	agent := enrollTaskAPIAgent(t, ctx, db, token.Token, "permission matrix")
	sharedToken, err := db.CreateEnrollmentToken(store.WithConfigScope(ctx, bob.userID, false), core.EnrollmentTokenRequest{Name: "shared permission matrix"})
	if err != nil {
		t.Fatal(err)
	}
	shared := enrollTaskAPIAgent(t, ctx, db, sharedToken.Token, "shared permission matrix")
	if err := db.Heartbeat(ctx, shared.ID, core.HeartbeatRequest{Features: []string{core.AgentFeatureIndependentEgress, core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCorePrepare}}); err != nil {
		t.Fatal(err)
	}
	grantConfigScopeAPIAgent(t, ctx, db, shared, alice, 21001)
	for _, test := range []struct {
		name                  string
		execute, read, manage bool
	}{
		{"execute only", true, false, false}, {"read only", false, true, false},
		{"manage only", false, false, true}, {"execute and read", true, true, false}, {"all", true, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			permissions := []core.Permission{core.PermissionAgentsRead}
			if test.execute {
				permissions = append(permissions, core.PermissionTasksExecute)
			}
			if test.read {
				permissions = append(permissions, core.PermissionTasksRead)
			}
			if test.manage {
				permissions = append(permissions, core.PermissionAgentsManage)
			}
			// Permission updates invalidate previous sessions. A fresh password login
			// must return exactly the durable grants, with no role-based expansion.
			admin.call("PUT", "/users/"+alice.userID, core.UserUpdate{Permissions: &permissions}, http.StatusOK, nil)
			alice = loginCoreInstallUser(t, alice, "alice")
			var session struct {
				Role        core.Role         `json:"role"`
				Permissions []core.Permission `json:"permissions"`
			}
			alice.call("GET", "/auth/session", nil, http.StatusOK, &session)
			if session.Role != core.RoleUser || core.HasPermission(session.Permissions, core.PermissionTasksExecute) != test.execute ||
				core.HasPermission(session.Permissions, core.PermissionTasksRead) != test.read {
				t.Fatalf("stale session permissions: %+v", session)
			}
			status := http.StatusForbidden
			if test.execute {
				status = http.StatusCreated
			}
			for _, target := range []core.Agent{agent, shared} {
				var task core.Task
				alice.call("POST", "/tasks", core.TaskRequest{AgentID: target.ID, Engine: core.EngineMihomo, Action: core.ActionInstall, CoreVersion: "stable"}, status, &task)
				if test.execute {
					want := http.StatusForbidden
					if test.read {
						want = http.StatusOK
					}
					alice.call("GET", "/tasks/"+task.ID, nil, want, nil)
					completeConfigScopeAPITask(t, ctx, db, task)
				}
			}
		})
	}
}

func loginCoreInstallUser(t *testing.T, client configScopeAPIClient, username string) configScopeAPIClient {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "token": "config scope password fixture"})
	request := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	client.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("password login: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	client.cookie = response.Result().Cookies()[0]
	client.csrf = result.CSRF
	return client
}

// Opt in alongside the browser matrix. This fixture uses real database users,
// cookies and CSRF requests; only the remote node completion is deterministic.
func TestCoreFirstInstallBrowserSessions(t *testing.T) {
	if os.Getenv("QCH_CORE_INSTALL_BROWSER") != "1" {
		t.Skip("enable with QCH_CORE_INSTALL_BROWSER=1")
	}
	for _, username := range []string{"alice", "bob"} {
		for _, mobile := range []bool{false, true} {
			t.Run(username+map[bool]string{false: "/desktop", true: "/mobile"}[mobile], func(t *testing.T) {
				db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
				for _, client := range []*configScopeAPIClient{&admin, &alice, &bob} {
					client.t = t
				}
				token, err := db.CreateEnrollmentToken(store.WithConfigScope(ctx, alice.userID, false), core.EnrollmentTokenRequest{Name: "browser install"})
				if err != nil {
					t.Fatal(err)
				}
				features := []string{core.AgentFeatureIndependentEgress, core.AgentFeaturePresetAutoInstall, core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCorePrepare}
				agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "Browser install", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineMihomo, core.EngineXray}, Features: features, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, token.Token)
				if err != nil {
					t.Fatal(err)
				}
				runtime := map[core.Engine]core.RuntimeState{}
				if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: features, Runtime: runtime}); err != nil {
					t.Fatal(err)
				}
				if username == "bob" {
					grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 0)
				}
				done := make(chan struct{})
				defer close(done)
				go func() {
					ticker := time.NewTicker(100 * time.Millisecond)
					defer ticker.Stop()
					installs := 0
					for {
						select {
						case <-done:
							return
						case <-ticker.C:
							task, err := db.ClaimTask(ctx, agent.ID)
							if err != nil || task == nil {
								continue
							}
							result := core.TaskResultRequest{LeaseID: task.LeaseID, Success: true, TrafficSettled: true}
							if task.Action == core.ActionInstall {
								installs++
								if installs == 1 {
									result.Success = false
									result.Error = "fixture download failure: " + strings.Repeat("download checksum diagnostic ", 12)
								}
							}
							if result.Success && (task.Action == core.ActionInstall || task.InstallIfMissing) {
								runtime[task.Engine] = core.RuntimeState{Installed: true, Version: "1.19.30", ServiceStatus: "active"}
								_ = db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: features, Runtime: runtime})
							}
							_ = db.CompleteTask(ctx, agent.ID, task.ID, result)
						}
					}
				}()
				frontendRoot := filepath.Join("..", "..", "frontend")
				mux := http.NewServeMux()
				mux.Handle("/api/", alice.handler)
				mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(frontendRoot))))
				mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
					content, err := os.ReadFile(filepath.Join(frontendRoot, "index.html"))
					if err != nil {
						http.Error(w, err.Error(), 500)
						return
					}
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					content = bytes.Replace(content, []byte("</body>"), []byte(`<script type="module" src="/assets/core_install_browser_flow.mjs"></script></body>`), 1)
					_, _ = w.Write(content)
				})
				server := httptest.NewServer(mux)
				defer server.Close()
				arguments := []string{filepath.Join(frontendRoot, "core_install_browser_session.mjs"), server.URL + "/?user=" + username + "&agent=" + agent.ID}
				if mobile {
					arguments = append(arguments, "--mobile")
				}
				command := exec.CommandContext(ctx, "node", arguments...)
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("browser session: %v\n%s", err, output)
				}
				t.Log(string(output))
				tasks, err := db.ListTasks(ctx, agent.ID, 100)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, task := range tasks {
					if task.InstallIfMissing {
						found = true
						if task.Status != core.TaskSucceeded || task.ConfigVersion < 1 || task.SharedInstance != (username == "bob") {
							t.Fatalf("incorrect browser preparation task: %+v", task)
						}
					}
				}
				if !found {
					t.Fatal("browser never submitted automatic installation")
				}
			})
		}
	}
}
