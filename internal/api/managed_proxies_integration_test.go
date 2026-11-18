package api

import (
	"github.com/qimaoww/qcontrolhub/internal/core"
	"testing"
)

func TestManagedProxyNodePermissions(t *testing.T) {
	db, ctx, admin, owner, shared := newConfigScopeAPIFixture(t)
	node, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "proxy-test")
	req := core.TaskRequest{AgentID: node.ID, Action: core.ActionUpdateNginx}
	owner.call("POST", "/tasks", req, 409, nil)
	shared.call("POST", "/tasks", req, 404, nil)
	grantConfigScopeAPIAgent(t, ctx, db, node, shared, 21001)
	check := "/agents/" + node.ID + "/proxies/nginx/check"
	status := core.ProxyStatus{State: "pending", Running: "1.22.1-9+deb12u10", Installed: "1.31.9-1~bookworm", Managed: true}
	if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: append(node.Features, core.AgentFeatureManagedProxies), ManagedProxies: map[string]core.ProxyStatus{"nginx": status}}); err != nil {
		t.Fatal(err)
	}
	for _, client := range []configScopeAPIClient{admin, owner, shared} {
		var agents []core.Agent
		client.call("GET", "/agents", nil, 200, &agents)
		found := false
		for _, a := range agents {
			if a.ID == node.ID {
				found = true
				if client.userID == shared.userID && a.ManagedProxies != nil {
					t.Fatal("shared account received host status")
				}
				if client.userID != shared.userID && a.ManagedProxies["nginx"] != status {
					t.Fatal("owner report mismatch")
				}
			}
		}
		if !found {
			t.Fatal("expected allocated node")
		}
	}
	shared.call("POST", check, nil, 403, nil)
	shared.call("POST", "/tasks", req, 403, nil)
	var update core.Task
	owner.call("POST", "/tasks", req, 201, &update)
	completeConfigScopeAPITask(t, ctx, db, update)
	perms := []core.Permission{core.PermissionAgentsRead}
	admin.call("PUT", "/users/"+owner.userID, core.UserUpdate{Permissions: &perms}, 200, nil)
	owner = loginCoreInstallUser(t, owner, "alice")
	owner.call("POST", "/tasks", req, 403, nil)
	var first, second core.Task
	owner.call("POST", check, nil, 202, &first)
	owner.call("POST", check, nil, 202, &second)
	if first.ID != second.ID || !second.Reused {
		t.Fatal("concurrent check not reused")
	}
	completeConfigScopeAPITask(t, ctx, db, first)
}
