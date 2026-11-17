package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestProxyIngressTypePersistenceAndPatchSemantics(t *testing.T) {
	db, ctx, url := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "type-owner")
	user, recipient := sharedTestUser(t, db, ctx, "type-guest")
	agent := sharedTestAgent(t, db, owner)
	features := []string{core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureIndependentEgress, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCoreLifecycle, core.AgentFeatureSharedProxyIngress}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: features}); err != nil {
		t.Fatal(err)
	}
	save := func(grants *[]core.ProxyIngress) (core.AgentSharing, error) {
		t.Helper()
		current, err := db.AgentSharing(owner, agent.ID)
		if err != nil {
			t.Fatal(err)
		}
		return db.SetAgentSharing(owner, agent.ID, core.AgentSharingRequest{Revision: current.Revision, Shares: []core.AgentSharingRecipient{{Username: user.Username, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21001}, ProxyIngresses: grants}}})
	}
	grants := []core.ProxyIngress{{Mode: "nginx", Port: 443, Generation: 999}, {Mode: "nginx", Port: 8443}, {Mode: "caddy"}}
	sharing, err := save(&grants)
	if err != nil {
		t.Fatal(err)
	}
	share := sharing.Shares[0]
	if _, err := db.RespondAgentShare(recipient, share.ID, core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "accept"}); err != nil {
		t.Fatal(err)
	}
	var before []core.ProxyIngress
	var revision int64
	if err := db.pool.QueryRow(ctx, `SELECT proxy_ingresses,proxy_ingress_revision FROM agent_shares WHERE id=$1`, share.ID).Scan(&before, &revision); err != nil {
		t.Fatal(err)
	}
	if len(before) != 2 || before[0].Mode != "caddy" || before[1].Port != 0 || before[1].Generation == 999 || len(before[1].Legacy) > 0 {
		t.Fatal("noncanonical grants", before)
	}
	if _, err := save(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := save(&grants); err != nil {
		t.Fatal(err)
	}
	var after []core.ProxyIngress
	var afterRevision int64
	if err := db.pool.QueryRow(ctx, `SELECT proxy_ingresses,proxy_ingress_revision FROM agent_shares WHERE id=$1`, share.ID).Scan(&after, &afterRevision); err != nil || revision != afterRevision || !reflect.DeepEqual(before, after) {
		t.Fatal("unchanged save rewrote authority", after, err)
	}
	reopened, err := OpenWithConfigKey(ctx, url, true, testEncryptionKey("config-scope"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	access, err := reopened.UserAgentAccess(recipient, user.ID)
	if err != nil || len(access.Shares[0].ProxyIngresses) != 2 {
		t.Fatal("authority lost after reopen", err)
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	for _, mode := range []string{"nginx", "caddy"} {
		for _, port := range []int{443, 8443, 9443} {
			_, allowed, err := proxyIngressGrantTx(ctx, tx, agent.ID, user.ID, core.TLSFrontend{Mode: mode, PublicPort: port})
			if err != nil || !allowed {
				t.Fatal("type grant retained port restriction", mode, port, err)
			}
		}
	}
	tx.Rollback(ctx)
	bad := []core.ProxyIngress{{Mode: "arbitrary"}}
	if _, err := save(&bad); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown proxy accepted", err)
	}
	empty := []core.ProxyIngress{}
	if _, err := save(&empty); err != nil {
		t.Fatal(err)
	}
	if _, err := save(&grants); err != nil {
		t.Fatal(err)
	}
	var fresh []core.ProxyIngress
	if err := db.pool.QueryRow(ctx, `SELECT proxy_ingresses FROM agent_shares WHERE id=$1`, share.ID).Scan(&fresh); err != nil || fresh[0].Generation == before[0].Generation || len(fresh[0].Legacy) != 0 {
		t.Fatal("regrant restored revoked authority", err)
	}
}

func TestProxyIngressTypeMigrationAndMixedAgents(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "upgrade-owner")
	user, recipient := sharedTestUser(t, db, ctx, "upgrade-guest")
	agent := sharedTestAgent(t, db, owner)
	legacyFeatures := []string{core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureIndependentEgress, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCoreLifecycle, core.AgentFeatureSharedProxyIngressLegacy}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: legacyFeatures}); err != nil {
		t.Fatal(err)
	}
	current, err := db.AgentSharing(owner, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	sharing, err := db.SetAgentSharing(owner, agent.ID, core.AgentSharingRequest{Revision: current.Revision, Shares: []core.AgentSharingRecipient{{Username: user.Username, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21001}}}})
	if err != nil {
		t.Fatal(err)
	}
	share := sharing.Shares[0]
	if _, err := db.RespondAgentShare(recipient, share.ID, core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "accept"}); err != nil {
		t.Fatal(err)
	}
	legacy := []core.ProxyIngress{{Mode: "nginx", Port: 443, Generation: 2}, {Mode: "nginx", Port: 8443, Generation: 3}, {Mode: "caddy", Port: 9443, Generation: 4}}
	if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET proxy_ingresses=$2,proxy_ingress_revision=5 WHERE id=$1`, share.ID, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `DELETE FROM qcontrolhub_schema_migrations WHERE version>=101; INSERT INTO qcontrolhub_schema_migrations(version) VALUES(101)`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var canonical []core.ProxyIngress
	var revision int64
	if err := db.pool.QueryRow(ctx, `SELECT proxy_ingresses,proxy_ingress_revision FROM agent_shares WHERE id=$1`, share.ID).Scan(&canonical, &revision); err != nil {
		t.Fatal(err)
	}
	if len(canonical) != 2 || len(canonical[1].Legacy) != 2 || canonical[1].Generation != 6 || canonical[1].Port != 0 {
		t.Fatal("legacy merge", canonical)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var again int64
	if err := db.pool.QueryRow(ctx, `SELECT proxy_ingress_revision FROM agent_shares WHERE id=$1`, share.ID).Scan(&again); err != nil || again != revision {
		t.Fatal("migration rewrote authority", err)
	}
	access, err := db.UserAgentAccess(recipient, user.ID)
	if err != nil || access.Shares[0].ProxyIngressStatus != "upgrade_required" || len(access.Shares[0].ProxyIngresses[1].Legacy) != 0 {
		t.Fatal("old Agent feedback or public alias leak", err)
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO port_traffic_policies(id,agent_id,name,engine,port,protocol,cycle,cycle_anchor,limit_bytes,created_at,updated_at,share_id)
	 VALUES('trf_0000000000000001',$1,'legacy backend','mihomo',21001,'both','monthly',CURRENT_DATE,0,now(),now(),$2)`, agent.ID, share.ID); err != nil {
		t.Fatal(err)
	}
	policies, err := db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || len(policies) != 1 || policies[0].SharedQuota == nil || policies[0].SharedQuota.Revoked || len(policies[0].SharedQuota.ProxyIngresses) != 3 {
		t.Fatal("legacy policy projection", policies, err)
	}
	for _, g := range policies[0].SharedQuota.ProxyIngresses {
		if g.Scope != "" || g.Port == 0 || len(g.Legacy) > 0 {
			t.Fatal("type scope leaked to old Agent", g)
		}
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, lease := range legacy {
		g, allowed, err := proxyIngressGrantTx(ctx, tx, agent.ID, user.ID, core.TLSFrontend{Mode: lease.Mode, PublicPort: lease.Port, SharedIngressGeneration: lease.Generation})
		if err != nil || !allowed || g != lease.Generation {
			t.Fatal("valid old snapshot invalidated", lease, err)
		}
	}
	if _, allowed, err := proxyIngressGrantTx(ctx, tx, agent.ID, user.ID, core.TLSFrontend{Mode: "nginx", PublicPort: 9443}); allowed || !errors.Is(err, ErrConflict) {
		t.Fatal("old Agent received new semantic", err)
	}
	tx.Rollback(ctx)
	features := append(legacyFeatures, core.AgentFeatureSharedProxyIngress)
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: features}); err != nil {
		t.Fatal(err)
	}
	policies, err = db.AgentPortTrafficPolicies(ctx, agent.ID)
	if err != nil || !reflect.DeepEqual(policies[0].SharedQuota.ProxyIngresses, canonical) {
		t.Fatal("upgraded Agent did not receive canonical policy", err)
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO agent_shared_instance_ownership(agent_id,engine,share_id,owner_id,config_id,config_version,updated_at)
	 VALUES($1,'mihomo',$2,$3,'cfg_partial',1,now())`, agent.ID, share.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET proxy_ingresses=$2,proxy_ingress_revision=proxy_ingress_revision+1 WHERE id=$1`, share.ID, canonical[:1]); err != nil {
		t.Fatal(err)
	}
	frontends := []core.TLSFrontend{{Mode: "nginx", PublicPort: 443}, {Mode: "caddy", PublicPort: 9443}}
	checkRoutes := func(want int) {
		t.Helper()
		tx, err := db.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		actual, err := synchronizedProxyFrontendsTx(ctx, tx, agent.ID, "cfg_partial", 1, frontends, legacy)
		if err != nil || len(actual) != want || want == 1 && actual[0].Mode != "caddy" {
			t.Fatal("cleanup receipt released wrong routes", actual, err)
		}
	}
	checkRoutes(2) // Offline/unacknowledged nodes still reserve the old route.
	var reportRevision int64
	if err := db.pool.QueryRow(ctx, `SELECT proxy_ingress_revision+invitation_revision FROM agent_shares WHERE id=$1`, share.ID).Scan(&reportRevision); err != nil {
		t.Fatal(err)
	}
	report := core.ProxyIngressReport{ShareID: share.ID, Revision: reportRevision, Error: "cleanup failed"}
	if err := db.recordProxyIngressReports(ctx, agent.ID, []core.ProxyIngressReport{report}); err != nil {
		t.Fatal(err)
	}
	checkRoutes(2)
	report.Error = ""
	if err := db.recordProxyIngressReports(ctx, agent.ID, []core.ProxyIngressReport{report}); err != nil {
		t.Fatal(err)
	}
	checkRoutes(1)
	if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET proxy_ingresses=$2,proxy_ingress_revision=proxy_ingress_revision+1 WHERE id=$1`, share.ID, canonical); err != nil {
		t.Fatal(err)
	}
	for _, state := range []struct {
		enabled  bool
		status   string
		disabled bool
	}{{true, "accepted", false}, {false, "accepted", false}, {true, "pending", false}, {true, "rejected", false}, {true, "accepted", true}} {
		if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET enabled=$2,status=$3 WHERE id=$1`, share.ID, state.enabled, state.status); err != nil {
			t.Fatal(err)
		}
		if _, err := db.pool.Exec(ctx, `UPDATE panel_users SET disabled=$2 WHERE id=$1`, user.ID, state.disabled); err != nil {
			t.Fatal(err)
		}
		tx, err := db.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, allowed, err := proxyIngressGrantTx(ctx, tx, agent.ID, user.ID, core.TLSFrontend{Mode: "nginx", PublicPort: 8443, SharedIngressGeneration: 3})
		tx.Rollback(ctx)
		expected := state.enabled && state.status == "accepted" && !state.disabled
		if err != nil || allowed != expected {
			t.Fatal("migration changed effective scope", state, err)
		}
	}
	// Bad legacy data must abort the entire migration, including its ledger.
	if _, err := db.pool.Exec(ctx, `UPDATE agent_shares SET proxy_ingresses='[{"mode":"unknown","port":443,"generation":2}]'::jsonb WHERE id=$1`, share.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `DELETE FROM qcontrolhub_schema_migrations WHERE version>=102`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err == nil {
		t.Fatal("invalid legacy authority migrated")
	}
	var version int
	if err := db.pool.QueryRow(ctx, `SELECT max(version) FROM qcontrolhub_schema_migrations`).Scan(&version); err != nil || version != 101 {
		t.Fatal("failed migration committed ledger", version, err)
	}
}
