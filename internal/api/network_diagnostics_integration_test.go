package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func diagnosticTestParameters() core.DiagnosticParameters {
	return core.DiagnosticParameters{Tool: "mtr", Protocol: "icmp", Provider: "disable-geoip", Count: 2, IntervalMS: 200, TimeoutMS: 200, MaxHops: 4, Size: 64, Port: 5201, Duration: 2, Streams: 1, Bandwidth: 1000000}
}
func TestNetworkDiagnosticsAuthorizationAndLifecycle(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	a, _ := ownedConfigScopeAPIAgent(t, ctx, db, admin, "diagnostic-a")
	b, _ := ownedConfigScopeAPIAgent(t, ctx, db, admin, "diagnostic-b")
	for i, agent := range []core.Agent{a, b} {
		grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 5201)
		caps := core.DiagnosticCapabilities{Tools: map[string]core.DiagnosticTool{"ping": {Version: "ICMP"}, "nexttrace": {Version: "v1.7.6"}, "iperf3": {Version: "3.18"}}, Providers: map[string]string{"disable-geoip": "", "ipdb.one": "missing credentials"}, Addresses: []string{[]string{"192.0.2.2", "192.0.2.3"}[i]}}
		if i == 0 {
			caps.Tools["nexttrace"] = core.DiagnosticTool{AutoInstall: true, Error: "NextTrace 未安装"}
		}
		if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: append(agent.Features, core.AgentFeatureNetworkDiagnostics)}); err != nil {
			t.Fatal(err)
		}
		if err := db.UpdateDiagnosticCapabilities(ctx, agent.ID, &caps); err != nil {
			t.Fatal(err)
		}
	}
	var nodes []core.DiagnosticNode
	alice.call("GET", "/network-diagnostics/nodes", nil, 200, &nodes)
	if len(nodes) != 0 {
		t.Fatal("visibility was promoted to network execution")
	}
	access := acceptConfigScopeAPIInvitations(alice)
	for _, sh := range access.Shares {
		admin.call("PUT", "/agents/"+sh.AgentID+"/diagnostic-grants/"+sh.ID, map[string]bool{"enabled": true}, 204, nil)
	}
	alice.call("GET", "/network-diagnostics/nodes", nil, 200, &nodes)
	if len(nodes) != 2 {
		t.Fatalf("granted shared nodes: %+v", nodes)
	}
	endpoints := map[string]core.DiagnosticEndpoint{}
	for _, n := range nodes {
		endpoints[n.AgentID] = core.DiagnosticEndpoint{AgentID: n.AgentID, InstanceID: n.InstanceID, Address: n.Capabilities.Addresses[0]}
	}
	request := core.DiagnosticRequest{RequestID: "first", Node1: endpoints[a.ID], Node2: endpoints[b.ID], Parameters: diagnosticTestParameters(), Bidirectional: true}
	for _, size := range []int{-1, 27, 1501} {
		invalid := request
		invalid.Parameters.Size = size
		alice.call("POST", "/network-diagnostics", invalid, 400, nil)
	}
	invalid := request
	invalid.Node2.Address = "8.8.8.8"
	alice.call("POST", "/network-diagnostics", invalid, 400, nil)
	invalid = request
	invalid.Parameters.Provider = "ipdb.one"
	alice.call("POST", "/network-diagnostics", invalid, 409, nil)
	invalid = request
	invalid.Node2 = invalid.Node1
	alice.call("POST", "/network-diagnostics", invalid, 400, nil)
	var session core.DiagnosticSession
	alice.call("POST", "/network-diagnostics", request, 201, &session)
	alice.call("POST", "/network-diagnostics", request, 201, nil)
	wrong := request
	wrong.Parameters.Port = 5202
	alice.call("POST", "/network-diagnostics", wrong, 409, nil)
	bob.call("GET", "/network-diagnostics/"+session.ID, nil, 404, nil)
	bob.call("POST", "/network-diagnostics/"+session.ID+"/stop", nil, 404, nil)
	var task core.Task
	alice.call("GET", "/tasks/"+session.ID, nil, 200, &task)
	if task.Action != core.ActionNetworkDiagnostic {
		t.Fatal("missing ordinary task record")
	}
	if task, err := db.ClaimTask(ctx, a.ID); err != nil || task != nil {
		t.Fatalf("diagnostic occupied ordinary dispatch: %+v %v", task, err)
	}
	for _, agent := range []core.Agent{a, b} {
		commands, err := db.AgentDiagnosticCommands(ctx, agent.ID)
		if err != nil || len(commands) != 1 {
			t.Fatalf("dispatch: %+v %v", commands, err)
		}
		c := commands[0]
		if c.Source.AgentID != agent.ID {
			t.Fatal("incorrect direction")
		}
		state := "succeeded"
		if agent.ID == b.ID {
			state = "failed"
		}
		r := core.DiagnosticResult{ID: c.ID, State: state, Cleaned: true, Output: json.RawMessage(`{"schema_version":1,"stats":[]}`)}
		if err = db.CompleteDiagnosticCommand(ctx, agent.ID, r); err != nil {
			t.Fatal(err)
		}
		if err = db.CompleteDiagnosticCommand(ctx, agent.ID, r); err != nil {
			t.Fatal(err)
		}
	}
	alice.call("GET", "/network-diagnostics/"+session.ID, nil, 200, &session)
	if session.State != "failed" || len(session.Results) != 2 {
		t.Fatalf("single-sided failure discarded results: %+v", session)
	}
	alice.call("GET", "/tasks/"+session.ID, nil, 200, &task)
	if task.Status != core.TaskFailed {
		t.Fatal("task terminal status diverged")
	}
	// An explicit numeric custom target grants no execution or data access on
	// any other managed node, and only the authorized source receives work.
	custom := request
	custom.TargetMode = "ip"
	custom.Bidirectional = false
	custom.RequestID = "custom-target"
	custom.Node2 = core.DiagnosticEndpoint{Address: "198.51.100.9"}
	custom.Parameters.Tool = "ping"
	for _, target := range []string{"--help", "8.8.8.8\n--tcp", "localhost", "127.0.0.1", "::1", "fe80::1%eth0", "255.255.255.255", "0.0.0.0", "224.0.0.1", "ff02::1"} {
		bad := custom
		bad.Node2.Address = target
		alice.call("POST", "/network-diagnostics", bad, 400, nil)
	}
	bad := custom
	bad.Node2.AgentID = b.ID
	alice.call("POST", "/network-diagnostics", bad, 400, nil)
	bad = custom
	bad.Bidirectional = true
	alice.call("POST", "/network-diagnostics", bad, 400, nil)
	var external core.DiagnosticSession
	alice.call("POST", "/network-diagnostics", custom, 201, &external)
	bob.call("GET", "/network-diagnostics/"+external.ID, nil, 404, nil)
	commands, e := db.AgentDiagnosticCommands(ctx, a.ID)
	if e != nil || len(commands) != 1 || commands[0].Target.Address != "198.51.100.9" {
		t.Fatalf("custom dispatch: %+v %v", commands, e)
	}
	if other, e := db.AgentDiagnosticCommands(ctx, b.ID); e != nil || len(other) != 0 {
		t.Fatalf("custom target dispatched to another node: %+v %v", other, e)
	}
	if e = db.CompleteDiagnosticCommand(ctx, a.ID, core.DiagnosticResult{ID: commands[0].ID, State: "succeeded", Cleaned: true, Output: json.RawMessage(`{"sent":2,"received":2}`)}); e != nil {
		t.Fatal(e)
	}
	alice.call("GET", "/network-diagnostics/"+external.ID, nil, 200, &external)
	alice.call("GET", "/tasks/"+external.ID, nil, 200, &task)
	if external.State != "succeeded" || task.Status != core.TaskSucceeded {
		t.Fatal("custom target result/task not accessible to source creator")
	}
	// A shared user's diagnostic key needs execution/read permissions, never
	// host administration. Revoking it stops both ends through reconciliation.
	permissions := []core.Permission{core.PermissionAgentsRead, core.PermissionTasksRead, core.PermissionTasksExecute}
	key, created := testCreateBusinessKey(t, alice, permissions)
	foreign, _ := testCreateBusinessKey(t, bob, permissions)
	weak, _ := testCreateBusinessKey(t, alice, []core.Permission{core.PermissionTasksRead})
	weak.call("GET", "/network-diagnostics", nil, 403, nil)
	keyed := request
	keyed.RequestID = "key-created"
	var keyedSession core.DiagnosticSession
	key.call("POST", "/network-diagnostics", keyed, 201, &keyedSession)
	foreign.call("GET", "/network-diagnostics/"+keyedSession.ID, nil, 404, nil)
	foreign.call("POST", "/network-diagnostics/"+keyedSession.ID+"/stop", nil, 404, nil)
	foreign.call("POST", "/tasks/"+keyedSession.ID+"/retry", nil, 404, nil)
	alice.call("DELETE", "/settings/api-keys/"+created.Key.ID, nil, 200, nil)
	key.call("GET", "/network-diagnostics/"+keyedSession.ID, nil, 401, nil)
	for _, agent := range []core.Agent{a, b} {
		commands, e := db.AgentDiagnosticCommands(ctx, agent.ID)
		if e != nil || len(commands) != 1 || commands[0].Role != "stop" {
			t.Fatalf("revoked key still executable: %+v %v", commands, e)
		}
		if e = db.CompleteDiagnosticCommand(ctx, agent.ID, core.DiagnosticResult{ID: commands[0].ID, State: "canceled", Cleaned: true}); e != nil {
			t.Fatal(e)
		}
	}
	// A new retry revalidates both allocations and keeps the exact parameters.
	var retry core.Task
	alice.call("POST", "/tasks/"+session.ID+"/retry", nil, http.StatusCreated, &retry)
	for _, sh := range access.Shares {
		if sh.AgentID == b.ID {
			admin.call("PUT", "/agents/"+sh.AgentID+"/diagnostic-grants/"+sh.ID, map[string]any{"enabled": false, "revision": 1}, 204, nil)
		}
	}
	alice.call("GET", "/network-diagnostics/"+retry.ID, nil, 403, nil)
	alice.call("GET", "/tasks/"+retry.ID, nil, 404, nil)
	for _, agent := range []core.Agent{a, b} {
		commands, err := db.AgentDiagnosticCommands(ctx, agent.ID)
		if err != nil || len(commands) != 1 || commands[0].Role != "stop" {
			t.Fatalf("revocation did not propagate to both endpoints: %+v %v", commands, err)
		}
		if err = db.CompleteDiagnosticCommand(ctx, agent.ID, core.DiagnosticResult{ID: commands[0].ID, State: "canceled", Cleaned: true}); err != nil {
			t.Fatal(err)
		}
	}
	wrong = request
	wrong.RequestID = "forged"
	wrong.Node2.Address = "8.8.8.8"
	alice.call("POST", "/network-diagnostics", wrong, 403, nil)
}
