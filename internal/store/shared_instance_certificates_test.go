package store

import (
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestSharedInstanceStartChecksCertificateGrantAndRetainsAppliedPair(t *testing.T) {
	db, ctx, _ := isolatedConfigScopeStore(t)
	_, owner := sharedTestUser(t, db, ctx, "certificate-node-owner")
	user, recipient := sharedTestUser(t, db, ctx, "certificate-instance-user")
	agent := allEnginesSharedTestAgent(t, db, owner)
	sharedTestAllocation(t, db, ctx, user.ID, agent.ID, 10000, 21001)
	if _, err := db.pool.Exec(ctx, `UPDATE agents SET features=features || '["panel-certificates-v1"]'::jsonb WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	pair, err := certificates.SelfSigned([]string{"instance.example.test"}, "ecdsa256", 90)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := db.SaveCertificatePair(owner, "", "instance TLS", "import", pair)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetCertificateGrant(owner, certificate.ID, user.Username, false); err != nil {
		t.Fatal(err)
	}
	protocol, _ := serverconfig.FindProtocol(core.EngineMihomo, serverconfig.ProtocolTrojan)
	plan, _ := serverconfig.NewPlan(protocol)
	plan.Port, plan.PanelCertificateID, plan.TLSServerName = 21001, certificate.ID, "instance.example.test"
	if err := db.ResolveCertificatePlan(recipient, certificate.ID, core.EngineMihomo, &plan); err != nil {
		t.Fatal(err)
	}
	content, err := serverconfig.Generate(core.EngineMihomo, plan)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := serverconfig.MarshalClientMetadata(plan)
	if err != nil {
		t.Fatal(err)
	}
	config, err := db.SaveAgentConfigWithClientMetadata(recipient, core.Config{AgentID: agent.ID, Engine: core.EngineMihomo, Name: "own TLS", Content: content}, 0,
		ConfigClientMetadataMutation{Tag: plan.Tag, Content: metadata})
	if err != nil {
		t.Fatal(err)
	}
	deploy, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionDeploy, ConfigID: config.ID})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil || len(lease.Certificates) != 1 || lease.Certificates[0].Version != 1 {
		t.Fatalf("initial certificate deployment: %+v %v", lease, err)
	}
	if err := completeSharedTestTask(db, ctx, agent.ID, deploy.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	pair2, _ := certificates.SelfSigned([]string{"instance.example.test"}, "ecdsa256", 91)
	if _, err := db.SaveCertificatePair(owner, certificate.ID, "", "import", pair2); err != nil {
		t.Fatal(err)
	}
	// A broken/newer pair must not replace the immutable pair already deployed.
	if _, err := db.pool.Exec(ctx, `UPDATE certificate_versions SET secret='unreadable newer pair' WHERE certificate_id=$1 AND version=2`, certificate.ID); err != nil {
		t.Fatal(err)
	}
	start, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionStart})
	if err != nil {
		t.Fatal(err)
	}
	lease, err = db.ClaimTask(ctx, agent.ID)
	if err != nil || lease == nil || lease.ID != start.ID || len(lease.Certificates) != 0 || lease.ConfigVersion != 1 {
		t.Fatalf("start must authorize the deployed pair without sending keys or rotating files: %+v %v", lease, err)
	}
	if err := completeSharedTestTask(db, ctx, agent.ID, start.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
		t.Fatal(err)
	}
	pending, err := db.CreateTask(recipient, core.TaskRequest{AgentID: agent.ID, Engine: config.Engine, Action: core.ActionRestart})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetCertificateGrant(owner, certificate.ID, user.Username, true); err != nil {
		t.Fatal(err)
	}
	if lease, err = db.ClaimTask(ctx, agent.ID); err != nil || lease != nil {
		t.Fatalf("revoked certificate grant dispatched: %+v %v", lease, err)
	}
	rejected, err := db.GetTask(recipient, pending.ID)
	if err != nil || rejected.Status != core.TaskFailed || !strings.Contains(rejected.Error, "授权") {
		t.Fatalf("missing certificate authorization error: %+v %v", rejected, err)
	}
}
