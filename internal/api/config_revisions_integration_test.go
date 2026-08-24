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

func TestConfigRevisionAPIWithPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dataStore, err := store.Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer dataStore.Close()
	created, err := dataStore.CreateConfig(ctx, core.Config{
		Name: "revision API integration", Engine: core.EngineXray,
		Content: `{"log":{"loglevel":"warning"},"inbounds":[],"outbounds":[]}`,
	})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}
	defer cleanupRevisionAPIConfig(databaseURL, created.ID)
	updated, err := dataStore.UpdateConfig(ctx, created.ID, core.Config{
		Version: 1, Name: "revision API integration v2", Engine: core.EngineXray,
		Content: `{"log":{"loglevel":"info"},"inbounds":[],"outbounds":[]}`,
	})
	if err != nil {
		t.Fatalf("update config: %v", err)
	}

	adminToken := strings.Repeat("r", 48)
	handler := New(dataStore, Config{AdminToken: adminToken}).Handler()
	listResponse := revisionAPIRequest(t, handler, adminToken, http.MethodGet,
		"/api/v1/configs/"+created.ID+"/revisions?limit=10", nil)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list revisions status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var revisions []core.Config
	if err := json.Unmarshal(listResponse.Body.Bytes(), &revisions); err != nil || len(revisions) != 2 || revisions[0].Version != 2 || revisions[1].Version != 1 {
		t.Fatalf("list revisions = %+v, %v", revisions, err)
	}

	getResponse := revisionAPIRequest(t, handler, adminToken, http.MethodGet,
		"/api/v1/configs/"+created.ID+"/revisions/1", nil)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get revision status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}
	var first core.Config
	if err := json.Unmarshal(getResponse.Body.Bytes(), &first); err != nil || first.Version != 1 || first.Content != created.Content {
		t.Fatalf("get revision = %+v, %v", first, err)
	}

	restoreBody, _ := json.Marshal(map[string]int{"expected_version": updated.Version})
	restoreResponse := revisionAPIRequest(t, handler, adminToken, http.MethodPost,
		"/api/v1/configs/"+created.ID+"/revisions/1/restore", restoreBody)
	if restoreResponse.Code != http.StatusOK {
		t.Fatalf("restore revision status=%d body=%s", restoreResponse.Code, restoreResponse.Body.String())
	}
	var restored core.Config
	if err := json.Unmarshal(restoreResponse.Body.Bytes(), &restored); err != nil || restored.Version != 3 || restored.Content != created.Content {
		t.Fatalf("restore revision = %+v, %v", restored, err)
	}

	staleResponse := revisionAPIRequest(t, handler, adminToken, http.MethodPost,
		"/api/v1/configs/"+created.ID+"/revisions/2/restore", restoreBody)
	if staleResponse.Code != http.StatusConflict {
		t.Fatalf("stale restore status=%d body=%s", staleResponse.Code, staleResponse.Body.String())
	}
	missingID, _ := core.NewID("cfg")
	missingResponse := revisionAPIRequest(t, handler, adminToken, http.MethodGet,
		"/api/v1/configs/"+missingID+"/revisions", nil)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing list status=%d body=%s", missingResponse.Code, missingResponse.Body.String())
	}
}

func TestEncryptedConfigRevisionAPIFailsClosedWithoutKey(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("QCH_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	key := strings.Repeat("c", 32)
	encryptedStore, err := store.OpenWithConfigKey(ctx, databaseURL, true, key)
	if err != nil {
		t.Fatal(err)
	}
	config, err := encryptedStore.CreateConfig(ctx, core.Config{
		Name: "missing-key API config", Engine: core.EngineXray,
		Content: `{"log":{"loglevel":"warning"},"inbounds":[],"outbounds":[]}`,
	})
	if err != nil {
		encryptedStore.Close()
		t.Fatal(err)
	}
	defer func() {
		_ = encryptedStore.DeleteConfig(context.Background(), config.ID)
		encryptedStore.Close()
	}()
	plainStore, err := store.Open(ctx, databaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer plainStore.Close()
	adminToken := strings.Repeat("p", 48)
	response := revisionAPIRequest(t, New(plainStore, Config{AdminToken: adminToken}).Handler(), adminToken,
		http.MethodGet, "/api/v1/configs/"+config.ID+"/revisions/1", nil)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "rf2:") || strings.Contains(response.Body.String(), "loglevel") {
		t.Fatalf("missing-key revision response status=%d body=%s", response.Code, response.Body.String())
	}
}

func revisionAPIRequest(t *testing.T, handler http.Handler, token, method, target string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func cleanupRevisionAPIConfig(databaseURL, configID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return
	}
	defer connection.Close(ctx)
	_, _ = connection.Exec(ctx, `DELETE FROM config_revisions WHERE config_id=$1`, configID)
	_, _ = connection.Exec(ctx, `DELETE FROM tasks WHERE config_id=$1`, configID)
	_, _ = connection.Exec(ctx, `DELETE FROM configs WHERE id=$1`, configID)
}
