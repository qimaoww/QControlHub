package store

import (
	"errors"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestRetryDoesNotDeployANewerConfiguration(t *testing.T) {
	for _, action := range []core.Action{core.ActionValidate, core.ActionDeploy} {
		t.Run(string(action), func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			_, owner := sharedTestUser(t, db, ctx, "retry-owner")
			agent := sharedTestAgent(t, db, owner)
			config := sharedTestConfig(t, db, owner, agent.ID, 21001)
			original, err := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: action})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.CancelTask(owner, original.ID); err != nil {
				t.Fatal(err)
			}
			config.Content += "log-level: debug\n"
			if _, err := db.SaveAgentConfig(owner, config, config.Version); err != nil {
				t.Fatal(err)
			}
			if task, err := db.RetryTask(owner, original.ID); !errors.Is(err, ErrConflict) {
				t.Fatalf("retry silently selected a different revision: %+v, %v", task, err)
			}
			var queued int
			if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE status='pending'`).Scan(&queued); err != nil || queued != 0 {
				t.Fatalf("rejected retry left work queued: %d, %v", queued, err)
			}
		})
	}
}

func TestRetryRequiresDurableConfigurationRevision(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "legacy-retry-owner")
	agent := sharedTestAgent(t, db, owner)
	config := sharedTestConfig(t, db, owner, agent.ID, 21001)
	original, err := db.CreateTask(owner, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, ConfigID: config.ID, Action: core.ActionDeploy})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CancelTask(owner, original.ID); err != nil {
		t.Fatal(err)
	}
	// A legacy record without an exact version cannot authorize whichever
	// draft happens to be current after upgrading the panel.
	if _, err := db.pool.Exec(ctx, `UPDATE tasks SET config_version=NULL WHERE id=$1`, original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RetryTask(owner, original.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy task without a version was silently retried: %v", err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE tasks SET config_version=$2 WHERE id=$1`, original.ID, config.Version); err != nil {
		t.Fatal(err)
	}
	retry, err := db.RetryTask(owner, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := db.RetryTask(owner, original.ID)
	if err != nil || !duplicate.Reused || duplicate.ID != retry.ID {
		t.Fatalf("same revision retry is not idempotent: %+v %v", duplicate, err)
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.ConfigVersion != config.Version || claimed.ConfigContent != config.Content {
		t.Fatalf("retry lost the exact snapshot: %+v %v", claimed, err)
	}
}

func TestPurgedPrincipalCannotRecreatePersonalResources(t *testing.T) {
	for _, operation := range []string{"archive", "template", "settings", "substore-settings", "substore-target"} {
		t.Run(operation, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			user, owner := sharedTestUser(t, db, ctx, "deleted-owner")
			if _, err := db.PurgeUser(ctx, user.ID); err != nil {
				t.Fatal(err)
			}
			// Model a request which passed cookie middleware before purge. The
			// owner strings deliberately have no FK: legacy token owners survive.
			var err error
			switch operation {
			case "archive":
				_, err = db.CreateConfig(owner, core.Config{Name: "orphan", Engine: core.EngineMihomo, Content: "mixed-port: 21001\n"})
			case "template":
				_, err = db.CreateConfigTemplate(owner, "orphan", "mihomo", "mixed-port: 21001\n")
			case "settings":
				_, err = db.SavePanelSettingsRevision(owner, core.DefaultPanelSettings(), 1)
			case "substore-settings":
				_, err = db.SaveSubStoreSyncSettings(owner, "https://substore.example.test/fixture")
			case "substore-target":
				_, err = db.CreateSubStoreSyncTarget(owner, "orphan")
			}
			if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) {
				t.Errorf("deleted principal recreated %s: %v", operation, err)
			}
			for _, table := range []string{"configs", "config_templates", "user_panel_settings", "user_substore_settings", "substore_sync_targets"} {
				var count int
				if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE owner_id=$1`, user.ID).Scan(&count); err != nil || count != 0 {
					t.Errorf("orphan records in %s: %d, %v", table, count, err)
				}
			}
		})
	}
}

func TestArchiveAndTemplateCreationSerializeWithPurge(t *testing.T) {
	for _, operation := range []string{"archive", "template"} {
		t.Run(operation, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			user, owner := sharedTestUser(t, db, ctx, "purging-owner")
			blocker, err := db.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(ctx)
			if _, err := blocker.Exec(ctx, `SELECT id FROM panel_users WHERE id=$1 FOR UPDATE`, user.ID); err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() {
				var err error
				if operation == "archive" {
					_, err = db.CreateConfig(owner, core.Config{Name: "pending", Engine: core.EngineMihomo, Content: "mixed-port: 21001\n"})
				} else {
					_, err = db.CreateConfigTemplate(owner, "pending", "mihomo", "mixed-port: 21001\n")
				}
				finished <- err
			}()
			waitForPurgeBlockedBy(t, db, ctx, blocker.Conn().PgConn().PID())
			if _, err := blocker.Exec(ctx, `DELETE FROM panel_users WHERE id=$1`, user.ID); err != nil {
				t.Fatal(err)
			}
			if err := blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-finished; !errors.Is(err, ErrNotFound) {
				t.Fatalf("waiting write survived deletion: %v", err)
			}
		})
	}
}

func TestPrincipalRevocationPreservesOtherUsersAndMaintenance(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	alice, ownerA := sharedTestUser(t, db, ctx, "revision-alice")
	bob, ownerB := sharedTestUser(t, db, ctx, "revision-bob")
	ownerA = WithPrincipalAuth(ownerA, alice.ID, alice.AuthRevision)
	ownerB = WithPrincipalAuth(ownerB, bob.ID, bob.AuthRevision)
	agentA := sharedTestAgent(t, db, ownerA)
	agentB := sharedTestAgent(t, db, ownerB)
	permissions := []core.Permission{core.PermissionAgentsRead}
	if _, err := db.UpdateUser(ctx, alice.ID, core.UserUpdate{Permissions: &permissions}, ""); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentName(ownerA, agentA.ID, "stale name"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("old request kept revoked management: %v", err)
	}
	if err := db.SetAgentName(ownerB, agentB.ID, "unaffected B"); err != nil {
		t.Fatalf("A's revocation blocked B: %v", err)
	}
	// Automatic monitoring is explicitly trusted maintenance after a saved
	// configuration commits. Its context may derive from a panel request.
	endpoint := core.PortTrafficEndpoint{AgentID: agentA.ID, Name: "automatic monitor", Engine: core.EngineMihomo, Port: 21001, Protocol: "both"}
	if _, err := db.ReconcileAgentPortTrafficEndpoints(WithSystemScope(ownerA), agentA.ID, []core.PortTrafficEndpoint{endpoint}, true); err != nil {
		t.Fatalf("expired initiating session blocked system reconciliation: %v", err)
	}
	var monitors int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM port_traffic_policies WHERE agent_id=$1 AND port=21001 AND monitoring_enabled`, agentA.ID).Scan(&monitors); err != nil || monitors != 1 {
		t.Fatalf("automatic monitoring was lost: %d %v", monitors, err)
	}
	// Compatibility token workspaces intentionally have no database user.
	if _, err := db.CreateConfig(WithPrincipalAuth(WithConfigScope(ctx, "token_legacy", false), "", 0), core.Config{Name: "legacy token", Engine: core.EngineMihomo, Content: "mixed-port: 22001\n"}); err != nil {
		t.Fatalf("legacy token compatibility: %v", err)
	}
}
