package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestAccountingFieldMutationsWithPostgreSQL(t *testing.T) {
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
	db, err := store.OpenWithConfigKey(ctx, schema.URL, true, strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineMihomo, core.EngineShadowsocksRust} {
		t.Run(string(engine), func(t *testing.T) {
			enrollment, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "accounting fields"})
			if err != nil {
				t.Fatal(err)
			}
			agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "fields", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{engine}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, enrollment.Token)
			if err != nil {
				t.Fatal(err)
			}
			input, err := serverconfig.NewPlan(serverconfig.Protocols(engine)[0])
			if err != nil {
				t.Fatal(err)
			}
			input.Port, input.Tag = 21001, "one"
			content, err := serverconfig.Generate(engine, input)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := serverconfig.PlanPresetAccounting(engine, content)
			if err != nil {
				t.Fatal(err)
			}
			saved, err := db.SaveAgentConfig(ctx, core.Config{AgentID: agent.ID, Engine: engine, Name: "fields", Content: plan.Content}, 0)
			if err != nil {
				t.Fatal(err)
			}
			key, fragment := "outbounds", `[{"tag":"direct","protocol":"freedom","settings":{"domainStrategy":"UseIPv4"}}]`
			if engine == core.EngineSingBox {
				fragment = `[{"tag":"direct","type":"direct","bind_interface":"lo"}]`
			}
			if engine == core.EngineMihomo {
				key, fragment = "rules", "- DOMAIN,example.com,REJECT\n- MATCH,DIRECT\n"
			}
			if engine == core.EngineShadowsocksRust {
				key, fragment = "server_port", "21002"
			}
			body, _ := json.Marshal(map[string]any{"mutation": "modify", "fragment": fragment, "expected_version": saved.Version, "intent": "validate"})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agent.ID+"/configs/"+string(engine)+"/fields/"+key, bytes.NewReader(body))
			token := strings.Repeat("a", 48)
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			New(db, Config{AdminToken: token}).Handler().ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("field rejected: %d %s", w.Code, w.Body.String())
			}
			after, err := db.AgentConfig(ctx, agent.ID, engine)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := serverconfig.PrepareAccounting(engine, after.Content)
			if err != nil || compiled.Content != after.Content {
				t.Fatalf("saved stale compiled configuration: %v", err)
			}
			if after.Version != saved.Version+1 {
				t.Fatal("field mutation did not save exactly one revision")
			}
			// Simulate another save winning between a caller's save and enqueue.
			_, err = db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: engine, Action: core.ActionDeploy, ConfigID: saved.ID, ExpectedConfigVersion: saved.Version})
			if !errors.Is(err, store.ErrConflict) {
				t.Fatalf("stale save deployed a different version: %v", err)
			}
			task, err := db.CreateTask(ctx, core.TaskRequest{AgentID: agent.ID, Engine: engine, Action: core.ActionValidate, ConfigID: after.ID, ExpectedConfigVersion: after.Version})
			if err != nil || task.ConfigVersion != after.Version {
				t.Fatalf("matching task version: %+v %v", task, err)
			}
			if engine == core.EngineShadowsocksRust && compiled.Ports[0].Port != 21002 {
				t.Fatal("old port mark retained")
			}
			if engine != core.EngineShadowsocksRust {
				for _, enabled := range []bool{true, false, true} {
					body, _ := json.Marshal(map[string]any{"agent_id": agent.ID, "engine": engine, "tag": "one", "port": 21001, "block_mainland_source": enabled, "block_mainland_destination": enabled, "expected_version": after.Version, "intent": "validate"})
					r := httptest.NewRequest(http.MethodPut, "/api/v1/access-controls", bytes.NewReader(body))
					r.Header.Set("Authorization", "Bearer "+token)
					w := httptest.NewRecorder()
					New(db, Config{AdminToken: token}).Handler().ServeHTTP(w, r)
					if w.Code != http.StatusOK {
						t.Fatalf("access update: %d %s", w.Code, w.Body.String())
					}
					after, err = db.AgentConfig(ctx, agent.ID, engine)
					if err != nil {
						t.Fatal(err)
					}
					compiled, err = serverconfig.PrepareAccounting(engine, after.Content)
					if err != nil || compiled.Content != after.Content {
						t.Fatalf("access update left stale outbound mapping: %v", err)
					}
					policies := serverconfig.DiscoverMainlandAccessPolicies(engine, after.Content)
					if len(policies) != 1 || policies[0].BlockMainlandDestination != enabled || policies[0].BlockMainlandSource != enabled {
						t.Fatalf("access policy changed: %+v", policies)
					}
				}
			}
		})
	}
}
