package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestManagedTLSDomainOnlyCaddyAPI(t *testing.T) {
	db, ctx, admin, _, bob := newConfigScopeAPIFixture(t)
	enrollment, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "domain-only Caddy"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "domain-only Caddy", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineSingBox}, Features: []string{core.AgentFeatureIndependentEgress, core.AgentFeatureManagedTLS}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, enrollment.Token)
	if err != nil {
		t.Fatal(err)
	}
	base := "/agents/" + agent.ID + "/configs/sing-box"
	var plan serverconfig.Input
	admin.call("POST", base+"/plans", map[string]any{"protocol": "vless-ws-tls"}, http.StatusCreated, &plan)
	// The only field a new Caddy user needs to supply.
	plan.TLSServerName = "proxy.example.test"
	var saved configMutationResult
	admin.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}, http.StatusOK, &saved)
	if strings.Contains(saved.Config.Content, `"tls"`) || strings.Contains(saved.Config.Content, "certificate_path") {
		t.Fatal("Caddy backend unexpectedly requires a core certificate")
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || len(claimed.TLSFrontends) != 1 {
		t.Fatalf("missing managed frontend: %+v %v", claimed, err)
	}
	frontend := claimed.TLSFrontends[0]
	if frontend.Mode != "caddy" || frontend.Domain != plan.TLSServerName || frontend.PublicPort != 443 || frontend.BackendPort != plan.Port || frontend.Path != plan.TransportPath {
		t.Fatalf("wrong reverse proxy: %+v", frontend)
	}
	if err = db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	var workspace struct{ Inbounds []serverconfig.Input }
	admin.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
	if len(workspace.Inbounds) != 1 {
		t.Fatal("missing saved inbound")
	}
	restored := workspace.Inbounds[0]
	if restored.TLSMode != "caddy" || restored.Listen != "127.0.0.1" || restored.Port != plan.Port || restored.TLSPublicPort != 443 || restored.TransportPath != plan.TransportPath || restored.Credential != plan.Credential {
		t.Fatalf("generated settings did not survive save/read: %+v", restored)
	}
	var entries []clientAccessEntry
	admin.call("GET", "/client-access?outbound_engine=sing-box", nil, http.StatusOK, &entries)
	if len(entries) != 1 || len(entries[0].Profiles) != 1 || entries[0].AddressRequired {
		t.Fatalf("domain-only export still needs a node address: %+v", entries)
	}
	profile := entries[0].Profiles[0]
	uri, err := url.Parse(profile.Profile.URI)
	if err != nil || uri.Hostname() != plan.TLSServerName || uri.Port() != "443" || uri.Query().Get("sni") != plan.TLSServerName || uri.Query().Get("path") != plan.TransportPath || uri.User.Username() != plan.Credential {
		t.Fatalf("incorrect client link: %+v", profile)
	}
	var outbound struct {
		Server string `json:"server"`
		Port   int    `json:"server_port"`
		TLS    struct {
			Enabled    bool   `json:"enabled"`
			Insecure   bool   `json:"insecure"`
			ServerName string `json:"server_name"`
		} `json:"tls"`
	}
	if err = json.Unmarshal(profile.Outbound, &outbound); err != nil || outbound.Server != plan.TLSServerName || outbound.Port != 443 || !outbound.TLS.Enabled || outbound.TLS.Insecure || outbound.TLS.ServerName != plan.TLSServerName {
		t.Fatalf("incorrect client TLS: %s %v", profile.Outbound, err)
	}
	bob.call("GET", "/client-access", nil, http.StatusOK, &entries)
	if len(entries) != 0 {
		t.Fatal("another account can see the managed domain")
	}
}

func TestManagedTLSPlanSaveAndEditedRetryAPI(t *testing.T) {
	db, ctx, admin, _, bob := newConfigScopeAPIFixture(t)
	enrollment, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "managed TLS API"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "managed TLS", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineSingBox}, Features: []string{core.AgentFeatureIndependentEgress, core.AgentFeatureManagedTLS}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, enrollment.Token)
	if err != nil {
		t.Fatal(err)
	}
	base := "/agents/" + agent.ID + "/configs/sing-box"
	var plan serverconfig.Input
	admin.call("POST", base+"/plans", map[string]any{"protocol": "vless-ws-tls"}, http.StatusCreated, &plan)
	if plan.TLSMode != "caddy" || plan.TLSPublicPort != 443 || plan.Listen != "127.0.0.1" || plan.CertificatePath != "" {
		t.Fatalf("invalid public defaults: %+v", plan)
	}
	plan.TLSMode = "nginx"
	plan.TLSPublicPort = 9443
	plan.TLSServerName = "tls.example.test"
	plan.TLSALPN = "http/1.1"
	plan.TransportHost = "host.example.test"
	var saved configMutationResult
	response := admin.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}, http.StatusOK, &saved)
	if strings.Contains(string(response), "PRIVATE KEY") || len(saved.Task.TLSFrontends) > 0 {
		t.Fatal("private frontend material leaked through save API")
	}
	var workspace struct {
		Inbounds []serverconfig.Input `json:"inbounds"`
	}
	response = admin.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
	if len(workspace.Inbounds) != 1 {
		t.Fatal("workspace")
	}
	restored := workspace.Inbounds[0]
	if restored.Protocol != plan.Protocol || restored.TLSMode != "nginx" || restored.TLSPublicPort != 9443 || restored.TLSALPN != "http/1.1" || restored.TLSClientCertificate == "" || restored.TLSServerName != plan.TLSServerName || restored.TransportHost != plan.TransportHost {
		t.Fatalf("lost managed frontend parameters: %+v", restored)
	}
	if strings.Contains(string(response), "PRIVATE KEY") {
		t.Fatal("signing or leaf key leaked through workspace")
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil {
		t.Fatal(err)
	}
	if err = db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: false, Error: "fixture deployment error"}); err != nil {
		t.Fatal(err)
	}
	var editor struct {
		Task   core.Task   `json:"task"`
		Config core.Config `json:"config"`
	}
	admin.call("GET", "/tasks/"+saved.Task.ID+"/retry-editor", nil, http.StatusOK, &editor)
	if editor.Config.ID != saved.Config.ID || editor.Config.Version != 1 || editor.Task.Error == "" {
		t.Fatal("retry editor opened incorrect configuration")
	}
	bob.call("GET", "/tasks/"+saved.Task.ID+"/retry-editor", nil, http.StatusNotFound, nil)
	admin.call("POST", "/tasks/"+saved.Task.ID+"/edit-retry", map[string]any{"content": "{invalid", "expected_version": 1}, http.StatusBadRequest, nil)
	var source map[string]any
	_ = json.Unmarshal([]byte(saved.Config.Content), &source)
	source["log"] = map[string]any{"level": "warn"}
	edited, _ := json.Marshal(source)
	var retried configMutationResult
	admin.call("POST", "/tasks/"+saved.Task.ID+"/edit-retry", map[string]any{"content": string(edited), "expected_version": 1}, http.StatusCreated, &retried)
	if retried.Task.ConfigID != saved.Config.ID || retried.Task.ConfigVersion != 2 || retried.Task.Action != core.ActionDeploy || retried.Config.Version != 2 {
		t.Fatal("retry did not save and bind the edited revision")
	}
	admin.call("POST", "/tasks/"+saved.Task.ID+"/edit-retry", map[string]any{"content": string(edited), "expected_version": 1}, http.StatusConflict, nil)
}

func TestManagedTLSSharedDomainOnlyPortsAPI(t *testing.T) {
	db, ctx, _, owner, recipient := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "shared-domain-only", core.EngineSingBox)
	agent.Features = append(agent.Features, core.AgentFeatureManagedTLS, core.AgentFeatureSharedCoreInstances)
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineSingBox: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 443, 23456, 23457)
	base := "/agents/" + agent.ID + "/configs/sing-box"
	var plan serverconfig.Input
	for index, wantPort := range []int{23456, 23457} {
		recipient.call("POST", base+"/plans", map[string]string{"protocol": "vless-ws-tls"}, http.StatusCreated, &plan)
		if plan.Port != wantPort || plan.TLSPublicPort != 443 {
			t.Fatalf("plan ignored the caller's free allocated ports: %+v", plan)
		}
		plan.TLSServerName = "shared.example.test"
		var saved configMutationResult
		recipient.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": index, "intent": "deploy", "input": plan}, http.StatusOK, &saved)
		claimed, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || claimed == nil || !claimed.SharedInstance || len(claimed.TLSFrontends) != index+1 {
			t.Fatalf("incorrect shared deployment: %+v %v", claimed, err)
		}
		if err := db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
			t.Fatal(err)
		}
	}
	recipient.call("POST", base+"/plans", map[string]string{"protocol": "vless-ws-tls"}, http.StatusBadRequest, nil)
	var regenerated serverconfig.Input
	recipient.call("POST", base+"/plans", map[string]any{"protocol": "vless-ws-tls", "input": plan}, http.StatusCreated, &regenerated)
	if regenerated.Port != plan.Port || regenerated.Credential == plan.Credential || regenerated.TLSServerName != plan.TLSServerName {
		t.Fatal("full allocation prevented existing credentials from regenerating")
	}
}
