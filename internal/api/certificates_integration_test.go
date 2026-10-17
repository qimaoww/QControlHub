package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestCertificateOrdinaryUserIsolationSharingAndDeployment(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	var cert core.Certificate
	alice.call("POST", "/certificates", map[string]any{"name": "Alice TLS", "source": "self-signed", "domains": []string{"edge.example.test"}, "key_type": "ecdsa256", "days": 90, "owner_id": bob.userID}, 201, &cert)
	if cert.OwnerID != alice.userID || cert.Version != 1 {
		t.Fatalf("wrong ownership/version: %+v", cert)
	}
	for _, client := range []configScopeAPIClient{bob, admin} {
		client.call("GET", "/certificates/"+cert.ID, nil, 404, nil)
		var list struct{ Items []core.Certificate }
		client.call("GET", "/certificates", nil, 200, &list)
		if len(list.Items) != 0 {
			t.Fatal("private resource exposed")
		}
		client.call("POST", "/certificates/"+cert.ID+"/replace", map[string]string{"certificate": "private", "private_key": "private"}, 400, nil)
	}
	var account core.CertificateAccount
	alice.call("POST", "/certificate-accounts", map[string]any{"name": "Alice DNS", "kind": "dns", "provider": "cloudflare", "credentials": map[string]string{"zone": "example.test", "token": "secret-token-fixture"}}, 201, &account)
	raw := alice.call("GET", "/certificate-accounts", nil, 200, nil)
	if strings.Contains(string(raw), "secret-token-fixture") {
		t.Fatal("credential escaped API")
	}
	bob.call("POST", "/certificate-accounts/"+account.ID+"/check", nil, 404, nil)
	bob.call("PUT", "/certificate-accounts/"+account.ID+"/credentials", map[string]any{"name": "stolen", "credentials": map[string]string{"zone": "example.test", "token": "other-token"}}, 404, nil)
	alice.call("PUT", "/certificate-accounts/"+account.ID+"/credentials", map[string]any{"name": "rotated DNS", "credentials": map[string]string{"zone": "example.test", "token": "rotated-secret-fixture"}}, 200, nil)
	alice.call("POST", "/certificates/"+cert.ID+"/grants", map[string]string{"username": "bob"}, 204, nil)
	var detail map[string]json.RawMessage
	raw = bob.call("GET", "/certificates/"+cert.ID, nil, 200, &detail)
	if strings.Contains(string(raw), "PRIVATE KEY") || strings.Contains(string(raw), "secret-token-fixture") {
		t.Fatal("secret in shared details")
	}
	bob.call("POST", "/certificates/"+cert.ID+"/grants", map[string]string{"username": "alice"}, 404, nil)
	agent := enrollConfigScopeAPIAgent(t, ctx, db)
	agent.Features = append(agent.Features, core.AgentFeatureCertificates, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCorePrepare)
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	grantConfigScopeAPIAgent(t, ctx, db, agent, bob, 24443)
	admin.call("POST", "/certificate-http-grants", map[string]string{"agent_id": agent.ID, "username": "bob", "domain": "auth.example.test"}, 204, nil)
	bob.call("POST", "/certificate-http-grants", map[string]string{"agent_id": agent.ID, "username": "alice", "domain": "auth.example.test"}, 404, nil)
	var httpGrants []core.HTTPChallengeGrant
	bob.call("GET", "/certificate-http-grants", nil, 200, &httpGrants)
	if len(httpGrants) != 1 || httpGrants[0].Username != "bob" {
		t.Fatal("missing explicit domain grant")
	}
	raw = alice.call("GET", "/certificate-http-grants", nil, 200, nil)
	if strings.Contains(string(raw), "auth.example.test") {
		t.Fatal("unrelated HTTP grant exposed")
	}
	admin.call("DELETE", "/certificate-http-grants", httpGrants[0], 204, nil)

	protocol, _ := serverconfig.FindProtocol(core.EngineMihomo, serverconfig.ProtocolTrojan)
	plan, err := serverconfig.NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	plan.Port = 24443
	plan.TLSServerName = "edge.example.test"
	plan.PanelCertificateID = cert.ID
	var saved struct {
		Config core.Config `json:"config"`
		Task   core.Task   `json:"task"`
	}
	body := map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}
	bob.call("POST", "/agents/"+agent.ID+"/configs/mihomo/server-inbounds", body, http.StatusOK, &saved)
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || len(claimed.Certificates) != 1 || claimed.Certificates[0].Version != 1 {
		t.Fatalf("authorized deployment: %+v %v", claimed, err)
	}
	if err = db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, TrafficSettled: true}); err != nil {
		t.Fatal(err)
	}
	raw = bob.call("GET", "/tasks/"+claimed.ID, nil, 200, nil)
	if strings.Contains(string(raw), "PRIVATE KEY") || strings.Contains(string(raw), "private_key") {
		t.Fatal("secret escaped task history")
	}
	pair, err := certificates.SelfSigned([]string{"edge.example.test"}, "ecdsa256", 100)
	if err != nil {
		t.Fatal(err)
	}
	alice.call("POST", "/certificates/"+cert.ID+"/replace", pair, 200, &cert)
	if cert.Version != 2 {
		t.Fatal("replacement version")
	}
	var cfg core.Config
	bob.call("GET", "/agents/"+agent.ID+"/configs/mihomo", nil, 200, &cfg)
	// An already queued task must lose access when its certificate grant is revoked.
	var pending core.Task
	bob.call("POST", "/tasks", core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigID: cfg.ID}, 201, &pending)
	alice.call("DELETE", "/certificates/"+cert.ID+"/grants", map[string]string{"username": "bob"}, 204, nil)
	claimed, err = db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed != nil {
		t.Fatalf("revoked certificate dispatched: %+v %v", claimed, err)
	}
	bob.call("GET", "/certificates/"+cert.ID, nil, 404, nil)
	alice.call("DELETE", "/certificates/"+cert.ID, nil, 409, nil)
	t.Log("Real ordinary-user password/CSRF sessions: create -> share -> select -> shared-instance task -> version replacement -> revoke queued deployment; no secret in history")
}
