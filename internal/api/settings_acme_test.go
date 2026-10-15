package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestACMEEmailSettingsAPI(t *testing.T) {
	_, _, admin, alice, bob := newConfigScopeAPIFixture(t)
	for _, client := range []configScopeAPIClient{admin, alice} {
		var settings core.PanelSettings
		client.call("GET", "/settings", nil, http.StatusOK, &settings)
		settings.ACMEEmail = "  certs@example.test  "
		client.call("PUT", "/settings", settings, http.StatusOK, &settings)
		client.call("GET", "/settings", nil, http.StatusOK, &settings)
		if settings.ACMEEmail != "certs@example.test" {
			t.Fatalf("email not saved: %q", settings.ACMEEmail)
		}
		var other core.PanelSettings
		bob.call("GET", "/settings", nil, http.StatusOK, &other)
		if other.ACMEEmail != "" {
			t.Fatal("another account inherited a private email")
		}
		payload, _ := json.Marshal(settings)
		var legacy map[string]any
		_ = json.Unmarshal(payload, &legacy)
		delete(legacy, "acme_email")
		client.call("PUT", "/settings", legacy, http.StatusOK, &settings)
		if settings.ACMEEmail != "certs@example.test" {
			t.Fatal("legacy client cleared the ACME account")
		}
		for _, invalid := range []string{"invalid", "Name <certs@example.test>", "certs@example.test\r\nother@example.test"} {
			bad := settings
			bad.ACMEEmail = invalid
			client.call("PUT", "/settings", bad, http.StatusBadRequest, nil)
		}
		settings.ACMEEmail = ""
		client.call("PUT", "/settings", settings, http.StatusOK, &settings)
		if settings.ACMEEmail != "" {
			t.Fatal("explicit clearing was ignored")
		}
	}
}

func TestManagedTLSAccountDefaultForSharedDeployments(t *testing.T) {
	db, ctx, admin, owner, recipient := newConfigScopeAPIFixture(t)
	setEmail := func(client configScopeAPIClient, email string) {
		t.Helper()
		var settings core.PanelSettings
		client.call("GET", "/settings", nil, http.StatusOK, &settings)
		settings.ACMEEmail = email
		client.call("PUT", "/settings", settings, http.StatusOK, nil)
	}
	setEmail(admin, "admin@example.test")
	setEmail(owner, "owner@example.test")
	setEmail(recipient, "recipient@example.test")
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "acme-shared", core.EngineSingBox)
	agent.Features = append(agent.Features, core.AgentFeatureManagedTLS, core.AgentFeatureACMEFallback, core.AgentFeatureSharedCoreInstances)
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineSingBox: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 23456, 8443)
	base := "/agents/" + agent.ID + "/configs/sing-box"
	var plan serverconfig.Input
	recipient.call("POST", base+"/plans", map[string]string{"protocol": "vless-ws-tls"}, http.StatusCreated, &plan)
	plan.Port, plan.TLSPublicPort, plan.TLSServerName = 23456, 8443, "tls.example.test"
	var saved configMutationResult
	recipient.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}, http.StatusOK, &saved)
	claim := func(task core.Task, email string) {
		t.Helper()
		got, err := db.ClaimTask(ctx, agent.ID)
		if err != nil || got == nil || got.ID != task.ID || got.SharedInstance != (task.Action == core.ActionDeploy) || len(got.TLSFrontends) != 1 || got.TLSFrontends[0].ACMEEmail != email {
			t.Fatalf("incorrect account/snapshot: %+v %v", got, err)
		}
		if err := db.CompleteTask(ctx, agent.ID, got.ID, core.TaskResultRequest{LeaseID: got.LeaseID, Success: false, Error: "fixture TLS failure", TrafficSettled: true}); err != nil {
			t.Fatal(err)
		}
	}
	// Editing personal settings must not mutate a queued execution snapshot.
	setEmail(recipient, "updated@example.test")
	claim(saved.Task, "recipient@example.test")
	var retry core.Task
	recipient.call("POST", "/tasks/"+saved.Task.ID+"/retry", nil, http.StatusCreated, &retry)
	claim(retry, "updated@example.test")
	// Even an administrator submitting this configuration uses its durable
	// owner's account, rather than the administrator's or host owner's email.
	admin.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineSingBox, ConfigID: saved.Config.ID, Action: core.ActionValidate}, http.StatusCreated, &retry)
	claim(retry, "updated@example.test")
	var workspace struct{ Inbounds []serverconfig.Input }
	recipient.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
	if len(workspace.Inbounds) != 1 || workspace.Inbounds[0].TLSACMEEmail != "" {
		t.Fatal("inherited email became a fixed preset override")
	}
	for _, mode := range []string{"caddy", "nginx"} {
		plan.TLSMode, plan.TLSACMEEmail = mode, "override@example.test"
		recipient.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": plan.Tag, "expected_version": saved.Config.Version, "intent": "deploy", "input": plan}, http.StatusOK, &saved)
		want := "override@example.test"
		if mode == "nginx" {
			want = ""
		}
		claim(saved.Task, want)
	}
	features := []string{}
	for _, feature := range agent.Features {
		if feature != core.AgentFeatureACMEFallback {
			features = append(features, feature)
		}
	}
	agent.Features = features
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineSingBox: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	plan.TLSMode, plan.TLSACMEEmail = "caddy", ""
	recipient.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": plan.Tag, "expected_version": saved.Config.Version, "intent": "deploy", "input": plan}, http.StatusConflict, nil)
}
