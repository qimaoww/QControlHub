package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestProxyIngressRevocationWaitsForLifecycleAndRetriesCleanup(t *testing.T) {
	manager, _, policies, _ := sharedTrafficFixture(t)
	grant := core.ProxyIngress{Mode: "nginx", Port: 443, Generation: 2}
	for _, p := range policies {
		p.SharedQuota.ProxyIngresses = []core.ProxyIngress{grant}
		p.SharedQuota.ProxyIngressRevision = 2
	}
	client := &Client{traffic: manager, creds: credentials{AgentID: "agt_shared"}}
	ctx := context.Background()
	if err := client.receiveTrafficPolicies(ctx, policies); err != nil {
		t.Fatal(err)
	}
	const revokedShare = "shr_0000000000000001"
	stopped, failed := false, true
	manager.inspectProxyIngresses = func(_ context.Context, _ core.Engine, id string, grants []core.ProxyIngress) (bool, error) {
		return id == revokedShare && !stopped && !core.ProxyIngressAuthorized(grants, core.TLSFrontend{Mode: "nginx", PublicPort: 443, SharedIngressGeneration: 2}), nil
	}
	manager.haltShare = func(_ context.Context, _ core.Engine, id string) error {
		if id != revokedShare {
			t.Fatal("stopped another owner")
		}
		if failed {
			return errors.New("offline service manager")
		}
		stopped = true
		return nil
	}
	for _, p := range policies {
		if p.SharedQuota.ID == revokedShare {
			p.SharedQuota.ProxyIngresses = nil
			p.SharedQuota.ProxyIngressRevision = 3
		}
	}
	client.taskLifecycleMu.Lock()
	if err := client.receiveTrafficPolicies(ctx, policies); err != nil || stopped {
		t.Fatalf("policy interrupted an operation/rollback: %v", err)
	}
	for _, report := range client.ingressReports() {
		if report.Revision != 2 {
			t.Fatal("acknowledged unapplied revocation")
		}
	}
	client.taskLifecycleMu.Unlock()
	if err := client.tryApplyTrafficPolicies(ctx); err == nil {
		t.Fatal("failed cleanup acknowledged")
	}
	for _, report := range client.ingressReports() {
		if report.Error == "" {
			t.Fatal("missing cleanup failure")
		}
	}
	failed = false
	if err := client.tryApplyTrafficPolicies(ctx); err != nil || !stopped {
		t.Fatalf("cleanup did not recover: %v", err)
	}
	for _, report := range client.ingressReports() {
		if report.Error != "" || report.ShareID == revokedShare && report.Revision != 3 {
			t.Fatalf("invalid acknowledgement: %+v", report)
		}
	}
	old := []core.TLSFrontend{{Mode: "nginx", PublicPort: 443, SharedIngressGeneration: 2}}
	if err := manager.authorizeProxyIngresses(revokedShare, old); err == nil {
		t.Fatal("old snapshot authorized after revocation")
	}
	for _, p := range policies {
		if p.SharedQuota.ID == revokedShare {
			fresh := grant
			fresh.Generation = 4
			p.SharedQuota.ProxyIngresses = []core.ProxyIngress{fresh}
			p.SharedQuota.ProxyIngressRevision = 4
		}
	}
	if err := client.receiveTrafficPolicies(ctx, policies); err != nil {
		t.Fatal(err)
	}
	if err := manager.authorizeProxyIngresses(revokedShare, old); err == nil {
		t.Fatal("regrant revived an old deployment")
	}
	old[0].SharedIngressGeneration = 4
	if err := manager.authorizeProxyIngresses(revokedShare, old); err != nil {
		t.Fatal(err)
	}
	for _, p := range policies {
		if p.SharedQuota.ID == revokedShare {
			p.SharedQuota.ProxyIngressRevision = 2
			p.SharedQuota.ProxyIngresses = []core.ProxyIngress{grant}
		}
	}
	if err := manager.SetPolicies(ctx, policies, "agt_shared"); err == nil {
		t.Fatal("delayed policy replaced renewed authority")
	}
	restarted := NewTrafficManager(filepath.Join(filepath.Dir(manager.statePath), "agent.json"))
	restarted.now = manager.now
	if err := restarted.SetPolicies(ctx, policies, "agt_shared"); err == nil {
		t.Fatal("restart lost authorization policy ordering")
	}
}

func TestProxyIngressTypeExecutionBoundary(t *testing.T) {
	const share = "shr_0000000000000001"
	for _, mode := range []string{"nginx", "caddy"} {
		grant := core.ProxyIngress{Mode: mode, Scope: core.ProxyIngressTypeScope, Generation: 7, Legacy: []core.ProxyIngressLease{{Port: 443, Generation: 2}}}
		quota := &core.SharedTrafficQuota{ID: share, ProxyIngresses: []core.ProxyIngress{grant}}
		manager := &TrafficManager{records: map[string]*trafficRecord{"one": {Policy: core.PortTrafficPolicy{SharedQuota: quota}}}}
		for _, port := range []int{443, 8443, 9443} {
			f := core.TLSFrontend{Mode: mode, PublicPort: port, SharedIngressGeneration: 7}
			if err := manager.authorizeProxyIngresses(share, []core.TLSFrontend{f}); err != nil {
				t.Fatal("type restricted to old listener", err)
			}
			if manager.authorizeProxyIngresses("shr_0000000000000002", []core.TLSFrontend{f}) == nil {
				t.Fatal("cross-instance authority")
			}
			f.SharedIngressGeneration = 0
			if manager.authorizeProxyIngresses(share, []core.TLSFrontend{f}) == nil {
				t.Fatal("snapshot omitted execution authority")
			}
			f.SharedIngressGeneration = 2
			if allowed := manager.authorizeProxyIngresses(share, []core.TLSFrontend{f}) == nil; allowed != (port == 443) {
				t.Fatal("old lease widened", port)
			}
		}
		quota.Revoked = true
		if manager.authorizeProxyIngresses(share, []core.TLSFrontend{{Mode: mode, PublicPort: 443, SharedIngressGeneration: 2}}) == nil {
			t.Fatal("revocation retained migration lease")
		}
	}
}
