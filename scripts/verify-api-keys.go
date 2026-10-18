//go:build ignore

// Local HTTP verification against the task-owned qch_api_keys database only.
// Seeded Agent acknowledgements simulate a managed node; no core is launched.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dbURL := os.Getenv("QCH_TEST_DATABASE_URL")
	parsed, err := url.Parse(dbURL)
	must(err)
	if parsed.Hostname() != "127.0.0.1" || parsed.Path != "/qch_api_keys" {
		panic("verification requires the dedicated local qch_api_keys database")
	}
	out := os.Getenv("QCH_VERIFY_OUTPUT")
	if out == "" {
		out = ".preview/admin-api-keys"
	}
	must(os.MkdirAll(out, 0700))
	adminBytes, err := os.ReadFile(os.Getenv("QCH_VERIFY_ADMIN_TOKEN_FILE"))
	must(err)
	admin := string(adminBytes)
	db, err := store.Open(ctx, dbURL, true)
	must(err)
	defer db.Close()
	scope := store.WithConfigScope(ctx, "", true)
	enroll, err := db.CreateEnrollmentToken(scope, core.EnrollmentTokenRequest{Name: "PR304 diagnostic fixture"})
	must(err)
	public, err := core.NewToken()
	must(err)
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "PR304 local fixture", OS: "linux", Arch: "amd64", PublicKey: public, Capabilities: []core.Engine{core.EngineMihomo}, Features: []string{core.AgentFeatureCoreLogs, core.AgentFeatureCoreLogStatus, core.AgentFeatureManagedConfigRead, core.AgentFeatureIndependentEgress}}, enroll.Token)
	must(err)
	must(db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: agent.Features, Runtime: map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true, Version: "fixture-version"}}}))
	protocol, _ := serverconfig.FindProtocol(core.EngineMihomo, serverconfig.ProtocolSS2022)
	plan, err := serverconfig.NewPlan(protocol)
	must(err)
	plan.Port = 21184
	plan.Tag = "diagnostic-entry"
	content, err := serverconfig.Generate(core.EngineMihomo, plan)
	must(err)
	content += "\nsecret: PR304_CONFIG_CREDENTIAL\n"
	cfg, err := db.SaveAgentConfig(scope, core.Config{AgentID: agent.ID, Name: "PR304 fixture config", Engine: core.EngineMihomo, Content: content}, 0)
	must(err)
	task, err := db.CreateTask(scope, core.TaskRequest{AgentID: agent.ID, Engine: core.EngineMihomo, Action: core.ActionValidate, ConfigID: cfg.ID})
	must(err)
	claimed, err := db.ClaimTask(ctx, agent.ID)
	must(err)
	must(db.CompleteTask(ctx, agent.ID, task.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: false, Output: "parse configuration failed at line 12 password=PR304_TASK_CREDENTIAL", Error: "test fixture: invalid upstream port 443"}))
	batchID, err := core.NewID("log")
	must(err)
	must(db.StoreCoreLogs(ctx, agent.ID, core.CoreLogBatch{ID: batchID, Entries: []core.CoreLogEntry{{Engine: core.EngineMihomo, InstanceID: "main", Service: core.CoreLogService(core.EngineMihomo, "main", "systemd"), Level: "error", Message: "fixture failure line 12 token=PR304_LOG_CREDENTIAL"}}}))
	base := os.Getenv("QCH_API_BASE")
	if base == "" {
		base = "http://127.0.0.1:38584/api/v1"
	}
	client := http.Client{Timeout: 15 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	report := []map[string]any{}
	call := func(secret, method, path string, input any, want int) []byte {
		body, err := json.Marshal(input)
		must(err)
		r, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
		must(err)
		r.Header.Set("Authorization", "Bearer "+secret)
		r.Header.Set("Content-Type", "application/json")
		res, err := client.Do(r)
		must(err)
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		must(err)
		report = append(report, map[string]any{"method": method, "path": path, "status": res.StatusCode, "bytes": len(data), "request_id": res.Header.Get("X-Request-ID")})
		if res.StatusCode != want {
			panic(fmt.Sprintf("%s %s got %d want %d", method, path, res.StatusCode, want))
		}
		if method == "GET" && (strings.Contains(string(data), secret) || strings.Contains(string(data), "PR304_CONFIG_CREDENTIAL") || strings.Contains(string(data), "PR304_TASK_CREDENTIAL") || strings.Contains(string(data), "PR304_LOG_CREDENTIAL")) {
			panic("diagnostic credential redaction failed")
		}
		return data
	}
	input := core.APIKeyInput{Name: "AI HTTP 验证", Permissions: core.DiagnosticPermissions(), IPAllowlist: []string{"127.0.0.1/32"}}
	var created struct {
		Key    core.APIKey `json:"key"`
		Secret string      `json:"secret"`
	}
	must(json.Unmarshal(call(admin, "POST", "/settings/api-keys", input, 201), &created))
	key := created.Key
	secret := created.Secret
	created.Secret = ""
	for _, path := range []string{"/api-key", "/settings/deployment", "/agents", "/agents/" + agent.ID + "/configs/mihomo/state", "/agents/" + agent.ID + "/configs/mihomo", "/tasks?agent_id=" + agent.ID + "&status=failed&limit=10", "/tasks/" + task.ID, "/core-log-sources", "/core-logs?agent_id=" + agent.ID + "&instance_id=main&engine=mihomo&limit=10", "/audit?limit=10"} {
		call(secret, "GET", path, nil, 200)
	}
	call(secret, "POST", "/tasks", map[string]string{}, 403)
	call(secret, "GET", "/settings/api-keys", nil, 403)
	input.Revision = key.Revision
	input.IPAllowlist = []string{"192.0.2.0/24"}
	must(json.Unmarshal(call(admin, "PUT", "/settings/api-keys/"+key.ID, input, 200), &key))
	call(secret, "GET", "/api-key", nil, 403)
	input.Revision = key.Revision
	input.IPAllowlist = []string{"127.0.0.1"}
	must(json.Unmarshal(call(admin, "PUT", "/settings/api-keys/"+key.ID, input, 200), &key))
	call(secret, "GET", "/api-key", nil, 200)
	input.Revision = key.Revision
	input.Disabled = true
	must(json.Unmarshal(call(admin, "PUT", "/settings/api-keys/"+key.ID, input, 200), &key))
	call(secret, "GET", "/api-key", nil, 401)
	input.Revision = key.Revision
	input.Disabled = false
	must(json.Unmarshal(call(admin, "PUT", "/settings/api-keys/"+key.ID, input, 200), &key))
	call(secret, "GET", "/api-key", nil, 200)
	command := exec.CommandContext(ctx, "sh", "docs/admin-api-keys/diagnose.sh", agent.ID, "mihomo")
	command.Env = append(os.Environ(), "QCH_API_BASE="+base, "QCH_API_KEY="+secret)
	diagnostic, err := command.Output()
	must(err)
	examplePaths := []string{}
	for _, line := range bytes.Split(bytes.TrimSpace(diagnostic), []byte("\n")) {
		var item struct {
			Path string `json:"path"`
		}
		must(json.Unmarshal(line, &item))
		examplePaths = append(examplePaths, item.Path)
	}
	for _, path := range []string{"/settings/deployment", "/agents", "/deployments", "/agents/" + agent.ID + "/configs/mihomo/state", "/agents/" + agent.ID + "/configs/mihomo", "/tasks", "/tasks/" + task.ID, "/core-log-sources", "/core-logs", "/audit"} {
		found := false
		for _, actual := range examplePaths {
			if actual == path {
				found = true
			}
		}
		if !found {
			panic("documented diagnostic flow omitted " + path)
		}
	}
	if strings.Contains(string(diagnostic), secret) || strings.Contains(string(diagnostic), "PR304_CONFIG_CREDENTIAL") || strings.Contains(string(diagnostic), "PR304_TASK_CREDENTIAL") || strings.Contains(string(diagnostic), "PR304_LOG_CREDENTIAL") {
		panic("documented example leaked a credential")
	}
	must(os.WriteFile(filepath.Join(out, "diagnostic.jsonl"), diagnostic, 0600))
	must(os.WriteFile(filepath.Join(out, "restart-key"), []byte(secret), 0600))
	metadata, _ := json.MarshalIndent(map[string]any{"agent_id": agent.ID, "config_id": cfg.ID, "task_id": task.ID, "key_id": key.ID, "requests": report, "example": "docs/admin-api-keys/diagnose.sh", "example_exit": 0, "example_paths": examplePaths, "agent_mode": "simulated acknowledgement; actual HTTP API requests", "completed_at": time.Now().UTC()}, "", "  ")
	must(os.WriteFile(filepath.Join(out, "http-verification.json"), metadata, 0600))
	fmt.Println(string(metadata))
}
