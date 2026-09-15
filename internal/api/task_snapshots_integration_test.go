package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

func TestTaskAPIPrefersRecentConfigurationSnapshot(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := store.OpenWithConfigKey(ctx, databaseURL, true, strings.Repeat("cache-test-key-", 3))
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()
	enrollment, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{
		Name: "task API cached read", TTLMinutes: 5, MaxUses: 1,
	})
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	agent := enrollTaskAPIAgent(t, ctx, dataStore, enrollment.Token, "task-api-cached-read")
	t.Cleanup(func() { cleanupTaskAPIFixture(t, databaseURL, enrollment.ID, []string{agent.ID}) })

	read, err := dataStore.CreateTask(ctx, core.TaskRequest{
		AgentID: agent.ID, Action: core.ActionReadConfig, Engine: core.EngineMihomo,
	})
	if err != nil {
		t.Fatalf("create initial read task: %v", err)
	}
	claimed, err := dataStore.ClaimTask(ctx, agent.ID)
	if err != nil || claimed == nil || claimed.ID != read.ID {
		t.Fatalf("claim initial read task = %+v, %v", claimed, err)
	}
	content := "mixed-port: 7890\nmode: rule\nproxies: []\n"
	if err := dataStore.CompleteTask(ctx, agent.ID, read.ID, core.TaskResultRequest{
		LeaseID: claimed.LeaseID, Success: true, Output: content,
	}); err != nil {
		t.Fatalf("complete initial read task: %v", err)
	}

	adminToken := strings.Repeat("c", 48)
	handler := New(dataStore, Config{AdminToken: adminToken}).Handler()
	post := func(payload any) *httptest.ResponseRecorder {
		t.Helper()
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode task request: %v", err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(encoded))
		request.Header.Set("Authorization", "Bearer "+adminToken)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(ctx)

	cachedResponse := post(map[string]any{
		"agent_id": agent.ID, "action": core.ActionReadConfig, "engine": core.EngineMihomo,
		"prefer_cached": true,
	})
	if cachedResponse.Code != http.StatusOK {
		t.Fatalf("cached read status=%d body=%s", cachedResponse.Code, cachedResponse.Body.String())
	}
	var cached core.Task
	if err := json.Unmarshal(cachedResponse.Body.Bytes(), &cached); err != nil ||
		!cached.Reused || cached.ID != read.ID || cached.Status != core.TaskSucceeded || cached.ConfigContent != "" {
		t.Fatalf("cached read task = %+v, %v", cached, err)
	}
	snapshotResponse := taskAPIRequest(t, handler, adminToken, http.MethodGet, "/api/v1/tasks/"+cached.ID+"/config-snapshot")
	if snapshotResponse.Code != http.StatusOK || !strings.Contains(snapshotResponse.Body.String(), "mixed-port") {
		t.Fatalf("cached snapshot status=%d body=%s", snapshotResponse.Code, snapshotResponse.Body.String())
	}

	if response := post(map[string]any{
		"agent_id": agent.ID, "action": core.ActionStatus, "engine": core.EngineMihomo,
		"prefer_cached": true,
	}); response.Code != http.StatusBadRequest {
		t.Fatalf("non-read cache hint status=%d body=%s", response.Code, response.Body.String())
	}
	for name, extra := range map[string]map[string]any{
		"expected revision": {"expected_config_version": 1},
		"automatic install": {"install_if_missing": true},
		"TCP settings":      {"tcp_settings": map[string]string{"net.core.somaxconn": "4096"}},
		"install source":    {"core_source": "mirror"},
	} {
		t.Run(name, func(t *testing.T) {
			payload := map[string]any{
				"agent_id": agent.ID, "action": core.ActionReadConfig, "engine": core.EngineMihomo,
				"prefer_cached": true,
			}
			for key, value := range extra {
				payload[key] = value
			}
			if response := post(payload); response.Code != http.StatusBadRequest {
				t.Fatalf("cached read bypassed validation: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	for _, test := range []struct {
		name, update, restore string
		status                int
	}{
		{"disabled engine", `UPDATE agents SET capabilities='[]' WHERE id=$1`,
			`UPDATE agents SET capabilities='["mihomo"]' WHERE id=$1`, http.StatusBadRequest},
		{"unsafe existing service", `UPDATE agents SET runtime='{"mihomo":{"existing_config_unsupported_reason":"ambiguous service"}}' WHERE id=$1`,
			`UPDATE agents SET runtime='{}' WHERE id=$1`, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := connection.Exec(ctx, test.update, agent.ID); err != nil {
				t.Fatal(err)
			}
			defer connection.Exec(ctx, test.restore, agent.ID)
			if response := post(map[string]any{
				"agent_id": agent.ID, "action": core.ActionReadConfig, "engine": core.EngineMihomo,
				"prefer_cached": true,
			}); response.Code != test.status {
				t.Fatalf("cached read bypassed eligibility: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	t.Run("unreadable cache falls back", func(t *testing.T) {
		var ciphertext string
		if err := connection.QueryRow(ctx, `SELECT config_content FROM tasks WHERE id=$1`, read.ID).Scan(&ciphertext); err != nil {
			t.Fatal(err)
		}
		defer connection.Exec(ctx, `UPDATE tasks SET config_content=$2 WHERE id=$1`, read.ID, ciphertext)
		for _, broken := range []string{"rf2:missing-key:unreadable", "rf2:malformed", ""} {
			if _, err := connection.Exec(ctx, `UPDATE tasks SET config_content=$2 WHERE id=$1`, read.ID, broken); err != nil {
				t.Fatal(err)
			}
			response := post(map[string]any{
				"agent_id": agent.ID, "action": core.ActionReadConfig, "engine": core.EngineMihomo,
				"prefer_cached": true,
			})
			if response.Code != http.StatusCreated {
				t.Errorf("unreadable cached snapshot did not trigger a read: status=%d body=%s", response.Code, response.Body.String())
				continue
			}
			var fresh core.Task
			if err := json.Unmarshal(response.Body.Bytes(), &fresh); err != nil || fresh.ID == read.ID || fresh.Reused || fresh.Status != core.TaskPending || fresh.ConfigContent != "" {
				t.Fatalf("fallback task = %+v, %v", fresh, err)
			}
			if err := dataStore.CancelTask(ctx, fresh.ID); err != nil {
				t.Fatal(err)
			}
		}
	})
	freshResponse := post(core.TaskRequest{
		AgentID: agent.ID, Action: core.ActionReadConfig, Engine: core.EngineMihomo,
	})
	if freshResponse.Code != http.StatusCreated {
		t.Fatalf("forced fresh read status=%d body=%s", freshResponse.Code, freshResponse.Body.String())
	}
	var fresh core.Task
	if err := json.Unmarshal(freshResponse.Body.Bytes(), &fresh); err != nil || fresh.ID == read.ID || fresh.Reused || fresh.Status != core.TaskPending {
		t.Fatalf("forced fresh read task = %+v, %v", fresh, err)
	}
}
