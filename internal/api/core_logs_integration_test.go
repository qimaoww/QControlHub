package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestCoreLogsLimitAppliesPerEngine(t *testing.T) {
	databaseURL := os.Getenv("QCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("requires PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema, err := testdb.IsolatePostgres(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer schema.Close(context.Background())
	dataStore, err := store.Open(ctx, schema.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dataStore.Close()
	engines := []core.Engine{core.EngineMihomo, core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust}
	enroll := func(name string) core.Agent {
		t.Helper()
		credential, err := dataStore.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		agent, err := dataStore.EnrollAgent(ctx, core.EnrollRequest{
			Name: name, OS: "linux", Arch: "amd64", Capabilities: engines,
			PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t)),
		}, credential.Token)
		if err != nil {
			t.Fatal(err)
		}
		return agent
	}
	primary, secondary := enroll("core-log-limit-primary"), enroll("core-log-limit-secondary")
	writeBatch := func(agentID string, entries []core.CoreLogEntry) {
		t.Helper()
		id, err := core.NewID("log")
		if err != nil {
			t.Fatal(err)
		}
		if err := dataStore.StoreCoreLogs(ctx, agentID, core.CoreLogBatch{ID: id, Entries: entries}); err != nil {
			t.Fatal(err)
		}
	}
	var entries []core.CoreLogEntry
	for _, engine := range engines {
		for _, suffix := range []string{"needle old", "other middle", "needle new"} {
			level := "warning"
			if suffix == "other middle" {
				level = "info"
			}
			entries = append(entries, core.CoreLogEntry{Engine: engine, Level: level, Message: string(engine) + " " + suffix})
		}
	}
	writeBatch(primary.ID, entries)
	writeBatch(primary.ID, []core.CoreLogEntry{
		{Engine: core.EngineXray, Level: "info", Message: "xray busy 0"},
		{Engine: core.EngineXray, Level: "info", Message: "xray busy 1"},
		{Engine: core.EngineXray, Level: "info", Message: "xray busy 2"},
	})
	entries = nil
	for _, engine := range engines {
		entries = append(entries, core.CoreLogEntry{Engine: engine, Level: "warning", Message: string(engine) + " foreign"})
	}
	writeBatch(secondary.ID, entries)
	adminToken := strings.Repeat("l", 48)
	handler := New(dataStore, Config{AdminToken: adminToken}).Handler()
	request := func(query string, wantStatus int) []core.CoreLogEntry {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/core-logs?"+query, nil)
		r.Header.Set("Authorization", "Bearer "+adminToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != wantStatus {
			t.Fatalf("query %q: status=%d, body=%s", query, w.Code, w.Body.String())
		}
		if wantStatus != http.StatusOK {
			return nil
		}
		var result []core.CoreLogEntry
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	all := request("limit=500", http.StatusOK)
	if len(all) != 19 {
		t.Fatalf("fixture entries=%d, want 19", len(all))
	}
	var before int64
	for _, entry := range all {
		if entry.Message == "xray busy 0" {
			before = entry.ID
		}
	}
	if before == 0 {
		t.Fatal("missing pagination fixture")
	}
	scope := "agent_id=" + primary.ID + "&"
	newest := []string{"xray busy 2", "ss-rust needle new", "sing-box needle new", "mihomo needle new"}
	warnings := []string{"ss-rust needle new", "sing-box needle new", "xray needle new", "mihomo needle new"}
	foreign := []string{"ss-rust foreign", "sing-box foreign", "xray foreign", "mihomo foreign"}
	for _, test := range []struct {
		name, query string
		want        []string
	}{
		{"all nodes share each engine's cap", "limit=1", foreign},
		{"all nodes two per engine", "limit=2", append(append([]string{}, foreign...), newest...)},
		{"busy engine cannot crowd out other engines", scope + "limit=1", newest},
		{"two per engine with descending merged order", scope + "limit=2", []string{"xray busy 2", "xray busy 1", "ss-rust needle new", "ss-rust other middle", "sing-box needle new", "sing-box other middle", "mihomo needle new", "mihomo other middle"}},
		{"single engine", scope + "engine=xray&limit=2", []string{"xray busy 2", "xray busy 1"}},
		{"level applied before limit", scope + "level=warning&limit=1", warnings},
		{"search applied before limit", scope + "q=NEEDLE&limit=1", warnings},
		{"cursor applied before limit", scope + fmt.Sprintf("before=%d&limit=1", before), warnings},
		{"empty matches", scope + "q=no-such-message&limit=1", []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := request(test.query, http.StatusOK)
			messages := make([]string, 0, len(result))
			for i, entry := range result {
				if i > 0 && result[i-1].ID <= entry.ID {
					t.Fatal("merged logs are not ordered newest first")
				}
				messages = append(messages, entry.Message)
			}
			if !reflect.DeepEqual(messages, test.want) {
				t.Fatalf("messages=%v, want %v", messages, test.want)
			}
		})
	}
	if got := request("", http.StatusOK); len(got) != len(all) {
		t.Fatalf("default limit returned %d entries, want %d", len(got), len(all))
	}
	for _, limit := range []string{"0", "-1", "501", "invalid"} {
		request("limit="+limit, http.StatusBadRequest)
	}
}
