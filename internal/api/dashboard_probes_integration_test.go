package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestDashboardProbePairsCRUDPermissionsAndScheduling(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	a, _ := ownedConfigScopeAPIAgent(t, ctx, db, admin, "probe-a")
	b, _ := ownedConfigScopeAPIAgent(t, ctx, db, admin, "probe-b")
	for i, agent := range []core.Agent{a, b} {
		grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 5201)
		caps := core.DiagnosticCapabilities{
			Tools:     map[string]core.DiagnosticTool{"ping": {Version: "ICMP"}},
			Addresses: []string{[]string{"192.0.2.10", "192.0.2.20"}[i]},
		}
		if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: append(agent.Features, core.AgentFeatureNetworkDiagnostics)}); err != nil {
			t.Fatal(err)
		}
		if err := db.UpdateDiagnosticCapabilities(ctx, agent.ID, &caps); err != nil {
			t.Fatal(err)
		}
	}
	access := acceptConfigScopeAPIInvitations(alice)
	for _, sh := range access.Shares {
		admin.call("PUT", "/agents/"+sh.AgentID+"/diagnostic-grants/"+sh.ID, map[string]bool{"enabled": true}, 204, nil)
	}
	var nodes []core.DiagnosticNode
	alice.call("GET", "/network-diagnostics/nodes", nil, 200, &nodes)
	endpoints := map[string]core.DiagnosticEndpoint{}
	for _, n := range nodes {
		endpoints[n.AgentID] = core.DiagnosticEndpoint{AgentID: n.AgentID, InstanceID: n.InstanceID, Address: n.Capabilities.Addresses[0]}
	}

	// Read-only key (agents.read + tasks.read without tasks.execute) may read pairs
	// cleanly, but must be denied on mutations and manual probe execution.
	readonlyKey, _ := testCreateBusinessKey(t, alice, []core.Permission{core.PermissionAgentsRead, core.PermissionTasksRead})
	rwKey, createdRW := testCreateBusinessKey(t, alice, []core.Permission{core.PermissionAgentsRead, core.PermissionTasksRead, core.PermissionTasksExecute})

	var initialList core.DashboardProbeListResponse
	readonlyKey.call("GET", "/dashboard/probes", nil, 200, &initialList)

	req := core.DashboardProbePairRequest{
		Name:                "HK-SG Ping",
		Node1:               endpoints[a.ID],
		Node2:               endpoints[b.ID],
		Bidirectional:       true,
		AutoEnabled:         true,
		AutoIntervalSeconds: 30,
		Parameters:          core.DiagnosticParameters{Tool: "ping", Protocol: "icmp", Count: 3, IntervalMS: 200, TimeoutMS: 1000, Size: 56},
	}
	readonlyKey.call("POST", "/dashboard/probes", req, 403, nil)

	var pair core.DashboardProbePair
	rwKey.call("POST", "/dashboard/probes", req, 201, &pair)
	if pair.ID == "" || !pair.Availability.Ready || !pair.Bidirectional {
		t.Fatalf("created probe pair unexpected: %+v", pair)
	}

	// Bob cannot view, update, trigger, stop, or delete Alice's probe pair.
	bob.call("PUT", "/dashboard/probes/"+pair.ID, req, 404, nil)
	bob.call("POST", "/dashboard/probes/"+pair.ID+"/run", nil, 404, nil)
	bob.call("POST", "/dashboard/probes/"+pair.ID+"/stop", nil, 404, nil)
	bob.call("DELETE", "/dashboard/probes/"+pair.ID, nil, 404, nil)

	// Stale revision check protects concurrent edits.
	badRev := req
	badRev.Revision = 99
	alice.call("PUT", "/dashboard/probes/"+pair.ID, badRev, 409, nil)

	// Trigger due auto-probe via scheduler.
	if err := db.RunDueDashboardProbes(ctx, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatalf("run due probes: %v", err)
	}
	for i, agent := range []core.Agent{a, b} {
		commands, err := db.AgentDiagnosticCommands(ctx, agent.ID)
		if err != nil || len(commands) != 1 {
			t.Fatalf("expected 1 probe command on agent %d, got %+v err=%v", i, commands, err)
		}
		avg := 14.2 + float64(i)*3.1
		out := map[string]any{
			"sent": 3, "received": 3, "loss_percent": 0.0,
			"min_ms": 11.0, "avg_ms": avg, "max_ms": 18.0, "stdev_ms": 1.4, "reached": true,
		}
		raw, _ := json.Marshal(out)
		if err := db.CompleteDiagnosticCommand(ctx, agent.ID, core.DiagnosticResult{
			ID: commands[0].ID, State: "succeeded", Cleaned: true, Output: raw,
		}); err != nil {
			t.Fatal(err)
		}
	}

	var listAfter core.DashboardProbeListResponse
	readonlyKey.call("GET", "/dashboard/probes", nil, 200, &listAfter)
	if len(listAfter.Pairs) != 1 || listAfter.Pairs[0].Latest == nil {
		t.Fatalf("missing completed probe sample: %+v", listAfter.Pairs)
	}
	sample := listAfter.Pairs[0].Latest
	if sample.Forward == nil || sample.Reverse == nil || sample.Forward.AvgMS == nil || *sample.Forward.AvgMS != 14.2 || *sample.Reverse.AvgMS != 17.3 {
		t.Fatalf("unexpected bidirectional sample metrics: %+v", sample)
	}

	// Revoking shared diagnostic grant exposes clear unavailability reason.
	for _, sh := range access.Shares {
		if sh.AgentID == b.ID {
			admin.call("PUT", "/agents/"+sh.AgentID+"/diagnostic-grants/"+sh.ID, map[string]any{"enabled": false, "revision": 1}, 204, nil)
		}
	}
	alice.call("GET", "/dashboard/probes", nil, 200, &listAfter)
	if listAfter.Pairs[0].Availability.Ready || listAfter.Pairs[0].Availability.Reason == "" {
		t.Fatalf("expected grant revocation unavailability reason: %+v", listAfter.Pairs[0].Availability)
	}
	alice.call("POST", "/dashboard/probes/"+pair.ID+"/run", nil, http.StatusForbidden, nil)

	// Revoking API key that registered auto-probe disables its automatic runs.
	for _, sh := range access.Shares {
		if sh.AgentID == b.ID {
			admin.call("PUT", "/agents/"+sh.AgentID+"/diagnostic-grants/"+sh.ID, map[string]any{"enabled": true, "revision": 2}, 204, nil)
		}
	}
	alice.call("DELETE", "/settings/api-keys/"+createdRW.Key.ID, nil, 200, nil)
	updateNext := req
	updateNext.Name = "HK-SG Updated"
	updateNext.Revision = pair.Revision
	alice.call("PUT", "/dashboard/probes/"+pair.ID, updateNext, 200, &pair)
	pairKey, pairKeyInfo := testCreateBusinessKey(t, alice, []core.Permission{core.PermissionAgentsRead, core.PermissionTasksRead, core.PermissionTasksExecute})
	updateNext.Revision = pair.Revision
	updateNext.AutoEnabled = true
	pairKey.call("PUT", "/dashboard/probes/"+pair.ID, updateNext, 200, &pair)
	alice.call("DELETE", "/settings/api-keys/"+pairKeyInfo.Key.ID, nil, 200, nil)
	if err := db.RunDueDashboardProbes(ctx, time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	alice.call("GET", "/dashboard/probes", nil, 200, &listAfter)
	if listAfter.Pairs[0].AutoEnabled {
		t.Fatal("revoked API key scheduled probe remained auto_enabled")
	}

	alice.call("DELETE", "/dashboard/probes/"+pair.ID, nil, 204, nil)
	alice.call("GET", "/dashboard/probes", nil, 200, &listAfter)
	if len(listAfter.Pairs) != 0 {
		t.Fatalf("deleted probe still present: %+v", listAfter.Pairs)
	}
}

func TestDashboardProbeChainABCDExtension(t *testing.T) {
	db, ctx, admin, alice, _ := newConfigScopeAPIFixture(t)
	a, _ := ownedConfigScopeAPIAgent(t, ctx, db, admin, "chain-a")
	b, _ := ownedConfigScopeAPIAgent(t, ctx, db, admin, "chain-b")
	c, _ := ownedConfigScopeAPIAgent(t, ctx, db, admin, "chain-c")
	d, _ := ownedConfigScopeAPIAgent(t, ctx, db, admin, "chain-d")
	addrs := []string{"192.0.2.11", "192.0.2.12", "192.0.2.13", "192.0.2.14"}
	for i, agent := range []core.Agent{a, b, c, d} {
		grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 5201)
		caps := core.DiagnosticCapabilities{
			Tools:     map[string]core.DiagnosticTool{"ping": {Version: "ICMP"}},
			Addresses: []string{addrs[i]},
		}
		if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: append(agent.Features, core.AgentFeatureNetworkDiagnostics)}); err != nil {
			t.Fatal(err)
		}
		if err := db.UpdateDiagnosticCapabilities(ctx, agent.ID, &caps); err != nil {
			t.Fatal(err)
		}
	}
	access := acceptConfigScopeAPIInvitations(alice)
	for _, sh := range access.Shares {
		admin.call("PUT", "/agents/"+sh.AgentID+"/diagnostic-grants/"+sh.ID, map[string]bool{"enabled": true}, 204, nil)
	}
	var nodes []core.DiagnosticNode
	alice.call("GET", "/network-diagnostics/nodes", nil, 200, &nodes)
	ep := map[string]core.DiagnosticEndpoint{}
	for _, n := range nodes {
		ep[n.AgentID] = core.DiagnosticEndpoint{AgentID: n.AgentID, InstanceID: n.InstanceID, Address: n.Capabilities.Addresses[0]}
	}

	// Start with A-B and extend into A-B-C-D with explicit group_name.
	createReq := core.DashboardProbePairRequest{
		Name:                "核心全程专线",
		GroupName:           "亚太核心",
		Nodes:               []core.DiagnosticEndpoint{ep[a.ID], ep[b.ID]},
		Bidirectional:       true,
		AutoEnabled:         true,
		AutoIntervalSeconds: 60,
		Parameters:          core.DiagnosticParameters{Tool: "ping", Protocol: "icmp", Count: 4, IntervalMS: 200, TimeoutMS: 1000, Size: 56},
	}
	var chain core.DashboardProbePair
	alice.call("POST", "/dashboard/probes", createReq, 201, &chain)

	extendReq := createReq
	extendReq.Nodes = []core.DiagnosticEndpoint{ep[a.ID], ep[b.ID], ep[c.ID], ep[d.ID]}
	extendReq.Revision = chain.Revision
	alice.call("PUT", "/dashboard/probes/"+chain.ID, extendReq, 200, &chain)
	if len(chain.Nodes) != 4 || chain.GroupName != "亚太核心" {
		t.Fatalf("extended chain unexpected: %+v", chain)
	}

	if err := db.RunDueDashboardProbes(ctx, time.Now().UTC().Add(time.Second)); err != nil {
		t.Fatalf("run due chain probes: %v", err)
	}
	// Run all agent polls until the multi-round A<->B<->C<->D commands complete.
	agents := []core.Agent{a, b, c, d}
	for round := 0; round < 4; round++ {
		progressed := false
		for idx, ag := range agents {
			cmds, err := db.AgentDiagnosticCommands(ctx, ag.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, cmd := range cmds {
				progressed = true
				avg := 10.0 + float64(idx+round)
				loss := 0.0
				if cmd.Source.AgentID == b.ID && cmd.Target.AgentID == c.ID {
					loss = 10.0
				}
				raw, _ := json.Marshal(map[string]any{
					"sent": 5, "received": 5, "loss_percent": loss,
					"min_ms": avg - 2, "avg_ms": avg, "max_ms": avg + 2, "stdev_ms": 0.8, "reached": true,
				})
				if err := db.CompleteDiagnosticCommand(ctx, ag.ID, core.DiagnosticResult{
					ID: cmd.ID, State: "succeeded", Cleaned: true, Output: raw,
				}); err != nil {
					t.Fatal(err)
				}
			}
		}
		if !progressed {
			break
		}
	}

	var listAfter core.DashboardProbeListResponse
	alice.call("GET", "/dashboard/probes", nil, 200, &listAfter)
	if len(listAfter.Pairs) != 1 || listAfter.Pairs[0].Latest == nil || len(listAfter.Pairs[0].Latest.Hops) != 3 {
		t.Fatalf("expected 3 measured hops on latest chain sample: %+v", listAfter.Pairs)
	}
	sample := listAfter.Pairs[0].Latest
	if sample.Forward == nil || sample.Reverse == nil || !sample.Forward.Reached || !sample.Reverse.Reached {
		t.Fatalf("expected bidirectional aggregated forward/reverse chain metrics: %+v", sample)
	}
	if sample.Forward.LossPercent != 10 {
		t.Fatalf("expected combined forward chain loss 10%%, got %+v", sample.Forward)
	}
}

func TestDashboardProbeGroupUniquenessConcurrentAndOwners(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	a, _ := ownedConfigScopeAPIAgent(t, ctx, db, admin, "group-source")
	caps := core.DiagnosticCapabilities{Tools: map[string]core.DiagnosticTool{"ping": {Version: "ICMP"}}, Addresses: []string{"192.0.2.10"}}
	if err := db.Heartbeat(ctx, a.ID, core.HeartbeatRequest{Features: append(a.Features, core.AgentFeatureNetworkDiagnostics)}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateDiagnosticCapabilities(ctx, a.ID, &caps); err != nil {
		t.Fatal(err)
	}
	grantConfigScopeAPIAgent(t, ctx, db, a, alice, 5201)
	access := acceptConfigScopeAPIInvitations(alice)
	for _, sh := range access.Shares {
		admin.call("PUT", "/agents/"+sh.AgentID+"/diagnostic-grants/"+sh.ID, map[string]bool{"enabled": true}, 204, nil)
	}
	var nodes []core.DiagnosticNode
	alice.call("GET", "/network-diagnostics/nodes", nil, 200, &nodes)
	req := core.DashboardProbePairRequest{GroupName: "专线", TargetMode: "ip", Node1: core.DiagnosticEndpoint{AgentID: a.ID, Address: "192.0.2.10"}, Node2: core.DiagnosticEndpoint{Address: "1.1.1.1"}}
	var ownerPair core.DashboardProbePair
	admin.call("POST", "/dashboard/probes", req, 201, &ownerPair)
	req.Node1.InstanceID = nodes[0].InstanceID
	// Real API handlers race on one owner namespace. Exactly one commit wins.
	race := func(method string, paths []string, payload core.DashboardProbePairRequest, success int) {
		t.Helper()
		results := make(chan *httptest.ResponseRecorder, len(paths))
		start := make(chan struct{})
		for _, path := range paths {
			go func(path string) {
				<-start
				raw, _ := json.Marshal(payload)
				request := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(raw))
				request.AddCookie(alice.cookie)
				request.Header.Set(csrfHeader, alice.csrf)
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				alice.handler.ServeHTTP(response, request)
				results <- response
			}(path)
		}
		close(start)
		won := 0
		for range paths {
			response := <-results
			if response.Code == success {
				won++
			} else if response.Code != 409 || !strings.Contains(response.Body.String(), "分组已有链路") {
				t.Fatalf("race: %d %s", response.Code, response.Body.String())
			}
		}
		if won != 1 {
			t.Fatalf("race winners=%d", won)
		}
	}
	race("POST", []string{"/dashboard/probes", "/dashboard/probes", "/dashboard/probes", "/dashboard/probes"}, req, 201)
	var listed core.DashboardProbeListResponse
	alice.call("GET", "/dashboard/probes", nil, 200, &listed)
	if len(listed.Pairs) != 1 || listed.Pairs[0].GroupName != "专线" {
		t.Fatalf("groups: %+v", listed)
	}
	alice.call("POST", "/dashboard/probes", req, 409, nil)
	// Node administrators cannot operate a shared user's chain by its ID.
	admin.call("DELETE", "/dashboard/probes/"+listed.Pairs[0].ID, nil, 404, nil)
	bob.call("GET", "/dashboard/probes", nil, 200, &listed)
	if len(listed.Pairs) != 0 {
		t.Fatal("cross-owner group leak")
	}
	var one, two core.DashboardProbePair
	req.GroupName = "候选一"
	alice.call("POST", "/dashboard/probes", req, 201, &one)
	req.GroupName = "候选二"
	alice.call("POST", "/dashboard/probes", req, 201, &two)
	req.GroupName = "并发改组"
	race("PUT", []string{"/dashboard/probes/" + one.ID, "/dashboard/probes/" + two.ID}, req, 200)
	// Omitted names remain API-compatible and get distinct server-side groups.
	req.GroupName = ""
	alice.call("POST", "/dashboard/probes", req, 201, &one)
	alice.call("POST", "/dashboard/probes", req, 201, &two)
	if one.GroupName == "" || one.GroupName == two.GroupName {
		t.Fatal("unnamed compatibility merged groups")
	}
}
