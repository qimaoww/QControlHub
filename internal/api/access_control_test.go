package api

import (
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
