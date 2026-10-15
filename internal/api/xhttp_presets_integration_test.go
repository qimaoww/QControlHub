package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/qimaoww/qcontrolhub/internal/authn"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
	"github.com/qimaoww/qcontrolhub/internal/testdb"
)

func TestXHTTPPresetSaveReloadSnapshotsWithPostgreSQL(t *testing.T) {
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
	db, err := store.OpenWithConfigKey(ctx, schema.URL, true, strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	enrollment, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "XHTTP presets"})
	if err != nil {
		t.Fatal(err)
	}
	features := []string{core.AgentFeatureIndependentEgress, core.AgentFeatureManagedTLS, core.AgentFeatureXHTTP}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "XHTTP matrix", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineXray}, Features: features, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, enrollment.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: features, Runtime: map[core.Engine]core.RuntimeState{core.EngineXray: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	const token = "xhttp-integration-test"
	handler := New(db, Config{AdminToken: token}).Handler()
	base := "/api/v1/agents/" + agent.ID + "/configs/xray"
	version := 0
	// Public Reality probes have hermetic DNS/TLS tests. Managed local targets
	// must pass this real API without invoking that public-only probe.
	for index, key := range []string{serverconfig.ProtocolXHTTPTLS, serverconfig.ProtocolXHTTPTLSReality, serverconfig.ProtocolXHTTPRealityTLS, serverconfig.ProtocolXHTTPFive} {
		t.Run(key, func(t *testing.T) {
			protocol, _ := serverconfig.FindProtocol(core.EngineXray, key)
			input, err := serverconfig.NewPlan(protocol)
			if err != nil {
				t.Fatal(err)
			}
			input.Port, input.ClientPort = 22000+index, 8443
			input.XHTTPBackendPort, input.XHTTPTLSPort = 32000+index, 33000+index
			if key == serverconfig.ProtocolXHTTPTLS {
				input.XHTTPBackendPort, input.XHTTPTLSPort = 0, 0
			}
			input.ClientAddress = "2001:db8::10"
			input.Listen = "::"
			input.ClientHost = "reality.example.test"
			input.XHTTPTLSAddress = "cdn.example.test"
			input.XHTTPTLSClientPort = 443
			input.XHTTPTLSHost = "tls.example.test"
			input.XHTTPExtra = `{"xmux":{"maxConnections":0,"maxConcurrency":"4-8","hMaxReusableSecs":"60-120"}}`
			var config core.Config
			for _, operation := range []string{"add", "modify", "delete"} {
				original := ""
				if operation != "add" {
					original = input.Tag
				}
				if operation == "modify" {
					input.Tag += "-edited"
					input.TransportPath += "-edited"
				}
				body, _ := json.Marshal(map[string]any{"operation": operation, "original_tag": original, "expected_version": version, "intent": "deploy", "input": input})
				response := revisionAPIRequest(t, handler, token, http.MethodPost, base+"/server-inbounds", body)
				var result configMutationResult
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
					t.Fatalf("%s: %d %s", operation, response.Code, response.Body.String())
				}
				version++
				config = result.Config
				if result.Config.Version != version || result.Task.ConfigVersion != version {
					t.Fatal("task revision mismatch")
				}
				response = revisionAPIRequest(t, handler, token, http.MethodGet, base+"/workspace", nil)
				var workspace struct {
					Inbounds []serverconfig.Input `json:"inbounds"`
				}
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &workspace) != nil {
					t.Fatal("workspace", response.Body.String())
				}
				if operation == "delete" {
					if len(workspace.Inbounds) != 0 {
						t.Fatal("orphaned managed backend")
					}
					continue
				}
				if len(workspace.Inbounds) != 1 {
					t.Fatalf("group ownership lost: %+v", workspace.Inbounds)
				}
				restored := workspace.Inbounds[0]
				if restored.Protocol != key || restored.Tag != input.Tag || restored.TransportPath != input.TransportPath || restored.XHTTPExtra != input.XHTTPExtra || restored.ClientAddress != input.ClientAddress || restored.ClientHost != input.ClientHost {
					t.Fatalf("workspace lost metadata: %+v", restored)
				}
				if key != serverconfig.ProtocolXHTTPTLS && (restored.XHTTPTLSAddress != input.XHTTPTLSAddress || restored.XHTTPTLSClientPort != 443 || restored.XHTTPTLSHost != input.XHTTPTLSHost) {
					t.Fatal("TLS endpoint metadata lost")
				}
				profile, err := serverconfig.BuildClientProfile(restored, "203.0.113.10", "")
				if err != nil || profile.Xray == "" {
					t.Fatal("export", err)
				}
				if key == serverconfig.ProtocolXHTTPFive && len(profile.Variants) != 5 {
					t.Fatal("five client modes lost")
				}
				metadata, err := db.ConfigClientMetadata(ctx, config.ID, version)
				if err != nil {
					t.Fatal(err)
				}
				frontends, err := serverconfig.ManagedTLSFrontends(core.EngineXray, config.Content, metadata)
				if err != nil {
					t.Fatal(err)
				}
				if (key == serverconfig.ProtocolXHTTPTLS) != (len(frontends) == 0) {
					t.Fatal("incorrect native/proxy dependency")
				}
				// The public task API must not reveal its encrypted execution snapshot.
				if result.Task.ConfigContent != "" || len(result.Task.TLSFrontends) != 0 {
					t.Fatal("private execution snapshot leaked")
				}
			}
		})
	}
}
