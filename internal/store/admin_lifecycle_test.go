package store

import (
	"context"
	"errors"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func removeAdministrator(db *Store, ctx context.Context, id, action string) error {
	var err error
	switch action {
	case "purge":
		_, err = db.PurgeUser(ctx, id)
	case "disable":
		_, err = db.SetUserDisabled(ctx, id, true)
	case "demote":
		role := core.RoleUser
		_, err = db.UpdateUser(ctx, id, core.UserUpdate{Role: &role}, "")
	}
	return err
}

func createLifecycleAdministrator(t *testing.T, db *Store, ctx context.Context, username string) core.User {
	t.Helper()
	user, err := db.CreateUser(ctx, core.UserRequest{Username: username, Role: core.RoleAdmin}, "fixture-hash")
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func TestAdministratorProtectionIgnoresDisabledAccountsAndAdminScopes(t *testing.T) {
	for _, action := range []string{"purge", "disable", "demote"} {
		t.Run(action, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			last := createLifecycleAdministrator(t, db, ctx, "admin")
			disabled := createLifecycleAdministrator(t, db, ctx, "disabled-admin")
			if _, err := db.SetUserDisabled(ctx, disabled.ID, true); err != nil {
				t.Fatal(err)
			}
			// Admin scope and an admin username do not prove another login.
			adminScope := WithConfigScope(ctx, "", true)
			if err := removeAdministrator(db, adminScope, last.ID, action); !errors.Is(err, ErrConflict) {
				t.Fatalf("last administrator %s = %v", action, err)
			}
			users, err := db.ListUsers(adminScope)
			if err != nil {
				t.Fatal(err)
			}
			for _, user := range users {
				if user.ID == last.ID && user.AccountActions.DeleteRestriction != "last_administrator" {
					t.Fatalf("disabled administrator counted as available: %+v", user)
				}
			}
			if err := removeAdministrator(db, adminScope, disabled.ID, "purge"); err != nil {
				t.Fatalf("disabled administrator should remain deletable: %v", err)
			}
			other := createLifecycleAdministrator(t, db, ctx, "remaining-admin")
			if err := removeAdministrator(db, adminScope, last.ID, action); err != nil {
				t.Fatalf("another administrator exists: %v", err)
			}
			if _, _, err := db.UserForLogin(ctx, other.Username); err != nil {
				t.Fatalf("remaining login lost: %v", err)
			}
		})
	}
}

func TestConcurrentAdministratorRemovalPreservesLogin(t *testing.T) {
	for _, first := range []string{"purge", "disable", "demote"} {
		for _, second := range []string{"purge", "disable", "demote"} {
			t.Run(first+"/"+second, func(t *testing.T) {
				db, ctx, _ := isolatedConfigScopeStore(t)
				alice := createLifecycleAdministrator(t, db, ctx, "alice-admin")
				bob := createLifecycleAdministrator(t, db, ctx, "bob-admin")
				blocker, err := db.pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer blocker.Rollback(ctx)
				if _, err := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('qcontrolhub:users'))`); err != nil {
					t.Fatal(err)
				}
				results := make(chan error, 2)
				go func() { results <- removeAdministrator(db, ctx, alice.ID, first) }()
				go func() { results <- removeAdministrator(db, ctx, bob.ID, second) }()
				waitForPurgeBlockedBy(t, db, ctx, blocker.Conn().PgConn().PID())
				if err := blocker.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				succeeded, protected := 0, 0
				for range 2 {
					err := <-results
					if err == nil {
						succeeded++
					} else if errors.Is(err, ErrConflict) {
						protected++
					} else {
						t.Fatal(err)
					}
				}
				var active int
				if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM panel_users WHERE role='admin' AND NOT disabled`).Scan(&active); err != nil {
					t.Fatal(err)
				}
				if succeeded != 1 || protected != 1 || active != 1 {
					t.Fatalf("concurrent removals: succeeded=%d protected=%d active=%d", succeeded, protected, active)
				}
			})
		}
	}
}

func TestAccountMutationRechecksWaitingAdministratorAuthority(t *testing.T) {
	for _, action := range []string{"purge", "disable", "demote", "create"} {
		t.Run(action, func(t *testing.T) {
			db, ctx, _ := isolatedConfigScopeStore(t)
			actor := createLifecycleAdministrator(t, db, ctx, "actor")
			target := createLifecycleAdministrator(t, db, ctx, "target")
			requestCtx := WithUserManagementAuth(WithConfigScope(ctx, actor.ID, true), actor.ID, actor.AuthRevision, true)
			blocker, err := db.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(ctx)
			if _, err := blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('qcontrolhub:users'))`); err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() {
				if action == "create" {
					_, err := db.CreateUser(requestCtx, core.UserRequest{Username: "unauthorized-create", Role: core.RoleAdmin}, "fixture-hash")
					finished <- err
				} else {
					finished <- removeAdministrator(db, requestCtx, target.ID, action)
				}
			}()
			waitForPurgeBlockedBy(t, db, ctx, blocker.Conn().PgConn().PID())
			if _, err := blocker.Exec(ctx, `UPDATE panel_users SET disabled=true,auth_revision=auth_revision+1 WHERE id=$1`, actor.ID); err != nil {
				t.Fatal(err)
			}
			if err := blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-finished; !errors.Is(err, ErrForbidden) {
				t.Fatalf("stale administrator was allowed to %s: %v", action, err)
			}
			if user, err := db.UserForSession(ctx, target.ID); err != nil || user.Role != core.RoleAdmin {
				t.Fatalf("unauthorized mutation changed target: %+v %v", user, err)
			}
		})
	}
}
