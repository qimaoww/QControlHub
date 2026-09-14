package store

import (
	"errors"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestUserPermissionStoreRejectsAdministratorGrant(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	admin := WithConfigScope(ctx, "", true)
	forbidden := []core.Permission{core.PermissionOverviewRead, core.PermissionUsersManage}
	t.Run("create", func(t *testing.T) {
		_, err := db.CreateUser(admin, core.UserRequest{
			Username: "forbidden-grant", Role: core.RoleUser, Permissions: forbidden,
		}, "password hash fixture")
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("direct store create should reject the grant, got %v", err)
		}
	})
	t.Run("update", func(t *testing.T) {
		user, err := db.CreateUser(admin, core.UserRequest{
			Username: "limited-user", Role: core.RoleUser, Permissions: []core.Permission{core.PermissionOverviewRead},
		}, "password hash fixture")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.UpdateUser(admin, user.ID, core.UserUpdate{Permissions: &forbidden}, ""); !errors.Is(err, ErrInvalid) {
			t.Fatalf("direct store update should reject the grant, got %v", err)
		}
	})
}

func TestLegacyUserPermissionDoesNotBlockRevocation(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	admin := WithConfigScope(ctx, "", true)
	user, err := db.CreateUser(admin, core.UserRequest{
		Username: "legacy-grant", Role: core.RoleUser, Permissions: []core.Permission{core.PermissionOverviewRead},
	}, "password hash fixture")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an older database/backup with an impossible grant.
	if _, err := db.pool.Exec(ctx, `UPDATE panel_users SET permissions='["overview.read","users.manage"]' WHERE id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	t.Run("login and session", func(t *testing.T) {
		loggedIn, _, err := db.UserForLogin(ctx, user.Username)
		if err != nil {
			t.Fatal(err)
		}
		session, err := db.UserForSession(ctx, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, resolved := range []core.User{loggedIn, session} {
			if core.HasPermission(resolved.Permissions, core.PermissionUsersManage) {
				t.Error("a legacy non-administrator grant remains effective")
			}
		}
	})
	t.Run("disable", func(t *testing.T) {
		disabled, err := db.SetUserDisabled(admin, user.ID, true)
		if err != nil || !disabled.Disabled {
			t.Fatalf("could not disable legacy account: %+v %v", disabled, err)
		}
		var retainsGrant bool
		if err := db.pool.QueryRow(ctx, `SELECT permissions @> '["users.manage"]'::jsonb FROM panel_users WHERE id=$1`, user.ID).Scan(&retainsGrant); err != nil {
			t.Fatal(err)
		}
		if retainsGrant {
			t.Fatal("the update did not remove the legacy grant from storage")
		}
	})
}
