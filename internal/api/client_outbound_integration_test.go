package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestClientOutboundChoicesUseAuthorizedDeployedRevision(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	agent := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, agent, alice, 21001)
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 21002)
	base := "/agents/" + agent.ID + "/configs/mihomo"
	var deployed core.Config
	alice.call("PUT", base, configScopeAPIConfig(t, 21001), http.StatusOK, &deployed)
	var task core.Task
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: deployed.Engine, Action: core.ActionDeploy, ConfigID: deployed.ID}, http.StatusCreated, &task)
	completeConfigScopeAPITask(t, ctx, db, task)
	original := serverconfig.ParseAll(deployed.Engine, deployed.Content)[0]
	draft := configScopeAPIConfig(t, 21001)
	draft.Version = deployed.Version
	draftCredential := serverconfig.ParseAll(draft.Engine, draft.Content)[0].Credential
	alice.call("PUT", base, draft, http.StatusOK, nil)
	alice.call("PUT", "/agents/"+agent.ID+"/client-address", map[string]any{
		"profile": clientProfileSelector{Engine: deployed.Engine, Tag: original.Tag, Port: original.Port},
		"address": "peer-port.example.test",
	}, http.StatusOK, nil)
	for _, engine := range []string{"xray", "sing-box"} {
		var entries []clientAccessEntry
		alice.call("GET", "/client-access?outbound_engine="+engine, nil, http.StatusOK, &entries)
		if len(entries) != 1 || len(entries[0].Profiles) != 1 {
			t.Fatal("authorized deployed peer missing")
		}
		profile := entries[0].Profiles[0]
		if len(profile.Outbound) == 0 || profile.OutboundError != "" ||
			!strings.Contains(string(profile.Outbound), original.Credential) ||
			strings.Contains(string(profile.Outbound), draftCredential) ||
			!strings.Contains(string(profile.Outbound), "peer-port.example.test") {
			t.Fatal("peer export used a draft credential or ignored per-port address")
		}
		bob.call("GET", "/client-access?outbound_engine="+engine, nil, http.StatusOK, &entries)
		if len(entries) != 0 {
			t.Fatal("sharing a host leaked another user's deployed peer credentials")
		}
	}
	var ordinary []clientAccessEntry
	alice.call("GET", "/client-access", nil, http.StatusOK, &ordinary)
	if len(ordinary[0].Profiles[0].Outbound) != 0 {
		t.Fatal("ordinary client listing gained unnecessary outbound payloads")
	}
	alice.call("GET", "/client-access?outbound_engine=unknown", nil, http.StatusBadRequest, nil)
}
