package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestWorkspaceDraftPreviewAndAtomicSharedFieldSave(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	agent := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 21001)
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21002)
	base := "/agents/" + agent.ID + "/configs/mihomo"
	var own, other core.Config
	alice.call("PUT", base, configScopeAPIConfig(t, 21001), http.StatusOK, &own)
	bob.call("PUT", base, configScopeAPIConfig(t, 21002), http.StatusOK, &other)
	draft := "# user draft comment\nprofile:\n  store-selected: true\n  custom-counter: 18446744073709551615\n  defaults: &future [18446744073709551615]\n  linked: *future\n" + own.Content
	var preview agentConfigWorkspaceResource
	alice.call("POST", base+"/workspace/preview", configDraftPreview{Content: draft, ExpectedVersion: own.Version}, http.StatusOK, &preview)
	if preview.Config == nil || preview.Config.ID != own.ID || preview.Config.Content != draft || len(preview.Inbounds) != 1 || preview.Inbounds[0].Port != 21001 {
		t.Fatal("preview did not use this account's complete draft and saved version")
	}
	var field struct {
		Fragment string `json:"fragment"`
		Version  int    `json:"version"`
	}
	alice.call("POST", base+"/fields/custom-counter/preview", configDraftPreview{Content: draft, ExpectedVersion: own.Version}, http.StatusBadRequest, nil)
	alice.call("POST", base+"/fields/log-level/preview", configDraftPreview{Content: draft, ExpectedVersion: own.Version}, http.StatusOK, &field)
	if field.Version != own.Version || field.Fragment == "" {
		t.Fatal("field preview lost its saved version or draft fragment")
	}
	alice.call("POST", base+"/workspace/preview", configDraftPreview{Content: draft, ExpectedVersion: own.Version + 1}, http.StatusConflict, nil)
	alice.call("POST", base+"/workspace/preview", configDraftPreview{Content: "listeners: [", ExpectedVersion: own.Version}, http.StatusBadRequest, nil)
	alice.call("POST", base+"/workspace/preview", configDraftPreview{Content: "listeners: []\n---\nlisteners: []", ExpectedVersion: own.Version}, http.StatusBadRequest, nil)
	var unchanged core.Config
	alice.call("GET", base, nil, http.StatusOK, &unchanged)
	if unchanged.Version != own.Version || unchanged.Content != own.Content {
		t.Fatal("preview wrote a saved configuration")
	}
	var tasks []core.Task
	alice.call("GET", "/tasks", nil, http.StatusOK, &tasks)
	if len(tasks) != 0 {
		t.Fatal("preview enqueued a task")
	}
	var result configMutationResult
	alice.call("POST", base+"/fields/log-level", map[string]any{
		"mutation": "modify", "fragment": "debug", "content": draft, "expected_version": own.Version,
		"name": own.Name, "intent": "validate",
	}, http.StatusOK, &result)
	if result.Config.Version != own.Version+1 || result.Task.ConfigID != own.ID || result.Task.ConfigVersion != result.Config.Version || result.Task.Action != core.ActionValidate {
		t.Fatal("form and source draft were not committed as one pinned revision/task")
	}
	for _, expected := range []string{"18446744073709551615", "# user draft comment", "log-level: debug", "&future", "*future"} {
		if !strings.Contains(result.Config.Content, expected) {
			t.Fatalf("form save lost %q", expected)
		}
	}
	completeConfigScopeAPITask(t, ctx, db, result.Task)
	var deploy core.Task
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigID: own.ID, ExpectedConfigVersion: result.Config.Version}, http.StatusCreated, &deploy)
	completeConfigScopeAPITask(t, ctx, db, deploy)
	var deployment []core.Deployment
	alice.call("GET", "/deployments", nil, http.StatusOK, &deployment)
	if len(deployment) != 1 || deployment[0].ConfigID != own.ID || deployment[0].ConfigVersion != result.Config.Version {
		t.Fatal("confirmed deployment did not belong to the edited account/revision")
	}
	bob.call("GET", base, nil, http.StatusOK, &unchanged)
	if unchanged.ID != other.ID || unchanged.Version != other.Version || unchanged.Content != other.Content {
		t.Fatal("shared draft/save/deployment touched another user's configuration")
	}
	// A complete source submitted alongside a field must still pass the same
	// allocated-port checks as a normal complete-source save.
	wrongPort := strings.ReplaceAll(draft, "21001", "21002")
	alice.call("POST", base+"/fields/log-level", map[string]any{
		"mutation": "modify", "fragment": "warning", "content": wrongPort, "expected_version": result.Config.Version, "intent": "validate",
	}, http.StatusConflict, nil)
	alice.call("POST", "/agents/"+agent.ID+"/configs/xray/workspace/preview", configDraftPreview{Content: "{}", ExpectedVersion: 0}, http.StatusNotFound, nil)
	alice.call("GET", base, nil, http.StatusOK, &unchanged)
	if unchanged.Version != result.Config.Version || unchanged.Content != result.Config.Content {
		t.Fatal("a rejected draft changed the saved baseline")
	}
}
