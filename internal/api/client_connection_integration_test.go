package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestClientConnectionOptionsStayWithConfigOwner(t *testing.T) {
	for _, engine := range core.AllEngines() {
		t.Run(string(engine), func(t *testing.T) {
			db, ctx, admin, owner, recipient := newConfigScopeAPIFixture(t)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "dial-options", engine)
			agent.Features = append(agent.Features, core.AgentFeatureSharedCoreInstances)
			if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true}}}); err != nil {
				t.Fatal(err)
			}
			grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 21002, 21003)
			base := "/agents/" + agent.ID + "/configs/" + string(engine)
			for i, actor := range []configScopeAPIClient{owner, recipient} {
				var input serverconfig.Input
				actor.call("POST", base+"/plans", map[string]string{"protocol": serverconfig.ProtocolSS2022}, http.StatusCreated, &input)
				input.Tag, input.Port = "same-tag", 21001+i
				// API callers may omit the default transport and retain an inactive
				// path from a previous mode. Native readback must preserve overrides.
				input.Transport, input.TransportPath = "", "/inactive-path"
				input.ClientAddress, input.ClientPort = "edge-"+strconv.Itoa(i)+".example.test", 34443+i
				body := map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": input}
				var result configMutationResult
				actor.call("POST", base+"/server-inbounds", body, http.StatusOK, &result)
				actor.call("POST", base+"/server-inbounds", body, http.StatusConflict, nil)
				task, err := db.ClaimTask(ctx, agent.ID)
				if err != nil || task == nil {
					t.Fatal(err)
				}
				parsed, ok := serverconfig.Parse(engine, task.ConfigContent)
				if !ok || parsed.Port != input.Port || parsed.ClientAddress != "" || task.SharedInstance != (i == 1) {
					t.Fatal("client dial options changed server snapshot or instance")
				}
				completion := core.TaskResultRequest{LeaseID: task.LeaseID, Success: true, TrafficSettled: true}
				if task.SharedInstance {
					completion.InstanceStatus = &core.SharedInstanceStatus{Engine: engine, ShareID: task.SharedTrafficID, ConfigID: task.ConfigID, ConfigVersion: task.ConfigVersion, TaskID: task.ID, AppliedTaskID: task.ID, Status: "active", ObservedAt: time.Now().UTC()}
				}
				if err := db.CompleteTask(ctx, agent.ID, task.ID, completion); err != nil {
					t.Fatal(err)
				}
			}
			for i, actor := range []configScopeAPIClient{owner, recipient} {
				var workspace struct{ Inbounds []serverconfig.Input }
				actor.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
				address := "edge-" + strconv.Itoa(i) + ".example.test"
				if len(workspace.Inbounds) != 1 || workspace.Inbounds[0].ClientAddress != address || workspace.Inbounds[0].ClientPort != 34443+i {
					t.Fatal("workspace mixed users or lost dial options")
				}
				var entries []clientAccessEntry
				actor.call("GET", "/client-access?outbound_engine=sing-box", nil, http.StatusOK, &entries)
				if len(entries) != 1 || len(entries[0].Profiles) != 1 {
					t.Fatal("foreign client profile")
				}
				profile := entries[0].Profiles[0]
				if profile.Address != address || !profile.AddressOverridden {
					t.Fatal("client display disagrees with export")
				}
				var outbound map[string]any
				if err := json.Unmarshal(profile.Outbound, &outbound); err != nil {
					t.Fatal(err)
				}
				if outbound["server"] != address || outbound["server_port"] != float64(34443+i) {
					t.Fatalf("export lost custom endpoint: %s", profile.Outbound)
				}
			}
			var workspace struct{ Inbounds []serverconfig.Input }
			admin.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
			if len(workspace.Inbounds) != 0 {
				t.Fatal("node administrator inherited another user's private configuration")
			}
		})
	}
}

func TestXHTTPSplitRotationStaysWithConfigOwner(t *testing.T) {
	db, ctx, admin, owner, recipient := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "split-rotation", core.EngineXray)
	grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 21002, 21003)
	base := "/agents/" + agent.ID + "/configs/xray"
	for i, actor := range []configScopeAPIClient{owner, recipient} {
		var input serverconfig.Input
		actor.call("POST", base+"/plans", map[string]string{"protocol": serverconfig.ProtocolVLESSXHTTP}, http.StatusCreated, &input)
		input.RealityServerName, input.BlockMainlandDestination = "reality.example.test", false
		actor.call("POST", base+"/plans", map[string]any{"protocol": input.Protocol, "input": input}, http.StatusCreated, &input)
		input.Tag, input.Port = "same-tag", 21001+i
		input.ClientAddress, input.ClientPort = " [2001:db8::10] ", 34443+i
		extra := map[string]any{"downloadSettings": map[string]any{"address": " [2001:db8::11] ", "port": input.ClientPort, "network": "xhttp", "security": "reality",
			"xhttpSettings": map[string]any{"path": input.TransportPath}, "realitySettings": map[string]any{"serverName": input.RealityServerName, "publicKey": input.RealityPublicKey, "shortId": input.RealityShortID, "mldsa65Verify": input.RealityMLDSA65Verify}}}
		encoded, _ := json.Marshal(extra)
		input.XHTTPExtra, input.Listen = string(encoded), "::"
		var saved configMutationResult
		for _, operation := range []string{"add", "modify"} {
			if operation == "modify" {
				previous := input.RealityMLDSA65Verify
				actor.call("POST", base+"/plans", map[string]any{"protocol": input.Protocol, "input": input}, http.StatusCreated, &input)
				if input.RealityMLDSA65Verify == previous {
					t.Fatal("API regeneration reused the previous ML-DSA identity")
				}
				input.Tag, input.Port = "same-tag", 21001+i
			}
			body := map[string]any{"operation": operation, "expected_version": saved.Config.Version, "intent": "deploy", "input": input}
			if operation == "modify" {
				body["original_tag"] = input.Tag
			}
			actor.call("POST", base+"/server-inbounds", body, http.StatusOK, &saved)
			if saved.Task.SharedInstance != (i == 1) || saved.Task.ConfigID != saved.Config.ID || saved.Task.ConfigVersion != saved.Config.Version {
				t.Fatal("rotation targeted another instance or revision")
			}
			task, err := db.ClaimTask(ctx, agent.ID)
			if err != nil || task == nil || task.ID != saved.Task.ID {
				t.Fatal(err)
			}
			parsed, ok := serverconfig.Parse(core.EngineXray, task.ConfigContent)
			if !ok || parsed.RealityMLDSA65Seed != input.RealityMLDSA65Seed || parsed.RealityMLDSA65Verify != input.RealityMLDSA65Verify {
				t.Fatal("task snapshot did not rotate the actual server seed")
			}
			completion := core.TaskResultRequest{LeaseID: task.LeaseID, Success: true, TrafficSettled: true}
			if task.SharedInstance {
				completion.InstanceStatus = &core.SharedInstanceStatus{Engine: task.Engine, ShareID: task.SharedTrafficID, ConfigID: task.ConfigID, ConfigVersion: task.ConfigVersion, TaskID: task.ID, AppliedTaskID: task.ID, Status: "active", ObservedAt: time.Now().UTC()}
			}
			if err := db.CompleteTask(ctx, agent.ID, task.ID, completion); err != nil {
				t.Fatal(err)
			}
			var workspace agentConfigWorkspaceResource
			actor.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
			if len(workspace.Inbounds) != 1 || workspace.Inbounds[0].XHTTPExtra != input.XHTTPExtra {
				t.Fatal("workspace lost private download settings")
			}
			var entries []clientAccessEntry
			actor.call("GET", "/client-access?outbound_engine=xray", nil, http.StatusOK, &entries)
			if len(entries) != 1 || len(entries[0].Profiles) != 1 || entries[0].ConfigID != saved.Config.ID {
				t.Fatal("client export crossed configuration ownership")
			}
			if entries[0].Profiles[0].Address != "2001:db8::10" {
				t.Fatal("displayed address differs from the native client endpoint")
			}
			var out struct {
				Settings       struct{ VNext []struct{ Address string } }
				StreamSettings struct {
					XHTTPSettings struct {
						Extra struct {
							DownloadSettings struct {
								Address         string
								RealitySettings struct{ MLDSA65Verify string }
							}
						}
					}
				}
			}
			if err := json.Unmarshal(entries[0].Profiles[0].Outbound, &out); err != nil {
				t.Fatal(err)
			}
			download := out.StreamSettings.XHTTPSettings.Extra.DownloadSettings
			if len(out.Settings.VNext) != 1 || out.Settings.VNext[0].Address != "2001:db8::10" || download.Address != "2001:db8::11" || download.RealitySettings.MLDSA65Verify != input.RealityMLDSA65Verify {
				t.Fatal("export lost the normalized endpoint or active ML-DSA identity")
			}
		}
	}
	var workspace agentConfigWorkspaceResource
	admin.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
	if len(workspace.Inbounds) != 0 {
		t.Fatal("node management exposed another user's private configuration")
	}
}
