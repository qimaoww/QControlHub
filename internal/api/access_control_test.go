package api

import (
	"net/http"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestReconcileShadowsocksRustPoliciesKeepsPerPortSourceAndSharesDestination(t *testing.T) {
	entries := []serverconfig.MainlandAccessPolicy{
		{Tag: "first", Port: 8388, Kind: "shadowsocks", Engine: core.EngineShadowsocksRust},
		{Tag: "second", Port: 8389, Kind: "shadowsocks", Engine: core.EngineShadowsocksRust},
	}
	existing := []core.MainlandAccessPolicy{{
		AgentID: "agt_test", ConfigVersion: 1, Tag: "first", Port: 8388, Kind: "shadowsocks", Engine: core.EngineShadowsocksRust,
		BlockMainlandSource: true,
	}}
	policies := reconcileShadowsocksRustPolicies(entries, existing, "agt_test", 2, true, "second", 8389, false, true)
	if len(policies) != 2 || !policies[0].BlockMainlandDestination || !policies[1].BlockMainlandDestination ||
		!policies[0].BlockMainlandSource || policies[1].BlockMainlandSource {
		t.Fatalf("shared destination/per-port source policies = %+v", policies)
	}
	policies = reconcileShadowsocksRustPolicies(entries, policies, "agt_test", 3, false, "second", 8389, true, true)
	if len(policies) != 2 || policies[0].BlockMainlandDestination || policies[1].BlockMainlandDestination ||
		!policies[0].BlockMainlandSource || !policies[1].BlockMainlandSource {
		t.Fatalf("disabled destination/preserved source policies = %+v", policies)
	}
	policies = reconcileShadowsocksRustPolicies(entries[1:], policies, "agt_test", 4, false, "", 0, false, false)
	if len(policies) != 1 || policies[0].Tag != "second" {
		t.Fatalf("removed inbound policy was retained: %+v", policies)
	}
}

func TestSSRustSourceEditsKeepRestrictions(t *testing.T) {
	existing := []core.MainlandAccessPolicy{{AgentID: "node", Engine: core.EngineShadowsocksRust, Tag: "one", Port: 8388, BlockMainlandSource: true}}
	for _, entry := range []serverconfig.MainlandAccessPolicy{{Tag: "one", Port: 8389}, {Tag: "renamed", Port: 8388}} {
		policies := reconcileShadowsocksRustPolicies([]serverconfig.MainlandAccessPolicy{entry}, existing, "node", 2, false, "", 0, false, false)
		if len(policies) != 1 || !policies[0].BlockMainlandSource || policies[0].Tag != entry.Tag || policies[0].Port != entry.Port {
			t.Fatalf("edit lost restriction: %+v", policies)
		}
	}
}

// The same endpoint is used from the inbound editor. A rejected task must not
// save a new source revision or a host policy in a shared user's workspace.
func TestInboundAccessMutationKeepsSharedBoundaryAtomic(t *testing.T) {
	db, ctx, _, owner, recipient := newConfigScopeAPIFixture(t)
	agent, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "access-atomic", core.EngineShadowsocksRust)
	grantConfigScopeAPIAgent(t, ctx, db, agent, recipient, 21001)
	base := "/agents/" + agent.ID + "/configs/ss-rust"
	var config core.Config
	recipient.call("PUT", base, map[string]any{
		"name": "shared ACL", "version": 0,
		"content": `{"server":"0.0.0.0","server_port":21001,"method":"aes-256-gcm","password":"access-test-password"}`,
	}, http.StatusOK, &config)
	recipient.call("PUT", "/access-controls", map[string]any{
		"agent_id": agent.ID, "engine": "ss-rust", "tag": "ss-rust-21001", "port": 21001,
		"expected_version": config.Version, "intent": "deploy", "block_mainland_source": true,
	}, http.StatusConflict, nil)
	var after core.Config
	recipient.call("GET", base, nil, http.StatusOK, &after)
	if after.Version != config.Version || after.Content != config.Content {
		t.Fatal("rejected shared host policy saved a new revision")
	}
	var entries []mainlandAccessResource
	recipient.call("GET", "/access-controls", nil, http.StatusOK, &entries)
	for _, entry := range entries {
		if entry.AgentID == agent.ID && (entry.BlockMainlandSource || entry.BlockMainlandDestination) {
			t.Fatal("rejected shared host policy was retained")
		}
	}
}
