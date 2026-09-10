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
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestPresetSaveKeepsConnectionAndIndependentExitsWithPostgreSQL(t *testing.T) {
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
	enroll := func(engine core.Engine) core.Agent {
		t.Helper()
		token, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "preset regression"})
		if err != nil {
			t.Fatal(err)
		}
		agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "presets", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{engine}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, token.Token)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true}}}); err != nil {
			t.Fatal(err)
		}
		return agent
	}
	// A node outside the save scope has a discovered monitor with no config.
	// Fleet-wide pruning would remove this record during someone else's save.
	other := enroll(core.EngineXray)
	endpoint := core.PortTrafficEndpoint{AgentID: other.ID, Engine: core.EngineXray, Name: "unrelated", Port: 25001, Protocol: core.TrafficProtocolBoth}
	if _, err := db.ReconcilePortTrafficEndpoints(ctx, []core.PortTrafficEndpoint{endpoint}, false); err != nil {
		t.Fatal(err)
	}
	const token = "preset-save-integration-token"
	server := New(db, Config{AdminToken: token})
	handler := server.Handler()
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo, core.EngineShadowsocksRust} {
		t.Run(string(engine), func(t *testing.T) {
			agent := enroll(engine)
			cancelled := false
			updates := server.registerConnection(agent.ID, "preset-test", func() { cancelled = true })
			defer server.unregisterConnection(agent.ID, "preset-test")
			input, err := serverconfig.NewPlan(serverconfig.Protocols(engine)[0])
			if err != nil {
				t.Fatal(err)
			}
			version := 0
			for _, step := range []struct {
				operation, tag, original, intent string
				port, ports                      int
			}{
				{"add", "first", "", "validate", 21001, 1},
				{"add", "second", "", "deploy", 21002, 2},
				{"modify", "renamed", "second", "deploy", 21003, 2},
				{"delete", "renamed", "renamed", "validate", 21003, 1},
			} {
				input.Tag, input.Port = step.tag, step.port
				if step.operation == "delete" {
					// Only original_tag is authoritative; old credentials and
					// current draft parameters need not remain valid.
					input = serverconfig.Input{}
				}
				body, _ := json.Marshal(map[string]any{"operation": step.operation, "original_tag": step.original,
					"expected_version": version, "intent": step.intent, "preserve_ss_rust_globals": true, "input": input})
				r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agent.ID+"/configs/"+string(engine)+"/server-inbounds", bytes.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				var result configMutationResult
				if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
					t.Fatalf("%s: %d %s", step.operation, w.Code, w.Body.String())
				}
				version++
				if result.Config.Version != version || result.Task.ConfigVersion != version || string(result.Task.Action) != step.intent || result.Task.ConfigContent != "" {
					t.Fatalf("save did not enqueue its exact revision: %+v", result.Task)
				}
				compiled, err := serverconfig.PrepareAccounting(engine, result.Config.Content)
				if err != nil || compiled.Content != result.Config.Content || len(compiled.Ports) != step.ports {
					t.Fatalf("saved config does not contain current independent exits: %v", err)
				}
				if len(compiled.Ports) == 2 {
					if compiled.Source == "core-api" && compiled.Ports[0].Outbounds[0] == compiled.Ports[1].Outbounds[0] {
						t.Fatal("two listeners share an outbound")
					}
					if compiled.Source == "nft-dual" && compiled.Ports[0].Mark == compiled.Ports[1].Mark {
						t.Fatal("two listeners share a socket mark")
					}
				}
				if cancelled {
					t.Fatal("saving disconnected the Agent before task delivery")
				}
				select {
				case <-updates:
				default:
					t.Fatal("changed monitoring policies were not pushed to the existing connection")
				}
				policies, err := db.AgentPortTrafficPolicies(ctx, other.ID)
				if err != nil || len(policies) != 1 || policies[0].Port != endpoint.Port {
					t.Fatalf("save touched an unrelated node: %+v %v", policies, err)
				}
			}
			base := "/api/v1/agents/" + agent.ID + "/configs/" + string(engine)
			call := func(suffix string, body any, want int) configMutationResult {
				t.Helper()
				encoded, _ := json.Marshal(body)
				r := httptest.NewRequest(http.MethodPost, base+suffix, bytes.NewReader(encoded))
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("%s: %d %s, want %d", suffix, w.Code, w.Body.String(), want)
				}
				var result configMutationResult
				if want == http.StatusOK {
					if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					version++
					if result.Config.Version != version || result.Task.ConfigVersion != version || result.Task.ConfigContent != "" {
						t.Fatal("response is not an exact-version sanitized result")
					}
				}
				return result
			}
			current, err := db.AgentConfig(ctx, agent.ID, engine)
			if err != nil {
				t.Fatal(err)
			}
			from, to := `"port": 21001`, `"port": 22001`
			if engine == core.EngineSingBox {
				from, to = `"listen_port": 21001`, `"listen_port": 22001`
			}
			if engine == core.EngineShadowsocksRust {
				from, to = `"server_port": 21001`, `"server_port": 22001`
			}
			if engine == core.EngineMihomo {
				from, to = `port: 21001`, `port: 22001`
			}
			source := map[string]any{"name": current.Name, "content": strings.Replace(current.Content, from, to, 1), "version": version, "intent": "deploy"}
			updated := call("/source", source, http.StatusOK)
			statusRequest := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+updated.Task.ID+"?view=status", nil)
			statusRequest.Header.Set("Authorization", "Bearer "+token)
			statusResponse := httptest.NewRecorder()
			handler.ServeHTTP(statusResponse, statusRequest)
			var status core.Task
			if statusResponse.Code != http.StatusOK || json.Unmarshal(statusResponse.Body.Bytes(), &status) != nil || status.ConfigVersion != version || status.ID != updated.Task.ID || status.Output != "" {
				t.Fatal("compact task state does not match saved task")
			}
			if engine == core.EngineXray {
				protocol, ok := serverconfig.FindProtocol(engine, serverconfig.ProtocolVLESS)
				if !ok {
					t.Fatal("missing Reality preset")
				}
				duplicate, err := serverconfig.NewPlan(protocol)
				if err != nil {
					t.Fatal(err)
				}
				duplicate.Tag, duplicate.Port, duplicate.RealityServerName = "first", 22001, "unreachable.example.test"
				body, _ := json.Marshal(map[string]any{"operation": "add", "expected_version": version, "intent": "deploy", "input": duplicate})
				r := httptest.NewRequest(http.MethodPost, base+"/server-inbounds", bytes.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "first") || strings.Contains(w.Body.String(), "TLS") || strings.Contains(w.Body.String(), "DNS") {
					t.Fatalf("duplicate identity waited for a network probe: %d %s", w.Code, w.Body.String())
				}
			}
			compiled, err := serverconfig.PrepareAccounting(engine, updated.Config.Content)
			if err != nil || len(compiled.Ports) != 1 || compiled.Ports[0].Port != 22001 || compiled.Content != updated.Config.Content {
				t.Fatalf("source did not replan: %+v %v", compiled.Ports, err)
			}
			call("/source", source, http.StatusConflict)
			call("/server-inbounds", map[string]any{"operation": "add", "original_tag": "first", "expected_version": version, "intent": "deploy"}, http.StatusBadRequest)
			field, fragment := "log", `{"level":"warn"}`
			if engine == core.EngineXray {
				fragment = `{"loglevel":"warning"}`
			}
			if engine == core.EngineMihomo {
				field, fragment = "log-level", "warning"
			}
			if engine == core.EngineShadowsocksRust {
				field, fragment = "no_delay", "false"
			}
			fields := map[string]any{"mutation": "modify", "fragment": fragment, "expected_version": version, "intent": "validate"}
			updated = call("/fields/"+field, fields, http.StatusOK)
			call("/fields/"+field, fields, http.StatusConflict)
			compiled, err = serverconfig.PrepareAccounting(engine, updated.Config.Content)
			if err != nil || compiled.Content != updated.Config.Content {
				t.Fatalf("field edit lost independent exits: %v", err)
			}
			// The runtime gate rejects task creation after the store has saved
			// the candidate inside its transaction. No revision may leak out.
			if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true, ExistingConfigUnsupportedReason: "test unsafe service"}}}); err != nil {
				t.Fatal(err)
			}
			call("/source", map[string]any{"name": current.Name, "content": updated.Config.Content, "version": version, "intent": "deploy"}, http.StatusConflict)
			current, err = db.AgentConfig(ctx, agent.ID, engine)
			if err != nil || current.Version != version || current.Content != updated.Config.Content {
				t.Fatal("failed task changed saved revision")
			}
			if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true}}}); err != nil {
				t.Fatal(err)
			}
			removed := call("/server-inbounds", map[string]any{"operation": "delete", "original_tag": "first", "expected_version": version, "intent": "validate"}, http.StatusOK)
			if len(serverconfig.DiscoverTrafficPorts(engine, removed.Config.Content)) != 0 || strings.Contains(removed.Config.Content, "qch-trf-") || strings.Contains(removed.Config.Content, "outbound_fwmark") {
				t.Fatal("last inbound deletion left stale attribution")
			}
			if _, err := db.ReconcileAgentPortTrafficEndpoints(ctx, agent.ID, []core.PortTrafficEndpoint{endpoint}, true); err == nil {
				t.Fatal("scoped reconciliation accepted a foreign node's endpoint")
			}
		})
	}
}

func TestPresetMutationsRequireBothWriteAndExecute(t *testing.T) {
	for _, permission := range []core.Permission{core.PermissionAgentConfigWrite, core.PermissionTasksExecute} {
		server := New(nil, Config{AdminToken: "test-admin"})
		server.roleTokens[sha256.Sum256([]byte("limited-token"))] = tokenPrincipal{Role: core.RoleUser, Permissions: []core.Permission{permission}}
		for _, path := range []string{"server-inbounds", "source", "fields/log"} {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/test/configs/xray/"+path, nil)
			r.Header.Set("Authorization", "Bearer limited-token")
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s with only %s = %d", path, permission, w.Code)
			}
		}
	}
}
