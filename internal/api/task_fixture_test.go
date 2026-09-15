package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func enrollTaskAPIAgent(t *testing.T, ctx context.Context, dataStore *store.Store, enrollmentToken, name string) core.Agent {
	t.Helper()
	publicKey := make([]byte, 32)
	if _, err := rand.Read(publicKey); err != nil {
		t.Fatalf("generate public key: %v", err)
	}
	agent, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: name, OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo},
		Features:     []string{core.AgentFeatureIndependentEgress},
		PublicKey:    base64.RawURLEncoding.EncodeToString(publicKey),
	}, enrollmentToken)
	if err != nil {
		t.Fatalf("enroll %s: %v", name, err)
	}
	return agent
}

func enrollTaskAPIAgentWithSource(t *testing.T, ctx context.Context, dataStore *store.Store, enrollmentToken, name string) core.Agent {
	t.Helper()
	publicKey := make([]byte, 32)
	if _, err := rand.Read(publicKey); err != nil {
		t.Fatalf("generate public key: %v", err)
	}
	agent, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
		Name: name, OS: "linux", Arch: "amd64",
		Capabilities: []core.Engine{core.EngineMihomo},
		Features:     []string{core.AgentFeatureSelfUpgrade, core.AgentFeatureMihomoDevelopmentSource},
		PublicKey:    base64.RawURLEncoding.EncodeToString(publicKey),
	}, enrollmentToken)
	if err != nil {
		t.Fatalf("enroll %s: %v", name, err)
	}
	return agent
}

func createTaskAPIFixture(t *testing.T, ctx context.Context, dataStore *store.Store, request core.TaskRequest) core.Task {
	t.Helper()
	task, err := dataStore.CreateTask(ctx, request)
	if err != nil {
		t.Fatalf("create %s task: %v", request.Action, err)
	}
	return task
}

func taskAPIRequest(t *testing.T, handler http.Handler, token, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertTaskAPIList(t *testing.T, handler http.Handler, token, target string, wantIDs []string, predicate func(core.Task) bool) []core.Task {
	t.Helper()
	response := taskAPIRequest(t, handler, token, http.MethodGet, target)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s status=%d body=%s", target, response.Code, response.Body.String())
	}
	var tasks []core.Task
	if err := json.Unmarshal(response.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("decode GET %s: %v", target, err)
	}
	if wantIDs != nil {
		got := make(map[string]struct{}, len(tasks))
		for _, task := range tasks {
			got[task.ID] = struct{}{}
		}
		if len(got) != len(wantIDs) {
			t.Fatalf("GET %s task IDs=%v, want %v", target, got, wantIDs)
		}
		for _, id := range wantIDs {
			if _, ok := got[id]; !ok {
				t.Fatalf("GET %s task IDs=%v, missing %s", target, got, id)
			}
		}
	}
	if predicate != nil {
		for _, task := range tasks {
			if !predicate(task) {
				t.Fatalf("GET %s returned task outside filter: %+v", target, task)
			}
		}
	}
	return tasks
}

func cleanupTaskAPIFixture(t *testing.T, databaseURL, enrollmentID string, agentIDs []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Errorf("connect for task API cleanup: %v", err)
		return
	}
	defer connection.Close(ctx)
	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Errorf("begin task API cleanup: %v", err)
		return
	}
	defer tx.Rollback(ctx)
	for _, agentID := range agentIDs {
		for _, statement := range []string{
			`DELETE FROM tasks WHERE agent_id=$1`,
			`DELETE FROM config_revisions WHERE agent_id=$1`,
			`DELETE FROM configs WHERE agent_id=$1`,
			`DELETE FROM agent_nonces WHERE agent_id=$1`,
			`DELETE FROM agents WHERE id=$1`,
		} {
			if _, err := tx.Exec(ctx, statement, agentID); err != nil {
				t.Errorf("clean task API fixture agent %s: %v", agentID, err)
				return
			}
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM enrollment_tokens WHERE id=$1`, enrollmentID); err != nil {
		t.Errorf("clean task API enrollment token: %v", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("commit task API cleanup: %v", err)
	}
}
