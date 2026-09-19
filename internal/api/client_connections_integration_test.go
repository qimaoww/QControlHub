package api

import (
	"github.com/qimaoww/qcontrolhub/internal/core"
	"net/http"
	"testing"
)

func TestClientConnectionAPIIsolation(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "connection-owner", core.EngineMihomo)
	report := core.ClientConnectionReport{Status: "ok", Connections: []core.ClientConnection{{Engine: core.EngineMihomo, Protocol: "trojan", Inbound: "entry", Transport: "tcp", ClientIP: "198.51.100.9", ClientPort: 50123, LocalIP: "192.0.2.1", LocalPort: 443}}}
	if err := db.StoreClientConnections(ctx, agent.ID, report); err != nil {
		t.Fatal(err)
	}
	for _, client := range []configScopeAPIClient{admin, alice} {
		var result core.ClientConnectionHistory
		client.call("GET", "/client-connections?agent_id="+agent.ID+"&client_ip=198.51.100.9&port=443", nil, http.StatusOK, &result)
		if result.Flows != 1 || len(result.Records) != 1 || result.Records[0].ClientIP != "198.51.100.9" {
			t.Fatalf("missing connection: %+v", result)
		}
	}
	var result core.ClientConnectionHistory
	bob.call("GET", "/client-connections?agent_id="+agent.ID, nil, http.StatusOK, &result)
	if result.Flows != 0 || len(result.Records) != 0 || len(result.Sources) != 0 {
		t.Fatalf("cross-account leak: %+v", result)
	}
}
