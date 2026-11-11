package api

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestMihomoUDP443APILifecycleAndScope(t *testing.T) {
	db, ctx, admin, owner, shared := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "udp443", core.EngineMihomo)
	agent.Features = append(agent.Features, core.AgentFeatureSharedCoreInstances)
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	grantConfigScopeAPIAgent(t, ctx, db, agent, shared, 21002, 21003)
	base := "/agents/" + agent.ID + "/configs/mihomo"
	var plan serverconfig.Input
	shared.call("POST", base+"/plans", map[string]string{"protocol": "vless"}, http.StatusCreated, &plan)
	if plan.Port != 443 || plan.BlockUDP443 == nil || !*plan.BlockUDP443 {
		t.Fatal("API defaults disagree")
	}
	plan.RealityServerName = "tls.example.test"
	for i, actor := range []configScopeAPIClient{owner, shared} {
		actor.call("POST", base+"/plans", map[string]string{"protocol": "vless"}, http.StatusCreated, &plan)
		plan.Tag = "same-name"
		plan.Port = 21001 + i
		plan.RealityServerName = "tls.example.test"
		var saved configMutationResult
		for index, enabled := range []bool{true, false, true} {
			operation := "modify"
			original := plan.Tag
			if index == 0 {
				operation = "add"
				original = ""
			}
			plan.BlockUDP443 = &enabled
			actor.call("POST", base+"/server-inbounds", map[string]any{"operation": operation, "original_tag": original, "expected_version": saved.Config.Version, "intent": "deploy", "input": plan}, http.StatusOK, &saved)
			completeConfigScopeAPITask(t, ctx, db, saved.Task)
			var workspace agentConfigWorkspaceResource
			actor.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
			if len(workspace.Inbounds) != 1 || workspace.Inbounds[0].Port != plan.Port || workspace.Inbounds[0].BlockUDP443 == nil || *workspace.Inbounds[0].BlockUDP443 != enabled {
				t.Fatal("saved policy lost or crossed user scope")
			}
			var rotated serverconfig.Input
			actor.call("POST", base+"/plans", map[string]any{"protocol": "vless", "input": plan}, http.StatusCreated, &rotated)
			if rotated.Port != plan.Port || rotated.BlockUDP443 == nil || *rotated.BlockUDP443 != enabled {
				t.Fatal("regeneration reset choices")
			}
		}
		actor.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": plan.Tag, "expected_version": 1, "intent": "deploy", "input": plan}, http.StatusConflict, nil)
		previous := plan.Tag
		plan.Tag = "renamed"
		actor.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": previous, "expected_version": saved.Config.Version, "intent": "deploy", "input": plan}, http.StatusOK, &saved)
		completeConfigScopeAPITask(t, ctx, db, saved.Task)
		var revisions []core.Config
		actor.call("GET", "/configs/"+saved.Config.ID+"/revisions", nil, http.StatusOK, &revisions)
		if len(revisions) != 4 {
			t.Fatal("same-day save archive overwritten")
		}
		var restored core.Config
		actor.call("POST", "/configs/"+saved.Config.ID+"/revisions/2/restore", map[string]any{"expected_version": saved.Config.Version}, http.StatusOK, &restored)
		parsed, ok := serverconfig.Parse(core.EngineMihomo, restored.Content)
		if !ok || parsed.Port != 21001+i || parsed.BlockUDP443 == nil || *parsed.BlockUDP443 {
			t.Fatal("archive restore enabled policy")
		}
		foreign := owner
		if i == 0 {
			foreign = shared
		}
		foreign.call("GET", "/configs/"+saved.Config.ID+"/revisions", nil, http.StatusNotFound, nil)
		foreign.call("POST", "/configs/"+saved.Config.ID+"/revisions/1/restore", map[string]any{"expected_version": restored.Version}, http.StatusNotFound, nil)
		actor.call("POST", base+"/server-inbounds", map[string]any{"operation": "delete", "original_tag": parsed.Tag, "expected_version": restored.Version, "intent": "deploy", "input": map[string]any{"tag": parsed.Tag, "port": parsed.Port}}, http.StatusOK, &saved)
		completeConfigScopeAPITask(t, ctx, db, saved.Task)
		t.Logf("actor=%d revisions=%s scope/lifecycle passed", i, strconv.Itoa(restored.Version))
	}

	for _, allowed := range []bool{false, true} {
		extra, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "udp443-permission-"+strconv.FormatBool(allowed), core.EngineMihomo)
		extra.Features = append(extra.Features, core.AgentFeatureSharedCoreInstances)
		if err := db.Heartbeat(ctx, extra.ID, core.HeartbeatRequest{Features: extra.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true}}}); err != nil {
			t.Fatal(err)
		}
		port := 21004
		if allowed {
			port = 443
		}
		grantConfigScopeAPIAgent(t, ctx, db, extra, shared, port)
		path := "/agents/" + extra.ID + "/configs/mihomo"
		shared.call("POST", path+"/plans", map[string]string{"protocol": "vless"}, http.StatusCreated, &plan)
		plan.RealityServerName = "tls.example.test"
		status := http.StatusConflict
		if allowed {
			status = http.StatusOK
		}
		var result configMutationResult
		shared.call("POST", path+"/server-inbounds", map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}, status, func() any {
			if allowed {
				return &result
			}
			return nil
		}())
		if allowed {
			completeConfigScopeAPITask(t, ctx, db, result.Task)
		}
	}
	var workspace agentConfigWorkspaceResource
	admin.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
	if len(workspace.Inbounds) != 0 {
		t.Fatal("administrator inherited private config")
	}
}
