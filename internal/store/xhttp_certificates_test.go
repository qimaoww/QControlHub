package store

import (
	"errors"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestXHTTPFiveCertificateGrantVersionsAndRestore(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "xhttp-cert-owner")
	user, recipient := sharedTestUser(t, db, ctx, "xhttp-cert-user")
	agent := allEnginesSharedTestAgent(t, db, owner)
	sharing, err := db.AgentSharing(owner, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	sharing, err = db.SetAgentSharing(owner, agent.ID, core.AgentSharingRequest{Revision: sharing.Revision,
		Shares: []core.AgentSharingRecipient{{Username: user.Username, Engines: []core.Engine{core.EngineXray}, Ports: []int{21001, 21002, 21003}, LimitBytes: 10000}}})
	if err != nil {
		t.Fatal(err)
	}
	share := sharing.Shares[0]
	if _, err = db.RespondAgentShare(recipient, share.ID, core.AgentShareResponseRequest{Revision: share.InvitationRevision, Decision: "accept"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET features=features||'["panel-certificates-v1","managed-tls-v1","xhttp-presets-v1","managed-tls-panel-certificates-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	names := []string{"tls.example.test", "reality.example.test"}
	pair, _ := certificates.SelfSigned(names, "ecdsa256", 90)
	certificate, err := db.SaveCertificatePair(owner, "", "five TLS", "import", pair)
	if err != nil {
		t.Fatal(err)
	}
	protocol, _ := serverconfig.FindProtocol(core.EngineXray, serverconfig.ProtocolXHTTPFive)
	plan, _ := serverconfig.NewPlan(protocol)
	plan.Port, plan.ClientPort, plan.XHTTPBackendPort, plan.XHTTPTLSPort = 21001, 21001, 21002, 21003
	plan.PanelCertificateID, plan.TLSServerName, plan.RealityServerName = certificate.ID, names[0], names[1]
	if err = db.ResolveCertificatePlan(recipient, certificate.ID, core.EngineXray, &plan); !errors.Is(err, ErrForbidden) {
		t.Fatal("ungranted certificate accepted", err)
	}
	if err = db.SetCertificateGrant(owner, certificate.ID, user.Username, false); err != nil {
		t.Fatal(err)
	}
	if err = db.ResolveCertificatePlan(recipient, certificate.ID, core.EngineXray, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.TLSMode != "" || plan.TLSEnabled {
		t.Fatal("certificate selection replaced five-in-one architecture")
	}
	content, err := serverconfig.Generate(core.EngineXray, plan)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := serverconfig.MarshalClientMetadataWithPrevious(plan, "")
	if err != nil {
		t.Fatal(err)
	}
	config, err := db.SaveAgentConfigWithClientMetadata(recipient, core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "five with certificate", Content: content}, 0, ConfigClientMetadataMutation{Tag: plan.Tag, Content: metadata})
	if err != nil {
		t.Fatal(err)
	}
	task, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionDeploy, ConfigID: config.ID})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil || len(lease.Certificates) != 1 || lease.Certificates[0].Version != 1 || len(lease.TLSFrontends) != 1 || lease.TLSFrontends[0].CertificatePath != plan.CertificatePath {
		t.Fatalf("group certificate lost execution binding: %+v %v", lease, err)
	}
	if err = completeSharedTestTask(db, ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	pair2, _ := certificates.SelfSigned(names, "ecdsa256", 91)
	if _, err = db.SaveCertificatePair(owner, certificate.ID, "", "import", pair2); err != nil {
		t.Fatal(err)
	}
	restored, err := db.RestoreConfigRevision(recipient, config.ID, 1, config.Version)
	if err != nil || restored.Version != 2 {
		t.Fatal("group certificate history restore failed", err)
	}
	if _, err = db.ConfigRevision(recipient, config.ID, 1); err != nil {
		t.Fatal("restore destroyed original archive", err)
	}
	task, err = db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionDeploy, ConfigID: config.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SetCertificateGrant(owner, certificate.ID, user.Username, true); err != nil {
		t.Fatal(err)
	}
	lease, err = db.ClaimTask(ctx, agent.ID)
	if err != nil || lease != nil {
		t.Fatal("revoked group certificate dispatched", err)
	}
	rejected, err := db.GetTask(recipient, task.ID)
	if err != nil || rejected.Status != core.TaskFailed {
		t.Fatal("revoked group certificate task did not fail", err)
	}
	if _, err = db.RestoreConfigRevision(recipient, config.ID, 1, restored.Version); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked certificate restored via group metadata", err)
	}
}
