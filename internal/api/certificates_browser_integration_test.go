package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Exercise browser form serialization and account-scoped selectors with actual
// password/CSRF sessions and PostgreSQL. CA/vendor contracts have separate gates.
func TestCertificateBrowserSessions(t *testing.T) {
	if os.Getenv("QCH_MULTI_USER_BROWSER") != "1" {
		t.Skip("enable with QCH_MULTI_USER_BROWSER=1")
	}
	for _, mode := range []string{"desktop", "mobile"} {
		t.Run(mode, func(t *testing.T) {
			db, ctx, _, alice, bob := newConfigScopeAPIFixture(t)
			node := enrollConfigScopeAPIAgent(t, ctx, db)
			features := append(node.Features, core.AgentFeatureCertificates, core.AgentFeatureSharedCoreInstances, core.AgentFeatureSharedCorePrepare)
			if err := db.Heartbeat(ctx, node.ID, core.HeartbeatRequest{Features: features}); err != nil {
				t.Fatal(err)
			}
			grantConfigScopeAPIAgent(t, ctx, db, node, bob, 24443)
			native := enrollNativeCertificateNode(t, ctx, db, alice)
			grantConfigScopeAPIAgent(t, ctx, db, native, bob, 24444)
			var sharing core.AgentSharing
			alice.call("GET", "/agents/"+native.ID+"/sharing", nil, 200, &sharing)
			root := "/etc/qagent/sing-box/shares/" + sharing.Shares[0].ID + "/"
			deployNativeCertificate(t, ctx, db, native, alice, core.EngineMihomo, "/etc/tls/browser.pem", "/etc/tls/browser.key", 24443)
			deployNativeCertificate(t, ctx, db, native, bob, core.EngineSingBox, root+"cert.pem", root+"key.pem", 24444)
			var owned, shared []core.ManagedCertificateSource
			alice.call("GET", "/certificate-managed-sources", nil, 200, &owned)
			bob.call("GET", "/certificate-managed-sources", nil, 200, &shared)
			if len(owned) != 1 || len(shared) != 1 {
				t.Fatal("missing native browser sources")
			}
			// Simulated Agent reports; real file guards and authenticated Agent
			// transport have independent regressions. Never pass keys to Chromium.
			pair, err := certificates.SelfSigned([]string{"edge.example.test"}, "ecdsa256", 90)
			if err != nil {
				t.Fatal(err)
			}
			pumpCtx, stop := context.WithCancel(ctx)
			pumpDone := make(chan struct{})
			go func() {
				defer close(pumpDone)
				ticker := time.NewTicker(50 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-pumpCtx.Done():
						return
					case <-ticker.C:
						sources, e := db.AgentManagedCertificateSources(pumpCtx, native.ID)
						if e != nil {
							return
						}
						for _, src := range sources {
							_ = db.ImportManagedCertificate(pumpCtx, native.ID, core.ManagedCertificateReport{SourceID: src.ID, TaskID: src.TaskID, Certificate: pair.Certificate, PrivateKey: pair.PrivateKey})
						}
					}
				}
			}()
			defer func() { stop(); <-pumpDone }()
			frontendRoot := filepath.Join("..", "..", "frontend")
			mux := http.NewServeMux()
			mux.Handle("/api/", alice.handler)
			mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(frontendRoot))))
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				http.ServeFile(w, r, filepath.Join(frontendRoot, "index.html"))
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			fixture, _ := json.Marshal(map[string]string{"shared": node.ID, "native": native.ID, "owner_source": owned[0].ID, "shared_source": shared[0].ID})
			command := exec.CommandContext(ctx, "node", filepath.Join(frontendRoot, "certificate_browser_session.mjs"), server.URL, mode)
			command.Env = append(os.Environ(), "QCH_CERTIFICATE_FIXTURE="+string(fixture))
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("certificate browser: %v\n%s", err, output)
			}
			t.Log(string(output))
		})
	}
}
