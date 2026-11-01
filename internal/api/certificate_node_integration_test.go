package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func enrollNativeCertificateNode(t *testing.T, ctx context.Context, db *store.Store, owner configScopeAPIClient) core.Agent {
	node, _ := enrollNativeCertificateNodeWithKey(t, ctx, db, owner)
	return node
}

func enrollNativeCertificateNodeWithKey(t *testing.T, ctx context.Context, db *store.Store, owner configScopeAPIClient) (core.Agent, ed25519.PrivateKey) {
	t.Helper()
	token, err := db.CreateEnrollmentToken(store.WithConfigScope(ctx, owner.userID, false), core.EnrollmentTokenRequest{Name: "native certificate fixture"})
	if err != nil {
		t.Fatal(err)
	}
	features := []string{core.AgentFeatureManagedTLS, core.AgentFeatureCertificates, core.AgentFeatureCaddyCertificates, core.AgentFeatureNodeCertificates, core.AgentFeatureSharedTraffic, core.AgentFeatureSharedEngines, core.AgentFeatureIndependentEgress, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCorePrepare, core.AgentFeatureSharedCoreLifecycle}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	node, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "native certificate fixture", OS: "linux", Arch: "amd64", Capabilities: core.AllEngines(), Features: features, PublicKey: authn.EncodePublicKey(public)}, token.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: features}); err != nil {
		t.Fatal(err)
	}
	return node, private
}

func TestNodeCertificateAuthenticatedAgentChannel(t *testing.T) {
	db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
	node, key := enrollNativeCertificateNodeWithKey(t, ctx, db, alice)
	deployNativeCertificate(t, ctx, db, node, alice, core.EngineSingBox, "/etc/tls/channel.pem", "/etc/tls/channel.key", 24443)
	var sources []core.ManagedCertificateSource
	alice.call("GET", "/certificate-managed-sources", nil, 200, &sources)
	if len(sources) != 1 {
		t.Fatal("missing source")
	}
	alice.call("PUT", "/certificate-managed-sources/"+sources[0].ID, map[string]string{"action": "enroll"}, 204, nil)
	server := httptest.NewServer(alice.handler)
	defer server.Close()
	connection := dialPublicIPProbeWSS(t, ctx, server.URL, node.ID, key)
	defer connection.CloseNow()
	if err := wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireHeartbeat, Heartbeat: &core.HeartbeatRequest{Features: node.Features}}); err != nil {
		t.Fatal("cannot send authenticated heartbeat")
	}
	readCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	var request core.ManagedCertificateSource
	for request.ID == "" {
		var message core.WireMessage
		if err := wsjson.Read(readCtx, connection, &message); err != nil {
			t.Fatal("source request not delivered over authenticated channel")
		}
		if message.Type == core.WireManagedCertificateSources && len(message.ManagedCertificateSources) > 0 {
			request = message.ManagedCertificateSources[0]
		}
	}
	if request.Source != "node" || request.Reference == nil || request.ID != sources[0].ID || request.TaskID != sources[0].TaskID {
		t.Fatal("wrong authenticated source request")
	}
	pair, err := certificates.SelfSigned([]string{"edge.example.test"}, "ecdsa256", 90)
	if err != nil {
		t.Fatal(err)
	}
	report := core.ManagedCertificateReport{SourceID: request.ID, TaskID: request.TaskID, Certificate: pair.Certificate, PrivateKey: pair.PrivateKey}
	if err = wsjson.Write(ctx, connection, core.WireMessage{Type: core.WireManagedCertificateReport, ManagedCertificateReport: &report}); err != nil {
		t.Fatal("cannot send certificate report")
	}
	var list struct{ Items []core.Certificate }
	for attempt := 0; attempt < 100; attempt++ {
		alice.call("GET", "/certificates", nil, 200, &list)
		if len(list.Items) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(list.Items) != 1 || list.Items[0].Source != "node" || list.Items[0].Version != 1 {
		t.Fatal("authenticated report was not imported")
	}
	bob.call("GET", "/certificates/"+list.Items[0].ID, nil, 404, nil)
}

func deployNativeCertificate(t *testing.T, ctx context.Context, db *store.Store, node core.Agent, client configScopeAPIClient, engine core.Engine, certpath, keypath string, port int) core.Task {
	t.Helper()
	base := "/agents/" + node.ID + "/configs/" + string(engine)
	var plan map[string]any
	client.call("POST", base+"/plans", map[string]string{"protocol": "trojan"}, 201, &plan)
	plan["tls_server_name"], plan["tls_mode"], plan["tls_enabled"] = "edge.example.test", "native", true
	plan["certificate_path"], plan["private_key_path"], plan["port"] = certpath, keypath, port
	client.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}, 200, nil)
	task, err := db.ClaimTask(ctx, node.ID)
	if err != nil || task == nil {
		t.Fatal("native certificate deployment did not produce a task")
	}
	result := core.TaskResultRequest{LeaseID: task.LeaseID, Success: true, TrafficSettled: true}
	if task.SharedInstance {
		result.InstanceStatus = &core.SharedInstanceStatus{Engine: engine, ShareID: task.SharedTrafficID, ConfigID: task.ConfigID, ConfigVersion: task.ConfigVersion, TaskID: task.ID, AppliedTaskID: task.ID, Status: "active", ObservedAt: time.Now()}
	}
	if err = db.CompleteTask(ctx, node.ID, task.ID, result); err != nil {
		t.Fatal(err)
	}
	return *task
}

func TestNodeCertificateExplicitEnrollmentAndVersions(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		t.Run(string(engine), func(t *testing.T) {
			db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
			node := enrollNativeCertificateNode(t, ctx, db, alice)
			deployNativeCertificate(t, ctx, db, node, alice, engine, "/etc/tls/native.pem", "/etc/tls/native.key", 24443)
			ownerCtx := store.WithConfigScope(ctx, alice.userID, false)
			before, err := db.AgentConfig(ownerCtx, node.ID, engine)
			if err != nil {
				t.Fatal(err)
			}
			var sources []core.ManagedCertificateSource
			raw := alice.call("GET", "/certificate-managed-sources", nil, 200, &sources)
			if len(sources) != 1 || sources[0].Source != "node" || sources[0].Status != "available" || strings.Contains(string(raw), "private_key_path") {
				t.Fatal("native source metadata missing or includes file requests")
			}
			src := sources[0]
			requests, err := db.AgentManagedCertificateSources(ctx, node.ID)
			if err != nil || len(requests) != 0 {
				t.Fatal("private material requested without consent")
			}
			pair, err := certificates.SelfSigned([]string{"edge.example.test"}, "ecdsa256", 90)
			if err != nil {
				t.Fatal(err)
			}
			report := core.ManagedCertificateReport{SourceID: src.ID, TaskID: src.TaskID, Certificate: pair.Certificate, PrivateKey: pair.PrivateKey}
			if db.ImportManagedCertificate(ctx, node.ID, report) == nil {
				t.Fatal("unsolicited report accepted")
			}
			bob.call("PUT", "/certificate-managed-sources/"+src.ID, map[string]string{"action": "enroll"}, 404, nil)
			admin.call("PUT", "/certificate-managed-sources/"+src.ID, map[string]string{"action": "enroll"}, 404, nil)
			alice.call("PUT", "/certificate-managed-sources/"+src.ID, map[string]string{"action": "resume"}, 400, nil)
			alice.call("PUT", "/certificate-managed-sources/"+src.ID, map[string]string{"action": "enroll", "certificate_path": "/etc/foreign/key"}, 400, nil)
			alice.call("PUT", "/certificate-managed-sources/"+src.ID, map[string]string{"action": "enroll"}, 204, nil)
			requests, err = db.AgentManagedCertificateSources(ctx, node.ID)
			if err != nil || len(requests) != 1 || requests[0].Reference == nil {
				t.Fatal("authorized source not requested")
			}
			if db.ImportManagedCertificate(ctx, "agt_foreign", report) == nil {
				t.Fatal("foreign agent report accepted")
			}
			if err = db.ImportManagedCertificate(ctx, node.ID, report); err != nil {
				t.Fatal(err)
			}
			var list struct{ Items []core.Certificate }
			alice.call("GET", "/certificates", nil, 200, &list)
			if len(list.Items) != 1 {
				t.Fatal("certificate was not enrolled")
			}
			cert := list.Items[0]
			if cert.Source != "node" || cert.RenewalManager != "external" || cert.AutoRenew || cert.Version != 1 {
				t.Fatal("incorrect native certificate ownership or renewal metadata")
			}
			check := func(version int, status string) {
				t.Helper()
				var detail struct {
					Certificate core.Certificate
					Versions    []core.Certificate
				}
				raw := alice.call("GET", "/certificates/"+cert.ID, nil, 200, &detail)
				if detail.Certificate.Version != version || detail.Certificate.SyncStatus != status || len(detail.Versions) != version || strings.Contains(string(raw), "PRIVATE KEY") {
					t.Fatal("version/status/secret boundary failed")
				}
			}
			check(1, "synced")
			newer, _ := certificates.SelfSigned([]string{"edge.example.test"}, "ecdsa256", 91)
			invalid := report
			invalid.PrivateKey = newer.PrivateKey
			if err = db.ImportManagedCertificate(ctx, node.ID, invalid); err != nil {
				t.Fatal(err)
			}
			check(1, "blocked")
			foreign, _ := certificates.SelfSigned([]string{"foreign.example.test"}, "ecdsa256", 92)
			invalid.Certificate, invalid.PrivateKey = foreign.Certificate, foreign.PrivateKey
			if err = db.ImportManagedCertificate(ctx, node.ID, invalid); err != nil {
				t.Fatal(err)
			}
			check(1, "blocked")
			block, _ := pem.Decode([]byte(pair.Certificate))
			leaf, _ := x509.ParseCertificate(block.Bytes)
			key, _ := certificates.ParseKey(pair.PrivateKey)
			leaf.NotBefore = time.Now().Add(-48 * time.Hour)
			leaf.NotAfter = time.Now().Add(-time.Hour)
			der, err := x509.CreateCertificate(rand.Reader, leaf, leaf, key.Public(), key)
			if err != nil {
				t.Fatal(err)
			}
			invalid = report
			invalid.Certificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
			if err = db.ImportManagedCertificate(ctx, node.ID, invalid); err != nil {
				t.Fatal(err)
			}
			check(1, "blocked")
			invalid = report
			invalid.TaskID = "old-task"
			if err = db.ImportManagedCertificate(ctx, node.ID, invalid); err != nil {
				t.Fatal(err)
			}
			check(1, "blocked")
			invalid = report
			invalid.Error = "PRIVATE KEY must never appear in diagnostics"
			if err = db.ImportManagedCertificate(ctx, node.ID, invalid); err != nil {
				t.Fatal(err)
			}
			check(1, "blocked")
			next := report
			next.Certificate, next.PrivateKey = newer.Certificate, newer.PrivateKey
			if err = db.ImportManagedCertificate(ctx, node.ID, next); err != nil {
				t.Fatal(err)
			}
			check(2, "synced")
			if err = db.ImportManagedCertificate(ctx, node.ID, report); err != nil {
				t.Fatal(err)
			}
			check(2, "blocked")
			bob.call("GET", "/certificates/"+cert.ID, nil, 404, nil)
			alice.call("POST", "/certificates/"+cert.ID+"/grants", map[string]string{"username": "bob"}, 204, nil)
			raw = bob.call("GET", "/certificates/"+cert.ID, nil, 200, nil)
			for _, secret := range []string{"PRIVATE KEY", src.ID, src.ConfigID, node.ID} {
				if strings.Contains(string(raw), secret) {
					t.Fatal("shared certificate exposed owner metadata")
				}
			}
			bob.call("PUT", "/certificate-managed-sources/"+src.ID, map[string]string{"action": "pause"}, 404, nil)
			alice.call("PUT", "/certificate-managed-sources/"+src.ID, map[string]string{"action": "pause"}, 204, nil)
			if db.ImportManagedCertificate(ctx, node.ID, next) == nil {
				t.Fatal("paused source updated")
			}
			features := []string{core.AgentFeatureCertificates, core.AgentFeatureCaddyCertificates}
			if err = db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: features}); err != nil {
				t.Fatal(err)
			}
			alice.call("PUT", "/certificate-managed-sources/"+src.ID, map[string]string{"action": "resume"}, 409, nil)
			if err = db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: node.Features}); err != nil {
				t.Fatal(err)
			}
			alice.call("PUT", "/certificate-managed-sources/"+src.ID, map[string]string{"action": "resume"}, 204, nil)
			after, err := db.AgentConfig(ownerCtx, node.ID, engine)
			if err != nil || before.Version != after.Version || before.Content != after.Content {
				t.Fatal("enrollment changed existing TLS configuration")
			}
			alice.call("DELETE", "/certificates/"+cert.ID, nil, 204, nil)
			if db.ImportManagedCertificate(ctx, node.ID, next) == nil {
				t.Fatal("deleted source recreated certificate")
			}
		})
	}
}

func TestNodeCertificateSharedInstanceIsolationAndRevocation(t *testing.T) {
	for _, engine := range []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox} {
		t.Run(string(engine), func(t *testing.T) {
			db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
			node := enrollNativeCertificateNode(t, ctx, db, alice)
			grantConfigScopeAPIAgent(t, ctx, db, node, bob, 24444)
			var sharing core.AgentSharing
			alice.call("GET", "/agents/"+node.ID+"/sharing", nil, 200, &sharing)
			if len(sharing.Shares) != 1 {
				t.Fatal("missing shared fixture")
			}
			share := sharing.Shares[0].ID
			root := "/etc/qagent/" + string(engine) + "/shares/" + share + "/"
			deployNativeCertificate(t, ctx, db, node, alice, engine, "/etc/tls/owner.pem", "/etc/tls/owner.key", 24443)
			deployNativeCertificate(t, ctx, db, node, bob, engine, root+"server.pem", root+"server.key", 24444)
			var ownerSources, sharedSources []core.ManagedCertificateSource
			alice.call("GET", "/certificate-managed-sources", nil, 200, &ownerSources)
			bob.call("GET", "/certificate-managed-sources", nil, 200, &sharedSources)
			if len(ownerSources) != 1 || len(sharedSources) != 1 || sharedSources[0].ShareID != share || ownerSources[0].ID == sharedSources[0].ID {
				t.Fatal("shared source ownership escaped")
			}
			src := sharedSources[0]
			alice.call("PUT", "/certificate-managed-sources/"+src.ID, map[string]string{"action": "enroll"}, 404, nil)
			admin.call("PUT", "/certificate-managed-sources/"+src.ID, map[string]string{"action": "enroll"}, 404, nil)
			bob.call("PUT", "/certificate-managed-sources/"+src.ID, map[string]string{"action": "enroll"}, 204, nil)
			pair, _ := certificates.SelfSigned([]string{"edge.example.test"}, "ecdsa256", 90)
			report := core.ManagedCertificateReport{SourceID: src.ID, TaskID: src.TaskID, Certificate: pair.Certificate, PrivateKey: pair.PrivateKey}
			if err := db.ImportManagedCertificate(ctx, node.ID, report); err != nil {
				t.Fatal(err)
			}
			var list struct{ Items []core.Certificate }
			bob.call("GET", "/certificates", nil, 200, &list)
			if len(list.Items) != 1 || list.Items[0].OwnerID != bob.userID {
				t.Fatal("node share changed certificate ownership")
			}
			cert := list.Items[0]
			alice.call("GET", "/certificates/"+cert.ID, nil, 404, nil)
			access, err := db.UserAgentAccess(ctx, bob.userID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.SetUserAgentAccess(ctx, bob.userID, core.AgentAccessRequest{Isolated: true, Revision: access.Revision}); err != nil {
				t.Fatal(err)
			}
			bob.call("GET", "/certificates", nil, 200, &list)
			if len(list.Items) != 1 || list.Items[0].SyncStatus != "unverified" {
				t.Fatal("share revocation status depends on an Agent report")
			}
			if db.ImportManagedCertificate(ctx, node.ID, report) == nil {
				t.Fatal("revoked share updated certificate")
			}
			bob.call("GET", "/certificates", nil, 200, &list)
			if len(list.Items) != 1 || list.Items[0].Version != 1 || list.Items[0].SyncStatus != "unverified" {
				t.Fatal("revocation lost valid retained version or source status")
			}
			bob.call("PUT", "/certificate-managed-sources/"+src.ID, map[string]string{"action": "resume"}, 403, nil)

		})
	}
}

func TestNodeCertificateRejectsUnrelatedSharedFiles(t *testing.T) {
	for _, root := range []string{"/etc/tls/foreign/", "/etc/qagent/sing-box/shares/shr_0123456789abcdef/"} {
		t.Run(root, func(t *testing.T) {
			db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
			node := enrollNativeCertificateNode(t, ctx, db, alice)
			grantConfigScopeAPIAgent(t, ctx, db, node, bob, 24444)
			deployNativeCertificate(t, ctx, db, node, bob, core.EngineSingBox, root+"cert.pem", root+"key.pem", 24444)
			var sources []core.ManagedCertificateSource
			bob.call("GET", "/certificate-managed-sources", nil, 200, &sources)
			if len(sources) != 0 {
				t.Fatal("shared configuration conferred export rights to unrelated files")
			}
			requests, err := db.AgentManagedCertificateSources(ctx, node.ID)
			if err != nil || len(requests) != 0 {
				t.Fatal("unrelated shared files requested from Agent")
			}
		})
	}
}
