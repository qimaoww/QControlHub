package api

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestNodeAddressDefaultsRespectOwnershipAndSharedProfiles(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "node-address")
	other, _ := ownedConfigScopeAPIAgent(t, ctx, db, bob, "node-address")
	endpoint := "/agents/" + agent.ID + "/client-address"
	bob.call("PUT", endpoint, map[string]string{"address": ""}, http.StatusNotFound, nil)
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21002)
	bob.call("PUT", endpoint, map[string]string{"address": "stolen.example"}, http.StatusForbidden, nil)
	bob.call("PUT", endpoint, map[string]string{"address": ""}, http.StatusForbidden, nil)
	var config core.Config
	bob.call("PUT", "/agents/"+agent.ID+"/configs/mihomo", configScopeAPIConfig(t, 21002), http.StatusOK, &config)
	var task core.Task
	bob.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy}, http.StatusCreated, &task)
	completeConfigScopeAPITask(t, ctx, db, task)
	selector := map[string]any{"profile": clientProfileSelector{Engine: config.Engine, Tag: "same-tag", Port: 21002}, "address": "port.example", "name": "private port", "address_mode": "ipv6"}
	bob.call("PUT", endpoint, selector, http.StatusOK, nil)
	for _, client := range []configScopeAPIClient{alice, admin} {
		for _, invalid := range []string{"https://example.com", "0.0.0.0", "bad/name", "::"} {
			client.call("PUT", endpoint, map[string]string{"address": invalid}, http.StatusBadRequest, nil)
		}
		for _, address := range []string{"Edge.Example.", "[2606:4700:4700::1111]", "9.9.9.9", "", ""} {
			client.call("PUT", endpoint, map[string]string{"address": address}, http.StatusOK, nil)
			body := bob.call("GET", "/client-access", nil, http.StatusOK, nil)
			if !bytes.Contains(body, []byte("port.example")) || !bytes.Contains(body, []byte("private port")) {
				t.Fatalf("node update lost independent port: %s", body)
			}
		}
	}
	var agents []core.Agent
	alice.call("GET", "/agents", nil, http.StatusOK, &agents)
	for _, loaded := range agents {
		if loaded.ID == agent.ID && (loaded.Labels["client_address"] != "" || loaded.Labels["public_host"] != "" || loaded.Labels["public_ip"] != "") {
			t.Fatal("automatic mode did not persist")
		}
	}
	loaded, err := db.GetAgent(ctx, other.ID)
	if err != nil || loaded.Labels["client_address"] != "" {
		t.Fatal("another node was changed")
	}
	// Port reset remains a distinct operation; it resumes inheriting the node.
	alice.call("PUT", endpoint, map[string]string{"address": "node.example"}, http.StatusOK, nil)
	bob.call("PUT", endpoint, map[string]any{"profile": selector["profile"], "address": ""}, http.StatusOK, nil)
	body := bob.call("GET", "/client-access", nil, http.StatusOK, nil)
	if !bytes.Contains(body, []byte("node.example")) || !bytes.Contains(body, []byte("private port")) {
		t.Fatalf("port reset lost name or node inheritance: %s", body)
	}
	if err := db.UpdateAgentMetrics(ctx, agent.ID, core.HostMetrics{PublicIPv4: "93.184.216.34"}); err != nil {
		t.Fatal(err)
	}
	alice.call("PUT", endpoint, map[string]string{"address": ""}, http.StatusOK, nil)
	body = bob.call("GET", "/client-access", nil, http.StatusOK, nil)
	if !bytes.Contains(body, []byte("93.184.216.34")) || bytes.Contains(body, []byte("node.example")) {
		t.Fatalf("automatic reset did not use live sources: %s", body)
	}
}
