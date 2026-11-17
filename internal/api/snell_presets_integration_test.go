package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestSnellPresetsScopedLifecycle(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%t", shared), func(t *testing.T) {
			db, ctx, admin, owner, recipient := newConfigScopeAPIFixtureWithin(t, 5*time.Minute)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "snell", core.EngineMihomo)
			firstPort := 21001
			if shared {
				firstPort = 22001
			}
			grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, firstPort, firstPort+1, firstPort+2, firstPort+3, firstPort+4, firstPort+5, firstPort+6, firstPort+7, 23101)
			client, foreign := owner, recipient
			if shared {
				client, foreign = recipient, owner
			}
			base := "/agents/" + agent.ID + "/configs/mihomo"
			version := 0
			keys := []string{serverconfig.ProtocolSnell, serverconfig.ProtocolSnellShadowTLS, serverconfig.ProtocolSnellV6, serverconfig.ProtocolSnellV6Unshaped, serverconfig.ProtocolSnellV6ShadowTLS, serverconfig.ProtocolSnellV6UnshapedShadowTLS, serverconfig.ProtocolSnellV6, serverconfig.ProtocolVLESSEncPlain}
			var previous agentConfigWorkspaceResource
			for i, key := range keys {
				var plan serverconfig.Input
				client.call("POST", base+"/plans", map[string]any{"protocol": key}, 201, &plan)
				plan.Port = firstPort + i
				plan.ClientAddress = "edge.example.test"
				if os.Getenv("QCH_SNELL_API_RUNNER") != "" {
					plan.ClientAddress = "127.0.0.1"
					if handshake := os.Getenv("QCH_SNELL_API_COVER"); handshake != "" && strings.Contains(key, "shadow-tls") {
						plan.SnellObfsHost, plan.SnellShadowTLSHandshake = "example.com", handshake
					}
				}
				var result configMutationResult
				body := map[string]any{"operation": "add", "expected_version": version, "intent": "deploy", "input": plan}
				client.call("POST", base+"/server-inbounds", body, 200, &result)
				if result.Config.OwnerID != client.userID || result.Task.SharedInstance != shared {
					t.Fatal("wrong owner or instance")
				}
				// CAS closes both repeated requests and concurrent submissions.
				client.call("POST", base+"/server-inbounds", body, 409, nil)
				completeSnellAPITask(t, ctx, db, result.Task, client)
				version = result.Config.Version
				var workspace agentConfigWorkspaceResource
				client.call("GET", base+"/workspace", nil, 200, &workspace)
				if len(workspace.Inbounds) != i+1 {
					t.Fatal("adding an inbound replaced an existing one")
				}
				for j, existing := range previous.Inbounds {
					if workspace.Inbounds[j].Tag != existing.Tag || workspace.Inbounds[j].Credential != existing.Credential || workspace.Inbounds[j].Protocol != existing.Protocol {
						t.Fatal("other inbound mutated")
					}
				}
				got := workspace.Inbounds[i]
				if got.Protocol != key || got.Credential != plan.Credential || got.SnellMode != plan.SnellMode || got.SnellReuse != plan.SnellReuse {
					t.Fatal("saved field or metadata drift")
				}
				// Export reads the stored workspace, including individually bound metadata.
				selected := workspace
				selected.Inbounds = []serverconfig.Input{got}
				var exported struct{ Content, Format string }
				client.call("GET", workspaceExportPath(base, selected), nil, 200, &exported)
				if strings.HasPrefix(key, "snell") && (!strings.Contains(exported.Content, plan.Credential) || plan.SnellVersion == 6 && !strings.Contains(exported.Content, "mode = "+plan.SnellMode)) {
					t.Fatal("export mismatch")
				}
				foreign.call("GET", workspaceExportPath(base, selected), nil, 409, nil)
				admin.call("GET", workspaceExportPath(base, selected), nil, 409, nil)
				previous = workspace
			}
			// Changing one duplicate preset rotates only that inbound's credentials.
			original := previous.Inbounds[6]
			target := original
			target.Tag = "renamed-snell"
			target.SnellReuse = false
			target.SnellUDP = false
			var rotated serverconfig.Input
			client.call("POST", base+"/plans", map[string]any{"protocol": target.Protocol, "input": target}, 201, &rotated)
			if rotated.Credential == target.Credential {
				t.Fatal("credential was reused")
			}
			target.Credential = rotated.Credential
			var result configMutationResult
			client.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": original.Tag, "expected_version": version, "intent": "deploy", "input": target}, 200, &result)
			completeSnellAPITask(t, ctx, db, result.Task, client)
			version = result.Config.Version
			var after agentConfigWorkspaceResource
			client.call("GET", base+"/workspace", nil, 200, &after)
			for i, input := range after.Inbounds {
				if i == 6 {
					if input.Tag != target.Tag || input.SnellReuse || input.SnellUDP || input.Credential != target.Credential {
						t.Fatal("selected edit lost")
					}
				} else if input.Credential != previous.Inbounds[i].Credential {
					t.Fatal("credential rotation crossed inbound")
				}
			}
			// Conflicting ports and duplicate tags must fail before creating a task.
			target.Tag = after.Inbounds[0].Tag
			client.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": version, "intent": "deploy", "input": target}, 400, nil)
			target.Tag = "port-conflict"
			target.Port = after.Inbounds[0].Port
			client.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": version, "intent": "deploy", "input": target}, 400, nil)
			client.call("POST", base+"/server-inbounds", map[string]any{"operation": "delete", "original_tag": "renamed-snell", "expected_version": version, "intent": "deploy"}, 200, &result)
			completeSnellAPITask(t, ctx, db, result.Task, client)
			client.call("GET", base+"/workspace", nil, 200, &after)
			if len(after.Inbounds) != 7 {
				t.Fatal("delete did not target one inbound")
			}
			for _, input := range after.Inbounds {
				if input.Tag == "renamed-snell" {
					t.Fatal("deleted inbound restored")
				}
			}
			var revisions []core.Config
			client.call("GET", "/configs/"+result.Config.ID+"/revisions?limit=20", nil, 200, &revisions)
			if len(revisions) != 10 {
				t.Fatalf("archive count %d", len(revisions))
			}
		})
	}
}

func completeSnellAPITask(t *testing.T, ctx context.Context, db *store.Store, task core.Task, client configScopeAPIClient) {
	t.Helper()
	runner := os.Getenv("QCH_SNELL_API_RUNNER")
	if runner == "" {
		completeConfigScopeAPITask(t, ctx, db, task)
		return
	}
	claimed, err := db.ClaimTask(ctx, task.AgentID)
	if err != nil || claimed == nil || claimed.ID != task.ID {
		t.Fatalf("claim: %v", err)
	}
	run := func(payload any) {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "payload.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.CommandContext(ctx, runner, path).CombinedOutput()
		if err != nil {
			t.Fatalf("live runner: %v\n%s", err, out)
		}
	}
	run(map[string]any{"Task": claimed})
	result := core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, TrafficSettled: true}
	if claimed.SharedInstance {
		result.InstanceStatus = &core.SharedInstanceStatus{Engine: claimed.Engine, ShareID: claimed.SharedTrafficID,
			ConfigID: claimed.ConfigID, ConfigVersion: claimed.ConfigVersion, TaskID: claimed.ID, AppliedTaskID: claimed.ID, Status: "active", ObservedAt: time.Now().UTC()}
	}
	if err := db.CompleteTask(ctx, task.AgentID, task.ID, result); err != nil {
		t.Fatal(err)
	}
	var entries []clientAccessEntry
	client.call("GET", "/client-access", nil, 200, &entries)
	var profiles []string
	for _, entry := range entries {
		if entry.ConfigID != task.ConfigID {
			continue
		}
		for _, profile := range entry.Profiles {
			if profile.MihomoError != "" || profile.MihomoYAML == "" {
				t.Fatal("missing deployed Mihomo export")
			}
			profiles = append(profiles, profile.MihomoYAML)
		}
	}
	if len(profiles) == 0 {
		t.Fatal("missing deployed client profiles")
	}
	run(map[string]any{"Profiles": profiles})
}
