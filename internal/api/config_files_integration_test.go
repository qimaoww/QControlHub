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

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestConfigFilesVersionedWithPostgreSQL(t *testing.T) {
	database := os.Getenv("QCH_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx := context.Background()
	schema, err := testdb.IsolatePostgres(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(ctx)
	db, err := store.OpenWithConfigKey(ctx, schema.URL, true, strings.Repeat("f", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enrollment, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "file scopes"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "files", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineXray}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, enrollment.Token)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Engine: core.EngineXray, Name: "files", Content: `{"inbounds":[{"tag":"a","port":1080}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"large":9007199254740993}`}, 0)
	if err != nil {
		t.Fatal(err)
	}
	const token = "configuration-files-integration-admin-token"
	handler := New(db, Config{AdminToken: token}).Handler()
	request := func(method string, body any, authorized bool) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/api/v1/agents/"+agent.ID+"/configs/xray/files", bytes.NewReader(data))
		if authorized {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request(http.MethodGet, nil, false); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated read: %d", got.Code)
	}
	response := request(http.MethodGet, nil, true)
	var bundle struct {
		Version int                       `json:"version"`
		Files   []serverconfig.ConfigFile `json:"files"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &bundle); err != nil || response.Code != 200 || len(bundle.Files) != 3 {
		t.Fatalf("read files: %d %s %v", response.Code, response.Body.String(), err)
	}
	bundle.Files[1].Content = `{"inbounds":[{"tag":"a","port":2080}]}`
	body := map[string]any{"name": "files", "version": saved.Version, "files": bundle.Files}
	response = request(http.MethodPut, body, true)
	if response.Code != 200 {
		t.Fatalf("save files: %d %s", response.Code, response.Body.String())
	}
	response = request(http.MethodPut, body, true)
	if response.Code != 409 {
		t.Fatalf("stale bundle accepted: %d %s", response.Code, response.Body.String())
	}
	got, err := db.AgentConfig(ctx, agent.ID, core.EngineXray)
	if err != nil || got.Version != saved.Version+1 || !strings.Contains(got.Content, "2080") || !strings.Contains(got.Content, "9007199254740993") {
		t.Fatalf("stored bundle: %+v %v", got, err)
	}
	bundle.Files[1].Path = "../escape.json"
	body["version"] = got.Version
	body["files"] = bundle.Files
	if response := request(http.MethodPut, body, true); response.Code != 400 {
		t.Fatalf("path traversal accepted: %d", response.Code)
	}
}
