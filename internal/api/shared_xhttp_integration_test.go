package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestSharedXHTTPRecipientLifecycleAndExports(t *testing.T) {
	for _, unrestricted := range []bool{false, true} {
		name := "allocated"
		if unrestricted {
			name = "unrestricted"
		}
		t.Run(name, func(t *testing.T) {
			db, ctx, _, owner, recipient := newConfigScopeAPIFixture(t)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "shared-xhttp", core.EngineXray)
			agent.Features = append(agent.Features, core.AgentFeatureManagedTLS, core.AgentFeatureSharedCoreInstances, core.AgentFeatureXHTTP)
			if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true}}}); err != nil {
				t.Fatal(err)
			}
			if err := db.SetAgentClientAddress(ctx, agent.ID, "edge.example.test"); err != nil {
				t.Fatal(err)
			}
			ports := []int{24443, 35001, 35002}
			if unrestricted {
				ports = []int{0}
			}
			grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, ports...)
			base := "/agents/" + agent.ID + "/configs/xray"
			var saved configMutationResult
			for _, key := range []string{serverconfig.ProtocolXHTTPTLS, serverconfig.ProtocolXHTTPTLSReality, serverconfig.ProtocolXHTTPRealityTLS, serverconfig.ProtocolXHTTPFive} {
				var plan serverconfig.Input
				recipient.call("POST", base+"/plans", map[string]string{"protocol": key}, http.StatusCreated, &plan)
				plan.Port, plan.ClientPort = 24443, 24443
				plan.ClientAddress = "upload.example.test"
				plan.XHTTPTLSAddress, plan.XHTTPTLSClientPort = "cdn.example.test", 443
				group := key != serverconfig.ProtocolXHTTPTLS
				if group {
					plan.XHTTPBackendPort, plan.XHTTPTLSPort = 35001, 35002
				}
				for _, operation := range []string{"add", "modify", "delete"} {
					original := plan.Tag
					if operation == "add" {
						original = ""
					}
					if operation == "modify" {
						plan.Tag += "-edited"
						plan.TransportPath += "-edited"
						plan.XHTTPExtra = `{"xmux":{"maxConnections":0,"maxConcurrency":"4-8"}}`
					}
					recipient.call("POST", base+"/server-inbounds", map[string]any{"operation": operation, "original_tag": original, "expected_version": saved.Config.Version, "intent": "deploy", "input": plan}, http.StatusOK, &saved)
					if !saved.Task.SharedInstance || saved.Task.SharedTrafficID == "" {
						t.Fatal("recipient deployment did not select its private instance")
					}
					claimed, err := db.ClaimTask(ctx, agent.ID)
					if err != nil || claimed == nil || claimed.ID != saved.Task.ID {
						t.Fatalf("claim: %+v %v", claimed, err)
					}
					endpoints, err := serverconfig.SharedTrafficEndpoints(core.EngineXray, claimed.ConfigContent)
					if err != nil || (operation != "delete" && (len(endpoints) != 1 || endpoints[0].Port != plan.Port)) || (operation == "delete" && len(endpoints) != 0) {
						t.Fatalf("shared accounting must cover only the public entry: %+v %v", endpoints, err)
					}
					wantFrontends := 0
					if group && operation != "delete" {
						wantFrontends = 1
					}
					if len(claimed.TLSFrontends) != wantFrontends {
						t.Fatalf("private instance lost managed dependencies: %+v", claimed.TLSFrontends)
					}
					if _, err := serverconfig.SharedInstanceRuntimeContent(core.EngineXray, claimed.ConfigContent); err != nil {
						t.Fatalf("private process configuration: %v", err)
					}
					status := "active"
					if operation == "delete" {
						status = "inactive"
					}
					if err := db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, TrafficSettled: true,
						InstanceStatus: &core.SharedInstanceStatus{Engine: claimed.Engine, ShareID: claimed.SharedTrafficID,
							ConfigID: claimed.ConfigID, ConfigVersion: claimed.ConfigVersion, AppliedTaskID: claimed.ID, TaskID: claimed.ID, Status: status, ObservedAt: time.Now().UTC()}}); err != nil {
						t.Fatal(err)
					}
					var workspace struct{ Inbounds []serverconfig.Input }
					finished, err := db.GetTask(ctx, claimed.ID)
					if err != nil || finished.Status != core.TaskSucceeded {
						t.Fatalf("applied %s reported failure: %+v %v", operation, finished, err)
					}
					recipient.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
					var clients []clientAccessEntry
					recipient.call("GET", "/client-access", nil, http.StatusOK, &clients)
					if operation == "delete" {
						if len(workspace.Inbounds) != 0 || len(clients) != 0 {
							t.Fatal("deleted group left orphaned dependencies or exports")
						}
						continue
					}
					if len(workspace.Inbounds) != 1 || workspace.Inbounds[0].Protocol != key || workspace.Inbounds[0].Tag != plan.Tag || workspace.Inbounds[0].XHTTPExtra != plan.XHTTPExtra || workspace.Inbounds[0].ClientAddress != plan.ClientAddress {
						t.Fatalf("recipient lost preset metadata: %+v", workspace.Inbounds)
					}
					if len(clients) != 1 || len(clients[0].Profiles) != 1 || clients[0].ConfigID != saved.Config.ID {
						t.Fatalf("recipient cannot export its deployment: %+v", clients)
					}
					profile := clients[0].Profiles[0].Profile
					if !strings.Contains(profile.Xray, plan.Credential) || (key != serverconfig.ProtocolXHTTPFive && !strings.Contains(profile.Xray, plan.TransportPath)) {
						t.Fatalf("%s %s: export credentials/path differ from the recipient's server", key, operation)
					}
					if key == serverconfig.ProtocolXHTTPFive {
						if len(profile.Variants) != 5 {
							t.Fatal("recipient lost five-in-one exports")
						}
						for _, variant := range profile.Variants {
							if !strings.Contains(variant.Profile.Xray, plan.Credential) {
								t.Fatal("variant credentials differ from server")
							}
						}
					}
					owner.call("GET", "/configs/"+saved.Config.ID+"/revisions", nil, http.StatusNotFound, nil)
					var ownerWorkspace struct{ Config *core.Config }
					owner.call("GET", base+"/workspace", nil, http.StatusOK, &ownerWorkspace)
					if ownerWorkspace.Config != nil {
						t.Fatal("private configuration appeared in the owner's workspace")
					}
				}
			}
		})
	}
}

func TestSharedXHTTPRequiresEveryAllocatedListener(t *testing.T) {
	for _, field := range []string{"port", "xhttp_backend_port", "xhttp_tls_port"} {
		t.Run(field, func(t *testing.T) {
			db, ctx, _, owner, recipient := newConfigScopeAPIFixture(t)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "shared-xhttp-ports", core.EngineXray)
			agent.Features = append(agent.Features, core.AgentFeatureManagedTLS, core.AgentFeatureSharedCoreInstances, core.AgentFeatureXHTTP)
			if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true}}}); err != nil {
				t.Fatal(err)
			}
			portsByField := map[string]int{"port": 24443, "xhttp_backend_port": 35001, "xhttp_tls_port": 35002}
			ports := []int{}
			for key, port := range portsByField {
				if key != field {
					ports = append(ports, port)
				}
			}
			grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, ports...)
			base := "/agents/" + agent.ID + "/configs/xray"
			var plan serverconfig.Input
			recipient.call("POST", base+"/plans", map[string]string{"protocol": serverconfig.ProtocolXHTTPFive}, http.StatusCreated, &plan)
			plan.Port, plan.ClientPort, plan.XHTTPBackendPort, plan.XHTTPTLSPort = 24443, 24443, 35001, 35002
			var conflict struct {
				Code, Field string
				Port        int
			}
			recipient.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}, http.StatusConflict, &conflict)
			if conflict.Code != "port_conflict" || conflict.Port != portsByField[field] || conflict.Field != field {
				t.Fatalf("wrong unallocated listener diagnostic: %+v", conflict)
			}
			var workspace struct{ Config *core.Config }
			recipient.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
			if workspace.Config != nil {
				t.Fatal("failed allocation check persisted a configuration")
			}
			var tasks []core.Task
			recipient.call("GET", "/tasks?agent_id="+agent.ID, nil, http.StatusOK, &tasks)
			if len(tasks) != 0 {
				t.Fatal("failed allocation check queued a deployment")
			}
		})
	}
}

func TestSharedXHTTPOwnerCoexistenceQuotaAndRevocation(t *testing.T) {
	db, ctx, _, owner, recipient := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "shared-xhttp-quota", core.EngineXray)
	agent.Features = append(agent.Features, core.AgentFeatureManagedTLS, core.AgentFeatureSharedCoreInstances, core.AgentFeatureXHTTP)
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentClientAddress(ctx, agent.ID, "edge.example.test"); err != nil {
		t.Fatal(err)
	}
	var sharing core.AgentSharing
	owner.call("GET", "/agents/"+agent.ID+"/sharing", nil, http.StatusOK, &sharing)
	owner.call("PUT", "/agents/"+agent.ID+"/sharing", core.AgentSharingRequest{Revision: sharing.Revision, Shares: []core.AgentSharingRecipient{{Username: "bob", Engines: []core.Engine{core.EngineXray}, Ports: []int{24443, 35001, 35002}, LimitBytes: 1000}}}, http.StatusOK, &sharing)
	acceptConfigScopeAPIInvitations(recipient)
	base := "/agents/" + agent.ID + "/configs/xray"
	var plans [2]serverconfig.Input
	var results [2]configMutationResult
	for i, client := range []configScopeAPIClient{owner, recipient} {
		client.call("POST", base+"/plans", map[string]string{"protocol": serverconfig.ProtocolXHTTPFive}, http.StatusCreated, &plans[i])
		plan := &plans[i]
		// Identical tags belong to different processes; listener ports do not.
		plan.Tag = "same-five"
		plan.Port, plan.ClientPort, plan.XHTTPBackendPort, plan.XHTTPTLSPort = 24444-i, 24444-i, 35003-2*i, 35004-2*i
		client.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}, http.StatusOK, &results[i])
		completeConfigScopeAPITask(t, ctx, db, results[i].Task)
	}
	if results[0].Task.SharedInstance || !results[1].Task.SharedInstance {
		t.Fatal("owner and recipient did not use separate core services")
	}
	for i, client := range []configScopeAPIClient{owner, recipient} {
		var clients []clientAccessEntry
		client.call("GET", "/client-access", nil, http.StatusOK, &clients)
		if len(clients) != 1 || clients[0].ConfigID != results[i].Config.ID || len(clients[0].Profiles) != 1 || len(clients[0].Profiles[0].Profile.Variants) != 5 || strings.Contains(clients[0].Profiles[0].Profile.Xray, plans[1-i].Credential) {
			t.Fatal("coexisting accounts lost exports or exposed foreign credentials")
		}
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 2 {
		t.Fatalf("internal listeners became quota endpoints: %+v %v", policies, err)
	}
	var policy core.PortTrafficPolicy
	for _, candidate := range policies {
		if candidate.ShareID == results[1].Task.SharedTrafficID {
			policy = candidate
		}
	}
	if policy.Port != plans[1].Port || policy.SharedQuota == nil || policy.SharedQuota.ID != results[1].Task.SharedTrafficID {
		t.Fatal("recipient lost its public quota policy")
	}
	clock := time.Now().UTC().Add(time.Second)
	start, end, _ := core.TrafficPeriodAt(policy.CycleAnchor, policy.Cycle, clock)
	if err := db.UpdatePortTrafficUsage(ctx, agent.ID, []core.PortTrafficUsage{{PolicyID: policy.ID, ResetGeneration: policy.ResetGeneration, ReceivedBytes: 1000, UsedBytes: 1000, LifetimeReceivedBytes: 1000, CounterEpoch: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CollectedAt: clock, PeriodStart: start, PeriodEnd: end, EnforcementAvailable: true, ShareID: policy.SharedQuota.ID, ShareUsedBytes: 1000}}, clock); err != nil {
		t.Fatal(err)
	}
	recipient.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": plans[1].Tag, "expected_version": results[1].Config.Version, "intent": "deploy", "input": plans[1]}, http.StatusConflict, nil)
	var workspace struct{ Config *core.Config }
	recipient.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
	if workspace.Config == nil || workspace.Config.Version != results[1].Config.Version {
		t.Fatal("quota rejection changed the recipient's saved revision")
	}
	owner.call("GET", "/agents/"+agent.ID+"/sharing", nil, http.StatusOK, &sharing)
	owner.call("PUT", "/agents/"+agent.ID+"/sharing", core.AgentSharingRequest{Revision: sharing.Revision}, http.StatusOK, &sharing)
	recipient.call("GET", base+"/workspace", nil, http.StatusNotFound, nil)
	var clients []clientAccessEntry
	recipient.call("GET", "/client-access", nil, http.StatusOK, &clients)
	if len(clients) != 0 {
		t.Fatal("revoked recipient retained client access")
	}
	policies, err = db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 2 {
		t.Fatal("revocation did not reach the runtime quota policy", err)
	}
	for _, candidate := range policies {
		if candidate.ID == policy.ID && (candidate.SharedQuota == nil || !candidate.SharedQuota.Revoked) {
			t.Fatal("recipient quota remains active after revocation")
		}
		if candidate.Port == plans[0].Port && candidate.SharedQuota != nil {
			t.Fatal("revoking the recipient changed the owner's policy")
		}
	}
	owner.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
	if workspace.Config == nil || workspace.Config.ID != results[0].Config.ID {
		t.Fatal("recipient revocation changed the owner's workspace")
	}
}
