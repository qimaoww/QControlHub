package store

import (
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestConnectionInboundPortRequiresUnambiguousName(t *testing.T) {
	ports := []core.PortTrafficEndpoint{{Name: "entry", Port: 8443}, {Name: "other", Port: 9443}}
	for _, tt := range []struct {
		name string
		want int
	}{{"entry", 8443}, {"other", 9443}, {"", 0}, {"absent", 0}} {
		if got := connectionInboundPort(tt.name, ports); got != tt.want {
			t.Fatalf("%q port=%d want=%d", tt.name, got, tt.want)
		}
	}
	ports = append(ports, core.PortTrafficEndpoint{Name: "entry", Port: 10443})
	if got := connectionInboundPort("entry", ports); got != 0 {
		t.Fatalf("ambiguous port=%d", got)
	}
}

func TestClientConnectionPortUsesHistoricalDeployment(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET capabilities='["xray"]',supported_capabilities='["xray"]' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute)
	input := core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "port history", Content: `{"inbounds":[{"tag":"entry","protocol":"vless","port":8443}]}`}
	config, err := db.SaveAgentConfig(ctx, input, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO tasks(id,agent_id,action,engine,config_id,config_version,status,created_at,started_at,finished_at)
 VALUES('tsk_port_old',$1,'deploy','xray',$2,1,'succeeded',$3,$3,$3)`, agent.ID, config.ID, old); err != nil {
		t.Fatal(err)
	}
	input.Content = `{"inbounds":[{"tag":"entry","protocol":"vless","port":9443}]}`
	if _, err := db.SaveAgentConfig(ctx, input, 1); err != nil {
		t.Fatal(err)
	}
	deployed := old.Add(time.Hour)
	if _, err := db.pool.Exec(ctx, `INSERT INTO tasks(id,agent_id,action,engine,config_id,config_version,status,created_at,started_at,finished_at)
 VALUES('tsk_port_new',$1,'deploy','xray',$2,2,'succeeded',$3,$3,$3)`, agent.ID, config.ID, deployed); err != nil {
		t.Fatal(err)
	}
	if err := db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: "log_0123456789abcdef", Entries: []core.CoreLogEntry{
		{Engine: core.EngineXray, Level: "info", Message: "from 8.8.8.8:50123 accepted tcp:1.1.1.1:443 [entry -> direct]", LoggedAt: old.Add(time.Minute)},
		{Engine: core.EngineXray, Level: "info", Message: "from 8.8.4.4:50124 accepted tcp:1.1.1.1:443 [entry -> direct]", LoggedAt: deployed.Add(time.Minute)},
		{Engine: core.EngineXray, Level: "info", Message: "from 9.9.9.9:50125 accepted tcp:1.1.1.1:443", LoggedAt: deployed.Add(time.Minute)},
	}}); err != nil {
		t.Fatal(err)
	}
	query := ClientConnectionQuery{AgentID: agent.ID, Since: old, Until: time.Now(), Limit: 100, Bucket: "hour"}
	history, err := db.ClientConnectionHistory(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Records) != 3 {
		t.Fatalf("records=%+v", history.Records)
	}
	expected := map[string]int{"8.8.8.8": 8443, "8.8.4.4": 9443, "9.9.9.9": 0}
	for _, record := range history.Records {
		if record.LocalPort != expected[record.ClientIP] || record.LocalIP != "" {
			t.Fatalf("wrong listener: %+v", record)
		}
	}
	// Pruned history must not fall back to today's changed port.
	if _, err := db.pool.Exec(ctx, `DELETE FROM config_revisions WHERE config_id=$1 AND version=1`, config.ID); err != nil {
		t.Fatal(err)
	}
	history, err = db.ClientConnectionHistory(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range history.Records {
		if r.ClientIP == "8.8.8.8" && r.LocalPort != 0 {
			t.Fatalf("used current configuration for old log: %+v", r)
		}
	}
	// An observation crossing a deployment cannot have a unique historical port.
	if _, err := db.pool.Exec(ctx, `UPDATE client_connections SET first_seen=$2,last_seen=$3 WHERE agent_id=$1 AND client_ip='8.8.4.4'`, agent.ID, deployed.Add(-time.Second), deployed.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	history, err = db.ClientConnectionHistory(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range history.Records {
		if r.ClientIP == "8.8.4.4" && r.LocalPort != 0 {
			t.Fatalf("assigned deployment-crossing tuple: %+v", r)
		}
	}
}
