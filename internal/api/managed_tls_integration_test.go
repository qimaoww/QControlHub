package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestManagedTLSPlanSaveAndEditedRetryAPI(t *testing.T) {
	db, ctx, admin, _, bob := newConfigScopeAPIFixture(t)
	enrollment, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "managed TLS API"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "managed TLS", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineSingBox}, Features: []string{core.AgentFeatureIndependentEgress, core.AgentFeatureManagedTLS}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, enrollment.Token)
	if err != nil {
		t.Fatal(err)
	}
	base := "/agents/" + agent.ID + "/configs/sing-box"
	var plan serverconfig.Input
	admin.call("POST", base+"/plans", map[string]any{"protocol": "vless-ws-tls"}, http.StatusCreated, &plan)
	if plan.TLSMode != "caddy" || plan.TLSPublicPort != 443 || plan.Listen != "127.0.0.1" || plan.CertificatePath != "" {
		t.Fatalf("invalid public defaults: %+v", plan)
	}
	plan.TLSMode = "nginx"
	plan.TLSPublicPort = 9443
	plan.TLSServerName = "tls.example.test"
	plan.TLSALPN = "http/1.1"
	plan.TransportHost = "host.example.test"
	var saved configMutationResult
	response := admin.call("POST", base+"/server-inbounds", map[string]any{"operation": "add", "expected_version": 0, "intent": "deploy", "input": plan}, http.StatusOK, &saved)
	if strings.Contains(string(response), "PRIVATE KEY") || len(saved.Task.TLSFrontends) > 0 {
		t.Fatal("private frontend material leaked through save API")
	}
	var workspace struct {
		Inbounds []serverconfig.Input `json:"inbounds"`
	}
	response = admin.call("GET", base+"/workspace", nil, http.StatusOK, &workspace)
	if len(workspace.Inbounds) != 1 {
		t.Fatal("workspace")
	}
	restored := workspace.Inbounds[0]
	if restored.Protocol != plan.Protocol || restored.TLSMode != "nginx" || restored.TLSPublicPort != 9443 || restored.TLSALPN != "http/1.1" || restored.TLSClientCertificate == "" || restored.TLSServerName != plan.TLSServerName || restored.TransportHost != plan.TransportHost {
		t.Fatalf("lost managed frontend parameters: %+v", restored)
	}
	if strings.Contains(string(response), "PRIVATE KEY") {
		t.Fatal("signing or leaf key leaked through workspace")
	}
	claimed, err := db.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil {
		t.Fatal(err)
	}
	if err = db.CompleteTask(ctx, agent.ID, claimed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: false, Error: "fixture deployment error"}); err != nil {
		t.Fatal(err)
	}
	var editor struct {
		Task   core.Task   `json:"task"`
		Config core.Config `json:"config"`
	}
	admin.call("GET", "/tasks/"+saved.Task.ID+"/retry-editor", nil, http.StatusOK, &editor)
	if editor.Config.ID != saved.Config.ID || editor.Config.Version != 1 || editor.Task.Error == "" {
		t.Fatal("retry editor opened incorrect configuration")
	}
	bob.call("GET", "/tasks/"+saved.Task.ID+"/retry-editor", nil, http.StatusNotFound, nil)
	admin.call("POST", "/tasks/"+saved.Task.ID+"/edit-retry", map[string]any{"content": "{invalid", "expected_version": 1}, http.StatusBadRequest, nil)
	var source map[string]any
	_ = json.Unmarshal([]byte(saved.Config.Content), &source)
	source["log"] = map[string]any{"level": "warn"}
	edited, _ := json.Marshal(source)
	var retried configMutationResult
	admin.call("POST", "/tasks/"+saved.Task.ID+"/edit-retry", map[string]any{"content": string(edited), "expected_version": 1}, http.StatusCreated, &retried)
	if retried.Task.ConfigID != saved.Config.ID || retried.Task.ConfigVersion != 2 || retried.Task.Action != core.ActionDeploy || retried.Config.Version != 2 {
		t.Fatal("retry did not save and bind the edited revision")
	}
	admin.call("POST", "/tasks/"+saved.Task.ID+"/edit-retry", map[string]any{"content": string(edited), "expected_version": 1}, http.StatusConflict, nil)
}
