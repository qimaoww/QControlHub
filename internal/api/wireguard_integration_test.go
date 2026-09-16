package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestWireGuardPresetLifecycleWithPostgreSQL(t *testing.T) {
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
	db, err := store.OpenWithConfigKey(ctx, schema.URL, true, strings.Repeat("w", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sql, err := pgx.Connect(ctx, schema.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close(ctx)
	const token = "wireguard-preset-integration-token"
	handler := New(db, Config{AdminToken: token}).Handler()
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox} {
		t.Run(string(engine), func(t *testing.T) {
			enrollment, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "WireGuard lifecycle"})
			if err != nil {
				t.Fatal(err)
			}
			agent, err := db.EnrollAgent(ctx, core.EnrollRequest{
				Name: "WireGuard", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{engine},
				Features:  []string{core.AgentFeatureIndependentEgress},
				PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t)),
			}, enrollment.Token)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{
				Features: []string{core.AgentFeatureIndependentEgress}, Runtime: map[core.Engine]core.RuntimeState{engine: {Installed: true}},
			}); err != nil {
				t.Fatal(err)
			}
			base := "/api/v1/agents/" + agent.ID + "/configs/" + string(engine)
			version := 0
			var current core.Config
			call := func(operation, original string, input serverconfig.Input, expected, status int) configMutationResult {
				t.Helper()
				body, _ := json.Marshal(map[string]any{
					"operation": operation, "original_tag": original, "input": input, "expected_version": expected, "intent": "validate",
				})
				response := revisionAPIRequest(t, handler, token, http.MethodPost, base+"/server-inbounds", body)
				if response.Code != status {
					t.Fatalf("%s %s: status=%d body=%s", engine, operation, response.Code, response.Body.String())
				}
				var result configMutationResult
				if status == http.StatusOK {
					if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					version++
					current = result.Config
					if current.Version != version || result.Task.ConfigVersion != version || result.Task.ConfigContent != "" {
						t.Fatal("save/task did not bind the exact sanitized version")
					}
				}
				return result
			}
			workspaceInput := func(tag string) serverconfig.Input {
				t.Helper()
				response := revisionAPIRequest(t, handler, token, http.MethodGet, base+"/workspace", nil)
				var workspace agentConfigWorkspaceResource
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &workspace) != nil {
					t.Fatalf("workspace: status=%d", response.Code)
				}
				for _, input := range workspace.Inbounds {
					if input.Tag == tag {
						return input
					}
				}
				t.Fatalf("workspace lost %s", tag)
				return serverconfig.Input{}
			}

			legacy, err := serverconfig.NewPlan(serverconfig.Protocols(engine)[0])
			if err != nil {
				t.Fatal(err)
			}
			legacy.Tag, legacy.Port = "legacy", 21001
			call("add", "", legacy, version, http.StatusOK)
			protocol, ok := serverconfig.FindProtocol(engine, serverconfig.ProtocolWireGuard)
			if !ok {
				t.Fatal("WireGuard preset missing")
			}
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.Tag, input.Port, input.WireGuardKeepalive = "wg", 21002, 0
			saved := call("add", "", input, version, http.StatusOK)
			if len(serverconfig.ParseAll(engine, current.Content)) != 2 || strings.Contains(current.Content, input.WireGuardClientPrivateKey) {
				t.Fatal("endpoint save lost legacy inbound or leaked client private key")
			}
			task, err := db.GetTask(ctx, saved.Task.ID)
			if err != nil || strings.Contains(task.ConfigContent, input.WireGuardClientPrivateKey) || task.ConfigVersion != version {
				t.Fatal("Agent task leaked client metadata or selected another version")
			}
			restored := workspaceInput(input.Tag)
			if restored.WireGuardClientPrivateKey != input.WireGuardClientPrivateKey || restored.WireGuardKeepalive != 0 {
				t.Fatal("workspace did not hydrate the exact client metadata")
			}
			var encrypted string
			if err := sql.QueryRow(ctx, `SELECT content FROM config_client_metadata WHERE config_id=$1 AND config_version=$2 AND profile_tag=$3`,
				current.ID, version, input.Tag).Scan(&encrypted); err != nil || strings.Contains(encrypted, input.WireGuardClientPrivateKey) {
				t.Fatal("client metadata was not encrypted at rest")
			}
			filesResponse := revisionAPIRequest(t, handler, token, http.MethodGet, base+"/files", nil)
			var bundle struct {
				Files []serverconfig.ConfigFile `json:"files"`
			}
			if filesResponse.Code != http.StatusOK || json.Unmarshal(filesResponse.Body.Bytes(), &bundle) != nil || len(bundle.Files) != 3 {
				t.Fatal("WireGuard configuration is not exposed as a paired bundle")
			}
			expectedPath := "inbounds/wg.json"
			if engine == core.EngineSingBox {
				expectedPath = "endpoints/wg.json"
			}
			if bundle.Files[2].Path != expectedPath || !strings.Contains(bundle.Files[2].Content, "qch-trf-21002") {
				t.Fatal("WireGuard source file lost its dedicated exit")
			}
			duplicate := legacy
			duplicate.Tag, duplicate.Port = "other", input.Port
			call("add", "", duplicate, version, http.StatusBadRequest)
			duplicate.Tag, duplicate.Port = input.Tag, 21004
			call("add", "", duplicate, version, http.StatusBadRequest)
			call("modify", input.Tag, input, version-1, http.StatusConflict)

			before := current
			next, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			next.Tag, next.Port = "renamed", 21003
			// This trigger fails after config, revision and metadata writes.
			if _, err := sql.Exec(ctx, `CREATE OR REPLACE FUNCTION reject_wireguard_task() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test WireGuard task failure'; END $$;
				CREATE TRIGGER reject_wireguard_task BEFORE INSERT ON tasks FOR EACH ROW EXECUTE FUNCTION reject_wireguard_task()`); err != nil {
				t.Fatal(err)
			}
			call("modify", input.Tag, next, version, http.StatusInternalServerError)
			call("delete", input.Tag, serverconfig.Input{}, version, http.StatusInternalServerError)
			unchanged, err := db.AgentConfig(ctx, agent.ID, engine)
			if err != nil || unchanged.Version != before.Version || unchanged.Content != before.Content {
				t.Fatal("failed task changed saved WireGuard configuration")
			}
			if _, err := db.ConfigRevision(ctx, current.ID, version+1); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("failed task leaked a configuration revision")
			}
			if metadata, err := db.ConfigClientMetadata(ctx, current.ID, version+1); err != nil || len(metadata) != 0 {
				t.Fatal("failed task leaked client metadata")
			}
			if _, err := sql.Exec(ctx, `DROP TRIGGER reject_wireguard_task ON tasks`); err != nil {
				t.Fatal(err)
			}
			call("modify", input.Tag, next, version, http.StatusOK)
			renamedVersion := version
			if restored := workspaceInput(next.Tag); restored.WireGuardClientPrivateKey != next.WireGuardClientPrivateKey {
				t.Fatal("renamed endpoint inherited old client private material")
			}
			oldMetadata, err := db.ConfigClientMetadata(ctx, current.ID, before.Version)
			if err != nil || !strings.Contains(oldMetadata[input.Tag], input.WireGuardClientPrivateKey) {
				t.Fatal("rename changed historical client metadata")
			}
			newMetadata, err := db.ConfigClientMetadata(ctx, current.ID, version)
			if err != nil || newMetadata[input.Tag] != "" || !strings.Contains(newMetadata[next.Tag], next.WireGuardClientPrivateKey) {
				t.Fatal("rename did not atomically move metadata to its new identity")
			}
			rotated, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			source := strings.Replace(current.Content, next.WireGuardClientPublicKey, rotated.WireGuardClientPublicKey, 1)
			body, _ := json.Marshal(map[string]any{"name": current.Name, "content": source, "version": version, "intent": "validate"})
			response := revisionAPIRequest(t, handler, token, http.MethodPost, base+"/source", body)
			if response.Code != http.StatusOK {
				t.Fatalf("source rotation: %d %s", response.Code, response.Body.String())
			}
			version++
			if restored := workspaceInput(next.Tag); restored.WireGuardClientPrivateKey != "" {
				t.Fatal("source rotation reused a stale client private key")
			}
			body, _ = json.Marshal(map[string]int{"expected_version": version})
			response = revisionAPIRequest(t, handler, token, http.MethodPost,
				"/api/v1/configs/"+current.ID+"/revisions/"+strconv.Itoa(renamedVersion)+"/restore", body)
			if response.Code != http.StatusOK {
				t.Fatalf("restore: %d %s", response.Code, response.Body.String())
			}
			version++
			if restored := workspaceInput(next.Tag); restored.WireGuardClientPrivateKey != next.WireGuardClientPrivateKey {
				t.Fatal("revision restore mixed old configuration with new client metadata")
			}
			call("delete", next.Tag, serverconfig.Input{}, version, http.StatusOK)
			if metadata, err := db.ConfigClientMetadata(ctx, current.ID, version); err != nil || metadata[next.Tag] != "" {
				t.Fatal("endpoint deletion retained current client metadata")
			}
			if inputs := serverconfig.ParseAll(engine, current.Content); len(inputs) != 1 || inputs[0].Tag != legacy.Tag {
				t.Fatal("endpoint deletion removed the legacy inbound")
			}
			call("delete", legacy.Tag, serverconfig.Input{}, version, http.StatusOK)
			if plan, err := serverconfig.PrepareIndependentEgress(engine, current.Content); err != nil || plan.Source != "disabled" {
				t.Fatalf("final deletion left an undeployable configuration: %v", err)
			}
		})
	}
}
