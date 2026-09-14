package api

import (
	"net/http"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// User management belongs to the administrator role. Explicit grants must
// agree with the store's existing role-based administrator scope.
//
// The console does not offer the capability, which is exactly why this needs a
// regression test: the invariant is only observable on the server side.
func TestUserRoleCannotHoldAdminEquivalentPermission(t *testing.T) {
	_, _, admin, _, _ := newConfigScopeAPIFixture(t)

	// Creating a user account with the capability is rejected.
	admin.call("POST", "/users", core.UserRequest{
		Username:    "escalation-attempt",
		Password:    "escalation attempt password",
		Role:        core.RoleUser,
		Permissions: []core.Permission{core.PermissionOverviewRead, core.PermissionUsersManage},
	}, http.StatusBadRequest, nil)

	// Once the account exists without it, the capability cannot be added later
	// either -- not on the account's own row, and not through a role change.
	var created core.User
	admin.call("POST", "/users", core.UserRequest{
		Username:    "limited-operator",
		Password:    "limited operator password",
		Role:        core.RoleUser,
		Permissions: []core.Permission{core.PermissionOverviewRead},
	}, http.StatusCreated, &created)

	admin.call("PUT", "/users/"+created.ID, core.UserUpdate{
		Permissions: &[]core.Permission{core.PermissionOverviewRead, core.PermissionUsersManage},
	}, http.StatusBadRequest, nil)

	// The rejected requests must not have changed the stored row.
	var users []core.User
	admin.call("GET", "/users", nil, http.StatusOK, &users)
	found := false
	for _, user := range users {
		if user.ID != created.ID {
			continue
		}
		found = true
		if core.HasPermission(user.Permissions, core.PermissionUsersManage) {
			t.Fatalf("user %q stored an administrator capability: %v", user.Username, user.Permissions)
		}
		if len(user.Permissions) != 1 || user.Permissions[0] != core.PermissionOverviewRead {
			t.Fatalf("user %q permissions changed after rejected requests: %v", user.Username, user.Permissions)
		}
	}
	if !found {
		t.Fatalf("created account %q is missing from the user list", created.ID)
	}

	// An administrator still holds the capability, so the guard cannot lock the
	// panel out of user management.
	if !core.HasPermission(core.AllPermissions(), core.PermissionUsersManage) {
		t.Fatal("the administrator role lost users.manage")
	}
}

func TestAdministratorPermissionsRoundTrip(t *testing.T) {
	_, _, admin, alice, _ := newConfigScopeAPIFixture(t)
	var created core.User
	admin.call("POST", "/users", core.UserRequest{
		Username: "another-admin", Password: "administrator password fixture",
		Role: core.RoleAdmin, Permissions: core.AllPermissions(),
	}, http.StatusCreated, &created)
	if !core.HasPermission(created.Permissions, core.PermissionUsersManage) {
		t.Fatal("administrator creation dropped user management")
	}
	// A client may send back the permission list from GET /users without
	// including a role change. It is valid for an existing administrator.
	admin.call("PUT", "/users/"+created.ID, core.UserUpdate{
		Permissions: &created.Permissions,
	}, http.StatusOK, &created)

	role := core.RoleAdmin
	all := core.AllPermissions()
	admin.call("PUT", "/users/"+alice.userID, core.UserUpdate{
		Role: &role, Permissions: &all,
	}, http.StatusOK, nil)
	role = core.RoleUser
	admin.call("PUT", "/users/"+alice.userID, core.UserUpdate{
		Role: &role, Permissions: &all,
	}, http.StatusBadRequest, nil)
	var demoted core.User
	admin.call("PUT", "/users/"+alice.userID, core.UserUpdate{Role: &role}, http.StatusOK, &demoted)
	if len(demoted.Permissions) != 0 {
		t.Fatalf("demoted administrator retained permissions: %v", demoted.Permissions)
	}
}
