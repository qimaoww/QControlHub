package api

import (
	"net/http"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// users.manage is administrator-equivalent: UpdateUser authorizes on
// scope.Admin, scope.Admin comes from the stored role, and an administrator
// resolves every agent without an owner filter. An account holding it can
// therefore promote itself and read every other account's fleet, so it must
// never be stored on a user row.
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

// The console does not offer users.manage for assignment. If a future change
// adds it, this test fails and points at the invariant, instead of silently
// widening every user account created from then on.
func TestGrantablePermissionsExcludeAdministratorCapabilities(t *testing.T) {
	grantable := core.GrantablePermissions()
	if core.HasPermission(grantable, core.PermissionUsersManage) {
		t.Fatal("users.manage is admin-equivalent and must not be grantable")
	}
	if len(grantable) != len(core.AllPermissions())-1 {
		t.Fatalf("grantable permissions = %d, all = %d; expected exactly one administrator-only capability",
			len(grantable), len(core.AllPermissions()))
	}
	// Normalization must drop the capability so the API layer's length check
	// reports an invalid request instead of storing a second administrator.
	stored := core.NormalizePermissions(
		[]core.Permission{core.PermissionOverviewRead, core.PermissionUsersManage},
		grantable,
	)
	if len(stored) != 1 || core.HasPermission(stored, core.PermissionUsersManage) {
		t.Fatalf("NormalizePermissions kept an administrator capability: %v", stored)
	}
	// A role that already carries full authority normalizes against the full set.
	if got := core.NormalizePermissions([]core.Permission{core.PermissionUsersManage}, core.AllPermissions()); len(got) != 1 {
		t.Fatalf("administrator normalization dropped users.manage: %v", got)
	}
}
