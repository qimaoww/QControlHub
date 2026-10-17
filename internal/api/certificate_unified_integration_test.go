package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCertificateHTTPSharingIntegratedConsentAndDomains(t *testing.T) {
	db, ctx, owner, alice, bob := newConfigScopeAPIFixture(t)
	node := enrollConfigScopeAPIAgent(t, ctx, db)
	features := append(node.Features, core.AgentFeatureCertificates, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCorePrepare)
	if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: features}); err != nil {
		t.Fatal(err)
	}
	var sharing core.AgentSharing
	owner.call("GET", "/agents/"+node.ID+"/sharing", nil, 200, &sharing)
	owner.call("PUT", "/agents/"+node.ID+"/sharing", map[string]any{"revision": sharing.Revision, "shares": []map[string]any{
		{"username": "alice", "engines": node.Capabilities, "ports": []int{24443}, "http_domains": []string{"AUTH.Example.Test.", "例子.测试"}},
		{"username": "bob", "engines": node.Capabilities, "ports": []int{24444}, "http_domains": []string{"bob.example.test"}}}}, 200, &sharing)
	if len(sharing.Shares) != 2 || len(sharing.Shares[0].HTTPDomains) != 2 {
		t.Fatalf("grants missing in sharing: %+v", sharing)
	}
	var nodes []core.CertificateHTTPNode
	alice.call("GET", "/certificate-http-nodes", nil, 200, &nodes)
	if len(nodes) != 1 || nodes[0].Available || !strings.Contains(nodes[0].Reason, "接受") {
		t.Fatalf("unaccepted sharing usable: %+v", nodes)
	}
	for _, sh := range sharing.Shares {
		client := alice
		if sh.Username == "bob" {
			client = bob
		}
		client.call("POST", "/agent-access/"+sh.ID+"/response", core.AgentShareResponseRequest{Revision: sh.InvitationRevision, Decision: "accept"}, 200, nil)
	}
	alice.call("GET", "/certificate-http-nodes", nil, 200, &nodes)
	if len(nodes) != 1 || !nodes[0].Available || nodes[0].Owned || strings.Join(nodes[0].Domains, ",") != "auth.example.test,xn--fsqu00a.xn--0zwm56d" {
		t.Fatalf("wrong normalized auth scope: %+v", nodes)
	}
	owner.call("GET", "/agents/"+node.ID+"/sharing", nil, 200, &sharing)
	// A user cannot grant an entry point merely because they may deploy a core.
	alice.call("POST", "/certificate-http-grants", map[string]string{"agent_id": node.ID, "username": "bob", "domain": "stolen.example.test"}, 404, nil)
	owner.call("PUT", "/agents/"+node.ID+"/sharing", map[string]any{"revision": sharing.Revision, "shares": []map[string]any{
		{"username": "alice", "engines": node.Capabilities, "ports": []int{24443}, "http_domains": []string{}},
		{"username": "bob", "engines": node.Capabilities, "ports": []int{24444}, "http_domains": []string{"bob.example.test"}}}}, 200, &sharing)
	alice.call("GET", "/certificate-http-nodes", nil, 200, &nodes)
	if nodes[0].Available || !strings.Contains(nodes[0].Reason, "尚未授予") {
		t.Fatal(nodes)
	}
	bob.call("GET", "/certificate-http-nodes", nil, 200, &nodes)
	if !nodes[0].Available || len(nodes[0].Domains) != 1 || nodes[0].Domains[0] != "bob.example.test" {
		t.Fatal("another user's grant changed")
	}
}

func TestCaddyImportVerifiedSourceIsolationVersionsAndTombstone(t *testing.T) {
	db, ctx, owner, alice, bob := newConfigScopeAPIFixture(t)
	token, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "managed sources"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "managed sources", OS: "linux", Arch: "amd64", Capabilities: core.AllEngines(), Features: []string{core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureIndependentEgress, core.AgentFeatureSharedCoreLifecycle}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, token.Token)
	if err != nil {
		t.Fatal(err)
	}
	features := append(node.Features, core.AgentFeatureManagedTLS, core.AgentFeatureCertificates, core.AgentFeatureCaddyCertificates, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCorePrepare)
	if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: features}); err != nil {
		t.Fatal(err)
	}
	grantConfigScopeAPIAgent(t, ctx, db, node, alice, 21001, 8443)
	grantConfigScopeAPIAgent(t, ctx, db, node, bob, 21002, 8444)
	deploy := func(client configScopeAPIClient, domain string, port, public int) {
		t.Helper()
		base := "/agents/" + node.ID + "/configs/sing-box"
		var plan map[string]any
		client.call("POST", base+"/plans", map[string]string{"protocol": "vless-ws-tls"}, 201, &plan)
		plan["tls_server_name"] = domain
		plan["port"] = port
		plan["tls_public_port"] = public
		var saved configMutationResult
		client.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}, 200, &saved)
		lease, err := db.ClaimTask(ctx, node.ID)
		if err != nil || lease == nil {
			t.Fatalf("claim: %+v %v", lease, err)
		}
		result := core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true, TrafficSettled: true}
		if lease.SharedInstance {
			result.InstanceStatus = &core.SharedInstanceStatus{Engine: lease.Engine, ShareID: lease.SharedTrafficID, ConfigID: lease.ConfigID, ConfigVersion: lease.ConfigVersion, TaskID: lease.ID, AppliedTaskID: lease.ID, Status: "active", ObservedAt: time.Now()}
		}
		if err = db.CompleteTask(ctx, node.ID, lease.ID, result); err != nil {
			t.Fatal(err)
		}
	}
	deploy(alice, "same.example.test", 21001, 8443)
	deploy(bob, "same.example.test", 21002, 8444)
	sources, err := db.AgentManagedCertificateSources(ctx, node.ID)
	if err != nil || len(sources) != 2 {
		t.Fatalf("discovery: %+v %v", sources, err)
	}
	pair, _ := certificates.SelfSigned([]string{"same.example.test"}, "ecdsa256", 90)
	for _, src := range sources {
		if err = db.ImportManagedCertificate(ctx, node.ID, core.ManagedCertificateReport{SourceID: src.ID, Certificate: pair.Certificate, PrivateKey: pair.PrivateKey}); err != nil {
			t.Fatal(err)
		}
	}
	var a, b struct{ Items []core.Certificate }
	alice.call("GET", "/certificates", nil, 200, &a)
	bob.call("GET", "/certificates", nil, 200, &b)
	if len(a.Items) != 1 || len(b.Items) != 1 || a.Items[0].ID == b.Items[0].ID || a.Items[0].OwnerID != alice.userID || a.Items[0].Source != "caddy" || a.Items[0].RenewalManager != "caddy" || a.Items[0].AutoRenew {
		t.Fatalf("tenant isolation: %+v %+v", a, b)
	}
	cert := a.Items[0]
	alice.call("POST", "/certificates/"+cert.ID+"/grants", map[string]string{"username": "bob"}, 204, nil)
	var source core.ManagedCertificateSource
	for _, src := range sources {
		if src.ID == cert.SourceID {
			source = src
		}
	}
	report := core.ManagedCertificateReport{SourceID: source.ID, Certificate: pair.Certificate, PrivateKey: pair.PrivateKey}
	if err = db.ImportManagedCertificate(ctx, "agt_foreign", report); err == nil {
		t.Fatal("foreign node report accepted")
	}
	if err = db.ImportManagedCertificate(ctx, node.ID, report); err != nil {
		t.Fatal(err)
	}
	next, _ := certificates.SelfSigned([]string{"same.example.test"}, "ecdsa256", 91)
	// Longer validity permits a same-second local CA renewal; older validity
	// cannot replace it when a delayed report arrives after reconnect.
	if err = db.ImportManagedCertificate(ctx, node.ID, core.ManagedCertificateReport{SourceID: source.ID, Certificate: next.Certificate, PrivateKey: next.PrivateKey}); err != nil {
		t.Fatal(err)
	}
	if err = db.ImportManagedCertificate(ctx, node.ID, report); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		Certificate core.Certificate
		Versions    []core.Certificate
		Grants      []core.CertificateGrant
	}
	raw := alice.call("GET", "/certificates/"+cert.ID, nil, 200, &detail)
	if detail.Certificate.Version != 2 || len(detail.Versions) != 2 || len(detail.Grants) != 1 || strings.Contains(string(raw), "PRIVATE KEY") {
		t.Fatalf("version or secret isolation failed: %s", raw)
	}
	raw = bob.call("GET", "/certificates/"+cert.ID, nil, 200, &detail)
	if len(detail.Versions) != 2 {
		t.Fatal("authorized recipient lost version history")
	}
	for _, internalID := range []string{source.ID, node.ID, cert.SourceConfig} {
		if internalID == "" || strings.Contains(string(raw), internalID) {
			t.Fatal("owner's source identity escaped through shared certificate history")
		}
	}
	owner.call("GET", "/certificates/"+cert.ID, nil, 404, nil)
	alice.call("PUT", "/certificate-managed-sources/"+source.ID, map[string]string{"action": "pause"}, 204, nil)
	if err = db.ImportManagedCertificate(ctx, node.ID, report); err == nil {
		t.Fatal("paused source updated")
	}
	alice.call("PUT", "/certificate-managed-sources/"+source.ID, map[string]string{"action": "resume"}, 204, nil)
	// Unmatched keys are rejected without changing the current valid pair.
	if err = db.ImportManagedCertificate(ctx, node.ID, core.ManagedCertificateReport{SourceID: source.ID, Certificate: next.Certificate, PrivateKey: pair.PrivateKey}); err != nil {
		t.Fatal(err)
	}
	alice.call("GET", "/certificates/"+cert.ID, nil, 200, &detail)
	if detail.Certificate.Version != 2 || detail.Certificate.SyncStatus != "blocked" {
		t.Fatal("invalid pair replaced current version")
	}
	alice.call("DELETE", "/certificates/"+cert.ID, nil, 204, nil)
	if _, err = db.AgentManagedCertificateSources(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.ImportManagedCertificate(ctx, node.ID, report); err == nil {
		t.Fatal("deleted certificate recreated")
	}
	alice.call("GET", "/certificates", nil, 200, &a)
	if len(a.Items) != 0 {
		t.Fatal("deleted certificate recreated")
	}
	// An unrelated tenant's same-domain certificate survives deletion unchanged.
	bob.call("GET", "/certificates/"+b.Items[0].ID, nil, 200, nil)
	raw = bob.call("GET", "/certificate-managed-sources", nil, 200, nil)
	var visible []core.ManagedCertificateSource
	if json.Unmarshal(raw, &visible) != nil || len(visible) != 1 {
		t.Fatal("source ownership escaped")
	}
}
