package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestClientConnectionAPIIsolation(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "connection-owner", core.EngineMihomo)
	report := clientConnectionFixtures{Status: "ok", Connections: []core.ClientConnection{{Engine: core.EngineMihomo, Protocol: "trojan", Inbound: "entry", Transport: "tcp", ClientIP: "198.51.100.9", ClientPort: 50123, LocalIP: "192.0.2.1", LocalPort: 443}}}
	if err := storeAPIConnectionLogs(ctx, db, agent.ID, report); err != nil {
		t.Fatal(err)
	}
	for _, client := range []configScopeAPIClient{admin, alice} {
		var result core.ClientConnectionHistory
		for _, suffix := range []string{"", "&include_non_public=false"} {
			client.call("GET", "/client-connections?agent_id="+agent.ID+suffix, nil, http.StatusOK, &result)
			if result.Flows != 0 || result.IPs != 0 || len(result.Records) != 0 || len(result.Timeline) != 0 {
				t.Fatalf("non-public connection shown by default: %+v", result)
			}
		}
		client.call("GET", "/client-connections?agent_id="+agent.ID+"&include_non_public=true&client_ip=198.51.100.9", nil, http.StatusOK, &result)
		if result.Flows != 1 || len(result.Records) != 1 || result.Records[0].ClientIP != "198.51.100.9" {
			t.Fatalf("missing connection: %+v", result)
		}
	}
	var result core.ClientConnectionHistory
	bob.call("GET", "/client-connections?include_non_public=true&agent_id="+agent.ID, nil, http.StatusOK, &result)
	if result.Flows != 0 || len(result.Records) != 0 || len(result.Sources) != 0 {
		t.Fatalf("cross-account leak: %+v", result)
	}
}

func TestClientConnectionAPIGrouping(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, alice, "group-owner", core.EngineMihomo)
	report := clientConnectionFixtures{Status: "ok", Connections: []core.ClientConnection{
		{ClientIP: "198.51.100.9", ClientPort: 50123},
		{ClientIP: "198.51.100.9", ClientPort: 50124},
	}}
	if err := storeAPIConnectionLogs(ctx, db, agent.ID, report); err != nil {
		t.Fatal(err)
	}
	path := "/client-connections?group_by=ip&include_non_public=true&agent_id=" + agent.ID
	for _, client := range []configScopeAPIClient{admin, alice} {
		var result, enriched core.ClientConnectionHistory
		client.call("GET", path+"&locations=cached", nil, http.StatusOK, &result)
		if result.Flows != 2 || result.IPs != 1 || len(result.Records) != 1 || len(result.Records[0].Endpoints) != 1 || result.Records[0].Endpoints[0].AgentID != agent.ID {
			t.Fatalf("grouped API result=%+v", result)
		}
		client.call("GET", path+"&locations=only&cursor="+result.PageCursor, nil, http.StatusOK, &enriched)
		if len(enriched.Records) != 1 || enriched.Records[0].ID != result.Records[0].ID || !enriched.Records[0].Location.NonPublic || len(enriched.Records[0].Endpoints) != 0 {
			t.Fatalf("grouped location result=%+v", enriched)
		}
	}
	var denied core.ClientConnectionHistory
	bob.call("GET", path, nil, http.StatusOK, &denied)
	if len(denied.Records) != 0 || len(denied.Sources) != 0 {
		t.Fatalf("grouped API leaked data: %+v", denied)
	}
}

// Exercise the same panel log ingestion path used in production.
func storeAPIConnectionLogs(ctx context.Context, db *store.Store, agentID string, report clientConnectionFixtures) error {
	batch := core.CoreLogBatch{ID: "log_1123456789abcdef"}
	for _, c := range report.Connections {
		batch.Entries = append(batch.Entries, core.CoreLogEntry{Engine: core.EngineMihomo, Level: "info", Message: fmt.Sprintf("[TCP] %s --> example.invalid:443 using DIRECT", net.JoinHostPort(c.ClientIP, strconv.Itoa(c.ClientPort)))})
	}
	return db.StoreCoreLogs(ctx, agentID, batch)
}

type clientConnectionFixtures struct {
	Connections []core.ClientConnection `json:"connections"`
	Status      string                  `json:"status"`
}
