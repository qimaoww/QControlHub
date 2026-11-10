package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type apiKeyCreation struct {
	Key    core.APIKey `json:"key"`
	Secret string      `json:"secret"`
}

func testCreateBusinessKey(t *testing.T, creator configScopeAPIClient, permissions []core.Permission) (configScopeAPIClient, apiKeyCreation) {
	t.Helper()
	var created apiKeyCreation
	creator.call("POST", "/settings/api-keys", core.APIKeyInput{Name: "test automation", Mode: core.APIKeyReadWrite, Permissions: permissions}, 201, &created)
	return configScopeAPIClient{t: t, handler: creator.handler, token: created.Secret}, created
}

func TestAPIKeyBusinessWritesAndIsolation(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	permissions := []core.Permission{}
	for _, p := range core.APIKeyPermissions() {
		if p != core.PermissionSecretsRead {
			permissions = append(permissions, p)
		}
	}
	key, created := testCreateBusinessKey(t, admin, permissions)
	var authorization map[string]any
	key.call("GET", "/api-key", nil, 200, &authorization)
	if authorization["mode"] != "read_write" {
		t.Fatal("mode not effective")
	}
	var config core.Config
	key.call("POST", "/configs", core.Config{Name: "API archive", Engine: core.EngineMihomo, Content: "mixed-port: 21001\nsecret: private-test\n"}, 201, &config)
	if strings.Contains(config.Content, "private-test") || config.ID == "" || config.Version != 1 {
		t.Fatal("redaction lost secret or identity")
	}
	config.Content = strings.ReplaceAll(config.Content, "21001", "21002")
	config.Name = "API saved"
	key.call("PUT", "/configs/"+config.ID, config, 200, &config)
	var actual core.Config
	admin.call("GET", "/configs/"+config.ID+"/revisions/2", nil, 200, &actual)
	if !strings.Contains(actual.Content, "private-test") || !strings.Contains(actual.Content, "21002") {
		t.Fatal("redacted save corrupted real source")
	}
	key.call("PUT", "/configs/"+config.ID, core.Config{Name: "stale", Engine: core.EngineMihomo, Content: "mixed-port: 1", Version: 1}, 409, nil)
	var other core.Config
	bob.call("POST", "/configs", core.Config{Name: "private other", Engine: core.EngineMihomo, Content: "mixed-port: 21003"}, 201, &other)
	key.call("PUT", "/configs/"+other.ID, other, 404, nil)
	if body := key.call("GET", "/configs", nil, 200, nil); strings.Contains(string(body), other.ID) {
		t.Fatal("administrator key crossed workspace")
	}
	// Normal and sharing users can manage only their own, bounded credentials.
	own, ownKey := testCreateBusinessKey(t, alice, []core.Permission{core.PermissionConfigsRead, core.PermissionConfigsWrite})
	own.call("POST", "/configs", core.Config{Name: "own", Engine: core.EngineMihomo, Content: "mixed-port: 21004"}, 201, nil)
	own.call("PUT", "/configs/"+config.ID, config, 404, nil)
	bob.call("PUT", "/settings/api-keys/"+ownKey.Key.ID, core.APIKeyInput{Name: "not mine", Permissions: []core.Permission{core.PermissionAgentsRead}, Revision: ownKey.Key.Revision}, 404, nil)
	own.call("GET", "/settings/api-keys", nil, 403, nil)
	own.call("POST", "/settings/api-keys", core.APIKeyInput{Name: "escalate", Mode: core.APIKeyReadWrite, Permissions: core.APIKeyPermissions()}, 403, nil)
	alice.call("POST", "/settings/api-keys", core.APIKeyInput{Name: "escalate", Mode: core.APIKeyReadWrite, Permissions: []core.Permission{core.PermissionUsersManage}}, 403, nil)
	// Identity and credential operations stay outside even a fully delegated key.
	for _, path := range []string{"/enrollment-tokens", "/users", "/settings/api-keys", "/certificate-accounts"} {
		key.call("POST", path, map[string]any{}, 403, nil)
	}
	key.call("PUT", "/users/"+alice.userID, map[string]any{"password": "replacement credential"}, 403, nil)
	key.call("PUT", "/users/"+alice.userID, map[string]any{"role": "admin"}, 403, nil)
	// Explicitly bounded user grants can never grant users.manage or mutate admins.
	key.call("PUT", "/users/"+alice.userID, map[string]any{"permissions": []string{"users.manage"}}, 400, nil)
	key.call("PUT", "/users/"+alice.userID, map[string]any{"permissions": []string{"agents.read"}}, 200, nil)
	own.call("GET", "/api-key", nil, 401, nil)
	// Mode downgrade takes effect on the next request and survives old-client saves.
	input := core.APIKeyInput{Name: created.Key.Name, Mode: core.APIKeyReadOnly, Permissions: []core.Permission{core.PermissionConfigsRead}, Revision: created.Key.Revision}
	admin.call("PUT", "/settings/api-keys/"+created.Key.ID, input, 200, &created.Key)
	key.call("DELETE", "/configs/"+config.ID, nil, 403, nil)
	key.call("GET", "/configs", nil, 200, nil)
	// Data and history still exist after every rejected mutation.
	admin.call("GET", "/configs/"+config.ID+"/revisions/2", nil, 200, &actual)
	if actual.Name != "API saved" {
		t.Fatal("rejected mutation changed source")
	}
	_ = db
	_ = ctx
}

func TestAPIKeyTaskRevocationAndCompoundCapabilities(t *testing.T) {
	db, ctx, admin, _, _ := newConfigScopeAPIFixture(t)
	host := enrollConfigScopeAPIAgent(t, ctx, db)
	var config core.Config
	admin.call("PUT", "/agents/"+host.ID+"/configs/mihomo", configScopeAPIConfig(t, 21901), 200, &config)
	weak, _ := testCreateBusinessKey(t, admin, []core.Permission{core.PermissionTasksExecute})
	weak.call("POST", "/tasks", core.TaskRequest{AgentID: host.ID, Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigID: config.ID}, 403, nil)
	tasks, err := db.ListTasks(ctx, host.ID, 100)
	if err != nil || len(tasks) != 0 {
		t.Fatal("denied compound task changed state")
	}
	key, created := testCreateBusinessKey(t, admin, []core.Permission{core.PermissionTasksExecute, core.PermissionTasksRead, core.PermissionAgentConfigWrite})
	var task core.Task
	key.call("POST", "/tasks", core.TaskRequest{AgentID: host.ID, Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigID: config.ID}, 201, &task)
	admin.call("DELETE", "/settings/api-keys/"+created.Key.ID, nil, 200, nil)
	if lease, err := db.ClaimTask(ctx, host.ID); err != nil || lease != nil {
		t.Fatalf("revoked work dispatched: %+v %v", lease, err)
	}
	var actual core.Task
	admin.call("GET", "/tasks/"+task.ID, nil, 200, &actual)
	if actual.Status != core.TaskCanceled {
		t.Fatalf("revoked task status=%s", actual.Status)
	}
	key.call("GET", "/tasks/"+task.ID, nil, 401, nil)
	// Server-issued identity fields cannot be injected through otherwise valid input.
	admin.call("POST", "/settings/api-keys", map[string]any{"name": "forged", "permissions": []string{"agents.read"}, "owner_id": "other"}, 400, nil)
}

func TestAPIKeyContractIsUsableWithoutSecretReading(t *testing.T) {
	_, _, admin, _, _ := newConfigScopeAPIFixture(t)
	key, _ := testCreateBusinessKey(t, admin, []core.Permission{core.PermissionAgentsRead})
	var document map[string]any
	body := key.call("GET", "/openapi.json", nil, 200, &document)
	if document["openapi"] != "3.1.0" || !json.Valid(body) || strings.Contains(string(body), "[redacted]") {
		t.Fatal("machine contract was redacted or invalid")
	}
	key.call("GET", "/certificates", nil, 403, nil)
}

func TestAPIKeyRevocationAtCommitRollsBackBusinessWrite(t *testing.T) {
	db, ctx, _, _, _ := newConfigScopeAPIFixture(t)
	server := New(db, configScopeFixtureConfig())
	reached, release := make(chan struct{}), make(chan struct{})
	notified := make(chan struct{}, 1)
	server.auditWriter = func(ctx context.Context, entry core.AuditLogEntry) error {
		if entry.Action == "config.updated" {
			afterRequestCommit(ctx, func() { notified <- struct{}{} })
			close(reached)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	admin := configScopeAPIClient{t: t, handler: server.Handler(), token: configScopeFixtureAdminToken}
	key, created := testCreateBusinessKey(t, admin, []core.Permission{core.PermissionConfigsWrite, core.PermissionConfigsRead})
	var config core.Config
	admin.call("POST", "/configs", core.Config{Name: "before", Engine: core.EngineMihomo, Content: "mixed-port: 21111"}, 201, &config)

	done := make(chan int, 1)
	go func() {
		input := config
		input.Name = "must roll back"
		body, _ := json.Marshal(input)
		request := httptest.NewRequest("PUT", "/api/v1/configs/"+config.ID, bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+key.token)
		result := httptest.NewRecorder()
		admin.handler.ServeHTTP(result, request)
		done <- result.Code
	}()
	select {
	case <-reached:
	case <-ctx.Done():
		t.Fatal("write did not reach commit boundary")
	}
	admin.call("DELETE", "/settings/api-keys/"+created.Key.ID, nil, 200, nil)
	close(release)
	select {
	case status := <-done:
		if status != 403 {
			t.Fatalf("revoked commit status=%d", status)
		}
	case <-ctx.Done():
		t.Fatal("revoked write deadlocked")
	}
	var saved core.Config
	admin.call("GET", "/configs/"+config.ID+"/revisions/1", nil, 200, &saved)
	if saved.Name != "before" {
		t.Fatal("revoked business mutation committed")
	}
	var history []core.Config
	admin.call("GET", "/configs/"+config.ID+"/revisions", nil, 200, &history)
	if len(history) != 1 {
		t.Fatal("rolled back write left an archive")
	}
	select {
	case <-notified:
		t.Fatal("rolled back write notified an Agent before commit")
	default:
	}
}

func TestAPIKeySensitiveEditAndCertificateBoundaries(t *testing.T) {
	_, _, admin, alice, bob := newConfigScopeAPIFixture(t)
	key, _ := testCreateBusinessKey(t, alice, []core.Permission{core.PermissionConfigsRead, core.PermissionConfigsWrite, core.PermissionCertificatesRead, core.PermissionCertificatesManage, core.PermissionCertificatesGrant})
	var config core.Config
	key.call("POST", "/configs", core.Config{Name: "retained", Engine: core.EngineMihomo, Content: "mixed-port: 21201\nsecret: keep-test\n"}, 201, &config)
	key.call("PUT", "/configs/"+config.ID, map[string]any{"name": "metadata saved", "engine": "mihomo", "version": config.Version}, 200, &config)
	var actual core.Config
	alice.call("GET", "/configs/"+config.ID+"/revisions/2", nil, 200, &actual)
	if !strings.Contains(actual.Content, "keep-test") {
		t.Fatal("omitted secret content lost")
	}
	key.call("POST", "/configs", core.Config{Name: "invalid", Engine: core.EngineMihomo, Content: "secret: '[redacted]'\n"}, 400, nil)
	key.call("PUT", "/configs/"+config.ID, map[string]any{"name": "invalid", "engine": "mihomo", "version": config.Version, "content": ""}, 400, nil)
	var cert core.Certificate
	key.call("POST", "/certificates", map[string]any{"name": "Key certificate", "source": "self-signed", "domains": []string{"key.example.test"}, "key_type": "ecdsa256", "days": 90, "owner_id": bob.userID}, 201, &cert)
	if cert.OwnerID != alice.userID {
		t.Fatal("untrusted owner overrode creator")
	}
	for _, client := range []configScopeAPIClient{bob, admin} {
		client.call("GET", "/certificates/"+cert.ID, nil, 404, nil)
	}
	key.call("POST", "/certificates/"+cert.ID+"/grants", map[string]string{"username": "bob"}, 204, nil)
	bob.call("GET", "/certificates/"+cert.ID, nil, 200, nil)
	other, _ := testCreateBusinessKey(t, bob, []core.Permission{core.PermissionCertificatesRead, core.PermissionCertificatesManage, core.PermissionCertificatesGrant})
	other.call("POST", "/certificates/"+cert.ID+"/grants", map[string]string{"username": "alice"}, 404, nil)
	other.call("DELETE", "/certificates/"+cert.ID, nil, 404, nil)
	key.call("POST", "/certificate-accounts", map[string]any{}, 403, nil)
	key.call("DELETE", "/certificates/"+cert.ID+"/grants", map[string]string{"username": "bob"}, 204, nil)
	other.call("GET", "/certificates/"+cert.ID, nil, 404, nil)
	// Secret export cannot be delegated implicitly through settings management.
	weak, _ := testCreateBusinessKey(t, admin, []core.Permission{core.PermissionSettingsManage})
	weak.call("POST", "/substore-sync/run", map[string]string{"target_id": "anything"}, 403, nil)
	weak.call("GET", "/preferences/theme", nil, 403, nil)
}

func TestAPIKeySharedInstanceAndRetryIsolation(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	host := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, host, alice, 21301)
	grantConfigScopeAPIAgent(t, ctx, db, host, bob, 21302)
	permissions := []core.Permission{core.PermissionAgentConfigRead, core.PermissionAgentConfigWrite, core.PermissionTasksRead, core.PermissionTasksExecute}
	key, _ := testCreateBusinessKey(t, alice, permissions)
	other, _ := testCreateBusinessKey(t, bob, permissions)
	var a, b core.Config
	key.call("PUT", "/agents/"+host.ID+"/configs/mihomo", configScopeAPIConfig(t, 21301), 200, &a)
	other.call("PUT", "/agents/"+host.ID+"/configs/mihomo", configScopeAPIConfig(t, 21302), 200, &b)
	var task core.Task
	key.call("POST", "/tasks", core.TaskRequest{AgentID: host.ID, Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigID: a.ID}, 201, &task)
	if !task.SharedInstance {
		t.Fatal("key used host or foreign instance")
	}
	other.call("GET", "/tasks/"+task.ID, nil, 404, nil)
	other.call("POST", "/tasks", core.TaskRequest{AgentID: host.ID, Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigID: a.ID}, 404, nil)
	claimed, err := db.ClaimTask(ctx, host.ID)
	if err != nil || claimed == nil || claimed.SharedTrafficID != task.SharedTrafficID {
		t.Fatalf("claim shared task: %v", err)
	}
	if err = db.CompleteTask(ctx, host.ID, task.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: false, Error: "isolated validation failure", TrafficSettled: true}); err != nil {
		t.Fatal(err)
	}
	// The creator's admin status cannot stand in for a missing configuration grant.
	var own core.Config
	admin.call("PUT", "/agents/"+host.ID+"/configs/mihomo", configScopeAPIConfig(t, 21303), 200, &own)
	var failed core.Task
	admin.call("POST", "/tasks", core.TaskRequest{AgentID: host.ID, Engine: core.EngineMihomo, Action: core.ActionValidate, ConfigID: own.ID}, 201, &failed)
	claimed, err = db.ClaimTask(ctx, host.ID)
	if err != nil || claimed == nil {
		t.Fatal("validation task not claimable")
	}
	if err = db.CompleteTask(ctx, host.ID, failed.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: false, Error: "invalid fixture"}); err != nil {
		t.Fatal(err)
	}
	weak, _ := testCreateBusinessKey(t, admin, []core.Permission{core.PermissionTasksExecute, core.PermissionTasksRead})
	weak.call("GET", "/tasks/"+failed.ID+"/retry-editor", nil, 403, nil)
	weak.call("POST", "/tasks/"+failed.ID+"/edit-retry", map[string]any{"content": own.Content, "expected_version": own.Version}, 403, nil)
	// Queued shared work is canceled when the owner withdraws the actual share.
	key.call("POST", "/tasks/"+task.ID+"/retry", nil, 201, &task)
	if _, err = db.SetUserAgentAccess(ctx, alice.userID, core.AgentAccessRequest{Isolated: true}); err != nil {
		t.Fatal(err)
	}
	if next, err := db.ClaimTask(ctx, host.ID); err != nil || next != nil {
		t.Fatalf("withdrawn share dispatched: %v", err)
	}
	var state core.Task
	admin.call("GET", "/tasks/"+task.ID, nil, 200, &state)
	if state.Status != core.TaskCanceled {
		t.Fatal("withdrawn queued task not canceled")
	}
}
