package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"slices"
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
	key, auth := testCreateBusinessKey(t, owner, permissions)
	var skill func(string, string, any, bool) map[string]any
	if liveRunner != "" {
		skill = releaseTLSClient(t, ctx, key, auth.Secret)
		diagnostic := skill("GET", "/api-key", nil, false)
		if perms, _ := json.Marshal(diagnostic["permissions"]); bytes.Contains(perms, []byte("secrets.read")) {
			t.Fatal("key has secret access")
		}
		skill("GET", "/openapi.json", nil, false)
	}
	const domain = "edge.example.test"
	var cert core.Certificate
	owner.call("POST", "/certificates", map[string]any{"name": "preview", "source": "self-signed", "domains": []string{domain}, "key_type": "ecdsa256", "days": 30}, 201, &cert)
	var path string
	bases := map[core.Engine]string{}
	secrets := []string{}
	var otherCredentials []string
	var firstPreset serverconfig.Input
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
		version := 0
		if e == core.EngineSingBox {
			// Two existing credentialed inbounds share this configuration with
			// the WebSocket route, but keep their own ports and credentials.
			for n := range 2 {
				var other serverconfig.Input
				owner.call("POST", base+"/plans", map[string]string{"protocol": "vmess-tcp"}, 201, &other)
				other.Port = 32101 + n
				other.Tag = fmt.Sprintf("other-%d", n)
				otherCredentials = append(otherCredentials, other.Credential)
				var stored configMutationResult
				owner.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "intent": "validate", "input": other, "expected_version": version}, 200, &stored)
				if stored.Config.Version != version+1 || stored.Task.Action != core.ActionValidate {
					t.Fatal("validate did not save and queue")
				}
				lease, err := db.ClaimTask(ctx, node.ID)
				if err != nil || lease == nil {
					t.Fatal("no validation lease")
				}
				if err = db.CompleteTask(ctx, node.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, Success: true}); err != nil {
					t.Fatal(err)
				}
				version++
			}
		}
		if i == 0 {
			firstPreset = p
			firstPreset.Credential = "[REDACTED]"
		}
		body := map[string]any{"operation": "add", "input": p, "intent": "deploy", "expected_instance_id": "", "expected_version": version}
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
	// Use a separate operation to exercise an acknowledged, incomplete
	// rollback followed by a later receipt on the same authenticated node.
	retry := "tsk_0909090909090909"
	body["task_id"] = retry
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	submit := httptest.NewRequest(http.MethodPost, "/api/v1"+execute, bytes.NewReader(payload))
	submit.Header.Set("Authorization", "Bearer "+auth.Secret)
	submit.Header.Set("Content-Type", "application/json")
	accepted := httptest.NewRecorder()
	key.handler.ServeHTTP(accepted, submit)
	if accepted.Code != 202 || accepted.Header().Get("Location") != "/api/v1/agents/"+node.ID+"/tls-migrations/"+retry {
		t.Fatal("missing operation lookup on acceptance")
	}
	partialLease, err := db.ClaimTask(ctx, node.ID)
	if err != nil || partialLease == nil || partialLease.ID != retry {
		t.Fatal("missing recovery test lease")
	}
	partial := core.TLSMigrationResult{State: "recovery_incomplete", Step: "restore", Code: "rollback_failed"}
	for range preview.Members {
		partial.Paths = append(partial.Paths, core.TLSRouteVerification{Candidate: "failed", Recovery: "unknown"})
	}
	if err = db.CompleteTask(ctx, node.ID, retry, core.TaskResultRequest{LeaseID: partialLease.LeaseID, TLSMigration: &partial}); err != nil {
		t.Fatal(err)
	}
	recoveryPath := "/agents/" + node.ID + "/tls-migrations/" + retry
	key.call("GET", recoveryPath, nil, 200, &op)
	ids, err := db.PendingTLSRecoveries(ctx, node.ID)
	if err != nil || len(ids) != 1 || ids[0] != retry || op.State != "recovery_incomplete" {
		t.Fatal("missing outstanding recovery")
	}
	partial.State = "rolled_back"
	for i := range partial.Paths {
		partial.Paths[i].Recovery = "succeeded"
	}
	if err = db.ConfirmTLSRecovery(ctx, node.ID, retry, &partial); err != nil {
		t.Fatal(err)
	}
	key.call("GET", recoveryPath, nil, 200, &op)
	ids, err = db.PendingTLSRecoveries(ctx, node.ID)
	if err != nil || len(ids) != 0 || op.State != "rolled_back" {
		t.Fatal("late rollback confirmation lost")
	}
	body["task_id"] = "tsk_0123456789abcdef"
	if skill != nil {
		plan := skill("POST", path, map[string]any{"expected_instance_id": "", "expected_version": 1, "target_mode": "caddy"}, false)
		if plan["plan_id"] != preview.PlanID {
			t.Fatal("release helper failed to read preview")
		}
		if runner := os.Getenv("QCH_TEST_TLS_FAULT_RUNNER"); runner != "" {
			if err := exec.CommandContext(ctx, runner, "route").Run(); err != nil {
				t.Fatal("test fault setup failed")
			}
			body["task_id"] = "tsk_0101010101010101"
			skill("POST", execute, body, false)
			lease, err := db.ClaimTask(ctx, node.ID)
			if err != nil || lease == nil {
				t.Fatal("missing lease")
			}
			recovery := isolatedTLSLease(t, ctx, liveRunner, lease)
			if recovery.State != "rolled_back" {
				t.Fatal("failed recovery", recovery)
			}
			for i, r := range lease.TLSMigration.Routes {
				if (recovery.Paths[i].Candidate == "failed") != (r.Engine == core.EngineMihomo) || recovery.Paths[i].Recovery != "succeeded" {
					t.Fatal("incorrect recovery receipt", recovery.Paths)
				}
			}
			if err = db.CompleteTask(ctx, node.ID, lease.ID, core.TaskResultRequest{LeaseID: lease.LeaseID, TLSMigration: recovery}); err != nil {
				t.Fatal(err)
			}
			actual := skill("GET", "/agents/"+node.ID+"/tls-migrations/"+lease.ID, nil, false)
			if actual["state"] != "rolled_back" {
				t.Fatal("Skill missed recovery")
			}
			t.Log("Injected wrong backend: two candidate paths healthy, Mihomo XHTTP failed; all three original paths restored")
			if err := exec.CommandContext(ctx, runner, "clear").Run(); err != nil {
				t.Fatal("fault cleanup failed")
			}
			body["task_id"] = "tsk_0123456789abcdef"
		}
		accepted := skill("POST", execute, body, false)
		if accepted["state"] != "pending" {
			t.Fatal("release helper failed to submit")
		}
	}
	key.call("POST", execute, body, 202, &op)
	if op.TaskID != body["task_id"] || op.State != "pending" {
		t.Fatal("no pending execution")
	}
	replay := maps.Clone(body)
	replay["task_id"] = retry
	var original store.TLSMigrationOperation
	key.call("POST", execute, replay, 200, &original)
	if original.TaskID != retry || original.State != "rolled_back" {
		t.Fatal("resubmitting a rolled-back task returned a different execution")
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
		// Drop the first result. A fresh Agent process reads the same
		// operation journal on reconnect; the control plane resumes its lease.
		resumed, err := db.RunningTask(ctx, node.ID)
		if err != nil || resumed == nil || resumed.ID != submitted.ID || resumed.LeaseID != submitted.LeaseID {
			t.Fatal("lost disconnected operation")
		}
		replay := isolatedTLSLease(t, ctx, liveRunner, resumed)
		if replay.State != result.State || !slices.Equal(result.Paths, replay.Paths) {
			t.Fatal("resumed receipt differs")
		}
		t.Log("Dropped successful receipt, re-opened original lease and confirmed all three candidate paths")
		result = *replay
	}
	if err = db.CompleteTask(ctx, node.ID, op.TaskID, core.TaskResultRequest{Success: true, LeaseID: submitted.LeaseID, TLSMigration: &result}); err != nil {
		t.Fatal(err)
	}
	status := "/agents/" + node.ID + "/tls-migrations/" + op.TaskID
	key.call("GET", status, nil, 200, &op)
	if skill != nil {
		end := skill("GET", status, nil, true)
		if end["state"] != "succeeded" {
			t.Fatal("release helper lost terminal receipt")
		}
	}
	if op.State != "succeeded" || len(op.Result.Paths) != 3 {
		t.Fatal("missing group receipt")
	}
	for i, pathResult := range op.Result.Paths {
		member := op.Plan.Members[i]
		if pathResult.Candidate != "succeeded" || pathResult.ConfigID != member.ConfigID || pathResult.InstanceID != member.InstanceID || pathResult.Engine != member.Engine || pathResult.Tag != member.Tag || pathResult.Path != member.Path {
			t.Fatal("route observations were not bound to the authorized plan member")
		}
	}
	grantee.call("GET", status, nil, 404, nil)
	var summary store.TLSPortSummary
	grantee.call("GET", "/agents/"+node.ID+"/tls-ports/443", nil, 200, &summary)
	if summary.VisibleRoutes != 0 || summary.State != "partial" {
		t.Fatal("hidden occupancy reported free", summary)
	}
	key.call("GET", "/agents/"+node.ID+"/tls-ports/443", nil, 200, &summary)
	if summary.VisibleRoutes != 3 || summary.State != "visible_in_use" {
		t.Fatal("wrong port scope", summary)
	}
	for i, e := range engines {
		var cfg core.Config
		owner.call("GET", bases[e], nil, 200, &cfg)
		wantVersion := 2
		if e == core.EngineSingBox {
			wantVersion = 4
			for n, v := range otherCredentials {
				found := false
				for _, p := range serverconfig.ParseAll(e, cfg.Content) {
					if p.Tag == fmt.Sprintf("other-%d", n) && p.Credential == v {
						found = true
					}
				}
				if !found {
					t.Fatal("untouched inbound credential or tag changed")
				}
			}
		}
		if cfg.Version != wantVersion || !strings.Contains(cfg.Content, secrets[i]) {
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
	base := bases[core.EngineXray]
	for _, action := range []string{"add", "modify"} {
		input := firstPreset
		submission := map[string]any{"operation": action, "input": input, "intent": "validate", "expected_instance_id": "", "expected_version": 2}
		if action == "modify" {
			submission["original_tag"] = input.Tag
		} else {
			input.Tag = "invalid-credential"
			submission["input"] = input
		}
		key.call("POST", base+"/server-inbounds", submission, 400, nil)
	}
	var visible core.Config
	key.call("GET", base, nil, 200, &visible)
	if visible.Version != 2 || strings.Contains(visible.Content, secrets[0]) {
		t.Fatal("invalid draft after structured input rejection")
	}
	key.call("PUT", base, map[string]any{"name": visible.Name, "version": visible.Version, "content": visible.Content}, 200, nil)
	var restored core.Config
	owner.call("GET", base, nil, 200, &restored)
	if restored.Version != 3 || !strings.Contains(restored.Content, secrets[0]) {
		t.Fatal("full-content editing lost the deployed credential")
	}

}
