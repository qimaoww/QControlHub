package api

import (
	"net/http"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestSocksPresetAccountIsolation(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo} {
		t.Run(string(engine), func(t *testing.T) {
			db, ctx, _, owner, shared := newConfigScopeAPIFixture(t)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "socks-presets", engine)
			if err := db.SetAgentClientAddress(ctx, agent.ID, "edge.example.test"); err != nil {
				t.Fatal(err)
			}
			grantConfigScopeAPIAgent(t, ctx, db, agent, shared, 32412, 32413)
			base := "/agents/" + agent.ID + "/configs/" + string(engine)
			for _, client := range []configScopeAPIClient{owner, shared} {
				var input serverconfig.Input
				client.call("POST", base+"/plans", map[string]string{"protocol": serverconfig.ProtocolSocks}, http.StatusCreated, &input)
				input.Tag, input.Port = "same-name", 24441
				if client.userID == shared.userID {
					input.Port = 32412
				}
				var saved configMutationResult
				body := map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": input}
				invalid := input
				invalid.Username = ""
				body["input"] = invalid
				client.call("POST", base+"/server-inbounds", body, http.StatusBadRequest, nil)
				body["input"] = input
				client.call("POST", base+"/server-inbounds", body, http.StatusOK, &saved)
				if saved.Task.SharedInstance != (client.userID == shared.userID) {
					t.Fatal("wrong target instance")
				}
				completeConfigScopeAPITask(t, ctx, db, saved.Task)
				client.call("POST", base+"/server-inbounds", body, http.StatusConflict, nil)
				input.Credential = "updated-socks-password"
				body["operation"], body["original_tag"], body["expected_version"], body["input"] = "modify", input.Tag, saved.Config.Version, input
				reader, _ := testCreateBusinessKey(t, client, []core.Permission{core.PermissionAgentConfigRead})
				reader.call("POST", base+"/server-inbounds", body, http.StatusForbidden, nil)
				writer, _ := testCreateBusinessKey(t, client, []core.Permission{core.PermissionAgentConfigRead, core.PermissionAgentConfigWrite, core.PermissionTasksRead, core.PermissionTasksExecute})
				writer.call("POST", base+"/server-inbounds", body, http.StatusOK, &saved)
				completeConfigScopeAPITask(t, ctx, db, saved.Task)
				var workspace agentConfigWorkspaceResource
				client.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
				if len(workspace.Inbounds) != 1 || workspace.Inbounds[0].Username != input.Username || workspace.Inbounds[0].Credential != input.Credential || workspace.Inbounds[0].Port != input.Port {
					t.Fatal("workspace mixed accounts or lost SOCKS5 credentials")
				}
				var exported struct{ Content string }
				path := workspaceExportPath(base, workspace)
				client.call("GET", path, nil, http.StatusOK, &exported)
				if exported.Content == "" {
					t.Fatal("SOCKS5 client export is empty")
				}
				foreign := shared
				if client.userID == shared.userID {
					foreign = owner
				}
				foreign.call("GET", path, nil, http.StatusConflict, nil)
			}
			var retained core.Config
			owner.call("GET", base, nil, http.StatusOK, &retained)
			inbounds := serverconfig.ParseAll(engine, retained.Content)
			if len(inbounds) != 1 || inbounds[0].Port != 24441 {
				t.Fatal("shared save changed owner's instance")
			}
		})
	}
}
