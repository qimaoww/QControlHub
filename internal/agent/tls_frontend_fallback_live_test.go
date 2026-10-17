package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
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
	for _, hang := range []bool{false, true} {
		t.Run(fmt.Sprintf("timeout_%t", hang), func(t *testing.T) { testManagedTLSIssuerFallback(t, hang) })
	}
}

func testManagedTLSIssuerFallback(t *testing.T, hang bool) {
	binary := os.Getenv("QCH_CADDY_BINARY")
	if binary == "" {
		t.Skip("requires actual Caddy binary")
	}
	var requests atomic.Int32
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if hang {
			<-r.Context().Done()
			return
		}
		http.Error(w, "primary certificate authority unavailable", http.StatusServiceUnavailable)
	}))
	defer unavailable.Close()
	dir := t.TempDir()
	challengePort := singBoxFreePort(t)
	authority, caPEM, orders := mockTLSACME(t, "fallback.example.test", challengePort)
	f := core.TLSFrontend{Tag: "fallback", Mode: "caddy", Domain: "fallback.example.test", PublicPort: singBoxFreePort(t), BackendPort: singBoxFreePort(t), Transport: "websocket", Path: "/proxy", ACMEEmail: "certs@example.test"}
	value, err := renderCaddyFrontends([]core.TLSFrontend{f}, dir)
	if err != nil {
		t.Fatal(err)
	}
	// An outgoing frontend may still own this port. Certificate preparation
	// must complete HTTP-01 before it replaces that listener or its routes.
	occupied, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(f.PublicPort)))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	var committed map[string]any
	_ = json.Unmarshal([]byte(value), &committed)
	relocateCaddyChallengeListener(t, committed, challengePort)
	value, err = certificatePreparationRoutes(value, nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = json.Unmarshal([]byte(value), &root)
	relocateCaddyChallengeListener(t, root, challengePort)
	apps := root["apps"].(map[string]any)
	policy := apps["tls"].(map[string]any)["automation"].(map[string]any)["policies"].([]any)[0].(map[string]any)
	issuers := policy["issuers"].([]any)
	primary := issuers[0].(map[string]any)
	primary["ca"], primary["test_ca"] = unavailable.URL+"/directory", unavailable.URL+"/directory"
	secondary := issuers[1].(map[string]any)
	secondary["ca"], secondary["test_ca"] = authority.URL+"/directory", authority.URL+"/directory"
	secondary["challenges"] = map[string]any{"http": map[string]any{"alternate_port": challengePort}, "tls-alpn": map[string]any{"disabled": true}}
	apps["http"].(map[string]any)["http_port"] = challengePort
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
	admin := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "caddy-admin.sock"))
	}}, Timeout: 2 * time.Second}
	defer admin.CloseIdleConnections()
	load := func(root map[string]any) error {
		data, _ := json.Marshal(root)
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/load", bytes.NewReader(data))
		request.Header.Set("Content-Type", "application/json")
		response, err := admin.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			data, _ := io.ReadAll(response.Body)
			return fmt.Errorf("reload: %s", data)
		}
		return nil
	}
	switched := false
	applied := false
	for ctx.Err() == nil {
		if hang && !switched && requests.Load() > 0 {
			// The production watchdog allows 65s; shorten only this fixture's wait.
			policy["issuers"] = []any{secondary}
			if err := load(root); err != nil {
				t.Fatal(err)
			}
			switched = true
		}
		if !applied {
			certificates, _ := filepath.Glob(filepath.Join(dir, "caddy", "certificates", "*", f.Domain, f.Domain+".crt"))
			if len(certificates) == 0 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			_ = occupied.Close()
			apps["http"].(map[string]any)["servers"] = committed["apps"].(map[string]any)["http"].(map[string]any)["servers"]
			if err := load(root); err != nil {
				t.Fatal(err)
			}
			applied = true
		}
		ca := caPEM
		{
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
				if summary := caddyFailureLog(dir, time.Time{}); !hang && !strings.Contains(summary, "primary certificate authority unavailable") {
					t.Fatalf("primary CA error missing from diagnostics: %s", summary)
				}
				if orders.Load() != 1 {
					t.Fatalf("expected one local ACME order, got %d", orders.Load())
				}
				policy["issuers"] = issuers
				if err := load(root); err != nil {
					t.Fatal(err)
				}
				time.Sleep(150 * time.Millisecond)
				if orders.Load() != 1 {
					t.Fatal("reloading issuer chain did not reuse certificate/account state")
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
