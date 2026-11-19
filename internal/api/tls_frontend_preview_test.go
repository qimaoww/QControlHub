package api

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestTLSFrontendPortPreview(t *testing.T) {
	db, ctx, admin, owner, recipient := newConfigScopeAPIFixtureWithin(t, 3*time.Minute)
	liveRunner := os.Getenv("QCH_TEST_TLS_LEASE_RUNNER")
	engines := []core.Engine{core.EngineXray, core.EngineMihomo, core.EngineSingBox}
	node, _ := ownedConfigScopeAPIAgent(t, ctx, db, owner, "isolated-preview", engines...)
	features := append(node.Features, core.AgentFeatureTLSMigration, core.AgentFeatureManagedTLS, core.AgentFeatureCertificates, core.AgentFeatureManagedTLSCertificates, core.AgentFeatureXHTTP, core.AgentFeatureXHTTPTLS, core.AgentFeatureNginxACME)
	runtime := map[core.Engine]core.RuntimeState{}
	for _, e := range engines {
		runtime[e] = core.RuntimeState{Installed: true, ServiceStatus: "active"}
	}
	if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: features, Runtime: runtime}); err != nil {
		t.Fatal(err)
	}
	permissions := []core.Permission{core.PermissionAgentConfigRead, core.PermissionAgentConfigWrite, core.PermissionTasksExecute, core.PermissionTasksRead}
	key, _ := testCreateBusinessKey(t, owner, permissions)
	const domain = "edge.example.test"
	var cert core.Certificate
	owner.call("POST", "/certificates", map[string]any{"name": "preview", "source": "self-signed", "domains": []string{domain}, "key_type": "ecdsa256", "days": 30}, 201, &cert)
	var path string
	bases := map[core.Engine]string{}
	secrets := []string{}
	for i, e := range engines {
		base := "/agents/" + node.ID + "/configs/" + string(e)
		bases[e] = base
		protocol := serverconfig.ProtocolXHTTPTLS
		if e == core.EngineSingBox {
			protocol = "vless-ws-tls"
		}
		var p serverconfig.Input
		owner.call("POST", base+"/plans", map[string]string{"protocol": protocol}, 201, &p)
		p.Port = 32001 + i
		p.TransportPath = fmt.Sprintf("/route%d", i)
		p.TLSMode = "nginx"
		p.TLSServerName = domain
		p.PanelCertificateID = cert.ID
		p.BlockMainlandDestination = false
		secrets = append(secrets, p.Credential)
		body := map[string]any{"operation": "add", "input": p, "intent": "deploy", "expected_instance_id": "", "expected_version": 0}
		var saved configMutationResult
		owner.call("POST", base+"/server-inbounds", body, 200, &saved)
		claimed, err := db.ClaimTask(ctx, node.ID)
		if err != nil || claimed == nil || claimed.ID != saved.Task.ID {
			t.Fatal("missing deployment lease", err)
		}
		if liveRunner != "" {
			isolatedTLSLease(t, ctx, liveRunner, claimed)
		}
		if err = db.CompleteTask(ctx, node.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			path = base + "/server-inbounds/" + url.PathEscape(p.Tag) + "/tls-frontend/preview"
		}
	}
	snapshot := func() []byte {
		var out []byte
		for _, e := range engines {
			out = append(out, key.call("GET", bases[e], nil, 200, nil)...)
		}
		for _, path := range []string{"/tasks?agent_id=" + node.ID, "/deployments"} {
			out = append(out, owner.call("GET", path, nil, 200, nil)...)
		}
		return out
	}
	before := snapshot()
	body := map[string]any{"target_mode": "caddy", "expected_instance_id": "", "expected_version": 1}
	var preview store.TLSFrontendPreview
	first := key.call("POST", path, body, 200, &preview)
	second := key.call("POST", path, body, 200, nil)
	if len(preview.Members) != 3 || preview.PublicPort != 443 || preview.PlanID == "" || len(preview.Blockers) != 0 || preview.Status != "runtime_evidence_required" {
		t.Fatalf("members=%d blockers=%v status=%s", len(preview.Members), preview.Blockers, preview.Status)
	}
	if !bytes.Equal(first, second) || !bytes.Equal(before, snapshot()) {
		t.Fatal("preview changed persistent state")
	}
	for _, v := range secrets {
		if bytes.Contains(first, []byte(v)) {
			t.Fatal("preview returned a connection credential")
		}
	}
	for _, member := range preview.Members {
		if member.From != "nginx" || member.To != "caddy" || member.Version != member.DeployedVersion || member.CertificateVersion != 1 {
			t.Fatal("invalid mode or revision projection")
		}
	}
	body["expected_instance_id"] = nil
	key.call("POST", path, body, 400, nil)
	delete(body, "expected_instance_id")
	key.call("POST", path, body, 400, nil)
	body["expected_instance_id"] = "shr_0000000000000000"
	key.call("POST", path, body, 409, nil)
	body["expected_instance_id"] = ""
	body["expected_version"] = 2
	key.call("POST", path, body, 409, nil)
	grantConfigScopeAPIAgent(t, ctx, db, node, recipient, 42001)
	grantee, _ := testCreateBusinessKey(t, recipient, permissions)
	body["expected_version"] = 1
	raw := grantee.call("POST", path, body, 409, nil)
	if bytes.Contains(raw, []byte(domain)) {
		t.Fatal("cross-instance information disclosure")
	}
	restricted, _ := testCreateBusinessKey(t, admin, []core.Permission{core.PermissionTasksRead})
	restricted.call("POST", path, body, 403, nil)
	if !bytes.Equal(before, snapshot()) {
		t.Fatal("rejected preview changed state")
	}
	execute := strings.TrimSuffix(path, "preview") + "migrate"
	body["plan_id"] = preview.PlanID
	body["task_id"] = "tsk_0123456789abcdef"
	var op store.TLSMigrationOperation
	key.call("POST", execute, body, 202, &op)
	if op.TaskID != body["task_id"] || op.State != "pending" {
		t.Fatal("no pending execution")
	}
	key.call("POST", execute, body, 202, nil)
	submitted, err := db.ClaimTask(ctx, node.ID)
	if err != nil || submitted == nil || submitted.TLSMigration == nil || len(submitted.TLSMigration.Routes) != 3 {
		t.Fatal("group not leased", err)
	}
	result := core.TLSMigrationResult{State: "succeeded", Step: "complete"}
	for range submitted.TLSMigration.Routes {
		result.Paths = append(result.Paths, core.TLSRouteVerification{Candidate: "succeeded", Recovery: "unknown"})
	}
	if liveRunner != "" {
		result = *isolatedTLSLease(t, ctx, liveRunner, submitted)
		if result.State != "succeeded" {
			t.Fatal("runtime verification", result)
		}
	}
	if err = db.CompleteTask(ctx, node.ID, op.TaskID, core.TaskResultRequest{Success: true, LeaseID: submitted.LeaseID, TLSMigration: &result}); err != nil {
		t.Fatal(err)
	}
	status := "/agents/" + node.ID + "/tls-migrations/" + op.TaskID
	key.call("GET", status, nil, 200, &op)
	if op.State != "succeeded" || len(op.Result.Paths) != 3 {
		t.Fatal("missing group receipt")
	}
	grantee.call("GET", status, nil, 404, nil)
	for i, e := range engines {
		var cfg core.Config
		owner.call("GET", bases[e], nil, 200, &cfg)
		if cfg.Version != 2 || !strings.Contains(cfg.Content, secrets[i]) {
			t.Fatal("connection credential or ownership changed")
		}
	}
	delete(body, "plan_id")
	delete(body, "task_id")
	body["expected_version"] = 2
	key.call("POST", path, body, 200, &preview)
	if len(preview.Blockers) != 0 || preview.ConnectionInterruptionPossible {
		t.Fatal("unchanged plan", preview.Blockers)
	}
	body["plan_id"] = preview.PlanID
	body["task_id"] = "tsk_0123456789abcdee"
	key.call("POST", execute, body, 200, &op)
	if op.State != "unchanged" {
		t.Fatal("unchanged frontend queued another deployment")
	}
}
