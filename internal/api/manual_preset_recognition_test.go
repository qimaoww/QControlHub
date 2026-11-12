package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestManualPresetRecognitionScopedSaveEditAndRead(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo} {
		for _, shared := range []bool{false, true} {
			t.Run(string(engine)+map[bool]string{false: "/owner", true: "/shared"}[shared], func(t *testing.T) {
				db, ctx, admin, owner, recipient := newConfigScopeAPIFixture(t)
				agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "manual-semantic", engine)
				grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 21001, 21002, 23101)
				client, foreign := owner, recipient
				if shared {
					client, foreign = recipient, owner
				}
				base := "/agents/" + agent.ID + "/configs/" + string(engine)
				source := `{"inbounds":[{"tag":"manual","listen":"0.0.0.0","port":21001,"protocol":"vless","settings":{"decryption":"none","clients":[{"id":"123e4567-e89b-42d3-a456-426614174000"}]}},{"tag":"editable","listen":"0.0.0.0","port":21002,"protocol":"vmess","settings":{"clients":[{"id":"123e4567-e89b-42d3-a456-426614174001","email":"first","level":9007199254740993},{"id":"123e4567-e89b-42d3-a456-426614174002","email":"second"}]},"future":{"nested":"keep"}}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`
				if engine == core.EngineSingBox {
					source = `{"inbounds":[{"tag":"manual","listen":"0.0.0.0","listen_port":21001,"type":"vless","users":[{"uuid":"123e4567-e89b-42d3-a456-426614174000"}]},{"tag":"editable","listen":"0.0.0.0","listen_port":21002,"type":"vmess","users":[{"uuid":"123e4567-e89b-42d3-a456-426614174001","name":"first"}],"future":{"nested":9007199254740993}}],"outbounds":[{"tag":"direct","type":"direct"}]}`
				}
				if engine == core.EngineMihomo {
					source = `listeners: [{name: manual, type: vless, listen: 0.0.0.0, port: 21001, users: [{uuid: 123e4567-e89b-42d3-a456-426614174000}]}, {name: editable, type: vmess, listen: 0.0.0.0, port: 21002, users: [{uuid: 123e4567-e89b-42d3-a456-426614174001, username: first, future: 9007199254740993}, {uuid: 123e4567-e89b-42d3-a456-426614174002, username: second}], future: {nested: keep}}]
rules: ['MATCH,DIRECT']`
				}
				var config core.Config
				client.call("PUT", base, map[string]any{"name": "manual", "engine": engine, "content": source, "version": 0}, http.StatusOK, &config)
				var workspace agentConfigWorkspaceResource
				client.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
				if len(workspace.Inbounds) != 2 || !workspace.Inbounds[0].SourceOnly || workspace.Inbounds[1].SourceOnly {
					t.Fatal("manual source misclassified")
				}
				if workspace.Config.Content != config.Content {
					t.Fatal("recognition changed source")
				}
				foreign.call("GET", base+"/server-inbounds/manual", nil, http.StatusNotFound, nil)
				admin.call("GET", base+"/server-inbounds/manual", nil, http.StatusNotFound, nil)
				candidate := workspace.Inbounds[1]
				candidate.Tag = "manual"
				body := map[string]any{"operation": "modify", "original_tag": "manual", "expected_version": config.Version, "intent": "validate", "input": candidate}
				// Do not trust a caller that drops the read-only projection marker.
				for i := 0; i < 2; i++ {
					client.call("POST", base+"/server-inbounds", body, http.StatusBadRequest, nil)
				}
				var unchanged core.Config
				client.call("GET", base, nil, http.StatusOK, &unchanged)
				if unchanged.Version != config.Version || unchanged.Content != config.Content {
					t.Fatal("failed preset replacement changed saved source")
				}
				candidate = workspace.Inbounds[1]
				candidate.Port = 23101
				body["original_tag"], body["input"], body["intent"] = "editable", candidate, "deploy"
				var result configMutationResult
				client.call("POST", base+"/server-inbounds", body, http.StatusOK, &result)
				if result.Task.ConfigVersion != result.Config.Version || result.Task.SharedInstance != shared || !strings.Contains(result.Config.Content, "9007199254740993") {
					t.Fatal("edit lost precision or task scope")
				}
				if engine != core.EngineSingBox && !strings.Contains(result.Config.Content, "second") {
					t.Fatal("edit lost extra account")
				}
				client.call("POST", base+"/server-inbounds", body, http.StatusConflict, nil)
				completeConfigScopeAPITask(t, ctx, db, result.Task)
				client.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
				if workspace.Deployment.State != "applied" || len(workspace.Inbounds) != 2 || !workspace.Inbounds[0].SourceOnly || workspace.Inbounds[1].SourceOnly || workspace.Inbounds[1].Port != 23101 {
					t.Fatal("save/deploy/read semantic drift")
				}
				var revisions []core.Config
				client.call("GET", "/configs/"+config.ID+"/revisions?limit=10", nil, http.StatusOK, &revisions)
				if len(revisions) != 2 || revisions[0].Version == revisions[1].Version || revisions[0].UpdatedAt.IsZero() {
					t.Fatal("successful saves did not retain distinct dated archives")
				}
			})
		}
	}
}
