package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

const configScopeFixtureAdminToken = "config-scope-administrator-fixture"
const configScopeFixturePassword = "config scope password fixture"

func configScopeFixtureConfig() Config {
	return Config{AdminToken: configScopeFixtureAdminToken, OperatorTokens: []string{"legacy-alice-fixture", "legacy-bob-fixture"},
		// Scope checks must not depend on an external geolocation provider.
		GeoIPHTTPClient: &http.Client{Transport: connectionGeoTransport(func(request *http.Request) (*http.Response, error) {
			return regionTestResponse(http.StatusOK, `{"country_code":"US","country":"United States"}`), nil
		})},
	}
}

func configScopeFixtureUser(name string) core.UserRequest {
	return core.UserRequest{Username: name, Password: configScopeFixturePassword, Role: core.RoleUser,
		Permissions: []core.Permission{
			core.PermissionOverviewRead, core.PermissionAgentsRead, core.PermissionAgentsManage,
			core.PermissionAgentConfigRead, core.PermissionAgentConfigWrite, core.PermissionConfigsRead,
			core.PermissionConfigsWrite, core.PermissionConfigsDelete, core.PermissionConfigsRestore,
			core.PermissionTemplatesRead, core.PermissionTemplatesWrite, core.PermissionTemplatesDelete,
			core.PermissionTasksRead, core.PermissionTasksExecute, core.PermissionClientAccessRead,
			core.PermissionDeploymentsRead, core.PermissionSettingsManage,
			core.PermissionSettingsRead, core.PermissionEnrollmentManage, core.PermissionAuditRead,
			core.PermissionCoreLogsRead, core.PermissionMetricsRead, core.PermissionTrafficRead, core.PermissionTrafficManage,
		}}
}

var configScopeTemplate struct {
	once    sync.Once
	baseURL string
	name    string
	users   map[string]core.User
	err     error
}

// CI has CREATEDB privileges. Build the common precondition once per process
// using current migrations and real API user creation, then clone the database.
// Tests still get separate catalogs, rows, sequences, pools and API sessions;
// all logins and subsequent business operations execute normally in each test.
// Local callers without CREATEDB keep the original fresh-schema fixture.
func configScopeFixtureDatabase(ctx context.Context, baseURL string) (string, func(context.Context) error, map[string]core.User, error) {
	if os.Getenv("QCH_TEST_API_FIXTURE_TEMPLATE") != "1" {
		schema, err := testdb.IsolatePostgres(ctx, baseURL)
		if err != nil {
			return "", nil, nil, err
		}
		return schema.URL, schema.Close, nil, nil
	}
	configScopeTemplate.once.Do(func() {
		configScopeTemplate.baseURL = baseURL
		configScopeTemplate.err = prepareConfigScopeTemplate(ctx, baseURL)
	})
	if configScopeTemplate.err != nil {
		return "", nil, nil, configScopeTemplate.err
	}
	databaseURL, name, err := createConfigScopeDatabase(ctx, baseURL, configScopeTemplate.name)
	if err != nil {
		return "", nil, nil, err
	}
	return databaseURL, func(ctx context.Context) error {
		return dropConfigScopeDatabase(ctx, baseURL, name)
	}, configScopeTemplate.users, nil
}

func prepareConfigScopeTemplate(ctx context.Context, baseURL string) error {
	databaseURL, name, err := createConfigScopeDatabase(ctx, baseURL, "")
	if err != nil {
		return err
	}
	configScopeTemplate.name = name
	db, err := store.OpenWithConfigKey(ctx, databaseURL, true, strings.Repeat("s", 32))
	if err != nil {
		return err
	}
	defer db.Close() // A template must have no connections while being cloned.
	handler := New(db, configScopeFixtureConfig()).Handler()
	configScopeTemplate.users = make(map[string]core.User)
	for _, username := range []string{"alice", "bob"} {
		payload, err := json.Marshal(configScopeFixtureUser(username))
		if err != nil {
			return err
		}
		request := httptest.NewRequest("POST", "/api/v1/users", bytes.NewReader(payload)).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+configScopeFixtureAdminToken)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			return fmt.Errorf("seed API fixture user %s: %d %s", username, response.Code, response.Body.String())
		}
		var user core.User
		if err := json.Unmarshal(response.Body.Bytes(), &user); err != nil {
			return err
		}
		configScopeTemplate.users[username] = user
	}
	return nil
}

func createConfigScopeDatabase(ctx context.Context, baseURL, template string) (string, string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", "", err
	}
	identifier := make([]byte, 8)
	if _, err := rand.Read(identifier); err != nil {
		return "", "", err
	}
	name := "qch_api_fixture_" + hex.EncodeToString(identifier)
	connection, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		return "", "", err
	}
	defer connection.Close(ctx)
	statement := "CREATE DATABASE " + pgx.Identifier{name}.Sanitize()
	if template != "" {
		statement += " TEMPLATE " + pgx.Identifier{template}.Sanitize()
	}
	if _, err := connection.Exec(ctx, statement); err != nil {
		return "", "", fmt.Errorf("create isolated API fixture database: %w", err)
	}
	parsed.Path = "/" + name
	query := parsed.Query()
	query.Del("search_path") // TestMain's schema belongs to the parent database.
	parsed.RawQuery = query.Encode()
	return parsed.String(), name, nil
}

func dropConfigScopeDatabase(ctx context.Context, baseURL, name string) error {
	connection, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		return err
	}
	defer connection.Close(ctx)
	_, err = connection.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
	return err
}

func closeConfigScopeTemplate(ctx context.Context) error {
	if configScopeTemplate.name == "" {
		return nil
	}
	return dropConfigScopeDatabase(ctx, configScopeTemplate.baseURL, configScopeTemplate.name)
}

func TestConfigScopeFixtureIsolation(t *testing.T) {
	modes := []string{"schema"}
	if os.Getenv("QCH_TEST_API_FIXTURE_TEMPLATE") == "1" {
		modes = append(modes, "template")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			value := ""
			if mode == "template" {
				value = "1"
			}
			t.Setenv("QCH_TEST_API_FIXTURE_TEMPLATE", value)
			first, firstContext, firstAdmin, firstAlice, firstBob := newConfigScopeAPIFixture(t)
			second, secondContext, secondAdmin, secondAlice, secondBob := newConfigScopeAPIFixture(t)
			if mode == "template" && firstAlice.userID != secondAlice.userID {
				t.Fatal("fixture did not copy the seeded identity")
			}
			// Even identical seeded IDs cannot make cookies valid in another server.
			foreignSession := firstAlice
			foreignSession.handler = secondAlice.handler
			foreignSession.call("GET", "/auth/session", nil, http.StatusUnauthorized, nil)
			firstAdmin.call("DELETE", "/users/"+firstAlice.userID, nil, http.StatusNoContent, nil)
			firstAlice.call("GET", "/auth/session", nil, http.StatusUnauthorized, nil)
			secondAlice.call("GET", "/auth/session", nil, http.StatusOK, nil)
			firstBob.call("GET", "/auth/session", nil, http.StatusOK, nil)
			secondBob.call("GET", "/auth/session", nil, http.StatusOK, nil)

			firstAgent := enrollConfigScopeAPIAgent(t, firstContext, first)
			var agents []core.Agent
			secondAdmin.call("GET", "/agents", nil, http.StatusOK, &agents)
			if len(agents) != 0 {
				t.Fatalf("another fixture's node leaked: %+v", agents)
			}
			secondAgent := enrollConfigScopeAPIAgent(t, secondContext, second)
			if firstAgent.Name != secondAgent.Name || firstAgent.ID == secondAgent.ID {
				t.Fatal("same-name nodes must remain separate resources")
			}
			firstAdmin.call("GET", "/agents", nil, http.StatusOK, &agents)
			if len(agents) != 1 || agents[0].ID != firstAgent.ID {
				t.Fatalf("fixture node ownership changed: %+v", agents)
			}
			// A later clone must still contain the pristine users and no nodes.
			_, _, thirdAdmin, thirdAlice, thirdBob := newConfigScopeAPIFixture(t)
			thirdAlice.call("GET", "/auth/session", nil, http.StatusOK, nil)
			thirdBob.call("GET", "/auth/session", nil, http.StatusOK, nil)
			thirdAdmin.call("GET", "/agents", nil, http.StatusOK, &agents)
			if len(agents) != 0 {
				t.Fatalf("mutations reached the fixture template: %+v", agents)
			}
		})
	}
}
