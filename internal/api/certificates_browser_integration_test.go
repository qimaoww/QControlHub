package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
			frontendRoot := filepath.Join("..", "..", "frontend")
			mux := http.NewServeMux()
			mux.Handle("/api/", alice.handler)
			mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(frontendRoot))))
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				http.ServeFile(w, r, filepath.Join(frontendRoot, "index.html"))
			})
			server := httptest.NewServer(mux)
			defer server.Close()
			fixture, _ := json.Marshal(map[string]string{"shared": node.ID})
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
