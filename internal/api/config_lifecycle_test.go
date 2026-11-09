package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

// Exercise the actual transactional API for every catalog entry. Every step
// reads its revision back, compares the neighboring listener, and checks the
// execution snapshot, rather than asserting only a successful HTTP response.
func TestCatalogInboundLifecycle(t *testing.T) {
	for _, engine := range core.AllEngines() {
		for _, shared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared=%v", engine, shared), func(t *testing.T) {
				db, ctx, admin, owner, recipient := newConfigScopeAPIFixture(t)
				node, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "lifecycle", engine)
				node.Features = append(node.Features, core.AgentFeatureXHTTP, core.AgentFeatureXHTTPTLS, core.AgentFeatureInstanceStatus, core.AgentFeatureManagedTLS, core.AgentFeatureManagedTLSCertificates, core.AgentFeatureManagedTLSH2)
				if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: node.Features, Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true, ServiceStatus: "inactive"}}}); err != nil {
					t.Fatal(err)
				}
				client, foreign := owner, recipient
				if shared {
					grantConfigScopeAPIAgent(t, ctx, db, node, recipient, 0)
					client, foreign = recipient, owner
				}
				base := "/agents/" + node.ID + "/configs/" + string(engine)
				var saved configMutationResult
				finish := func() { t.Helper(); completeConfigScopeAPITask(t, ctx, db, saved.Task) }
				mutate := func(operation, original string, input serverconfig.Input) {
					t.Helper()
					client.call("POST", base+"/server-inbounds", map[string]any{"operation": operation, "original_tag": original, "expected_version": saved.Config.Version, "intent": "deploy", "input": input, "preserve_ss_rust_globals": true}, http.StatusOK, &saved)
					finish()
				}
				for _, protocol := range serverconfig.Protocols(engine) {
					t.Run(protocol.Key, func(t *testing.T) {
						client.t, foreign.t = t, t
						input, err := serverconfig.NewPlan(protocol)
						if err != nil {
							t.Fatal(err)
						}
						input.Tag, input.Port = "selected", 24111
						input.TLSMode, input.TLSServerName = "native", "fixture.example.test"
						input.CertificatePath, input.PrivateKeyPath = "/tmp/qch-fixture.crt", "/tmp/qch-fixture.key"
						input.RealityServerName = "www.amazon.com"
						if input.RealityTarget == "" {
							input.RealityTarget = "www.amazon.com:443"
						}
						mutate("add", "", input)
						var workspace agentConfigWorkspaceResource
						client.call("GET", base+"/workspace", nil, 200, &workspace)
						if len(workspace.Inbounds) != 1 || workspace.Inbounds[0].Tag != input.Tag || workspace.Inbounds[0].Protocol != input.Protocol {
							t.Fatalf("preset did not round trip: %+v", workspace.Inbounds)
						}
						foreign.call("GET", "/configs/"+saved.Config.ID+"/revisions", nil, 404, nil)
						admin.call("GET", base, nil, 404, nil)
						siblingProtocol, _ := serverconfig.FindProtocol(engine, "ss2022")
						sibling, err := serverconfig.NewPlan(siblingProtocol)
						if err != nil {
							t.Fatal(err)
						}
						sibling.Tag, sibling.Port = "neighbor", 24112
						mutate("add", "", sibling)
						input.Tag, input.Port = "renamed", 24113
						mutate("modify", "selected", input)
						var objects []serverconfig.ManagedObject
						client.call("GET", base+"/workspace", nil, 200, &workspace)
						objects = workspace.ManagedObjects
						var section string
						for _, object := range objects {
							if object.Inbound && object.Tag == input.Tag {
								section = object.Section
							}
						}
						if section == "" {
							t.Fatal("renamed identity disappeared")
						}
						disabled := []serverconfig.ManagedResource{{Section: section, Tag: input.Tag}}
						client.call("PUT", base+"/management", map[string]any{"version": saved.Config.Version, "intent": "deploy", "disabled": disabled}, 200, &saved)
						finish()
						input.Port = 24114
						mutate("modify", input.Tag, input)
						state, err := db.ConfigManagementState(store.WithConfigScope(ctx, client.userID, false), saved.Config.ID, saved.Config.Version)
						if err != nil || len(state.Disabled) != 1 {
							t.Fatalf("edit reenabled source: %+v %v", state, err)
						}
						client.call("PUT", base+"/management", map[string]any{"version": saved.Config.Version, "intent": "deploy", "disabled": []any{}}, 200, &saved)
						finish()
						inputs := serverconfig.ParseAll(engine, saved.Config.Content)
						if len(inputs) != 2 {
							t.Fatalf("neighbor lost: %+v", inputs)
						}
						for _, parsed := range inputs {
							if parsed.Tag == sibling.Tag && (parsed.Port != sibling.Port || parsed.Credential != sibling.Credential) {
								t.Fatal("neighbor changed")
							}
						}
						mutate("delete", input.Tag, serverconfig.Input{})
						mutate("delete", sibling.Tag, serverconfig.Input{})
						if len(serverconfig.ParseAll(engine, saved.Config.Content)) != 0 {
							t.Fatal("delete leaked a listener")
						}
						if strings.Contains(saved.Config.Content, "qch-trf-") {
							t.Fatal("last delete leaked accounting copies")
						}
					})
				}
			})
		}
	}
}

func TestOutboundLifecycleAcrossNativeEnginesAndScopes(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo} {
		for _, shared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared=%v", engine, shared), func(t *testing.T) {
				db, ctx, admin, owner, recipient := newConfigScopeAPIFixture(t)
				node, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "outbound lifecycle", engine)
				client, foreign := owner, recipient
				if shared {
					grantConfigScopeAPIAgent(t, ctx, db, node, recipient, 24111, 24112)
					client, foreign = recipient, owner
				}
				base := "/agents/" + node.ID + "/configs/" + string(engine)
				// Standalone creation works before any inbound or prior configuration.
				section, key, kind := "outbounds", "tag", "type"
				direct := `{"tag":"original","type":"direct"}`
				if engine == core.EngineXray {
					kind = "protocol"
					direct = `{"tag":"original","protocol":"freedom"}`
				}
				if engine == core.EngineMihomo {
					section, key = "proxies", "name"
					direct = `{"name":"original","type":"direct"}`
				}
				var saved configMutationResult
				client.call("POST", base+"/outbounds", map[string]any{"version": 0, "intent": "validate", "section": section, "operation": "add", "fragment": direct}, 200, &saved)
				completeConfigScopeAPITask(t, ctx, db, saved.Task)
				// Add native listeners, then preserve conditional routing throughout edits.
				source := `{"inbounds":[{"tag":"first","type":"http","listen":"127.0.0.1","listen_port":24111},{"tag":"neighbor","type":"http","listen":"127.0.0.1","listen_port":24112}],"outbounds":[{"tag":"original","type":"direct"}],"route":{"final":"original","rules":[{"domain":["private.example"],"outbound":"original"}]},"future":9007199254740993}`
				if engine == core.EngineXray {
					source = `{"inbounds":[{"tag":"first","protocol":"http","listen":"127.0.0.1","port":24111},{"tag":"neighbor","protocol":"http","listen":"127.0.0.1","port":24112}],"outbounds":[{"tag":"original","protocol":"freedom"}],"routing":{"rules":[{"type":"field","domain":["private.example"],"outboundTag":"original"}]},"future":9007199254740993}`
				}
				if engine == core.EngineMihomo {
					source = "listeners: [{name: first, type: http, listen: 127.0.0.1, port: 24111}, {name: neighbor, type: http, listen: 127.0.0.1, port: 24112}]\nproxies: [{name: original, type: direct}]\nrules: ['DOMAIN,private.example,original','MATCH,original']\nfuture: 9007199254740993\n"
				}
				if shared {
					// Unknown root listeners cannot bypass shared-instance isolation.
					client.call("POST", base+"/source", map[string]any{"name": "outbound lifecycle", "version": saved.Config.Version, "intent": "validate", "content": source}, 400, nil)
					source = strings.ReplaceAll(source, `,"future":9007199254740993`, "")
					source = strings.ReplaceAll(source, "future: 9007199254740993\n", "")
				}
				client.call("POST", base+"/source", map[string]any{"name": "outbound lifecycle", "version": saved.Config.Version, "intent": "validate", "content": source}, 200, &saved)
				completeConfigScopeAPITask(t, ctx, db, saved.Task)
				protocols := []string{"direct", "vless", "vmess", "trojan", "shadowsocks", "socks", "http"}
				if engine != core.EngineXray {
					protocols = append(protocols, "hysteria2", "tuic", "anytls")
				}
				for _, protocol := range protocols {
					t.Run(protocol, func(t *testing.T) {
						client.t, foreign.t = t, t
						object := map[string]any{key: "edge", kind: protocol, "future": map[string]any{"nested": "keep"}, "exact": json.Number("9007199254740993")}
						if engine == core.EngineMihomo {
							if protocol == "shadowsocks" {
								object[kind] = "ss"
							}
							if protocol == "socks" {
								object[kind] = "socks5"
							}
						}
						if protocol == "direct" && engine == core.EngineXray {
							object[kind] = "freedom"
						}
						if engine == core.EngineXray && protocol != "direct" {
							object["settings"] = map[string]any{"servers": []any{map[string]any{"address": "127.0.0.1", "port": 24443}}}
						}
						if engine != core.EngineXray && protocol != "direct" {
							object["server"] = "127.0.0.1"
							object["server_port"] = 24443
							if engine == core.EngineMihomo {
								delete(object, "server_port")
								object["port"] = 24443
							}
						}
						fragment, _ := json.Marshal(object)
						submit := func(operation, tag string, extra map[string]any, status int) {
							t.Helper()
							body := map[string]any{"version": saved.Config.Version, "intent": "validate", "section": section, "operation": operation, "tag": tag, "fragment": string(fragment)}
							for k, v := range extra {
								body[k] = v
							}
							var result configMutationResult
							client.call("POST", base+"/outbounds", body, status, &result)
							if status == 200 {
								saved = result
								completeConfigScopeAPITask(t, ctx, db, saved.Task)
							}
						}
						submit("add", "", map[string]any{"inbound_tag": "first", "inbound_port": 24111}, 200)
						submit("bind", "edge", map[string]any{"inbound_tag": "neighbor", "inbound_port": 24112, "fragment": ""}, 200)
						before := saved.Config.Version
						foreign.call("GET", base+"/outbounds/"+section+"/edge", nil, 404, nil)
						admin.call("GET", base+"/outbounds/"+section+"/edge", nil, 404, nil)
						object[key] = "renamed"
						fragment, _ = json.Marshal(object)
						submit("modify", "edge", nil, 200)
						if saved.Config.Version != before+1 {
							t.Fatal("rename saved twice")
						}
						if !strings.Contains(saved.Config.Content, "9007199254740993") || !strings.Contains(saved.Config.Content, "nested") {
							t.Fatal("native outbound extensions lost during rename")
						}
						bindings := serverconfig.ManagedOutboundBindings(engine, saved.Config.Content)
						if bindings["first"] != "renamed" || bindings["neighbor"] != "renamed" {
							t.Fatal("shared rename left stale selectors", bindings)
						}
						disabled := []serverconfig.ManagedResource{{Section: section, Tag: "renamed"}}
						management := map[string]any{"version": saved.Config.Version, "intent": "validate", "disabled": disabled, "outbound_tag": "renamed"}
						client.call("PUT", base+"/management", management, 400, nil)
						management["replacement_tag"] = "original"
						client.call("PUT", base+"/management", management, 200, &saved)
						completeConfigScopeAPITask(t, ctx, db, saved.Task)
						management["version"] = saved.Config.Version
						management["disabled"] = []any{}
						delete(management, "replacement_tag")
						client.call("PUT", base+"/management", management, 200, &saved)
						completeConfigScopeAPITask(t, ctx, db, saved.Task)
						submit("delete", "renamed", map[string]any{"fragment": ""}, 200)
						if !shared && !strings.Contains(saved.Config.Content, "9007199254740993") || !strings.Contains(saved.Config.Content, "private.example") {
							t.Fatal("unrelated native source lost")
						}
						bindings = serverconfig.ManagedOutboundBindings(engine, saved.Config.Content)
						if bindings["first"] != "original" || bindings["neighbor"] != "original" {
							t.Fatal("explicit replacement not retained", bindings)
						}
					})
				}
			})
		}
	}
}

func TestOutboundReadEditFromScopedNativeSnapshot(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo} {
		for _, shared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/shared=%v", engine, shared), func(t *testing.T) {
				db, ctx, _, owner, recipient := newConfigScopeAPIFixture(t)
				node, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "outbound read", engine)
				client, foreign := owner, recipient
				if shared {
					grantConfigScopeAPIAgent(t, ctx, db, node, recipient, 24111)
					client, foreign = recipient, owner
				}
				base := "/agents/" + node.ID + "/configs/" + string(engine)
				content := `{"inbounds":[{"tag":"manual","type":"http","listen_port":24111}],"outbounds":[{"tag":"edge","type":"socks","server":"old.example","server_port":443}],"route":{"final":"edge"}}`
				section, key := "outbounds", "tag"
				if engine == core.EngineXray {
					content = `{"inbounds":[{"tag":"manual","protocol":"http","port":24111}],"outbounds":[{"tag":"edge","protocol":"socks","settings":{"servers":[{"address":"old.example","port":443}]}}]}`
				}
				if engine == core.EngineMihomo {
					section, key = "proxies", "name"
					content = "listeners: [{name: manual, type: http, port: 24111}]\nproxies: [{name: edge, type: socks5, server: old.example, port: 443}]\nrules: ['MATCH,edge']\n"
				}
				expectedVersion := 0
				if shared {
					var initial configMutationResult
					client.call("POST", base+"/source", map[string]any{"name": "native shared", "content": strings.ReplaceAll(content, "old.example", "panel.example"), "version": 0, "intent": "deploy"}, 200, &initial)
					completeConfigScopeAPITask(t, ctx, db, initial.Task)
					expectedVersion = initial.Config.Version
				}
				var read core.Task
				client.call("POST", "/tasks", core.TaskRequest{AgentID: node.ID, Engine: engine, Action: core.ActionReadManagedConfig}, 201, &read)
				lease, err := db.ClaimTask(ctx, node.ID)
				if err != nil || lease == nil || lease.ID != read.ID {
					t.Fatal("snapshot claim", err)
				}
				if err = db.CompleteTask(ctx, node.ID, read.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, Output: content}); err != nil {
					t.Fatal(err)
				}
				path := base + "/outbounds/" + section + "/edge?snapshot=" + read.ID
				var source struct {
					Version  int    `json:"version"`
					Fragment string `json:"fragment"`
					Object   string `json:"object"`
				}
				client.call("GET", path, nil, 200, &source)
				foreign.call("GET", path, nil, 404, nil)
				if source.Version != expectedVersion || !strings.Contains(source.Object, "old.example") {
					t.Fatal("not the actual instance snapshot", source)
				}
				fragment := strings.ReplaceAll(source.Fragment, "old.example", "edited.example")
				var saved configMutationResult
				client.call("POST", base+"/outbounds", map[string]any{"operation": "modify", "section": section, "tag": "edge", "fragment": fragment, "snapshot_task_id": read.ID, "version": expectedVersion, "intent": "deploy"}, 200, &saved)
				if saved.Config.Version != expectedVersion+1 || !strings.Contains(saved.Config.Content, "edited.example") || saved.Task.SharedInstance != shared {
					t.Fatal("snapshot edit lost target", saved.Task.ID)
				}
				completeConfigScopeAPITask(t, ctx, db, saved.Task)
				client.call("GET", path, nil, 404, nil)
				var fresh struct {
					Fragment string `json:"fragment"`
				}
				client.call("GET", base+"/outbounds/"+section+"/edge", nil, 200, &fresh)
				if !strings.Contains(fresh.Fragment, "edited.example") || !strings.Contains(fresh.Fragment, key) {
					t.Fatal("saved native exit cannot be read again")
				}
			})
		}
	}
}
