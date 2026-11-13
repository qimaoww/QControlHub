package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func setAPIProxyIngresses(t *testing.T, admin, recipient configScopeAPIClient, agentID string, grants []core.ProxyIngress) core.AgentShare {
	t.Helper()
	var access core.AgentAccess
	admin.call("GET", "/users/"+recipient.userID+"/agent-access", nil, http.StatusOK, &access)
	request := core.AgentAccessRequest{Revision: access.Revision, Isolated: true}
	for _, share := range access.Shares {
		enabled := share.Enabled
		row := core.AgentShareRequest{AgentID: share.AgentID, Engines: share.Engines, Ports: share.Ports, LimitBytes: share.LimitBytes, Enabled: &enabled}
		if share.AgentID == agentID {
			row.ProxyIngresses = &grants
		}
		request.Shares = append(request.Shares, row)
	}
	admin.call("PUT", "/users/"+recipient.userID+"/agent-access", request, http.StatusOK, &access)
	for _, share := range access.Shares {
		if share.AgentID == agentID {
			return share
		}
	}
	t.Fatal("allocation missing")
	return core.AgentShare{}
}

func TestSharedProxyIngressAuthorizationAndRevocationAPI(t *testing.T) {
	for _, mode := range []string{"nginx", "caddy"} {
		t.Run(mode, func(t *testing.T) {
			db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
			token, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "managed ingress"})
			if err != nil {
				t.Fatal(err)
			}
			features := []string{core.AgentFeatureIndependentEgress, core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCoreLifecycle, core.AgentFeatureManagedTLS, core.AgentFeatureNginxACME, core.AgentFeatureManagedTLSCertificates, core.AgentFeatureSharedProxyIngress}
			agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "managed ingress", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineSingBox}, Features: features, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, token.Token)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: features, Runtime: map[core.Engine]core.RuntimeState{core.EngineSingBox: {Installed: true, ServiceStatus: "inactive"}}}); err != nil {
				t.Fatal(err)
			}
			grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 21001)
			grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21002)
			base := "/agents/" + agent.ID + "/configs/sing-box"
			plan, err := serverconfig.NewPlan(mustProtocol(t, core.EngineSingBox, "vless-ws-tls"))
			if err != nil {
				t.Fatal(err)
			}
			plan.TLSMode, plan.TLSPublicPort, plan.Port, plan.TLSServerName, plan.TransportPath = mode, 8443, 22001, "owner.example.test", "/owner"
			mutate := func(client configScopeAPIClient, p serverconfig.Input, version int, status int) configMutationResult {
				t.Helper()
				operation := "add"
				original := ""
				if version > 0 {
					operation = "modify"
					original = p.Tag
				}
				var result configMutationResult
				body := client.call("POST", base+"/server-inbounds", map[string]any{"operation": operation, "original_tag": original, "expected_version": version, "intent": "deploy", "input": p}, status, &result)
				if strings.Contains(string(body), "PRIVATE KEY") {
					t.Fatal("private key leaked")
				}
				return result
			}
			owned := mutate(admin, plan, 0, http.StatusOK)
			completeConfigScopeAPITask(t, ctx, db, owned.Task)
			guest := plan
			guest.Tag, guest.Port, guest.TLSServerName, guest.TransportPath = "guest", 21001, "alice.example.test", "/alice"
			mutate(alice, guest, 0, http.StatusConflict)
			alice.call("PUT", "/users/"+alice.userID+"/agent-access", map[string]any{"revision": 1, "isolated": true, "shares": []any{}}, http.StatusForbidden, nil)
			alice.call("PUT", "/agents/"+agent.ID+"/sharing", map[string]any{"revision": 1, "shares": []any{}}, http.StatusForbidden, nil)
			grants := []core.ProxyIngress{{Mode: mode, Scope: core.ProxyIngressTypeScope, Generation: 999999, Legacy: []core.ProxyIngressLease{{Port: 9443, Generation: 999999}}}}
			share := setAPIProxyIngresses(t, admin, alice, agent.ID, grants)
			if len(share.ProxyIngresses) != 1 || share.ProxyIngresses[0].Generation == 999999 || share.ProxyIngresses[0].Port != 0 || share.ProxyIngresses[0].Scope != core.ProxyIngressTypeScope || len(share.ProxyIngresses[0].Legacy) != 0 {
				t.Fatal("trusted client generation")
			}
			first := mutate(alice, guest, 0, http.StatusOK)
			claimed, err := db.ClaimTask(ctx, agent.ID)
			if err != nil || claimed == nil {
				t.Fatalf("claim: %v", err)
			}
			if !claimed.SharedInstance || claimed.SharedTrafficID != share.ID || len(claimed.TLSFrontends) != 1 || claimed.TLSFrontends[0].SharedIngressGeneration != share.ProxyIngresses[0].Generation {
				t.Fatal("missing scoped ingress execution grant")
			}
			if err := db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, TrafficSettled: true, InstanceStatus: &core.SharedInstanceStatus{Engine: claimed.Engine, ShareID: share.ID, ConfigID: claimed.ConfigID, ConfigVersion: claimed.ConfigVersion, TaskID: claimed.ID, AppliedTaskID: claimed.ID, Status: "active", ObservedAt: time.Now().UTC()}}); err != nil {
				t.Fatal(err)
			}
			setAPIProxyIngresses(t, admin, bob, agent.ID, grants)
			second := guest
			second.Tag, second.Port, second.TLSServerName, second.TransportPath = "bob", 21002, "bob.example.test", "/bob"
			other := mutate(bob, second, 0, http.StatusOK)
			completeConfigScopeAPITask(t, ctx, db, other.Task)
			// Public entry authorization must not reserve or bill the actual listener.
			policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range policies {
				if p.Port == plan.TLSPublicPort {
					t.Fatal("public entry was metered")
				}
			}
			var access core.AgentAccess
			alice.call("GET", "/agent-access", nil, http.StatusOK, &access)
			if len(access.Shares[0].Ports) != 1 || access.Shares[0].Ports[0] != 21001 {
				t.Fatal("grant changed exclusive backend reservations")
			}
			// Same path across different SNI remains unsupported.
			duplicate := guest
			duplicate.TransportPath = "/bob"
			var conflict struct{ Reason, Field string }
			alice.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": guest.Tag, "expected_version": first.Config.Version, "intent": "deploy", "input": duplicate}, http.StatusConflict, &conflict)
			if conflict.Reason != "route_conflict" || conflict.Field != "tls_public_port" {
				t.Fatalf("route error: %+v", conflict)
			}
			mixed := guest
			if mode == "nginx" {
				mixed.TLSMode = "caddy"
			} else {
				mixed.TLSMode = "nginx"
			}
			mixed.TLSPublicPort = 9443 // A free listener still requires the matching provider grant.
			mutate(alice, mixed, first.Config.Version, http.StatusConflict)
			// An allocation still cannot claim another user's loopback backend.
			badBackend := guest
			badBackend.Port = 21002
			mutate(alice, badBackend, first.Config.Version, http.StatusConflict)
			guest.TransportPath = "/alice-next"
			queued := mutate(alice, guest, first.Config.Version, http.StatusOK)
			revoked := setAPIProxyIngresses(t, admin, alice, agent.ID, []core.ProxyIngress{})
			if revoked.ProxyIngressStatus != "pending" {
				t.Fatalf("revocation prematurely effective: %+v", revoked)
			}
			var state core.Task
			alice.call("GET", "/tasks/"+queued.Task.ID, nil, http.StatusOK, &state)
			if state.Status != core.TaskCanceled {
				t.Fatalf("old pending task survived: %s", state.Status)
			}
			if task, err := db.ClaimTask(ctx, agent.ID); err != nil || task != nil {
				t.Fatalf("revoked work dispatched: %+v %v", task, err)
			}
			mutate(alice, guest, queued.Config.Version, http.StatusConflict)
			// Regrant creates a fresh generation; old leases/retries cannot reuse it.
			regained := setAPIProxyIngresses(t, admin, alice, agent.ID, grants)
			if regained.ProxyIngresses[0].Generation == share.ProxyIngresses[0].Generation {
				t.Fatal("regrant restored revoked generation")
			}
			retry := mutate(alice, guest, queued.Config.Version, http.StatusOK)
			completeConfigScopeAPITask(t, ctx, db, retry.Task)
			bob.call("GET", "/configs/"+retry.Config.ID+"/revisions", nil, http.StatusNotFound, nil)
			// Removing one owner's final inbound leaves the other deployment usable.
			var deleted configMutationResult
			alice.call("POST", base+"/server-inbounds", map[string]any{"operation": "delete", "original_tag": guest.Tag, "expected_version": retry.Config.Version, "intent": "deploy", "input": guest}, http.StatusOK, &deleted)
			completeConfigScopeAPITask(t, ctx, db, deleted.Task)
			var returned configMutationResult
			alice.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": deleted.Config.Version, "intent": "deploy", "input": guest}, http.StatusOK, &returned)
			policies, err = db.AgentPortTrafficPolicies(ctx, agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, p := range policies {
				found = found || p.ShareID == share.ID && p.Port == guest.Port && p.MonitoringEnabled
			}
			if !found {
				t.Fatal("returning backend has no enforcement before execution")
			}
			completeConfigScopeAPITask(t, ctx, db, returned.Task)
			second.TransportPath = "/bob-next"
			updated := mutate(bob, second, other.Config.Version, http.StatusOK)
			completeConfigScopeAPITask(t, ctx, db, updated.Task)
		})
	}
}

func mustProtocol(t *testing.T, engine core.Engine, key string) serverconfig.Protocol {
	t.Helper()
	p, ok := serverconfig.FindProtocol(engine, key)
	if !ok {
		t.Fatal(key)
	}
	return p
}

// A break-glass administrator's workspace remains a host deployment when the
// node belongs to a database user. Its empty owner ID is a reserved identity.
func TestSharedProxyIngressWithLegacyAdminHostWorkspace(t *testing.T) {
	for _, mode := range []string{"nginx", "caddy"} {
		t.Run(mode, func(t *testing.T) {
			db, ctx, admin, owner, guest := newConfigScopeAPIFixture(t)
			agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "owned ingress", core.EngineSingBox)
			agent.Features = append(agent.Features, core.AgentFeatureManagedTLS, core.AgentFeatureNginxACME, core.AgentFeatureManagedTLSCertificates, core.AgentFeatureSharedProxyIngress)
			if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineSingBox: {Installed: true}}}); err != nil {
				t.Fatal(err)
			}
			grantConfigScopeAPIAgent(t, ctx, db, agent, guest, 21001)
			base := "/agents/" + agent.ID + "/configs/sing-box"
			plan, err := serverconfig.NewPlan(mustProtocol(t, core.EngineSingBox, "vless-ws-tls"))
			if err != nil {
				t.Fatal(err)
			}
			plan.TLSMode, plan.TLSPublicPort, plan.Port, plan.TLSServerName, plan.TransportPath = mode, 443, 22001, "host.example.test", "/host"
			var host configMutationResult
			admin.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}, http.StatusOK, &host)
			completeConfigScopeAPITask(t, ctx, db, host.Task)
			plan.Port, plan.Tag, plan.TLSServerName, plan.TransportPath = 21001, "guest", "guest.example.test", "/guest"
			var candidate configMutationResult
			body := map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}
			guest.call("POST", base+"/server-inbounds", body, http.StatusConflict, nil)
			setAPIProxyIngresses(t, admin, guest, agent.ID, []core.ProxyIngress{{Mode: mode, Port: 443}})
			guest.call("POST", base+"/server-inbounds", body, http.StatusOK, &candidate)
			completeConfigScopeAPITask(t, ctx, db, candidate.Task)
			// Source deployment must use the saved current entry and the latest
			// grant, with an atomic rejection that leaves the draft reusable.
			setAPIProxyIngresses(t, admin, guest, agent.ID, []core.ProxyIngress{})
			source := map[string]any{"name": candidate.Config.Name, "content": candidate.Config.Content, "version": candidate.Config.Version, "intent": "deploy"}
			var conflict struct {
				Code, Reason, Field string
				RequestID           string `json:"request_id"`
			}
			guest.call("POST", base+"/source", source, http.StatusConflict, &conflict)
			if conflict.Code != "port_conflict" || conflict.Reason != "ingress_unauthorized" || conflict.Field != "tls_public_port" || conflict.RequestID == "" {
				t.Fatalf("source conflict lost diagnostics: %+v", conflict)
			}
			if task, err := db.ClaimTask(ctx, agent.ID); err != nil || task != nil {
				t.Fatalf("rejected source queued work: %+v %v", task, err)
			}
			var retained core.Config
			guest.call("GET", base, nil, http.StatusOK, &retained)
			if retained.ID != candidate.Config.ID || retained.Version != candidate.Config.Version || retained.Content != candidate.Config.Content {
				t.Fatal("rejected source deployment changed the draft")
			}
			regranted := setAPIProxyIngresses(t, admin, guest, agent.ID, []core.ProxyIngress{{Mode: mode, Port: 443}})
			var retry configMutationResult
			guest.call("POST", base+"/source", source, http.StatusOK, &retry)
			claimed, err := db.ClaimTask(ctx, agent.ID)
			if err != nil || claimed == nil || claimed.ID != retry.Task.ID || !claimed.SharedInstance || claimed.SharedTrafficID != regranted.ID || len(claimed.TLSFrontends) != 1 {
				t.Fatalf("source target: %+v %v", claimed, err)
			}
			frontend := claimed.TLSFrontends[0]
			if frontend.Mode != mode || frontend.PublicPort != 443 || frontend.BackendPort != plan.Port || frontend.SharedIngressGeneration != regranted.ProxyIngresses[0].Generation {
				t.Fatalf("stale source entry: %+v", frontend)
			}
			if err := db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, TrafficSettled: true, InstanceStatus: &core.SharedInstanceStatus{Engine: claimed.Engine, ShareID: regranted.ID, ConfigID: claimed.ConfigID, ConfigVersion: claimed.ConfigVersion, TaskID: claimed.ID, AppliedTaskID: claimed.ID, Status: "active", ObservedAt: time.Now().UTC()}}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
