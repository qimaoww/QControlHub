package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func probeCallRaw(client configScopeAPIClient, method, path string, body any) int {
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	} else {
		request.AddCookie(client.cookie)
		request.Header.Set(csrfHeader, client.csrf)
	}
	response := httptest.NewRecorder()
	client.handler.ServeHTTP(response, request)
	return response.Code
}

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

	// Empty or whitespace-only group_name must be rejected with 400.
	noGroupReq := core.DashboardProbePairRequest{
		Name:                "No Group",
		Node1:               endpoints[a.ID],
		Node2:               endpoints[b.ID],
		Bidirectional:       true,
		AutoEnabled:         true,
		AutoIntervalSeconds: 30,
		Parameters:          core.DiagnosticParameters{Tool: "ping", Protocol: "icmp", Count: 3, IntervalMS: 200, TimeoutMS: 1000, Size: 56},
	}
	rwKey.call("POST", "/dashboard/probes", noGroupReq, 400, nil)

	req := core.DashboardProbePairRequest{
		Name:                "HK-SG Ping",
		GroupName:           "核心互联",
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

	// Duplicate group_name for the same owner must fail with 409 Conflict.
	dupReq := req
	dupReq.Name = "Duplicate Attempt"
	rwKey.call("POST", "/dashboard/probes", dupReq, 409, nil)

	// Concurrent creation of the same group_name for one owner must yield exactly one success and one 409 Conflict.
	c, _ := ownedConfigScopeAPIAgent(t, ctx, db, admin, "probe-c")
	grantConfigScopeAPIAgent(t, ctx, db, c, alice, 5201)
	if err := db.Heartbeat(ctx, c.ID, core.HeartbeatRequest{Features: append(c.Features, core.AgentFeatureNetworkDiagnostics)}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateDiagnosticCapabilities(ctx, c.ID, &core.DiagnosticCapabilities{
		Tools:     map[string]core.DiagnosticTool{"ping": {Version: "ICMP"}},
		Addresses: []string{"192.0.2.30"},
	}); err != nil {
		t.Fatal(err)
	}
	access2 := acceptConfigScopeAPIInvitations(alice)
	for _, sh := range access2.Shares {
		if sh.AgentID == c.ID {
			admin.call("PUT", "/agents/"+sh.AgentID+"/diagnostic-grants/"+sh.ID, map[string]bool{"enabled": true}, 204, nil)
		}
	}
	alice.call("GET", "/network-diagnostics/nodes", nil, 200, &nodes)
	for _, n := range nodes {
		endpoints[n.AgentID] = core.DiagnosticEndpoint{AgentID: n.AgentID, InstanceID: n.InstanceID, Address: n.Capabilities.Addresses[0]}
	}

	startRace := make(chan struct{})
	var raceWG sync.WaitGroup
	raceStatuses := make(chan int, 2)
	for _, target := range []core.DiagnosticEndpoint{endpoints[b.ID], endpoints[c.ID]} {
		raceWG.Add(1)
		go func(ep core.DiagnosticEndpoint) {
			defer raceWG.Done()
			<-startRace
			raceReq := req
			raceReq.GroupName = "并发互斥组"
			raceReq.Node2 = ep
			raceStatuses <- probeCallRaw(alice, "POST", "/dashboard/probes", raceReq)
		}(target)
	}
	close(startRace)
	raceWG.Wait()
	close(raceStatuses)
	createdRace, conflictRace := 0, 0
	for code := range raceStatuses {
		if code == 201 {
			createdRace++
		} else if code == 409 {
			conflictRace++
		} else {
			t.Fatalf("unexpected concurrent group creation status: %d", code)
		}
	}
	if createdRace != 1 || conflictRace != 1 {
		t.Fatalf("expected 1 created and 1 conflict in concurrent group creation, got created=%d conflict=%d", createdRace, conflictRace)
	}

	// Cross-user isolation: bob and admin can create their own groups with the SAME group_name independently.
	grantConfigScopeAPIAgent(t, ctx, db, a, bob, 5202)
	grantConfigScopeAPIAgent(t, ctx, db, b, bob, 5202)
	bobAccess := acceptConfigScopeAPIInvitations(bob)
	for _, sh := range bobAccess.Shares {
		admin.call("PUT", "/agents/"+sh.AgentID+"/diagnostic-grants/"+sh.ID, map[string]bool{"enabled": true}, 204, nil)
	}
	var bobNodes []core.DiagnosticNode
	bob.call("GET", "/network-diagnostics/nodes", nil, 200, &bobNodes)
	bobEp := map[string]core.DiagnosticEndpoint{}
	for _, n := range bobNodes {
		bobEp[n.AgentID] = core.DiagnosticEndpoint{AgentID: n.AgentID, InstanceID: n.InstanceID, Address: n.Capabilities.Addresses[0]}
	}
	bobReq := req
	bobReq.Node1 = bobEp[a.ID]
	bobReq.Node2 = bobEp[b.ID]
	bobReq.AutoEnabled = false
	var bobPair core.DashboardProbePair
	bob.call("POST", "/dashboard/probes", bobReq, 201, &bobPair)
	var adminPair core.DashboardProbePair
	adminReq := req
	adminReq.Node1 = core.DiagnosticEndpoint{AgentID: a.ID, Address: "192.0.2.10"}
	adminReq.Node2 = core.DiagnosticEndpoint{AgentID: b.ID, Address: "192.0.2.20"}
	adminReq.AutoEnabled = false
	admin.call("POST", "/dashboard/probes", adminReq, 201, &adminPair)

	// Bob and Admin cannot view, update, trigger, stop, or delete Alice's probe pair even with identical group names.
	bob.call("PUT", "/dashboard/probes/"+pair.ID, bobReq, 404, nil)
	bob.call("POST", "/dashboard/probes/"+pair.ID+"/run", nil, 404, nil)
	bob.call("POST", "/dashboard/probes/"+pair.ID+"/stop", nil, 404, nil)
	bob.call("DELETE", "/dashboard/probes/"+pair.ID, nil, 404, nil)
	admin.call("PUT", "/dashboard/probes/"+pair.ID, adminReq, 404, nil)
	admin.call("DELETE", "/dashboard/probes/"+pair.ID, nil, 404, nil)

	// Clean up extra race/isolation pairs so the remaining scheduling test observes Alice's single pair cleanly.
	var aliceBeforeSched core.DashboardProbeListResponse
	alice.call("GET", "/dashboard/probes", nil, 200, &aliceBeforeSched)
	for _, p := range aliceBeforeSched.Pairs {
		if p.GroupName == "并发互斥组" {
			// Also verify renaming to an existing group returns 409 Conflict.
			renameConflict := req
			renameConflict.GroupName = "核心互联"
			renameConflict.Node2 = p.Node2
			renameConflict.Revision = p.Revision
			alice.call("PUT", "/dashboard/probes/"+p.ID, renameConflict, 409, nil)
			alice.call("DELETE", "/dashboard/probes/"+p.ID, nil, 204, nil)
		}
	}
	bob.call("DELETE", "/dashboard/probes/"+bobPair.ID, nil, 204, nil)
	admin.call("DELETE", "/dashboard/probes/"+adminPair.ID, nil, 204, nil)

	// Stale revision or missing revision check protects concurrent edits.
	badRev := req
	badRev.Revision = 99
	alice.call("PUT", "/dashboard/probes/"+pair.ID, badRev, 409, nil)
	zeroRev := req
	zeroRev.Revision = 0
	alice.call("PUT", "/dashboard/probes/"+pair.ID, zeroRev, 409, nil)

	// Refresh access list after second invitation acceptance so later grant-revocation subtest uses active share IDs.
	access = access2
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

	// Explicit delete removes the probe pair cleanly.
	alice.call("DELETE", "/dashboard/probes/"+pair.ID, nil, 204, nil)
	alice.call("GET", "/dashboard/probes", nil, 200, &listAfter)
	if len(listAfter.Pairs) != 0 {
		t.Fatalf("deleted probe still present: %+v", listAfter.Pairs)
	}

	// Concurrent group rename into an occupied group or two concurrent renames to the same group must be serialized.
	var p1, p2 core.DashboardProbePair
	r1 := req
	r1.GroupName = "分组一"
	r2 := req
	r2.GroupName = "分组二"
	r2.Node2 = endpoints[c.ID]
	r2.AutoEnabled = false
	alice.call("POST", "/dashboard/probes", r1, 201, &p1)
	alice.call("POST", "/dashboard/probes", r2, 201, &p2)
	renameStart := make(chan struct{})
	var renameWG sync.WaitGroup
	renameCodes := make(chan int, 2)
	for _, item := range []core.DashboardProbePair{p1, p2} {
		renameWG.Add(1)
		go func(target core.DashboardProbePair) {
			defer renameWG.Done()
			<-renameStart
			up := req
			up.GroupName = "目标合并组"
			up.Nodes = target.Nodes
			up.Node1 = target.Node1
			up.Node2 = target.Node2
			up.Revision = target.Revision
			renameCodes <- probeCallRaw(alice, "PUT", "/dashboard/probes/"+target.ID, up)
		}(item)
	}
	close(renameStart)
	renameWG.Wait()
	close(renameCodes)
	rOK, rConf := 0, 0
	for code := range renameCodes {
		if code == 200 {
			rOK++
		} else if code == 409 {
			rConf++
		} else {
			t.Fatalf("unexpected concurrent rename status: %d", code)
		}
	}
	if rOK != 1 || rConf != 1 {
		t.Fatalf("expected 1 success and 1 conflict on concurrent group rename, got ok=%d conflict=%d", rOK, rConf)
	}

	// Running probe stops cleanly when chain endpoints are edited, and deleting an intermediate hop agent cleans the multi-hop group.
	alice.call("GET", "/dashboard/probes", nil, 200, &listAfter)
	var targetPair core.DashboardProbePair
	for _, p := range listAfter.Pairs {
		if p.GroupName == "目标合并组" {
			targetPair = p
			break
		}
	}
	var startedPair core.DashboardProbePair
	alice.call("POST", "/dashboard/probes/"+targetPair.ID+"/run", nil, 201, &startedPair)
	if !startedPair.Availability.Running {
		t.Fatalf("expected running state after /run, got %+v", startedPair.Availability)
	}
	extendOnRun := req
	extendOnRun.GroupName = targetPair.GroupName
	extendOnRun.Nodes = []core.DiagnosticEndpoint{endpoints[a.ID], endpoints[c.ID], endpoints[b.ID]}
	extendOnRun.Revision = startedPair.Revision
	var updatedPair core.DashboardProbePair
	alice.call("PUT", "/dashboard/probes/"+targetPair.ID, extendOnRun, 200, &updatedPair)
	if updatedPair.LastSessionID != "" || updatedPair.ActiveState != "" || updatedPair.Availability.Running {
		t.Fatalf("editing endpoints while running must stop/detach previous session, got %+v", updatedPair)
	}

	// Delete intermediate node c and run cleanup: multi-hop probe referencing c must be cleaned up.
	admin.call("DELETE", "/agents/"+c.ID, nil, 204, nil)
	if err := db.CleanupDeletedAgents(ctx); err != nil {
		t.Fatal(err)
	}
	alice.call("GET", "/dashboard/probes", nil, 200, &listAfter)
	for _, p := range listAfter.Pairs {
		for _, ep := range p.Nodes {
			if ep.AgentID == c.ID {
				t.Fatalf("expected multi-hop probe referencing deleted intermediate agent to be cleaned up, got %+v", p)
			}
		}
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
