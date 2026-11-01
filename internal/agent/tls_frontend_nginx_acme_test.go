package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Invoked by both existing init-system sandboxes. The CA and service-status
// adapter are fixtures; issuance, renewal, certificate installation, Nginx
// validation/reload and WebSocket forwarding use actual proxy binaries.
func TestManagedTLSNginxACMESandbox(t *testing.T) {
	if os.Getenv("QCH_TEST_NGINX_ACME") != "1" {
		t.Skip("requires disposable managed TLS helper sandbox")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires disposable Docker node")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	root, state := filepath.Join(tlsFrontendRoot, "nginx-acme-fixture"), "/var/lib/qcontrolhub-nginx-acme-fixture"
	for _, path := range []string{root, state} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("fixture path already exists: %s", path)
		}
		t.Cleanup(func() { _ = os.RemoveAll(path) })
	}
	m := tlsFrontendManager{root: root, state: state}
	if err := m.prepareDirectories(); err != nil {
		t.Fatal(err)
	}
	status := filepath.Join(root, "status")
	if err := os.WriteFile(status, []byte("#!/bin/sh\nprintf 'active\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	m.services = &ServiceManager{kind: ServiceManagerSystemd, executable: status}
	challengePort := singBoxFreePort(t)
	ca, caPEM, orders := mockTLSACME(t, "auto.example.test", challengePort, 8*time.Second)
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
	f := core.TLSFrontend{Tag: "auto", Mode: "nginx", AutomaticCertificate: true, Domain: "auto.example.test", PublicPort: singBoxFreePort(t), BackendPort: port, Transport: "websocket", Path: "/nginx-ws", Host: "ws-host.example.test"}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	content, err := renderCaddyFrontends([]core.TLSFrontend{f}, state)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	_ = json.Unmarshal([]byte(content), &config)
	apps := config["apps"].(map[string]any)
	servers := apps["http"].(map[string]any)["servers"].(map[string]any)
	if len(servers) != 1 || servers["http_challenges"] == nil {
		t.Fatal("Caddy must not own Nginx HTTPS listener")
	}
	relocateCaddyChallengeListener(t, config, challengePort)
	automation := apps["tls"].(map[string]any)["automation"].(map[string]any)
	automation["renew_interval"] = "100ms"
	policy := automation["policies"].([]any)[0].(map[string]any)
	policy["renewal_window_ratio"] = 0.5
	issuer := policy["issuers"].([]any)[0].(map[string]any)
	issuer["ca"], issuer["test_ca"] = ca.URL+"/directory", ca.URL+"/directory"
	issuer["challenges"] = map[string]any{"http": map[string]any{"alternate_port": challengePort}, "tls-alpn": map[string]any{"disabled": true}}
	data, _ := json.Marshal(config)
	caddyPath := filepath.Join(root, "caddy-fixture.json")
	if err := m.writeConfig(caddyPath, string(data), false); err != nil {
		t.Fatal(err)
	}
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	start := func(binary string, args ...string) {
		t.Helper()
		log, err := os.CreateTemp(state, "process-*.log")
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(binary, args...)
		command.Dir, command.Env = state, append(os.Environ(), m.caddyEnvironment()...)
		command.Stdout, command.Stderr = log, log
		configureCommandIdentity(command, &identity)
		if err = command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait(); _ = log.Close() })
	}
	start(managedCaddyBinary, "run", "--config", caddyPath)
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
	wait("initial HTTP-01 issuance", func() bool { _, _, err := m.storedCertificate(f.Domain, roots); return err == nil })
	if _, _, err := m.storedCertificate(f.Domain); err == nil {
		t.Fatal("test authority silently entered system trust")
	}
	if _, err := m.copyAutomaticNginxCertificates([]core.TLSFrontend{f}, false, roots); err != nil {
		t.Fatal(err)
	}
	nginxPath := filepath.Join(root, "nginx.conf")
	nginx := renderNginxFrontends([]core.TLSFrontend{f}, root, state, true)
	if strings.Contains(nginx, "listen 80;") {
		t.Fatal("Nginx conflicts with certificate-only Caddy")
	}
	if err := m.writeConfig(nginxPath, nginx, false); err != nil {
		t.Fatal(err)
	}
	start(managedNginxBinary(), "-p", state+"/", "-c", nginxPath)
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
	wait("automatic renewal", func() bool { return orders.Load() >= 2 })
	deployments := tlsDeployments{"qagent-sing-box.service": {Frontends: []core.TLSFrontend{f}}}
	wait("renewal persisted", func() bool {
		certificate, key, err := m.storedCertificate(f.Domain, roots)
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
	if err := m.refreshNginxCertificates(ctx, deployments, roots); err == nil {
		t.Fatal("invalid Nginx config accepted during renewal")
	}
	after, _ := os.ReadFile(filepath.Join(root, frontendKey(f)+".crt"))
	if string(after) != string(oldCert) {
		t.Fatal("failed renewal did not restore certificate")
	}
	if err := m.writeConfig(nginxPath, nginx, false); err != nil {
		t.Fatal(err)
	}
	if err := m.refreshNginxCertificates(ctx, deployments, roots); err != nil {
		t.Fatal(err)
	}
	wait("renewed certificate served", func() bool { got, err := probe(); return err == nil && got != initial })
	files, err := m.copyAutomaticNginxCertificates([]core.TLSFrontend{f}, false, roots)
	if err != nil || len(files) != 0 {
		t.Fatal("unchanged renewal rewrote certificate", err)
	}
	deployments["qagent-sing-box.service"] = tlsDeployment{Frontends: []core.TLSFrontend{f}, Stopped: true}
	if err := m.refreshNginxCertificates(ctx, deployments, roots); err != nil {
		t.Fatal("stopped deployment refreshed", err)
	}
	t.Log(fmt.Sprintf("Nginx automatic HTTP-01 issuance, renewal, trusted TLS/WebSocket Host/path, staged validation preservation, rollback and retry passed (%d local CA orders)", orders.Load()))
}
