package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestPresetAutoInstallAPI(t *testing.T) {
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
	db, err := store.OpenWithConfigKey(ctx, schema.URL, true, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const token = "auto-install-api-fixture"
	handler := New(db, Config{AdminToken: token}).Handler()
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo, core.EngineShadowsocksRust} {
		for _, installed := range []bool{false, true} {
			t.Run(string(engine)+"/installed="+map[bool]string{false: "no", true: "yes"}[installed], func(t *testing.T) {
				enrollment, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "auto install"})
				if err != nil {
					t.Fatal(err)
				}
				features := []string{core.AgentFeatureIndependentEgress}
				if !installed {
					features = append(features, core.AgentFeaturePresetAutoInstall)
				}
				agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "auto install", OS: "linux", Arch: "amd64",
					Capabilities: []core.Engine{engine}, Features: features, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, enrollment.Token)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: features,
					Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: installed, Version: "development-kept"}}}); err != nil {
					t.Fatal(err)
				}
				input, err := serverconfig.NewPlan(serverconfig.Protocols(engine)[0])
				if err != nil {
					t.Fatal(err)
				}
				call := func(path string, value any, want int) configMutationResult {
					t.Helper()
					body, _ := json.Marshal(value)
					r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
					r.Header.Set("Authorization", "Bearer "+token)
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, r)
					if w.Code != want {
						t.Fatalf("POST %s: %d %s", path, w.Code, w.Body.String())
					}
					var result configMutationResult
					if want == http.StatusOK {
						if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
							t.Fatal(err)
						}
					}
					return result
				}
				base := "/api/v1/agents/" + agent.ID + "/configs/" + string(engine) + "/server-inbounds"
				for _, operation := range []string{"modify", "delete"} {
					call(base, map[string]any{"operation": operation, "install_if_missing": true, "intent": "validate", "input": input}, http.StatusBadRequest)
				}
				if _, err := db.AgentConfig(ctx, agent.ID, engine); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("invalid operation created configuration: %v", err)
				}
				result := call(base, map[string]any{"operation": "add", "install_if_missing": true, "intent": "deploy", "input": input}, http.StatusOK)
				if result.Task.InstallIfMissing != !installed || result.Task.Action != core.ActionDeploy ||
					result.Task.ConfigVersion != result.Config.Version || result.Task.CoreVersion != "" || result.Task.ConfigContent != "" {
					t.Fatalf("incorrect compound operation: %+v", result.Task)
				}
				tasks, err := db.ListTasks(ctx, agent.ID, 100)
				if err != nil || len(tasks) != 1 || tasks[0].ID != result.Task.ID || tasks[0].InstallIfMissing != !installed {
					t.Fatalf("save did not create one durable task: %+v %v", tasks, err)
				}
				// The generic task endpoint cannot smuggle an installation
				// into an arbitrary config task; only atomic add opts in.
				call("/api/v1/tasks", map[string]any{"agent_id": agent.ID, "engine": engine, "action": "validate",
					"config_id": result.Config.ID, "install_if_missing": true}, http.StatusBadRequest)
				current, err := db.GetAgent(ctx, agent.ID)
				if err != nil || current.Runtime[engine].Version != "development-kept" {
					t.Fatal("submitting an inbound optimistically changed the installed version")
				}
			})
		}
	}
}

func TestConfigWorkspaceNativeInboundTargets(t *testing.T) {
	db, ctx, admin, _, _ := newConfigScopeAPIFixture(t)
	agent := enrollConfigScopeAPIAgent(t, ctx, db)
	saved, err := db.SaveAgentConfig(ctx, core.Config{
		AgentID: agent.ID, Engine: core.EngineMihomo, Name: "native listener",
		Content: "listeners:\n  - name: native-socks\n    type: socks\n    port: 21001\n    listen: 0.0.0.0\nrules:\n  - MATCH,DIRECT\n",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	base := "/agents/" + agent.ID + "/configs/mihomo"
	var workspace agentConfigWorkspaceResource
	admin.call(http.MethodGet, base+"/workspace", nil, http.StatusOK, &workspace)
	if len(workspace.Inbounds) != 0 || len(workspace.InboundTargets) != 1 ||
		workspace.InboundTargets[0].Tag != "native-socks" || workspace.InboundTargets[0].Port != 21001 {
		t.Fatalf("non-preset inbound is not safely selectable: %+v", workspace)
	}
	var result configMutationResult
	admin.call(http.MethodPost, base+"/server-inbounds", map[string]any{
		"operation": "delete", "original_tag": "native-socks", "expected_version": saved.Version,
		"intent": "validate", "input": map[string]any{"tag": "native-socks", "port": 21001},
	}, http.StatusOK, &result)
	if result.Config.Version != saved.Version+1 || result.Task.ConfigVersion != result.Config.Version ||
		len(serverconfig.DiscoverMainlandAccessPolicies(core.EngineMihomo, result.Config.Content)) != 0 {
		t.Fatal("native deletion did not atomically save the selected configuration")
	}
}
