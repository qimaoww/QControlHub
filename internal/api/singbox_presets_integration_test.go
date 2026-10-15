package api

import (
	"context"
	"encoding/json"
	"encoding/pem"
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

func TestSingBoxNativePresetSaveReadExportWithPostgreSQL(t *testing.T) {
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
	enrollment, err := db.CreateEnrollmentToken(ctx, core.EnrollmentTokenRequest{Name: "sing-box presets"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.EnrollAgent(ctx, core.EnrollRequest{Name: "sing-box matrix", OS: "linux", Arch: "amd64", Capabilities: []core.Engine{core.EngineSingBox}, Features: []string{core.AgentFeatureIndependentEgress}, PublicKey: authn.EncodePublicKey(randomEnrollmentKey(t))}, enrollment.Token)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Heartbeat(ctx, agent.ID, core.HeartbeatRequest{Features: []string{core.AgentFeatureIndependentEgress}, Runtime: map[core.Engine]core.RuntimeState{core.EngineSingBox: {Installed: true}}}); err != nil {
		t.Fatal(err)
	}
	const token = "sing-box-native-preset-test"
	handler := New(db, Config{AdminToken: token}).Handler()
	base := "/api/v1/agents/" + agent.ID + "/configs/sing-box"
	certificateServer := httptest.NewTLSServer(http.NotFoundHandler())
	defer certificateServer.Close()
	certificate := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateServer.Certificate().Raw}))
	version, port := 0, 21000
	for _, protocol := range serverconfig.Protocols(core.EngineSingBox) {
		// Reality's external DNS/TLS target probe already has dedicated API
		// tests; both Reality transports run in the hermetic live-core matrix.
		if protocol.UsesEndpoint || protocol.PortForward || protocol.UsesReality {
			continue
		}
		t.Run(protocol.Key, func(t *testing.T) {
			response := revisionAPIRequest(t, handler, token, http.MethodPost, base+"/plans", []byte(`{"protocol":"`+protocol.Key+`"}`))
			var input serverconfig.Input
			if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &input) != nil {
				t.Fatalf("plan: %d %s", response.Code, response.Body.String())
			}
			// Keep this matrix testing native TLS; managed defaults have a
			// separate Agent feature and auxiliary deployment snapshot matrix.
			input.TLSMode = "native"
			input.CertificatePath = "/etc/qcontrolhub/tls/server.crt"
			input.PrivateKeyPath = "/etc/qcontrolhub/tls/server.key"
			port++
			input.Port = port
			if input.TLSEnabled {
				input.TLSServerName = "tls.example.test"
				input.TLSClientCertificate = certificate
			}
			if input.Transport == "http" || input.Transport == "httpupgrade" || input.Transport == "websocket" {
				input.TransportHost = "http.example.test"
			}
			if input.TLSEnabled && input.Transport == "http" {
				input.TLSALPN = "h2"
			}
			if input.Transport == "http" {
				input.TransportHTTPMethod = "POST"
			}
			for _, operation := range []string{"add", "modify"} {
				original := ""
				if operation == "modify" {
					original = input.Tag
					input.TransportPath += "-edited"
					if input.Transport == "raw" || input.Transport == "quic" {
						input.TransportPath = ""
					}
				}
				body, _ := json.Marshal(map[string]any{"operation": operation, "original_tag": original, "expected_version": version, "intent": "deploy", "input": input})
				response = revisionAPIRequest(t, handler, token, http.MethodPost, base+"/server-inbounds", body)
				var result configMutationResult
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
					t.Fatalf("save: %d %s", response.Code, response.Body.String())
				}
				version++
				if result.Config.Version != version || result.Task.ConfigVersion != version {
					t.Fatal("task did not bind saved version")
				}
				if strings.Contains(result.Config.Content, certificate) {
					t.Fatal("client trust material was sent to server config")
				}
				response = revisionAPIRequest(t, handler, token, http.MethodGet, base+"/workspace", nil)
				var workspace struct {
					Inbounds []serverconfig.Input `json:"inbounds"`
				}
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &workspace) != nil {
					t.Fatal("workspace")
				}
				var restored *serverconfig.Input
				for index := range workspace.Inbounds {
					if workspace.Inbounds[index].Tag == input.Tag {
						restored = &workspace.Inbounds[index]
					}
				}
				if restored == nil || restored.Protocol != input.Protocol || restored.Transport != input.Transport || restored.TransportHost != input.TransportHost || restored.TransportHTTPMethod != input.TransportHTTPMethod || restored.TLSServerName != input.TLSServerName || restored.TLSALPN != input.TLSALPN || restored.TLSClientCertificate != input.TLSClientCertificate || restored.TransportPath != input.TransportPath {
					t.Fatalf("workspace lost saved parameters: %+v", restored)
				}
				if len(workspace.Inbounds) != (version+1)/2 {
					t.Fatal("editing removed another inbound")
				}
				profile, err := serverconfig.BuildClientProfile(*restored, "203.0.113.9", "")
				if err != nil {
					t.Fatal(err)
				}
				if input.TLSEnabled && profile.Format != "sing-box client JSON" {
					t.Fatal("trust certificate was lost in export")
				}
				if _, err := serverconfig.SharedTrafficEndpoints(core.EngineSingBox, result.Config.Content); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
