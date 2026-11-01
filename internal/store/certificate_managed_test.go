package store

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestManagedCertificateMissingEncryptionBlocksSecretRequests(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := allEnginesSharedTestAgent(t, db, ctx)
	_, err := db.pool.Exec(ctx, `INSERT INTO managed_certificate_sources(id,agent_id,owner_id,config_id,task_id,engine,domain) VALUES('mcs_encryption_gate',$1,'admin','retained-config','retained-task',$2,'edge.example.test')`, agent.ID, core.EngineSingBox)
	if err != nil {
		t.Fatal(err)
	}
	previous := db.cryptor
	db.cryptor = nil
	defer func() { db.cryptor = previous }()
	sources, err := db.AgentManagedCertificateSources(ctx, agent.ID)
	if err != nil || len(sources) != 0 {
		t.Fatalf("missing encryption requested private material: %d %v", len(sources), err)
	}
	var status, reason string
	if err = db.pool.QueryRow(ctx, `SELECT status,error FROM managed_certificate_sources WHERE id='mcs_encryption_gate'`).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "blocked" || !strings.Contains(reason, "加密") {
		t.Fatalf("missing actionable block: %s %s", status, reason)
	}
	if _, err = db.pool.Exec(ctx, `INSERT INTO managed_certificate_sources(id,agent_id,owner_id,config_id,task_id,engine,domain,source_kind,source_key,status) VALUES('mcs_unselected',$1,'admin','retained-config','retained-task',$2,'edge.example.test','node','files','available')`, agent.ID, core.EngineSingBox); err != nil {
		t.Fatal(err)
	}
	if _, err = db.AgentManagedCertificateSources(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.pool.QueryRow(ctx, `SELECT status FROM managed_certificate_sources WHERE id='mcs_unselected'`).Scan(&status); err != nil || status != "available" {
		t.Fatal("encryption recovery would implicitly enroll an unselected source")
	}
}

func TestManagedCertificateSourceSchemaUpgradePreservesCaddyAndConsent(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	agent := allEnginesSharedTestAgent(t, db, ctx)
	if _, err := db.pool.Exec(ctx, `INSERT INTO managed_certificate_sources(id,agent_id,owner_id,config_id,task_id,engine,domain,status) VALUES('mcs_retained',$1,'admin','cfg_retained','tsk_retained',$2,'edge.example.test','paused')`, agent.ID, core.EngineSingBox); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the previous source table shape, then run the real migration.
	if _, err := db.pool.Exec(ctx, `DROP INDEX managed_certificate_sources_identity;
ALTER TABLE managed_certificate_sources DROP COLUMN source_kind;
ALTER TABLE managed_certificate_sources DROP COLUMN source_key;
ALTER TABLE managed_certificate_sources ADD UNIQUE(agent_id,owner_id,config_id,domain);
DELETE FROM qcontrolhub_schema_migrations;
INSERT INTO qcontrolhub_schema_migrations(version) VALUES(84)`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var kind, status string
	if err := db.pool.QueryRow(ctx, `SELECT source_kind,status FROM managed_certificate_sources WHERE id='mcs_retained'`).Scan(&kind, &status); err != nil || kind != "caddy" || status != "paused" {
		t.Fatal("upgrade changed retained Caddy source or consent")
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO managed_certificate_sources(id,agent_id,owner_id,config_id,task_id,engine,domain,source_kind,source_key,status) VALUES('mcs_node',$1,'admin','cfg_retained','tsk_retained',$2,'edge.example.test','node','files','available')`, agent.ID, core.EngineSingBox); err != nil {
		t.Fatal("native and Caddy sources cannot coexist after upgrade")
	}
}
