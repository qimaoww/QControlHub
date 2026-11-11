package store

import (
	"errors"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestProxyIngressConsentAndDisabledAccountDoNotReviveOldGrant(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "ingress-owner")
	user, recipient := sharedTestUser(t, db, ctx, "ingress-guest")
	agent := sharedTestAgent(t, db, owner)
	agent.Features = []string{core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureIndependentEgress, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCoreLifecycle, core.AgentFeatureSharedProxyIngress}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features}); err != nil {
		t.Fatal(err)
	}
	grants := []core.ProxyIngress{{Mode: "nginx", Port: 443}}
	save := func(enabled bool, requested *[]core.ProxyIngress) core.AgentShare {
		t.Helper()
		current, err := db.AgentSharing(owner, agent.ID)
		if err != nil {
			t.Fatal(err)
		}
		next, err := db.SetAgentSharing(owner, agent.ID, core.AgentSharingRequest{Revision: current.Revision,
			Shares: []core.AgentSharingRecipient{{Username: user.Username, Enabled: &enabled, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21001}, ProxyIngresses: requested}}})
		if err != nil {
			t.Fatal(err)
		}
		return next.Shares[0]
	}
	accept := func(share core.AgentShare) core.AgentShare {
		t.Helper()
		access, err := db.RespondAgentShare(recipient, share.ID, core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "accept"})
		if err != nil {
			t.Fatal(err)
		}
		return access.Shares[0]
	}
	share := accept(save(true, &grants))
	original := share.ProxyIngresses[0].Generation
	if _, err := db.SetAgentSharing(recipient, agent.ID, core.AgentSharingRequest{Revision: 1}); !errors.Is(err, ErrForbidden) {
		t.Fatal("recipient can grant host resources", err)
	}
	save(false, nil)
	share = accept(save(true, nil))
	if share.ProxyIngresses[0].Generation == original {
		t.Fatal("withdrawal and renewed consent restored an old grant")
	}
	original = share.ProxyIngresses[0].Generation
	if _, err := db.SetUserDisabled(ctx, user.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetUserDisabled(ctx, user.ID, false); err != nil {
		t.Fatal(err)
	}
	access, err := db.UserAgentAccess(recipient, user.ID)
	if err != nil || access.Shares[0].ProxyIngresses[0].Generation == original {
		t.Fatal("disable and re-enable restored an old grant", err)
	}
	stale := core.ProxyIngressReport{ShareID: share.ID, Revision: share.InvitationRevision + 2}
	if err := db.recordProxyIngressReports(ctx, agent.ID, []core.ProxyIngressReport{stale}); err != nil {
		t.Fatal(err)
	}
	var applied int64
	if err := db.pool.QueryRow(ctx, `SELECT proxy_ingress_applied_revision FROM agent_shares WHERE id=$1`, share.ID).Scan(&applied); err != nil || applied != 0 {
		t.Fatal("stale acknowledgement changed effective status", err)
	}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{}); err != nil {
		t.Fatal(err)
	}
	grants = append(grants, core.ProxyIngress{Mode: "nginx", Port: 8443})
	current, err := db.AgentSharing(owner, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetAgentSharing(owner, agent.ID, core.AgentSharingRequest{Revision: current.Revision,
		Shares: []core.AgentSharingRecipient{{Username: user.Username, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21001}, ProxyIngresses: &grants}}}); !errors.Is(err, ErrConflict) {
		t.Fatal("unsupported Agent accepted new grants", err)
	}
}

func TestProxyIngressMigrationFrom98DefaultsToIsolation(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "migration-owner")
	user, _ := sharedTestUser(t, db, ctx, "migration-guest")
	agent := sharedTestAgent(t, db, owner)
	current, err := db.AgentSharing(owner, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetAgentSharing(owner, agent.ID, core.AgentSharingRequest{Revision: current.Revision, Shares: []core.AgentSharingRecipient{
		{Username: user.Username, Engines: []core.Engine{core.EngineMihomo}, Ports: []int{21001}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `ALTER TABLE agent_shares DROP COLUMN proxy_ingresses,
	 DROP COLUMN proxy_ingress_revision,DROP COLUMN proxy_ingress_applied_revision,
	 DROP COLUMN proxy_ingress_applied_revoked,DROP COLUMN proxy_ingress_error;
	 ALTER TABLE agent_shared_instance_ownership DROP COLUMN applied_proxy_ingresses;
	 DELETE FROM qcontrolhub_schema_migrations WHERE version>=98;
	 INSERT INTO qcontrolhub_schema_migrations(version) VALUES(98)`); err != nil {
		t.Fatal(err)
	}
	if err := db.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		sharing, err := db.AgentSharing(owner, agent.ID)
		if err != nil || len(sharing.Shares) != 1 || len(sharing.Shares[0].ProxyIngresses) != 0 || len(sharing.Shares[0].Ports) != 1 || sharing.Shares[0].Ports[0] != 21001 {
			t.Fatal("migration changed isolation or backend reservations", err)
		}
		if err := db.migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
