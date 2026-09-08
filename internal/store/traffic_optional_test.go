package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func optionalTrafficTestStore(t *testing.T) (context.Context, *Store) {
	t.Helper()
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	schema, err := testdb.IsolatePostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { schema.Close(context.Background()) })
	db, err := Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return ctx, db
}

func TestMonitorOnlyEditsSurviveDiscoveryWithPostgreSQL(t *testing.T) {
	ctx, db := optionalTrafficTestStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	endpoints := []core.PortTrafficEndpoint{{AgentID: agent.ID, Engine: core.EngineMihomo, Name: "discovered", Port: 443, Protocol: core.TrafficProtocolBoth}}
	if _, err := db.ReconcilePortTrafficEndpoints(ctx, endpoints, false); err != nil {
		t.Fatal(err)
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 {
		t.Fatalf("discovery: %+v, %v", policies, err)
	}
	initial := policies[0]
	now := time.Now().UTC()
	if err := db.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{{
		PolicyID: initial.ID, ResetGeneration: initial.ResetGeneration, ReceivedBytes: 100, SentBytes: 200, UsedBytes: 300,
		PeriodStart: initial.CycleAnchor, PeriodEnd: initial.CycleAnchor.AddDate(0, 1, 0),
	}}, now); err != nil {
		t.Fatal(err)
	}
	request := core.PortTrafficPolicyRequest{
		AgentID: agent.ID, Name: "operator name", Engine: initial.Engine, Port: initial.Port,
		Protocol: initial.Protocol, Cycle: initial.Cycle, CycleAnchor: initial.CycleAnchor,
	}
	// POST also promotes an existing discovered row instead of duplicating it.
	edited, err := db.CreatePortTrafficPolicy(ctx, request)
	if err != nil || edited.ID != initial.ID || edited.QuotaEnabled || edited.AutoBlock || !edited.MetadataManaged || edited.UsedBytes != 300 {
		t.Fatalf("metadata-only edit: %+v, %v", edited, err)
	}
	endpoints[0].Name = "config renamed"
	endpoints[0].Protocol = core.TrafficProtocolTCP
	for _, prune := range []bool{false, true} {
		changed, err := db.ReconcilePortTrafficEndpoints(ctx, endpoints, prune)
		if err != nil || len(changed) != 0 {
			t.Fatalf("reconcile prune=%v: %v, %v", prune, changed, err)
		}
		policies, err = db.AgentPortTrafficPolicies(ctx, agent.ID)
		if err != nil || len(policies) != 1 || policies[0].Name != request.Name || policies[0].Protocol != initial.Protocol || policies[0].ResetGeneration != initial.ResetGeneration || policies[0].UsedBytes != 300 {
			t.Fatalf("operator metadata/counters overwritten: %+v, %v", policies, err)
		}
	}
	history, err := db.ListPortTrafficDailyUsage(ctx, agent.ID, initial.ID, now)
	if err != nil || len(history) != 1 || history[0].UsedBytes != 300 {
		t.Fatalf("history lost: %+v, %v", history, err)
	}
	request.Port = 8443
	created, err := db.CreatePortTrafficPolicy(ctx, request)
	if err != nil || created.ID == initial.ID || created.LimitBytes != 0 || created.QuotaEnabled || created.AutoBlock || !created.MonitoringEnabled {
		t.Fatalf("new monitor-only policy: %+v, %v", created, err)
	}
}

func TestOpenMigratesOptionalTrafficQuotaFromV45(t *testing.T) {
	ctx, db := optionalTrafficTestStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	request := core.PortTrafficPolicyRequest{
		AgentID: agent.ID, Name: "legacy quota", Engine: core.EngineMihomo, Port: 443,
		Protocol: core.TrafficProtocolTCP, Cycle: core.TrafficCycleMonthly, CycleAnchor: core.UTCDate(time.Now()), LimitBytes: 1024,
	}
	policy, err := db.CreatePortTrafficPolicy(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `
		ALTER TABLE port_traffic_policies DROP COLUMN metadata_managed;
		ALTER TABLE port_traffic_policies DROP CONSTRAINT port_traffic_policies_limit_bytes_check;
		ALTER TABLE port_traffic_policies ADD CONSTRAINT port_traffic_policies_limit_bytes_check CHECK (limit_bytes > 0);
		DELETE FROM qcontrolhub_schema_migrations;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(45);
	`); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(ctx, db.pool.Config().ConnString(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	policies, err := upgraded.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].ID != policy.ID || policies[0].LimitBytes != 1024 || !policies[0].QuotaEnabled {
		t.Fatalf("legacy quota changed during migration: %+v, %v", policies, err)
	}
	request.LimitBytes = 0
	updated, err := upgraded.UpdatePortTrafficPolicy(ctx, policy.ID, request)
	if err != nil || updated.QuotaEnabled || updated.AutoBlock || updated.LimitBytes != 0 || !updated.MetadataManaged {
		t.Fatalf("upgraded schema rejects monitor-only edit: %+v, %v", updated, err)
	}
	if _, err := upgraded.pool.Exec(ctx, `UPDATE port_traffic_policies SET limit_bytes=-1 WHERE id=$1`, policy.ID); err == nil {
		t.Fatal("negative limits must remain invalid")
	}
}

func TestManualTrafficSyncCountsRetainedPortsAtomically(t *testing.T) {
	ctx, db := optionalTrafficTestStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	endpoints := make([]core.PortTrafficEndpoint, 256)
	for index := range endpoints {
		endpoints[index] = core.PortTrafficEndpoint{
			AgentID: agent.ID, Name: "old monitor", Engine: core.EngineMihomo, Port: 1000 + index, Protocol: core.TrafficProtocolTCP,
		}
	}
	if _, err := db.ReconcilePortTrafficEndpoints(ctx, endpoints, true); err != nil {
		t.Fatal(err)
	}
	next := []core.PortTrafficEndpoint{{AgentID: agent.ID, Name: "new monitor", Engine: core.EngineMihomo, Port: 8443, Protocol: core.TrafficProtocolTCP}}
	if _, err := db.ReconcilePortTrafficEndpoints(ctx, next, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("manual sync must count retained ports: %v", err)
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 256 || policies[0].Port != 1000 || policies[255].Port != 1255 {
		t.Fatalf("rejected sync changed existing ports: %+v, %v", policies, err)
	}
	if _, err := db.ReconcilePortTrafficEndpoints(ctx, next, true); err != nil {
		t.Fatal(err)
	}
	policies, err = db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].Port != 8443 {
		t.Fatalf("automatic pruning must release old slots: %+v, %v", policies, err)
	}
}
