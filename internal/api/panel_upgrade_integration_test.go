package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

// Run via make panel-auth-upgrade-test. The old executable is built from an
// immutable historical commit, so this exercises real startup migrations and
// process-local sessions rather than simulating an upgrade with a new Server.
func TestPanelAuthUpgradeLifecycle(t *testing.T) {
	oldBinary, newBinary := os.Getenv("QCH_TEST_PREVIOUS_PANEL_BINARY"), os.Getenv("QCH_TEST_PANEL_BINARY")
	database := os.Getenv("QCH_TEST_DATABASE_URL")
	if oldBinary == "" || newBinary == "" || database == "" {
		t.Skip("panel binaries and QCH_TEST_DATABASE_URL are required; use make panel-auth-upgrade-test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	schema, err := testdb.IsolatePostgres(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := schema.Close(cleanup); err != nil {
			t.Error(err)
		}
	}()
	connection, err := pgx.Connect(ctx, schema.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	baseURL := "http://" + address
	const adminToken = "upgrade-lifecycle-environment-fixture"
	start := func(binary string, wantFailure string) func() {
		t.Helper()
		logFile, err := os.CreateTemp(t.TempDir(), "panel-*.log")
		if err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(ctx, binary)
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "QCH_") {
				command.Env = append(command.Env, value)
			}
		}
		command.Env = append(command.Env, "QCH_LISTEN="+address, "QCH_DATABASE_URL="+schema.URL,
			"QCH_ADMIN_TOKEN="+adminToken, "QCH_CONFIG_ENCRYPTION_KEY="+strings.Repeat("k", 32),
			"QCH_AGENT_PUBLIC_IP_PROBE_ENABLED=false", "QCH_ALLOW_INSECURE_DATABASE=true")
		command.Stdout, command.Stderr = logFile, logFile
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		stopped := false
		stop := func() {
			if !stopped {
				_ = command.Process.Signal(os.Interrupt)
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					_ = command.Process.Kill()
					<-done
				}
				stopped = true
				_ = logFile.Close()
			}
		}
		t.Cleanup(stop)
		probe := &http.Client{Timeout: time.Second}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case err := <-done:
				stopped = true
				_ = logFile.Close()
				logs, _ := os.ReadFile(logFile.Name())
				if wantFailure != "" && err != nil && bytes.Contains(logs, []byte(wantFailure)) {
					return stop
				}
				t.Fatalf("panel exited unexpectedly (%s); inspect private test logs", filepath.Base(binary))
			default:
			}
			if wantFailure == "" {
				response, err := probe.Get(baseURL + "/readyz")
				if err == nil {
					_ = response.Body.Close()
					if response.StatusCode == http.StatusOK {
						return stop
					}
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		stop()
		t.Fatal("panel startup timed out")
		return stop
	}
	request := func(client *http.Client, method, path string, body any, csrf string, status int, output any) {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(ctx, method, baseURL+"/api/v1"+path, bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(csrfHeader, csrf)
		response, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s request failed", method, path)
		}
		defer response.Body.Close()
		if response.StatusCode != status {
			t.Fatalf("%s %s returned %d, want %d", method, path, response.StatusCode, status)
		}
		if output != nil {
			if err := json.NewDecoder(response.Body).Decode(output); err != nil {
				t.Fatal(err)
			}
		} else {
			_, _ = io.Copy(io.Discard, response.Body)
		}
	}
	type loginSession struct {
		UserID      string            `json:"user_id"`
		WorkspaceID string            `json:"workspace_id"`
		Role        core.Role         `json:"role"`
		Permissions []core.Permission `json:"permissions"`
		CSRF        string            `json:"csrf_token"`
	}
	login := func(username, password string, status int) (*http.Client, loginSession) {
		t.Helper()
		jar, _ := cookiejar.New(nil) // Every login uses a new browser session.
		client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
		var session loginSession
		request(client, "POST", "/auth/login", map[string]string{"username": username, "token": password}, "", status, &session)
		return client, session
	}
	snapshot := func() (string, int) {
		t.Helper()
		var records string
		var version int
		if err := connection.QueryRow(ctx, `SELECT jsonb_agg(jsonb_build_array(id,username,password_hash,disabled,role,permissions,auth_revision) ORDER BY id)::text FROM panel_users`).Scan(&records); err != nil {
			t.Fatal(err)
		}
		if err := connection.QueryRow(ctx, `SELECT max(version) FROM qcontrolhub_schema_migrations`).Scan(&version); err != nil {
			t.Fatal(err)
		}
		return records, version // Contains hashes: compare only, never print.
	}

	stopOld := start(oldBinary, "")
	admin, adminSession := login("admin", adminToken, http.StatusOK)
	passwords := map[string]string{
		"ordinary": "original ordinary password", "admin": "original database admin password",
		"reset": "original reset password", "disabled": "original disabled password",
		"spaced": "\u3000 original 中文 password \t", "chinese": "中文密码",
	}
	users := make(map[string]core.User)
	for _, name := range []string{"ordinary", "admin", "reset", "disabled", "spaced", "chinese"} {
		role := core.RoleUser
		if name == "admin" {
			role = core.RoleAdmin
		}
		var user core.User
		request(admin, "POST", "/users", core.UserRequest{Username: name, Password: passwords[name], Role: role,
			Permissions: []core.Permission{core.PermissionOverviewRead, core.PermissionConfigsRead, core.PermissionConfigsWrite}}, adminSession.CSRF, http.StatusCreated, &user)
		users[name] = user
	}
	oldClient, originalSession := login("ordinary", passwords["ordinary"], http.StatusOK)
	_, originalAdmin := login("admin", passwords["admin"], http.StatusOK)
	if originalAdmin.UserID != users["admin"].ID || originalAdmin.WorkspaceID != users["admin"].ID {
		t.Fatal("old database administrator identity is incorrect")
	}
	var config core.Config
	request(oldClient, "POST", "/configs", core.Config{Name: "original-owned-config", Engine: core.EngineMihomo,
		Content: "mixed-port: 7890\nmode: rule\nrules: [MATCH,DIRECT]\n"}, originalSession.CSRF, http.StatusCreated, &config)
	request(admin, "PUT", "/users/"+users["reset"].ID, map[string]string{"password": "new reset password fixture"}, adminSession.CSRF, http.StatusOK, nil)
	passwords["reset"] = "new reset password fixture"
	login("reset", passwords["reset"], http.StatusOK)
	request(admin, "PUT", "/users/"+users["disabled"].ID, map[string]bool{"disabled": true}, adminSession.CSRF, http.StatusOK, nil)
	login("disabled", passwords["disabled"], http.StatusUnauthorized)
	login("spaced", passwords["spaced"], http.StatusUnauthorized)
	login("chinese", passwords["chinese"], http.StatusOK)
	before, oldVersion := snapshot()
	stopOld()
	t.Logf("old binary schema v%d: ordinary/admin/reset/Chinese logins succeed; literal whitespace password reproduces 401", oldVersion)

	// Force a failure inside the actual startup migration transaction.
	if _, err := connection.Exec(ctx, `CREATE FUNCTION reject_account_upgrade() RETURNS trigger LANGUAGE plpgsql AS
		$$ BEGIN RAISE EXCEPTION 'injected account upgrade failure'; END $$;
		CREATE TRIGGER reject_account_upgrade BEFORE UPDATE ON panel_users FOR EACH STATEMENT EXECUTE FUNCTION reject_account_upgrade()`); err != nil {
		t.Fatal(err)
	}
	start(newBinary, "injected account upgrade failure")
	failed, failedVersion := snapshot()
	if before != failed || oldVersion != failedVersion {
		t.Fatal("failed startup migration modified accounts or schema ledger")
	}
	stopOld = start(oldBinary, "")
	login("ordinary", passwords["ordinary"], http.StatusOK)
	login("admin", passwords["admin"], http.StatusOK)
	stopOld()
	if _, err := connection.Exec(ctx, `DROP TRIGGER reject_account_upgrade ON panel_users; DROP FUNCTION reject_account_upgrade()`); err != nil {
		t.Fatal(err)
	}
	t.Log("failed migration rolled back; old executable accepts original database credentials")

	for attempt := 0; attempt < 3; attempt++ {
		stopNew := start(newBinary, "")
		after, newVersion := snapshot()
		if before != after || newVersion <= oldVersion {
			t.Fatal("upgrade/restart changed account fields or failed to migrate")
		}
		request(oldClient, "GET", "/auth/session", nil, "", http.StatusUnauthorized, nil)
		for _, name := range []string{"ordinary", "admin", "reset", "spaced", "chinese"} {
			client, session := login(strings.ToUpper(name), passwords[name], http.StatusOK)
			expectedPermissions := users[name].Permissions
			if users[name].Role == core.RoleAdmin {
				expectedPermissions = core.AllPermissions() // Administrators include newly introduced capabilities.
			}
			if session.UserID != users[name].ID || session.WorkspaceID != users[name].ID || session.Role != users[name].Role || !reflect.DeepEqual(session.Permissions, expectedPermissions) {
				t.Fatalf("upgrade changed authenticated identity, role, permissions, or ownership for %s", name)
			}
			request(client, "GET", "/auth/session", nil, "", http.StatusOK, nil)
			if name == "ordinary" {
				var configs []core.Config
				request(client, "GET", "/configs", nil, "", http.StatusOK, &configs)
				if len(configs) != 1 || configs[0].ID != config.ID || configs[0].OwnerID != session.UserID {
					t.Fatal("upgrade changed owned data visibility")
				}
			}
		}
		_, tokenSession := login("admin", adminToken, http.StatusOK)
		if tokenSession.UserID != "" || tokenSession.Role != core.RoleAdmin {
			t.Fatal("environment token confused with database administrator")
		}
		login("disabled", passwords["disabled"], http.StatusUnauthorized)
		login("reset", "original reset password", http.StatusUnauthorized)
		login("ordinary", "wrong password fixture", http.StatusUnauthorized)
		stopNew()
		t.Logf("upgrade/restart %d: schema v%d; account fields unchanged; fresh identities, permissions, ownership and rejection checks passed", attempt+1, newVersion)
	}
}
