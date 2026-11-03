package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"gopkg.in/yaml.v3"
)

func TestNativeWebSocketTLSClientRoutingIsolation(t *testing.T) {
	db, ctx, admin, owner, recipient := newConfigScopeAPIFixture(t)
	var plans [2]serverconfig.Input
	var results [2]configMutationResult
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "native-ws-shared", core.EngineSingBox)
	agent.Features = append(agent.Features, core.AgentFeatureManagedTLS, core.AgentFeatureSharedCoreInstances)
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineSingBox: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 443, 21002)
	base := "/agents/" + agent.ID + "/configs/sing-box"
	clients := []configScopeAPIClient{owner, recipient}
	for i, client := range clients {
		client.call("POST", base+"/plans", map[string]string{"protocol": "vless-ws-tls"}, http.StatusCreated, &plans[i])
		plan := &plans[i]
		plan.Tag, plan.TLSServerName, plan.TransportPath = "same-tag", "same.example.test", "/same-path"
		plan.Port, plan.TLSPublicPort = 21001+i, 443
		// Native mode must keep the explicit listen port, not stale public metadata.
		if i == 0 {
			plan.TLSMode, plan.Listen, plan.Port = "native", "0.0.0.0", 444
		}
		plan.CertificatePath, plan.PrivateKeyPath = "/etc/certs/same.pem", "/etc/certs/same.key"
		client.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}, http.StatusOK, &results[i])
		completeConfigScopeAPITask(t, ctx, db, results[i].Task)
		if i == 1 {
			plan.TLSMode, plan.Listen, plan.Port, plan.TLSPublicPort = "native", "0.0.0.0", 443, 0
			client.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": plan.Tag, "expected_version": results[i].Config.Version, "intent": "deploy", "input": plan}, http.StatusOK, &results[i])
			if !results[i].Task.SharedInstance || results[i].Task.SharedTrafficID == "" {
				t.Fatal("shared native TLS used the host process")
			}
			completeConfigScopeAPITask(t, ctx, db, results[i].Task)
		}
		var workspace struct{ Inbounds []serverconfig.Input }
		client.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
		if len(workspace.Inbounds) != 1 {
			t.Fatal("missing private inbound")
		}
		saved := workspace.Inbounds[0]
		if saved.Port != plan.Port || saved.Listen != plan.Listen || saved.Credential != plan.Credential || saved.TLSServerName != plan.TLSServerName || saved.TransportHost != "" || saved.CertificatePath != plan.CertificatePath || saved.PrivateKeyPath != plan.PrivateKeyPath {
			t.Fatal("native readback changed connection or certificate references")
		}
		client.call("PUT", "/agents/"+agent.ID+"/client-address", map[string]any{"profile": clientProfileSelector{Engine: core.EngineSingBox, Tag: plan.Tag, Port: plan.Port}, "address": "203.0.113.10"}, http.StatusOK, nil)
	}
	check := func(client configScopeAPIClient, i int) {
		t.Helper()
		plan := plans[i]
		var entries []clientAccessEntry
		client.call("GET", "/client-access?outbound_engine=sing-box", nil, http.StatusOK, &entries)
		if len(entries) != 1 || entries[0].ConfigID != results[i].Config.ID || len(entries[0].Profiles) != 1 {
			t.Fatal("foreign or missing deployed profile")
		}
		item := entries[0].Profiles[0]
		uri, err := url.Parse(item.Profile.URI)
		if err != nil {
			t.Fatal(err)
		}
		wantHost := plan.TransportHost
		if wantHost == "" {
			wantHost = plan.TLSServerName
		}
		if uri.Hostname() != "203.0.113.10" || uri.Port() != strconv.Itoa(plan.Port) || uri.Query().Get("host") != wantHost || uri.Query().Get("sni") != plan.TLSServerName || uri.Query().Get("path") != plan.TransportPath || uri.User.Username() != plan.Credential {
			t.Fatal("URI lost scoped native TLS routing")
		}
		if strings.Contains(item.Profile.URI, plans[1-i].Credential) {
			t.Fatal("cross-user credentials exported")
		}
		var out map[string]any
		if err := json.Unmarshal(item.Outbound, &out); err != nil {
			t.Fatal(err)
		}
		ws := out["transport"].(map[string]any)
		tls := out["tls"].(map[string]any)
		if ws["headers"].(map[string]any)["Host"] != wantHost || tls["server_name"] != plan.TLSServerName || out["server"] != "203.0.113.10" || out["uuid"] != plan.Credential {
			t.Fatal("JSON lost scoped native TLS routing")
		}
		var proxy map[string]any
		if err := yaml.Unmarshal([]byte(item.MihomoYAML), &proxy); err != nil {
			t.Fatal(err)
		}
		if proxy["ws-opts"].(map[string]any)["headers"].(map[string]any)["Host"] != wantHost || proxy["uuid"] != plan.Credential {
			t.Fatal("Mihomo lost scoped routing")
		}
		var resource subStoreSyncResource
		client.call("GET", "/substore-sync", nil, http.StatusOK, &resource)
		if len(resource.Profiles) != 1 || resource.Profiles[0].ConfigID != results[i].Config.ID {
			t.Fatal("subscription API exposed foreign profiles")
		}
		profiles, err := New(db, Config{}).availableSubStoreProfiles(store.WithConfigScope(ctx, client.userID, false), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(profiles) != 1 || profiles[0].ConfigID != results[i].Config.ID || profiles[0].URI != item.Profile.URI {
			t.Fatal("subscription changed the scoped URI")
		}
		for _, address := range profiles[0].Addresses {
			parsed, err := url.Parse(address.URI)
			if err != nil || parsed.Hostname() != "203.0.113.10" || parsed.Query().Get("host") != wantHost {
				t.Fatal("subscription address override lost domain")
			}
		}
	}
	for i, client := range clients {
		check(client, i)
	}
	// Node administration alone must not expose a recipient's configuration.
	owner.call("GET", "/configs/"+results[1].Config.ID+"/revisions", nil, http.StatusNotFound, nil)
	// The explicit global administrator retains its existing configuration access.
	admin.call("GET", "/configs/"+results[1].Config.ID+"/revisions", nil, http.StatusOK, nil)
	recipient.call("GET", "/configs/"+results[0].Config.ID+"/revisions", nil, http.StatusNotFound, nil)
	// Editing explicit Host, SNI and path keeps each value, without affecting owner.
	plan := &plans[1]
	plan.TransportHost, plan.TLSServerName, plan.TransportPath = "custom.example.test", "custom-sni.example.test", "/edited"
	recipient.call("POST", base+"/server-inbounds", map[string]any{"operation": "modify", "original_tag": plan.Tag, "expected_version": results[1].Config.Version, "intent": "deploy", "input": plan}, http.StatusOK, &results[1])
	completeConfigScopeAPITask(t, ctx, db, results[1].Task)
	check(recipient, 1)
	check(owner, 0)
}
