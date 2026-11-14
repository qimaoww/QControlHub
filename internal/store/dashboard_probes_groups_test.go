package store

import (
	"testing"
	"time"
)

func TestDashboardProbeGroupsMigrationPreservesChainsAndHistory(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent, _ := enrollTaskTestAgent(t, ctx, db)
	if _, err := db.pool.Exec(ctx, `DROP INDEX dashboard_probe_pairs_owner_group_idx; ALTER TABLE dashboard_probe_pairs DROP CONSTRAINT dashboard_probe_group_required`); err != nil {
		t.Fatal(err)
	}
	for i, group := range []string{"骨干", "骨干", "", "骨干 (2)"} {
		id := []string{"legacy-a", "legacy-b", "legacy-c", "legacy-d"}[i]
		if _, err := db.pool.Exec(ctx, `INSERT INTO dashboard_probe_pairs(id,owner_id,name,group_name,node1_agent_id,node1_address,node2_address,parameters,created_at) VALUES($1,'owner','原链路',$2,$3,'192.0.2.1','1.1.1.1','{}',$4)`, id, group, agent.ID, time.Now().Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.pool.Exec(ctx, `INSERT INTO dashboard_probe_samples(id,pair_id,session_id,state,sampled_at) VALUES($1,$2,$1,'succeeded',now())`, "sample-"+id, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.pool.Exec(ctx, `DELETE FROM qcontrolhub_schema_migrations WHERE version=$1`, currentSchemaVersion); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var total, groups, samples int
	if err := db.pool.QueryRow(ctx, `SELECT count(*),count(DISTINCT group_name) FROM dashboard_probe_pairs WHERE owner_id='owner'`).Scan(&total, &groups); err != nil {
		t.Fatal(err)
	}
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM dashboard_probe_samples WHERE pair_id LIKE 'legacy-%'`).Scan(&samples); err != nil {
		t.Fatal(err)
	}
	if total != 4 || groups != 4 || samples != 4 {
		t.Fatalf("chains=%d groups=%d samples=%d", total, groups, samples)
	}
	var group string
	if err := db.pool.QueryRow(ctx, `SELECT group_name FROM dashboard_probe_pairs WHERE id='legacy-a'`).Scan(&group); err != nil || group != "骨干" {
		t.Fatalf("oldest group=%q %v", group, err)
	}
	// The index protects all callers, even direct competing SQL transactions.
	if _, err := db.pool.Exec(ctx, `UPDATE dashboard_probe_pairs SET group_name='骨干' WHERE id='legacy-b'`); err == nil {
		t.Fatal("database accepted duplicate group")
	}
	if _, err := db.pool.Exec(ctx, `UPDATE dashboard_probe_pairs SET group_name='' WHERE id='legacy-c'`); err == nil {
		t.Fatal("database accepted empty group")
	}
}
