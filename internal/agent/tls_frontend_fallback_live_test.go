package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Exercise Caddy's actual issuer failover without creating public CA accounts
// in CI. Only the second issuer is replaced by a local test CA; the client
// explicitly trusts that CA and still checks the certificate hostname.
func TestManagedTLSIssuerFallbackLive(t *testing.T) {
	binary := os.Getenv("QCH_CADDY_BINARY")
	if binary == "" {
		t.Skip("requires actual Caddy binary")
	}
	var requests atomic.Int32
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "primary certificate authority unavailable", http.StatusServiceUnavailable)
	}))
	defer unavailable.Close()
	dir := t.TempDir()
	f := core.TLSFrontend{Tag: "fallback", Mode: "caddy", Domain: "fallback.example.test", PublicPort: singBoxFreePort(t), BackendPort: singBoxFreePort(t), Transport: "websocket", Path: "/proxy", ACMEEmail: "certs@example.test"}
	value, err := renderCaddyFrontends([]core.TLSFrontend{f}, dir)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = json.Unmarshal([]byte(value), &root)
	apps := root["apps"].(map[string]any)
	policy := apps["tls"].(map[string]any)["automation"].(map[string]any)["policies"].([]any)[0].(map[string]any)
	issuers := policy["issuers"].([]any)
	primary := issuers[0].(map[string]any)
	primary["ca"], primary["test_ca"] = unavailable.URL+"/directory", unavailable.URL+"/directory"
	issuers[1] = map[string]any{"module": "internal"}
	apps["pki"] = map[string]any{"certificate_authorities": map[string]any{"local": map[string]any{"install_trust": false}}}
	data, _ := json.Marshal(root)
	path := filepath.Join(dir, "caddy.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "run", "--config", path)
	command.Stdout, command.Stderr = log, log
	command.Env = append(os.Environ(), "XDG_DATA_HOME="+dir, "XDG_CONFIG_HOME="+dir)
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait(); _ = log.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var lastErr error
	for ctx.Err() == nil {
		ca, readErr := os.ReadFile(filepath.Join(dir, "caddy", "pki", "authorities", "local", "root.crt"))
		if readErr == nil {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(ca) {
				t.Fatal("invalid test CA")
			}
			dialer := tls.Dialer{Config: &tls.Config{RootCAs: pool, ServerName: f.Domain, MinVersion: tls.VersionTLS12}, NetDialer: &net.Dialer{Timeout: time.Second}}
			conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(f.PublicPort)))
			lastErr = err
			if err == nil {
				conn.Close()
				if requests.Load() == 0 {
					t.Fatal("certificate was issued without trying the primary CA")
				}
				if summary := caddyFailureLog(dir, time.Time{}); !strings.Contains(summary, "primary certificate authority unavailable") {
					t.Fatalf("primary CA error missing from diagnostics: %s", summary)
				}
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	processLog, _ := os.ReadFile(filepath.Join(dir, "process.log"))
	caddyLog, _ := os.ReadFile(filepath.Join(dir, "caddy.log"))
	t.Fatal(fmt.Errorf("issuer fallback did not yield a trusted certificate: %v\n%s\n%s", lastErr, processLog, caddyLog))
}
