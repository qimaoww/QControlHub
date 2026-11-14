package api

import (
	"encoding/json"
	"net/http"
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
