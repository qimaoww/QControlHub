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
	admin.call("POST", check, nil, 409, nil)
	var activity nodeProxyReport
	owner.call("GET", "/agents/"+node.ID+"/proxies", nil, 200, &activity)
	if state := activity.Tasks[core.ActionUpdateNginx]; state.Status != core.TaskPending || state.FinishedAt != nil {
		t.Fatalf("queued activity %+v", state)
	}
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
	owner.call("GET", "/agents/"+node.ID+"/proxies", nil, 200, &activity)
	for _, action := range []core.Action{core.ActionUpdateNginx, core.ActionCheckNginx} {
		if state := activity.Tasks[action]; state.Status != core.TaskSucceeded || state.FinishedAt == nil {
			t.Fatalf("terminal %s %+v", action, state)
		}
	}
}

func TestManagedProxyAPIKeyManagement(t *testing.T) {
	db, ctx, _, owner, other := newConfigScopeAPIFixture(t)
	node, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "managed proxy key")
	base := "/agents/" + node.ID + "/proxies"
	write, _ := testCreateBusinessKey(t, owner, []core.Permission{core.PermissionAgentsRead, core.PermissionAgentsManage, core.PermissionTasksExecute, core.PermissionTasksRead})
	check, _ := testCreateBusinessKey(t, owner, []core.Permission{core.PermissionAgentsRead})
	var cred apiKeyCreation
	owner.call("POST", "/settings/api-keys", core.APIKeyInput{Name: "status", Permissions: []core.Permission{core.PermissionAgentsRead}}, 201, &cred)
	reader := configScopeAPIClient{t: t, handler: owner.handler, token: cred.Secret}
	foreign, _ := testCreateBusinessKey(t, other, []core.Permission{core.PermissionAgentsRead, core.PermissionAgentsManage, core.PermissionTasksExecute})
	var report nodeProxyReport
	reader.call("GET", base, nil, 200, &report)
	if report.Supported || report.Nginx.State != "unknown" || report.Caddy.State != "unknown" {
		t.Fatalf("legacy node report %+v", report)
	}
	foreign.call("GET", base, nil, 404, nil)
	grantConfigScopeAPIAgent(t, ctx, db, node, other, 21001)
	foreign.call("GET", base, nil, 403, nil)
	state := core.ProxyStatus{State: "active", Managed: true, Running: "v2.11.9"}
	if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: append(node.Features, core.AgentFeatureManagedProxies), ManagedProxies: map[string]core.ProxyStatus{"caddy": state}}); err != nil {
		t.Fatal(err)
	}
	reader.call("GET", base, nil, 200, &report)
	if !report.Supported || report.Caddy != state || report.AgentID != node.ID {
		t.Fatalf("proxy status %+v", report)
	}
	for _, mode := range []string{"nginx", "caddy"} {
		path := base + "/" + mode
		foreign.call("POST", path+"/update", nil, 403, nil)
		reader.call("POST", path+"/update", nil, 403, nil)
		check.call("POST", path+"/update", nil, 403, nil)
		for _, operation := range []string{"check", "update"} {
			client := write
			if operation == "check" {
				client = check
			}
			var task core.Task
			client.call("POST", path+"/"+operation, nil, 202, &task)
			if string(task.Action) != operation+"-"+mode || task.AgentID != node.ID {
				t.Fatalf("incorrect task %+v", task)
			}
			completeConfigScopeAPITask(t, ctx, db, task)
			write.call("GET", "/tasks/"+task.ID, nil, 200, nil)
		}
	}
	write.call("POST", base+"/other/update", nil, 400, nil)
}
