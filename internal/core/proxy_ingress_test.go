package core

import (
	"encoding/json"
	"testing"
)

func TestProxyIngressTypeAndLegacyLeases(t *testing.T) {
	g := ProxyIngress{Mode: "nginx", Scope: ProxyIngressTypeScope, Generation: 7, Legacy: []ProxyIngressLease{{Port: 443, Generation: 2}, {Port: 8443, Generation: 3}}}
	for _, port := range []int{443, 8443, 9443} {
		f := TLSFrontend{Mode: "nginx", PublicPort: port, SharedIngressGeneration: 7}
		if !ProxyIngressAuthorized([]ProxyIngress{g}, f) {
			t.Fatal("type grant rejected actual port", port)
		}
		f.Mode = "caddy"
		if ProxyIngressAuthorized([]ProxyIngress{g}, f) {
			t.Fatal("grant crossed provider")
		}
	}
	for _, tc := range []struct {
		port       int
		generation int64
		allowed    bool
	}{{443, 2, true}, {8443, 3, true}, {9443, 2, false}, {443, 3, false}, {443, 6, false}, {10085, 7, false}} {
		if actual := g.Authorizes(TLSFrontend{Mode: "nginx", PublicPort: tc.port, SharedIngressGeneration: tc.generation}); actual != tc.allowed {
			t.Fatalf("lease %+v: %v", tc, actual)
		}
	}
	if !proxyIngressPolicyValid([]ProxyIngress{g}, 8) || proxyIngressPolicyValid([]ProxyIngress{g, g}, 8) {
		t.Fatal("type policy uniqueness")
	}
	legacy := ProxyIngress{Mode: "nginx", Port: 443, Generation: 2}
	if proxyIngressPolicyValid([]ProxyIngress{g, legacy}, 8) || proxyIngressPolicyValid([]ProxyIngress{legacy, g}, 8) {
		t.Fatal("mixed authorization models accepted")
	}
	if !legacy.Valid() || legacy.Authorizes(TLSFrontend{Mode: "nginx", PublicPort: 8443, SharedIngressGeneration: 2}) {
		t.Fatal("legacy grant widened")
	}
	for _, raw := range []string{`{"mode":"nginx","port":0}`, `{"mode":"nginx","port":10085}`} {
		var parsed ProxyIngress
		if json.Unmarshal([]byte(raw), &parsed) == nil {
			t.Fatal("invalid legacy port accepted", raw)
		}
	}
	var parsed ProxyIngress
	if err := json.Unmarshal([]byte(`{"mode":"caddy","scope":"type","generation":4}`), &parsed); err != nil || !parsed.Valid() {
		t.Fatal("type wire grant", parsed, err)
	}
}
