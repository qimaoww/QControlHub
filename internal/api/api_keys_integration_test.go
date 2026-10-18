package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestAPIKeyAuthenticationLifecycleAndProxyPolicy(t *testing.T) {
	database := os.Getenv("QCH_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	schema, err := testdb.IsolatePostgres(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(context.Background())
	db, err := store.Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	conn, err := pgx.Connect(ctx, schema.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	proxies, _ := authn.ParseTrustedProxies([]string{"10.0.0.1/32", "10.0.0.2/32"})
	config := Config{AdminToken: "api-key-fixture-administrator-credential", TrustedProxies: proxies}
	server := New(db, config)
	handler := server.Handler()
	call := func(token, method, path string, body any, remote, xff string, status int) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(data))
		r.RemoteAddr = remote
		if remote == "" {
			r.RemoteAddr = "127.0.0.1:12345"
		}
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s got %d want %d: %s", method, path, w.Code, status, core.RedactDiagnostic(w.Body.String()))
		}
		return w
	}
	input := core.APIKeyInput{Name: "diagnostic", Permissions: core.DiagnosticPermissions(), IPAllowlist: []string{}}
	create := func(token string, in core.APIKeyInput) (core.APIKey, string) {
		t.Helper()
		w := call(token, "POST", "/settings/api-keys", in, "", "", 201)
		var result struct {
			Key    core.APIKey `json:"key"`
			Secret string      `json:"secret"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(result.Secret, "qch_key_") || len(result.Secret) != 51 {
			t.Fatal("invalid generated credential")
		}
		return result.Key, result.Secret
	}
	update := func(k core.APIKey, in core.APIKeyInput) core.APIKey {
		t.Helper()
		in.Revision = k.Revision
		w := call(config.AdminToken, "PUT", "/settings/api-keys/"+k.ID, in, "", "", 200)
		var result core.APIKey
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return result
	}
	k, secret := create(config.AdminToken, input)
	t.Run("metadata_digest_and_throttled_last_use", func(t *testing.T) {
		body := call(config.AdminToken, "GET", "/settings/api-keys", nil, "", "", 200).Body.String()
		if strings.Contains(body, secret) || strings.Contains(body, "digest") {
			t.Fatal("secret metadata leaked")
		}
		var digest []byte
		var record string
		if err := conn.QueryRow(ctx, `SELECT digest,row_to_json(k)::text FROM admin_api_keys k WHERE id=$1`, k.ID).Scan(&digest, &record); err != nil {
			t.Fatal(err)
		}
		want := sha256.Sum256([]byte(secret))
		if !bytes.Equal(digest, want[:]) || strings.Contains(record, secret) {
			t.Fatal("credential not hash-only")
		}
		call(secret, "GET", "/api-key", nil, "", "", 200)
		var first, last time.Time
		_ = conn.QueryRow(ctx, `SELECT last_used_at FROM admin_api_keys WHERE id=$1`, k.ID).Scan(&first)
		call(secret, "GET", "/overview", nil, "", "", 200)
		_ = conn.QueryRow(ctx, `SELECT last_used_at FROM admin_api_keys WHERE id=$1`, k.ID).Scan(&last)
		if first.IsZero() || !first.Equal(last) {
			t.Fatal("last use wrote on each request")
		}
	})
	t.Run("read_scope_and_behavior_boundaries", func(t *testing.T) {
		for _, path := range []string{"/overview", "/agents", "/deployments", "/configs", "/tasks", "/core-log-sources", "/core-logs", "/audit", "/settings/deployment"} {
			call(secret, "GET", path, nil, "", "", 200)
		}
		for _, route := range []struct{ method, path string }{{"POST", "/tasks"}, {"POST", "/settings/check-update"}, {"POST", "/settings/api-keys"}, {"GET", "/settings/api-keys"}, {"GET", "/users"}, {"GET", "/tasks/tsk_0000000000000000/retry-editor"}, {"GET", "/traffic-endpoints/sync"}, {"POST", "/traffic-policies/batch"}, {"PUT", "/preferences/node-order"}, {"DELETE", "/configs/cfg_fixture"}} {
			call(secret, route.method, route.path, nil, "", "", 403)
		}
		call(secret, "GET", "/auth/session", nil, "", "", 401)
		call(secret, "POST", "/auth/login", map[string]string{"token": secret}, "192.0.2.90:42", "", 401)
		narrower := input
		narrower.Permissions = []core.Permission{core.PermissionAgentsRead}
		k = update(k, narrower)
		call(secret, "GET", "/overview", nil, "", "", 403)
		call(secret, "GET", "/agents", nil, "", "", 200)
		call(secret, "GET", "/tasks", nil, "", "", 403)
		k = update(k, input)
	})
	t.Run("redacted_logs_ignore_original_representation_validators", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/api/v1/core-logs", nil)
		r.Header.Set("Authorization", "Bearer "+secret)
		r.Header.Set("If-None-Match", "*")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("ETag") != "" || !json.Valid(w.Body.Bytes()) {
			t.Fatalf("unredacted validator affected Key representation: status=%d", w.Code)
		}
	})
	t.Run("immediate_allowlist_and_proxy_chain", func(t *testing.T) {
		withIP := input
		withIP.IPAllowlist = []string{"192.168.5.77/24", "::ffff:192.168.5.99/120", "fd00:0:0::/48", "::1"}
		k = update(k, withIP)
		if len(k.IPAllowlist) != 3 || k.IPAllowlist[0] != "192.168.5.0/24" {
			t.Fatalf("not canonical: %v", k.IPAllowlist)
		}
		for _, tc := range []struct {
			remote, xff string
			status      int
		}{
			{"192.168.5.9:42", "", 200}, {"[::ffff:192.168.5.9]:42", "", 200}, {"[fd00::4]:42", "", 200}, {"[::1]:42", "", 200},
			{"192.168.6.9:42", "192.168.5.9", 403}, {"10.0.0.2:42", "192.168.5.9, 10.0.0.1", 200},
			{"10.0.0.2:42", "192.168.5.9, 192.168.6.9, 10.0.0.1", 403}, {"10.0.0.2:42", "forged, 192.168.5.9, 10.0.0.1", 403}, {"invalid", "192.168.5.9", 403},
		} {
			call(secret, "GET", "/api-key", nil, tc.remote, tc.xff, tc.status)
		}
		withIP.IPAllowlist = []string{"127.0.0.1"}
		k = update(k, withIP)
		call(secret, "GET", "/api-key", nil, "192.168.5.9:42", "", 403)
		call(secret, "GET", "/api-key", nil, "", "", 200)
		k = update(k, input)
		invalid := input
		invalid.IPAllowlist = []string{"bad"}
		invalid.Revision = k.Revision
		call(config.AdminToken, "PUT", "/settings/api-keys/"+k.ID, invalid, "", "", 400)
		invalid = input
		invalid.Permissions = []core.Permission{core.PermissionTasksExecute}
		call(config.AdminToken, "POST", "/settings/api-keys", invalid, "", "", 400)
	})
	t.Run("restart_and_environment_identity", func(t *testing.T) {
		db.Close()
		db, err = store.Open(ctx, schema.URL, true)
		if err != nil {
			t.Fatal(err)
		}
		handler = New(db, config).Handler()
		call(secret, "GET", "/api-key", nil, "", "", 200)
		rotated := config
		rotated.AdminToken = "replacement-environment-administrator"
		handler = New(db, rotated).Handler()
		call(secret, "GET", "/api-key", nil, "192.0.2.91:42", "", 401)
		handler = New(db, config).Handler()
		call(secret, "GET", "/api-key", nil, "", "", 200)
	})
	t.Run("consistent_credential_failures", func(t *testing.T) {
		disabled := input
		disabled.Disabled = true
		k = update(k, disabled)
		disabledBody := call(secret, "GET", "/api-key", nil, "192.0.2.92:42", "", 401).Body.String()
		k = update(k, input)
		_, err = conn.Exec(ctx, `UPDATE admin_api_keys SET expires_at=now()-interval '1 minute' WHERE id=$1`, k.ID)
		if err != nil {
			t.Fatal(err)
		}
		expired := call(secret, "GET", "/api-key", nil, "192.0.2.93:42", "", 401).Body.String()
		k = update(k, input)
		call(config.AdminToken, "DELETE", "/settings/api-keys/"+k.ID, nil, "", "", 200)
		revoked := call(secret, "GET", "/api-key", nil, "192.0.2.94:42", "", 401).Body.String()
		invalid := call("qch_key_"+strings.Repeat("z", 43), "GET", "/api-key", nil, "192.0.2.95:42", "", 401).Body.String()
		if disabledBody != expired || expired != revoked || revoked != invalid {
			t.Fatal("credential states disclosed")
		}
		call(config.AdminToken, "PUT", "/settings/api-keys/"+k.ID, input, "", "", 409)
	})
	t.Run("current_creator_status_and_ownership", func(t *testing.T) {
		admin := configScopeAPIClient{t: t, handler: handler, token: config.AdminToken}
		var user core.User
		admin.call("POST", "/users", core.UserRequest{Username: "keycreator", Password: "api key administrator password", Role: core.RoleAdmin}, 201, &user)
		login := func() configScopeAPIClient {
			r := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"keycreator","token":"api key administrator password"}`))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal("login failed")
			}
			var value map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &value)
			return configScopeAPIClient{t: t, handler: handler, cookie: w.Result().Cookies()[0], csrf: value["csrf_token"].(string), userID: user.ID}
		}
		creator := login()
		var result struct {
			Key    core.APIKey `json:"key"`
			Secret string      `json:"secret"`
		}
		creator.call("POST", "/settings/api-keys", input, 201, &result)
		w := call(result.Secret, "GET", "/api-key", nil, "", "", 200)
		if !strings.Contains(w.Body.String(), user.ID) {
			t.Fatal("key became a new workspace/account")
		}
		list := call(config.AdminToken, "GET", "/settings/api-keys", nil, "", "", 200).Body.String()
		if strings.Contains(list, result.Key.ID) {
			t.Fatal("another creator can list key")
		}
		admin.call("PUT", "/settings/api-keys/"+result.Key.ID, input, 404, nil)
		admin.call("PUT", "/users/"+user.ID, map[string]any{"disabled": true}, 200, nil)
		call(result.Secret, "GET", "/api-key", nil, "192.0.2.96:42", "", 401)
		admin.call("PUT", "/users/"+user.ID, map[string]any{"disabled": false}, 200, nil)
		call(result.Secret, "GET", "/api-key", nil, "192.0.2.97:42", "", 401)
		creator = login()
		creator.call("POST", "/settings/api-keys", input, 201, &result)
		admin.call("PUT", "/users/"+user.ID, map[string]any{"role": "user", "permissions": []string{"agents.read"}}, 200, nil)
		call(result.Secret, "GET", "/api-key", nil, "192.0.2.98:42", "", 401)
		admin.call("PUT", "/users/"+user.ID, map[string]any{"role": "admin"}, 200, nil)
		creator = login()
		creator.call("POST", "/settings/api-keys", input, 201, &result)
		if _, err := conn.Exec(ctx, `DELETE FROM panel_users WHERE id=$1`, user.ID); err != nil {
			t.Fatal(err)
		}
		call(result.Secret, "GET", "/api-key", nil, "192.0.2.99:42", "", 401)
	})
	t.Run("audit_without_secret", func(t *testing.T) {
		// Synchronous metadata mutation audits are committed before disclosure.
		var count int
		err := conn.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action IN ('api_key.created','api_key.updated','api_key.disabled','api_key.revoked')`).Scan(&count)
		if err != nil || count < 8 {
			t.Fatalf("missing mutation audits: %d %v", count, err)
		}
		var leaked bool
		_ = conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM audit_logs WHERE detail LIKE '%' || $1 || '%' OR target LIKE '%' || $1 || '%')`, secret).Scan(&leaked)
		if leaked {
			t.Fatal("audit credential leak")
		}
	})
}

func TestAPIKeyDiagnosticResourceIsolationAndRedaction(t *testing.T) {
	db, ctx, admin, alice, bob := newConfigScopeAPIFixture(t)
	host := enrollConfigScopeAPIAgent(t, ctx, db)
	grantConfigScopeAPIAgent(t, ctx, db, host, alice, 21101)
	grantConfigScopeAPIAgent(t, ctx, db, host, bob, 21102)
	aCtx := store.WithConfigScope(ctx, alice.userID, false)
	private := enrollConfigScopeAPIAgent(t, aCtx, db)
	if err := db.SetAgentAdminHidden(aCtx, private.ID, true); err != nil {
		t.Fatal(err)
	}
	features := append(host.Features, core.AgentFeatureCoreLogs, "core-log-instances-v1", "core-log-status-v1")
	if err := db.Heartbeat(ctx, host.ID, core.HeartbeatRequest{Features: features, Runtime: map[core.Engine]core.RuntimeState{core.EngineMihomo: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	input := core.APIKeyInput{Name: "resource diagnostic", Permissions: core.DiagnosticPermissions()}
	var key1, key2 struct {
		Key    core.APIKey `json:"key"`
		Secret string      `json:"secret"`
	}
	admin.call("POST", "/settings/api-keys", input, 201, &key1)
	admin.call("POST", "/settings/api-keys", input, 201, &key2)
	diag := configScopeAPIClient{t: t, handler: admin.handler, token: key1.Secret}
	second := configScopeAPIClient{t: t, handler: admin.handler, token: key2.Secret}
	// A Key uses the administrator's existing workspace, including draft IDs.
	content := configScopeAPIConfig(t, 7890).Content + "\nsecret: CONFIG_CREDENTIAL\n"
	var cfg core.Config
	admin.call("PUT", "/agents/"+host.ID+"/configs/mihomo", core.Config{Name: "diagnostic config", Engine: core.EngineMihomo, Content: content}, 200, &cfg)
	body := diag.call("GET", "/agents/"+host.ID+"/configs/mihomo", nil, 200, nil)
	if strings.Contains(string(body), "CONFIG_CREDENTIAL") || !strings.Contains(string(body), cfg.ID) || !strings.Contains(string(body), "7890") {
		t.Fatal("configuration redaction or identity failed")
	}
	second.call("GET", "/agents/"+host.ID+"/configs/mihomo", nil, 200, nil)
	// Config list/revision/detail and task snapshot must share the same guard.
	var archive core.Config
	admin.call("POST", "/configs", core.Config{Name: "diagnostic archive", Engine: core.EngineMihomo, Content: content}, 201, &archive)
	for _, path := range []string{"/configs", "/configs/" + archive.ID + "/revisions", "/configs/" + archive.ID + "/revisions/1", "/agents/" + host.ID + "/configs/mihomo/files", "/agents/" + host.ID + "/configs/mihomo/workspace", "/agents/" + host.ID + "/configs/mihomo/fields/dns"} {

		payload := diag.call("GET", path, nil, 200, nil)
		if strings.Contains(string(payload), "CONFIG_CREDENTIAL") {
			t.Fatalf("configuration leaked at %s", path)
		}
	}
	var task core.Task
	admin.call("POST", "/tasks", core.TaskRequest{AgentID: host.ID, Engine: core.EngineMihomo, Action: core.ActionValidate, ConfigID: cfg.ID}, 201, &task)
	claimed, err := db.ClaimTask(ctx, host.ID)
	if err != nil || claimed == nil || claimed.ID != task.ID {
		t.Fatalf("claim: %v", err)
	}
	if err := db.CompleteTask(ctx, host.ID, task.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: false, Output: "parse failed at line 8 password=TASK_CREDENTIAL", Error: "configuration invalid port 443 token=ERROR_CREDENTIAL"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/tasks?agent_id=" + host.ID + "&status=failed&limit=1", "/tasks/" + task.ID} {
		payload := diag.call("GET", path, nil, 200, nil)
		for _, credential := range []string{"CONFIG_CREDENTIAL", "TASK_CREDENTIAL", "ERROR_CREDENTIAL"} {
			if strings.Contains(string(payload), credential) {
				t.Fatalf("credential leaked at %s", path)
			}
		}
	}

	var read core.Task
	admin.call("POST", "/tasks", core.TaskRequest{AgentID: host.ID, Engine: core.EngineMihomo, Action: core.ActionReadConfig}, 201, &read)
	claimed, err = db.ClaimTask(ctx, host.ID)
	if err != nil || claimed == nil {
		t.Fatal("read claim failed")
	}
	if err := db.CompleteTask(ctx, host.ID, read.ID, core.TaskResultRequest{LeaseID: claimed.LeaseID, Success: true, Output: content}); err != nil {
		t.Fatal(err)
	}
	snapshot := diag.call("GET", "/tasks/"+read.ID+"/config-snapshot", nil, 200, nil)
	if strings.Contains(string(snapshot), "CONFIG_CREDENTIAL") {
		t.Fatal("snapshot leaked credential")
	}
	// Shared instance ownership is assigned by the panel, even with a forged
	// OwnerID in the Agent upload. Administrator can read its shared host logs.
	var access core.AgentAccess
	alice.call("GET", "/agent-access", nil, 200, &access)
	share := access.Shares[0]
	// Establish instance ownership through the existing store schema fixture.
	// A deployment task binds the real share; no API Key can create this task.
	var userConfig core.Config
	alice.call("PUT", "/agents/"+host.ID+"/configs/mihomo", configScopeAPIConfig(t, 21101), 200, &userConfig)
	var deploy core.Task
	alice.call("POST", "/tasks", core.TaskRequest{AgentID: host.ID, Engine: core.EngineMihomo, Action: core.ActionDeploy, ConfigID: userConfig.ID}, 201, &deploy)
	completeConfigScopeAPITask(t, ctx, db, deploy)
	for _, source := range []struct{ agent, instance, message string }{{host.ID, "main", "main password=LOG_CREDENTIAL"}, {host.ID, share.ID, "alice shared error line 12"}, {private.ID, "main", "private user log"}} {
		id, _ := core.NewID("log")
		err := db.StoreCoreLogs(ctx, source.agent, core.CoreLogBatch{ID: id, Entries: []core.CoreLogEntry{{Engine: core.EngineMihomo, InstanceID: source.instance, Service: core.CoreLogService(core.EngineMihomo, source.instance, "systemd"), OwnerID: bob.userID, Level: "error", Message: source.message}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	sources := diag.call("GET", "/core-log-sources", nil, 200, nil)
	if strings.Contains(string(sources), private.ID) {
		t.Fatal("private source leaked")
	}
	logs := diag.call("GET", "/core-logs?agent_id="+host.ID+"&engine=mihomo&limit=1", nil, 200, nil)
	if strings.Contains(string(logs), "LOG_CREDENTIAL") || strings.Contains(string(logs), "private user log") {
		t.Fatal("log isolation/redaction failed")
	}
	diag.call("GET", "/core-logs?agent_id="+host.ID+"&instance_id="+share.ID+"&user_id="+alice.userID, nil, 200, nil)
	for _, path := range []string{"/agents/" + private.ID + "/configs/mihomo", "/core-logs?agent_id=" + private.ID, "/metrics/" + private.ID} {
		diag.call("GET", path, nil, 404, nil)
	}
	before := diag.call("GET", "/agents", nil, 200, nil)
	if strings.Contains(string(before), private.ID) {
		t.Fatal("private list entry leaked")
	}
	// Another administrator's Key has its own log ownership view.
	var other core.User
	admin.call("POST", "/users", core.UserRequest{Username: "otheradmin", Password: "other administrator password", Role: core.RoleAdmin}, 201, &other)
	// The store-only metadata creation is scoped to the same durable identity
	// used by database authentication, avoiding a second test login helper.
	otherCtx := store.WithConfigScope(ctx, other.ID, true)
	_, otherSecret, err := db.CreateAPIKey(otherCtx, "user", other.ID, input, core.AuditLogEntry{Actor: "otheradmin"})
	if err != nil {
		t.Fatal(err)
	}
	otherDiag := configScopeAPIClient{t: t, handler: admin.handler, token: otherSecret}
	otherDiag.call("GET", "/core-logs?agent_id="+private.ID, nil, 404, nil)
	// Each Key keeps an independent grant. Narrowing one cannot affect another.
	input.Permissions = []core.Permission{core.PermissionAgentsRead}
	input.Revision = key1.Key.Revision
	admin.call("PUT", "/settings/api-keys/"+key1.Key.ID, input, 200, nil)
	diag.call("GET", "/core-logs", nil, 403, nil)
	second.call("GET", "/core-logs", nil, 200, nil)
}
