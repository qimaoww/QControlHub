package api

import (
	"encoding/json"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	if err = db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, TrafficSettled: true, InstanceStatus: &core.SharedInstanceStatus{Engine: claimed.Engine, ShareID: claimed.SharedTrafficID, ConfigID: claimed.ConfigID, ConfigVersion: claimed.ConfigVersion, TaskID: claimed.ID, AppliedTaskID: claimed.ID, Status: "active", ObservedAt: time.Now().UTC()}}); err != nil {
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

	// Replacing through the authenticated API must reach the existing worker
	// and the recipient's deployment channel without reselecting or saving.
	if err = db.RunCertificateMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	synced, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || synced == nil || len(synced.Certificates) != 1 || synced.Certificates[0].Version != 2 || synced.ConfigID != saved.Config.ID {
		t.Fatal("API replacement did not automatically reach bound shared instance", err)
	}
	if err = db.CompleteTask(ctx, agent.ID, synced.ID, core.TaskResultRequest{LeaseID: synced.LeaseID, Success: true, TrafficSettled: true, InstanceStatus: &core.SharedInstanceStatus{Engine: synced.Engine, ShareID: synced.SharedTrafficID, ConfigID: synced.ConfigID, ConfigVersion: synced.ConfigVersion, TaskID: synced.ID, AppliedTaskID: synced.ID, Status: "active", ObservedAt: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
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

func TestCertificateACMEAccountSelectionAndWorkerScopes(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	localCA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "local CA unavailable", 400) }))
	defer localCA.Close()
	old := certificates.Directories["letsencrypt-staging"]
	certificates.Directories["letsencrypt-staging"] = localCA.URL
	defer func() { certificates.Directories["letsencrypt-staging"] = old }()
	_, key, err := certificates.NewKey("ecdsa256")
	if err != nil {
		t.Fatal(err)
	}
	clients := []configScopeAPIClient{admin, alice, bob}
	accounts := make([]core.CertificateAccount, len(clients))
	issued := make([]core.Certificate, len(clients))
	for i, client := range clients {
		owner := client.userID
		// The environment administrator uses the reserved empty legacy workspace.
		scope := store.WithConfigScope(ctx, owner, i == 0)
		accounts[i], err = db.CreateCertificateAccount(scope, core.CertificateAccount{Name: "same name and email", Email: "same@example.test", Kind: "acme", CA: "letsencrypt-staging"}, certificates.ACMEAccount{Key: key})
		if err != nil {
			t.Fatal(err)
		}
		var listed []core.CertificateAccount
		client.call("GET", "/certificate-accounts", nil, 200, &listed)
		if len(listed) != 1 || listed[0].ID != accounts[i].ID {
			t.Fatalf("account selection: %+v", listed)
		}
		client.call("POST", "/certificates", map[string]any{"name": "API scope", "source": "acme", "domains": []string{"api.example.test"}, "method": "dns-manual", "key_type": "ecdsa256", "account_id": listed[0].ID}, 201, &issued[i])
	}
	if err = db.RunCertificateMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	for i, client := range clients {
		var detail struct {
			Orders []core.CertificateOrder `json:"orders"`
		}
		client.call("GET", "/certificates/"+issued[i].ID, nil, 200, &detail)
		if len(detail.Orders) != 1 || detail.Orders[0].Status != "queued" || detail.Orders[0].Stage != "retry" || strings.Contains(detail.Orders[0].Error, "读取 ACME") {
			t.Fatalf("API worker scope: %+v", detail)
		}
		for j, other := range clients {
			if i == j {
				continue
			}
			other.call("POST", "/certificates", map[string]any{"name": "foreign", "source": "acme", "domains": []string{"api.example.test"}, "method": "dns-manual", "key_type": "ecdsa256", "account_id": accounts[i].ID}, 404, nil)
		}
	}
	alice.call("POST", "/certificates/"+issued[1].ID+"/grants", map[string]string{"username": "bob"}, 204, nil)
	bob.call("POST", "/certificates/"+issued[1].ID+"/renew", nil, 403, nil)
	bob.call("PUT", "/certificates/"+issued[1].ID+"/auto-renew", map[string]bool{"enabled": true}, 403, nil)
	var details struct {
		Orders []core.CertificateOrder `json:"orders"`
	}
	alice.call("GET", "/certificates/"+issued[1].ID, nil, 200, &details)
	bob.call("POST", "/certificates/"+issued[1].ID+"/orders/"+details.Orders[0].ID+"/continue", nil, 403, nil)
	bob.call("POST", "/certificates/"+issued[1].ID+"/grants", map[string]string{"username": "alice"}, 404, nil)
}

// Actual authenticated API and encrypted PostgreSQL, with only the outbound
// provider reads replaced. No public provider credential or DNS write is used.
func TestCertificateDNSAccountsSaveRotateAndCheckWithoutZone(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	reads := map[string]int{}
	http.DefaultTransport = certificateAccountReadTransport(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Host {
		case "api.cloudflare.com":
			if r.URL.Query().Get("name") != "" || r.URL.Query().Get("per_page") != "1" {
				t.Fatal("domain-dependent account check")
			}
			reads["cloudflare"]++
			body = `{"success":true,"result":[]}`
		case "alidns.aliyuncs.com":
			if r.URL.Query().Get("Action") != "DescribeDomains" || r.URL.Query().Get("DomainName") != "" {
				t.Fatal("domain-dependent Alibaba check")
			}
			reads["alidns"]++
			body = `{"Domains":{"Domain":[]},"TotalCount":0}`
		case "dnspod.tencentcloudapi.com":
			if r.Header.Get("X-TC-Action") != "DescribeDomainList" {
				t.Fatal("domain-dependent DNSPod check")
			}
			reads["dnspod"]++
			body = `{"Response":{"DomainList":[],"DomainCountInfo":{"AllTotal":0}}}`
		default:
			return original.RoundTrip(r)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	for _, provider := range []string{"cloudflare", "alidns", "dnspod"} {
		credentials := map[string]string{}
		switch provider {
		case "cloudflare":
			credentials["token"] = "fixture-token"
		case "alidns":
			credentials["access_key_id"] = "fixture-id"
			credentials["access_key_secret"] = "fixture-secret"
		default:
			credentials["secret_id"] = "fixture-id"
			credentials["secret_key"] = "fixture-secret"
		}
		var account core.CertificateAccount
		alice.call("POST", "/certificate-accounts", map[string]any{"name": provider, "kind": "dns", "provider": provider, "credentials": credentials}, 201, &account)
		var stored certificates.DNSCredentials
		if _, err := db.CertificateAccountSecret(store.WithConfigScope(ctx, alice.userID, false), account.ID, "dns", &stored); err != nil || stored.Zone != "" {
			t.Fatalf("zone-free storage: %+v %v", stored, err)
		}
		alice.call("POST", "/certificate-accounts/"+account.ID+"/check", nil, 200, nil)
		for _, client := range []configScopeAPIClient{admin, bob} {
			client.call("POST", "/certificate-accounts/"+account.ID+"/check", nil, 404, nil)
			client.call("PUT", "/certificate-accounts/"+account.ID+"/credentials", map[string]any{"name": "foreign", "credentials": credentials}, 404, nil)
		}
		var updated core.CertificateAccount
		for key := range credentials {
			credentials[key] = "rotated-fixture"
		}
		alice.call("PUT", "/certificate-accounts/"+account.ID+"/credentials", map[string]any{"name": "rotated " + provider, "credentials": credentials}, 200, &updated)
		if updated.ID != account.ID {
			t.Fatal("credential rotation broke account references")
		}
		alice.call("POST", "/certificate-accounts/"+account.ID+"/check", nil, 200, nil)
		if reads[provider] != 2 {
			t.Fatalf("provider check not executed: %s %d", provider, reads[provider])
		}
	}
	t.Log("All three providers saved, rotated, read-checked without domains; foreign users and administrator denied")
}

type certificateAccountReadTransport func(*http.Request) (*http.Response, error)

func (f certificateAccountReadTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
