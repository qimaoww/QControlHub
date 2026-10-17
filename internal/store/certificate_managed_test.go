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
}
