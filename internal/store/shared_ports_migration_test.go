package store

import (
	"fmt"
	"slices"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestMigrateV59PreservesAllocatedAndEmptyPorts(t *testing.T) {
	db, ctx, databaseURL := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "ports-migration-owner")
	agent := sharedTestAgent(t, db, owner)
	users := make([]core.User, 2)
	shares := make([]core.AgentShare, 2)
	for i, ports := range [][]int{{21001, 21002}, {}} {
		user, _ := sharedTestUser(t, db, ctx, fmt.Sprintf("ports-migration-%d", i))
		users[i] = user
		access := sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 1000, ports...)
		shares[i] = access.Shares[0]
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET used_bytes=123;
		ALTER TABLE agent_shares DROP COLUMN ports_unrestricted;
		DELETE FROM qcontrolhub_schema_migrations WHERE version>=60;
		INSERT INTO qcontrolhub_schema_migrations(version) VALUES(59) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	migrated, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	for i, user := range users {
		access, err := migrated.UserAgentAccess(ctx, user.ID)
		if err != nil || len(access.Shares) != 1 {
			t.Fatalf("migration lost sharing: %+v %v", access, err)
		}
		got, prior := access.Shares[0], shares[i]
		if got.ID != prior.ID || got.Status != prior.Status || got.InvitationRevision != prior.InvitationRevision ||
			got.LimitBytes != prior.LimitBytes || got.UsedBytes != 123 || !slices.Equal(got.Ports, prior.Ports) {
			t.Fatalf("migration changed authority or ledger: %+v; was %+v", got, prior)
		}
	}
	var unrestricted int
	if err := migrated.pool.QueryRow(ctx, `SELECT count(*) FROM agent_shares WHERE ports_unrestricted`).Scan(&unrestricted); err != nil || unrestricted != 0 {
		t.Fatalf("migration implicitly granted unrestricted ports: %d %v", unrestricted, err)
	}
	sharedTestAllocation(t, migrated, ctx, users[0].ID, agent.ID, 1000, 0)
	reopened, err := OpenWithConfigKey(ctx, databaseURL, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if access, err := reopened.UserAgentAccess(ctx, users[0].ID); err != nil || len(access.Shares) != 1 ||
		!slices.Equal(access.Shares[0].Ports, []int{0}) || access.Shares[0].UsedBytes != 123 {
		t.Fatalf("restart lost unrestricted mode or reset the ledger: %+v %v", access, err)
	}
}
