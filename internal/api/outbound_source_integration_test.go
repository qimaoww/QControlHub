package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBindingPersistsSourceAndReusesUnchangedTask(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		for _, shared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared=%v", engine, shared), func(t *testing.T) {
				db, ctx, admin, owner, recipient := newConfigScopeAPIFixture(t)
				agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "binding-source", engine)
				client, foreign := owner, recipient
				if shared {
					grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 0)
					client, foreign = recipient, owner
				}
				source := `{"inbounds":[{"tag":"first","port":24111,"protocol":"http"},{"tag":"second","port":24112,"protocol":"http"}],"outbounds":[{"tag":"edge","protocol":"freedom"},{"tag":"same","protocol":"freedom"}],"routing":{"rules":[{"type":"field","domain":["private.example"],"outboundTag":"edge"}]},"exact":9007199254740993}`
				if engine == core.EngineSingBox {
					source = `{"inbounds":[{"tag":"first","listen_port":24111,"type":"socks"},{"tag":"second","listen_port":24112,"type":"socks"}],"outbounds":[{"tag":"edge","type":"direct"},{"tag":"same","type":"direct"}],"route":{"final":"edge","rules":[{"domain":["private.example"],"outbound":"edge"}]},"exact":9007199254740993}`
				}
				if engine == core.EngineMihomo {
					source = "listeners: [{name: first, type: http, port: 24111}, {name: second, type: http, port: 24112}]\nproxies: [{name: edge, type: socks5, server: 127.0.0.1, port: 24443}, {name: same, type: socks5, server: 127.0.0.1, port: 24443}]\nproxy-groups: [{name: selection, type: select, proxies: [edge, same]}]\nrules: ['DOMAIN,private.example,selection','MATCH,DIRECT']\nexact: 9007199254740993\n"
				}
				if shared {
					source = strings.ReplaceAll(source, `,"exact":9007199254740993`, "")
					source = strings.ReplaceAll(source, "exact: 9007199254740993\n", "")
				}
				// Reproduce a historical saved runtime containing verified copies.
				plan, err := serverconfig.PrepareAccounting(engine, source)
				if err != nil {
					t.Fatal(err)
				}
				saved, err := db.SaveAgentConfig(store.WithConfigScope(ctx, client.userID, false), core.Config{AgentID: agent.ID, Engine: engine, Name: "binding-source", Content: plan.Content}, 0)
				if err != nil {
					t.Fatal(err)
				}
				base := "/agents/" + agent.ID + "/configs/" + string(engine)
				var workspace agentConfigWorkspaceResource
				client.call("GET", base+"/workspace", nil, 200, &workspace)
				if strings.Contains(workspace.SourceContent, "qch-trf-") || !strings.Contains(workspace.Config.Content, "qch-trf-") {
					t.Fatal("legacy source/runtime boundary missing")
				}
				intent := "validate"
				submissions := 100
				bind := func(inbound string, port int, target string) configMutationResult {
					t.Helper()
					var result configMutationResult
					payload, _ := json.Marshal(map[string]any{"operation": "bind", "inbound_tag": inbound, "inbound_port": port, "tag": target, "version": saved.Version, "intent": intent})
					request := httptest.NewRequest("POST", "/api/v1"+base+"/outbounds", bytes.NewReader(payload))
					request.AddCookie(client.cookie)
					request.Header.Set(csrfHeader, client.csrf)
					submission := fmt.Sprintf("tsk_%016x", submissions)
					submissions++
					request.Header.Set("X-Config-Task-ID", submission)
					response := httptest.NewRecorder()
					client.handler.ServeHTTP(response, request)
					if response.Code != 200 {
						t.Fatalf("bind: %d %s", response.Code, response.Body.String())
					}
					if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					var receipt core.Task
					client.call("GET", "/tasks/"+submission+"?view=status", nil, 200, &receipt)
					if receipt.ID != result.Task.ID || receipt.ConfigVersion != result.Config.Version || (receipt.ID != submission && receipt.SubmissionID != submission) {
						t.Fatal("lost-response receipt cannot resolve reused binding")
					}
					foreign.call("GET", "/tasks/"+submission+"?view=status", nil, 404, nil)
					saved = result.Config
					return result
				}
				first := bind("first", 24111, "edge")
				if strings.Contains(saved.Content, "qch-trf-") || (!shared && !strings.Contains(saved.Content, "9007199254740993")) || !strings.Contains(saved.Content, "private.example") {
					t.Fatal("saved artifacts or lost native content")
				}
				runtime, err := db.ClaimTask(ctx, agent.ID)
				if err != nil || runtime == nil || !strings.Contains(runtime.ConfigContent, "qch-trf-") {
					t.Fatalf("missing compiled snapshot: %v", err)
				}
				actual, err := serverconfig.PrepareAccounting(engine, runtime.ConfigContent)
				if err != nil || len(actual.Ports) != 2 {
					t.Fatal("invalid runtime", err)
				}
				if err := db.CompleteTask(ctx, agent.ID, runtime.ID, core.TaskResultRequest{LeaseID: runtime.LeaseID, Success: true}); err != nil {
					t.Fatal(err)
				}
				repeated := bind("first", 24111, "edge")
				if repeated.Config.Version != first.Config.Version || repeated.Task.ID != first.Task.ID || !repeated.Task.Reused {
					t.Fatal("repeated binding added a revision/task")
				}
				changed := saved
				changed.Content, err = serverconfig.BindManagedOutbound(engine, saved.Content, "first", 24111, "same", serverconfig.ManagedState{})
				if err != nil {
					t.Fatal(err)
				}
				_, _, err = db.SaveAgentConfigAndTask(store.WithConfigScope(ctx, client.userID, false), changed, saved.Version, store.ConfigMutationOptions{Action: core.ActionValidate, ReuseUnchangedBinding: true, TaskID: "tsk_0000000000000065"})
				if !errors.Is(err, store.ErrConflict) {
					t.Fatalf("receipt collision changed operation: %v", err)
				}
				intent = "deploy"
				deployed := bind("first", 24111, "edge")
				completeConfigScopeAPITask(t, ctx, db, deployed.Task)
				redeployed := bind("first", 24111, "edge")
				if redeployed.Config.Version != deployed.Config.Version || redeployed.Task.ID != deployed.Task.ID || !redeployed.Task.Reused {
					t.Fatal("unchanged applied deployment was duplicated")
				}
				intent = "validate"
				second := bind("second", 24112, "edge")
				completeConfigScopeAPITask(t, ctx, db, second.Task)
				switched := bind("first", 24111, "same")
				completeConfigScopeAPITask(t, ctx, db, switched.Task)
				bindings := serverconfig.ManagedOutboundBindings(engine, saved.Content)
				if bindings["first"] != "same" || bindings["second"] != "edge" {
					t.Fatal("sibling binding lost", bindings)
				}
				sourceObjects, err := serverconfig.ManagedObjects(engine, saved.Content, serverconfig.ManagedState{})
				if err != nil {
					t.Fatal(err)
				}
				exits := 0
				for _, object := range sourceObjects {
					if !object.Inbound {
						exits++
					}
				}
				expected := 2
				if engine == core.EngineMihomo {
					expected = 3
				}
				if exits != expected {
					t.Fatalf("user outbounds changed: %d", exits)
				}
				if !shared {
					foreign.call("GET", base+"/workspace", nil, 404, nil)
				} else {
					var other agentConfigWorkspaceResource
					foreign.call("GET", base+"/workspace", nil, 200, &other)
					if other.Config != nil {
						t.Fatal("source leaked to node owner")
					}
				}
				var other agentConfigWorkspaceResource
				admin.call("GET", base+"/workspace", nil, 200, &other)
				if other.Config != nil {
					t.Fatal("source leaked to administrator")
				}
				client.call("POST", base+"/outbounds", map[string]any{"operation": "bind", "inbound_tag": "first", "inbound_port": 24111, "tag": "edge", "version": first.Config.Version, "intent": "validate"}, 409, nil)
				var current core.Config
				client.call("GET", base, nil, 200, &current)
				if current.Version != saved.Version || current.Content != saved.Content {
					t.Fatal("conflict changed saved source")
				}
			})
		}
	}
}

func expectedTaskContent(t *testing.T, engine core.Engine, source string) string {
	t.Helper()
	plan, err := serverconfig.PrepareIndependentEgress(engine, source)
	if err != nil {
		t.Fatal(err)
	}
	return plan.Content
}
