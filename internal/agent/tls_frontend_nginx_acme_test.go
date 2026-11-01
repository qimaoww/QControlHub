package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Invoked by both existing init-system sandboxes. The isolated CA is a fixture;
// ACME, Caddy-to-Nginx service switching, renewal, validation/reload and
// WebSocket forwarding use actual clients, init services and proxy binaries.
func TestManagedTLSNginxACMESandbox(t *testing.T) {
	if os.Getenv("QCH_TEST_NGINX_ACME") != "1" {
		t.Skip("requires disposable managed TLS helper sandbox")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires disposable Docker node")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	kind := os.Getenv("QCH_TEST_TLS_MANAGER")
	if kind == "" {
		kind = ServiceManagerSystemd
	}
	services, err := NewServiceManager(kind)
	if err != nil {
		t.Fatal(err)
	}
	m := tlsFrontendManager{tlsFrontendRoot, tlsFrontendState, services, NewCoreUpdater()}
	root, state := m.root, m.state
	// The preceding persistence fixture left its verified internal Caddy config.
	// Start the real existing service, then switch the same node to Nginx.
	caddyService, _, _ := m.serviceDefinition("caddy", false)
	if _, err := services.command(ctx, caddyService, core.ActionStart); err != nil {
		t.Fatal(err)
	}
	if err := setServiceEnabled(ctx, caddyService, true, services); err != nil {
		t.Fatal(err)
	}
	ca, caPEM, orders := mockTLSACME(t, "auto.example.test", 80, 8*time.Second)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("test root")
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/nginx-ws" || r.Host != "ws-host.example.test" || r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "incorrect WebSocket route or Host", http.StatusBadRequest)
			return
		}
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Upgrade", "websocket")
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	defer backend.Close()
	_, backendPort, _ := net.SplitHostPort(backend.Listener.Addr().String())
	port, _ := strconv.Atoi(backendPort)
	f := core.TLSFrontend{Tag: "auto", Mode: "nginx", AutomaticCertificate: true, ACMEEmail: "certs@example.test", Domain: "auto.example.test", PublicPort: singBoxFreePort(t), BackendPort: port, Transport: "websocket", Path: "/nginx-ws", Host: "ws-host.example.test"}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	var eabCalls atomic.Int32
	eab := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.ParseForm() != nil || r.PostForm.Get("email") != f.ACMEEmail {
			http.Error(w, "incorrect EAB request", http.StatusBadRequest)
			return
		}
		eabCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "eab_kid": "fixture", "eab_hmac_key": "Zml4dHVyZS1lYWIta2V5"})
	}))
	defer eab.Close()
	options := nginxACMEOptions{issuers: []nginxACMEIssuer{{directory: ca.URL + "/unavailable"}, {directory: ca.URL + "/directory", eab: eab.URL}}, roots: roots}
	if err := m.ensureNginxCertificates(ctx, []core.TLSFrontend{f}, false, options); err != nil {
		t.Fatal(err)
	}
	if eabCalls.Load() != 1 {
		t.Fatal("issuer fallback did not request EAB credentials")
	}
	wait := func(label string, check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				logs, _ := filepath.Glob(filepath.Join(state, "*.log"))
				for _, path := range logs {
					data, _ := os.ReadFile(path)
					t.Log(string(data))
				}
				t.Fatal(label, ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	wait("initial HTTP-01 issuance", func() bool { _, _, err := m.storedNginxCertificate(f.Domain, roots); return err == nil })
	if _, _, err := m.storedNginxCertificate(f.Domain); err == nil {
		t.Fatal("test authority silently entered system trust")
	}
	if _, err := m.copyAutomaticNginxCertificates([]core.TLSFrontend{f}, false, roots); err != nil {
		t.Fatal(err)
	}
	snapshots, err := m.snapshotRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nginxPath := filepath.Join(root, "nginx.conf")
	nginx := renderNginxFrontends([]core.TLSFrontend{f}, root, state, true)
	if !strings.Contains(nginx, "listen 80;") {
		t.Fatal("independent Nginx lacks HTTP-01 route")
	}
	deployments := tlsDeployments{"qagent-sing-box.service": {Frontends: []core.TLSFrontend{f}}}
	if err := m.sync(ctx, deployments); err != nil {
		t.Fatal(err)
	}
	nginxService, _, _ := m.serviceDefinition("nginx", false)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = services.command(cleanup, nginxService, core.ActionStop)
		_ = setServiceEnabled(cleanup, nginxService, false, services)
		_ = os.RemoveAll(filepath.Join(root, "acme"))
		_ = os.Remove(filepath.Join(root, frontendKey(f)+".crt"))
		_ = os.Remove(filepath.Join(root, frontendKey(f)+".key"))
	})
	if status, err := services.status(ctx, caddyService); err != nil || status != "inactive" {
		t.Fatalf("Caddy remains active: %s %v", status, err)
	}
	if enabled, err := serviceEnableState(ctx, caddyService, services); err != nil || enabled != "disabled" {
		t.Fatalf("Caddy remains enabled: %s %v", enabled, err)
	}
	// An old automatic-Nginx manifest cannot express its certificate-only
	// Caddy helper. Rollback must restore the exact old listener/service state.
	if err := m.restoreRuntime(ctx, deployments, snapshots); err != nil {
		t.Fatal("Caddy-to-Nginx rollback", err)
	}
	if status, err := services.status(ctx, caddyService); err != nil || status != "active" {
		t.Fatalf("rollback did not restore Caddy: %s %v", status, err)
	}
	if err := m.sync(ctx, deployments); err != nil {
		t.Fatal("Caddy-to-Nginx retry", err)
	}
	probe := func() (string, error) {
		dialer := tls.Dialer{Config: &tls.Config{RootCAs: roots, ServerName: f.Domain, MinVersion: tls.VersionTLS12}}
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(f.PublicPort)))
		if err != nil {
			return "", err
		}
		defer conn.Close()
		if err := probeFrontendProtocol(ctx, f, dialer.Config); err != nil {
			return "", err
		}
		return string(conn.(*tls.Conn).ConnectionState().PeerCertificates[0].Raw), nil
	}
	var initial string
	wait("Nginx trusted TLS/WebSocket", func() bool { var err error; initial, err = probe(); return err == nil })
	wait("renewal due", func() bool {
		cert, key, err := m.storedNginxCertificate(f.Domain, roots)
		if err != nil {
			return false
		}
		leaf, err := verifyNginxCertificate(certificates.Pair{Certificate: cert, PrivateKey: key}, f.Domain, roots)
		return err == nil && nginxCertificateDue(leaf)
	})
	if err := m.ensureNginxCertificates(ctx, []core.TLSFrontend{f}, true, options); err != nil {
		t.Fatal(err)
	}
	if orders.Load() < 2 {
		t.Fatal("renewal did not place a new ACME order")
	}
	if eabCalls.Load() != 1 {
		t.Fatal("renewal failed to reuse the persisted ACME account")
	}
	wait("renewal persisted", func() bool {
		certificate, key, err := m.storedNginxCertificate(f.Domain, roots)
		if err != nil {
			return false
		}
		pair, _ := tls.X509KeyPair([]byte(certificate), []byte(key))
		return len(pair.Certificate) > 0 && string(pair.Certificate[0]) != initial
	})
	// Both successful validation and a later business-validation failure must
	// preserve the installed pair so the periodic refresh still sees renewal.
	oldCert, _ := os.ReadFile(filepath.Join(root, frontendKey(f)+".crt"))
	oldKey, _ := os.ReadFile(filepath.Join(root, frontendKey(f)+".key"))
	validationFailure := errors.New("fixture business validation failed")
	for _, fail := range []bool{false, true} {
		err := m.withAutomaticNginxCertificates([]core.TLSFrontend{f}, false, func() error {
			staged, _ := os.ReadFile(filepath.Join(root, frontendKey(f)+".crt"))
			if string(staged) == string(oldCert) {
				return errors.New("renewal was not staged for validation")
			}
			if err := m.validateFrontendConfigs(ctx, []core.TLSFrontend{f}); err != nil {
				return err
			}
			if fail {
				return validationFailure
			}
			return nil
		}, roots)
		if !fail && err != nil || fail && !errors.Is(err, validationFailure) {
			t.Fatalf("staged validation fail=%t: %v", fail, err)
		}
		afterCert, _ := os.ReadFile(filepath.Join(root, frontendKey(f)+".crt"))
		afterKey, _ := os.ReadFile(filepath.Join(root, frontendKey(f)+".key"))
		if string(afterCert) != string(oldCert) || string(afterKey) != string(oldKey) {
			t.Fatal("validation changed the installed certificate pair")
		}
	}
	// A failed reload validation restores the old pair; the retry must still
	// install the renewal after the staged validations above.
	if err := m.writeConfig(nginxPath, nginx+"\ninvalid_fixture_directive;\n", false); err != nil {
		t.Fatal(err)
	}
	if err := m.refreshNginxCertificates(ctx, deployments, options); err == nil {
		t.Fatal("invalid Nginx config accepted during renewal")
	}
	after, _ := os.ReadFile(filepath.Join(root, frontendKey(f)+".crt"))
	if string(after) != string(oldCert) {
		t.Fatal("failed renewal did not restore certificate")
	}
	if err := m.writeConfig(nginxPath, nginx, false); err != nil {
		t.Fatal(err)
	}
	if err := m.refreshNginxCertificates(ctx, deployments, options); err != nil {
		t.Fatal(err)
	}
	wait("renewed certificate served", func() bool { got, err := probe(); return err == nil && got != initial })
	files, err := m.copyAutomaticNginxCertificates([]core.TLSFrontend{f}, false, roots)
	if err != nil || len(files) != 0 {
		t.Fatal("unchanged renewal rewrote certificate", err)
	}
	deployments["qagent-sing-box.service"] = tlsDeployment{Frontends: []core.TLSFrontend{f}, Stopped: true}
	if err := m.refreshNginxCertificates(ctx, deployments, options); err != nil {
		t.Fatal("stopped deployment refreshed", err)
	}
	failed := f
	failed.Domain = "failed.example.test"
	brokenCA := nginxACMEOptions{issuers: []nginxACMEIssuer{{directory: ca.URL + "/unavailable"}}, roots: roots}
	if err := m.ensureNginxCertificates(ctx, []core.TLSFrontend{failed}, true, brokenCA); err == nil {
		t.Fatal("failed CA reported renewal success")
	}
	var retry time.Time
	if err := readNginxACME(m.nginxACMEPath("retry", failed.Domain), &retry); err != nil || retry.Before(time.Now().Add(59*time.Minute)) {
		t.Fatal("failed renewal was not backed off", err)
	}
	if err := m.ensureNginxCertificates(ctx, []core.TLSFrontend{failed}, true, brokenCA); err != nil {
		t.Fatal("backoff placed another failing order", err)
	}
	t.Logf("Independent ACME issuance/renewal, real Caddy stopped/disabled, Nginx trusted TLS/WebSocket Host/path, staged validation, rollback and retry passed (%d local CA orders)", orders.Load())
}
