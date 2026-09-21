package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestClientConnectionsMigrateVersion63(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := sharedTestAgent(t, db, ctx)
	task, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionStatus})
	if err != nil {
		t.Fatal(err)
	}
	before, err := db.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Version 63 already exists on main without the connection-history tables.
	if _, err := db.pool.Exec(ctx, `DROP TABLE client_connection_locations,client_connections,client_connection_sources;
		DELETE FROM qcontrolhub_schema_migrations WHERE version>=64;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(63) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.pool.QueryRow(ctx, `SELECT max(version) FROM qcontrolhub_schema_migrations`).Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("migration version=%d err=%v", version, err)
	}
	if after, err := db.GetTask(ctx, task.ID); err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("migration changed existing task: %+v %v", after, err)
	}
	report := clientConnectionFixtures{Status: "ok", Connections: []core.ClientConnection{{Engine: core.EngineMihomo, Protocol: "trojan", Inbound: "entry", Transport: "tcp", ClientIP: "8.8.8.8", ClientPort: 50123, LocalIP: "127.0.0.1", LocalPort: 443}}}
	if err := db.storeClientConnectionFixtures(ctx, agent.ID, report); err != nil {
		t.Fatal(err)
	}
	location := ClientConnectionLocation{IP: "8.8.8.8", RetryAfter: time.Now().Add(time.Hour), ClientIPLocation: core.ClientIPLocation{CountryCode: "US", Country: "United States"}}
	if err := db.SaveClientConnectionLocations(ctx, []ClientConnectionLocation{location}); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	history, err := db.ClientConnectionHistory(ctx, ClientConnectionQuery{Since: now.Add(-time.Hour), Until: now.Add(time.Minute), Limit: 100, Bucket: "hour"})
	if err != nil || len(history.Records) != 1 || len(history.Sources) != 1 || history.Flows != 1 {
		t.Fatalf("upgraded connection history=%+v err=%v", history, err)
	}
	locations, err := db.ClientConnectionLocations(ctx, []string{location.IP})
	if err != nil || locations[location.IP].CountryCode != "US" {
		t.Fatalf("upgraded location cache=%+v err=%v", locations, err)
	}
}
