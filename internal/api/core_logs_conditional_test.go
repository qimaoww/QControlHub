package api

import (
	"bytes"
	"fmt"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCoreLogConditionalReadRechecksOwnership(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "conditional-owner")
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21001)
	if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: "log_abcdefabcdef1234", Entries: []core.CoreLogEntry{
		{Engine: core.EngineMihomo, Level: "warning", Message: "owner-only diagnostic"},
	}}); err != nil {
		t.Fatal(err)
	}
	fetch := func(client configScopeAPIClient, query, tag string, status int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/core-logs"+query, nil)
		request.AddCookie(client.cookie)
		request.Header.Set("If-None-Match", tag)
		response := httptest.NewRecorder()
		client.handler.ServeHTTP(response, request)
		if response.Code != status {
			t.Fatalf("%s: status=%d, body=%s", query, response.Code, response.Body.String())
		}
		return response
	}
	query := "?agent_id=" + agent.ID
	tag := fetch(alice, query, "", http.StatusOK).Header().Get("ETag")
	fetch(alice, query, tag, http.StatusNotModified)
	fetch(bob, query, tag, http.StatusNotFound)
	// Aggregate reads must also evaluate ownership before comparing the tag.
	fetch(bob, "", tag, http.StatusOK)
	alice.call(http.MethodDelete, "/agents/"+agent.ID, nil, http.StatusNoContent, nil)
	fetch(alice, query, tag, http.StatusNotFound)
}

func TestCoreLogSharedSessionsAndPrivacy(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	host := enrollConfigScopeAPIAgent(t, ctx, db)
	host.Features = append(host.Features, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCorePrepare, core.AgentFeatureCoreLogs, core.AgentFeatureCoreLogStatus, core.AgentFeatureCoreLogInstances)
	if err := db.Heartbeat(ctx, host.ID, core.HeartbeatRequest{Features: host.Features}); err != nil {
		t.Fatal(err)
	}
	ownA, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "private-A")
	ownB, _ := ownedConfigScopeAPIAgent(t, ctx, db, bob, "private-B")
	shareIDs := []string{}
	for i, client := range []configScopeAPIClient{alice, bob} {
		grantConfigScopeAPIAgent(t, ctx, db, host, client, 21001+i)
		var config core.Config
		client.call("PUT", "/agents/"+host.ID+"/configs/mihomo", configScopeAPIConfig(t, 21001+i), http.StatusOK, &config)
		var task core.Task
		client.call("POST", "/tasks", core.TaskRequest{AgentID: host.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy}, http.StatusCreated, &task)
		claimed, err := db.ClaimTask(ctx, host.ID)
		if err != nil || claimed == nil || !claimed.SharedInstance {
			t.Fatalf("claim %+v %v", claimed, err)
		}
		shareIDs = append(shareIDs, claimed.SharedTrafficID)
		if err := db.CompleteTask(ctx, host.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, TrafficSettled: true}); err != nil {
			t.Fatal(err)
		}
	}
	write := func(agent, instance, message string) {
		t.Helper()
		id, _ := core.NewID("log")
		if err := db.StoreCoreLogs(ctx, agent, core.CoreLogBatch{ID: id, Entries: []core.CoreLogEntry{{Engine: core.EngineMihomo, InstanceID: instance, Service: core.CoreLogService(core.EngineMihomo, instance, "systemd"), Level: "warning", Message: message}}}); err != nil {
			t.Fatal(err)
		}
	}
	write(host.ID, "main", "host-main")
	write(host.ID, shareIDs[0], "alice-instance")
	write(host.ID, shareIDs[1], "bob-instance")
	write(ownA.ID, "main", "alice-private")
	write(ownB.ID, "main", "bob-private")
	for _, item := range []struct {
		client configScopeAPIClient
		want   []string
	}{{admin, []string{"host-main", "alice-instance", "bob-instance"}}, {alice, []string{"alice-instance", "alice-private"}}, {bob, []string{"bob-instance", "bob-private"}}} {
		var entries []core.CoreLogEntry
		item.client.call("GET", "/core-logs", nil, http.StatusOK, &entries)
		if len(entries) != len(item.want) {
			t.Fatalf("scope entries %+v", entries)
		}
		found := map[string]bool{}
		for _, entry := range entries {
			found[entry.Message] = true
		}
		for _, message := range item.want {
			if !found[message] {
				t.Fatalf("missing %s", message)
			}
		}
	}
	for _, query := range []string{"agent_id=" + ownA.ID, "agent_id=" + ownB.ID} {
		admin.call("GET", "/core-logs?"+query, nil, http.StatusNotFound, nil)
	}
	for _, query := range []string{"agent_id=" + ownB.ID, "agent_id=" + host.ID + "&instance_id=" + shareIDs[1], "user_id=" + bob.userID, "agent_id=" + host.ID + "&instance_id=main"} {
		alice.call("GET", "/core-logs?"+query, nil, http.StatusNotFound, nil)
	}
	var logs []core.CoreLogEntry
	alice.call("GET", "/core-logs?agent_id="+host.ID+"&engine=mihomo&level=warning&q=instance&limit=1", nil, http.StatusOK, &logs)
	if len(logs) != 1 || logs[0].InstanceID != shareIDs[0] {
		t.Fatal("filtered scope leaked")
	}
	alice.call("GET", fmt.Sprintf("/core-logs?agent_id=%s&before=%d&limit=1", host.ID, logs[0].ID), nil, http.StatusOK, &logs)
	if len(logs) != 0 {
		t.Fatal("history leaked another instance")
	}
	if err := db.Heartbeat(ctx, host.ID, core.HeartbeatRequest{Features: host.Features, SharedInstances: []core.SharedInstanceStatus{
		{Engine: core.EngineMihomo, ShareID: shareIDs[0], Status: "active", CoreLogStatus: "failed", CoreLogError: "permission-denied"},
		{Engine: core.EngineMihomo, ShareID: shareIDs[1], Status: "active", CoreLogStatus: "active"},
	}}); err != nil {
		t.Fatal(err)
	}
	var sources []store.CoreLogSource
	alice.call("GET", "/core-log-sources", nil, http.StatusOK, &sources)
	for _, source := range sources {
		if source.AgentID == host.ID && (source.InstanceID != shareIDs[0] || source.Status != "failed") {
			t.Fatalf("shared state leaked %+v", source)
		}
		if source.OwnerID == bob.userID {
			t.Fatal("foreign user exposed")
		}
	}
	bob.call("GET", "/core-log-sources", nil, http.StatusOK, &sources)
	for _, source := range sources {
		if source.AgentID == host.ID && source.Status != "active" {
			t.Fatal("one failure overwrote another source")
		}
	}
	admin.call("GET", "/core-log-sources", nil, http.StatusOK, &sources)
	for _, source := range sources {
		if source.AgentID == ownA.ID || source.AgentID == ownB.ID {
			t.Fatal("admin source privacy")
		}
	}
	// Conditional refresh uses the same live authorization after grant revocation.
	fetch := func(client configScopeAPIClient, tag string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/v1/core-logs", nil)
		r.AddCookie(client.cookie)
		r.Header.Set("If-None-Match", tag)
		w := httptest.NewRecorder()
		client.handler.ServeHTTP(w, r)
		return w
	}
	first := fetch(alice, "")
	tag := first.Header().Get("ETag")
	if tag == "" {
		t.Fatal("missing ETag")
	}
	if _, err := db.SetUserAgentAccess(ctx, alice.userID, core.AgentAccessRequest{Isolated: true}); err != nil {
		t.Fatal(err)
	}
	after := fetch(alice, tag)
	if after.Code != http.StatusOK || bytes.Contains(after.Body.Bytes(), []byte("alice-instance")) {
		t.Fatalf("revoked conditional refresh: %d %s", after.Code, after.Body.String())
	}
	admin.call("PUT", "/users/"+bob.userID, map[string]any{"disabled": true}, http.StatusOK, nil)
	if result := fetch(bob, ""); result.Code != http.StatusUnauthorized {
		t.Fatalf("disabled session survived: %d", result.Code)
	}
}
