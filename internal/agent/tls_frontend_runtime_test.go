package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"github.com/qimaoww/qcontrolhub/internal/certificates"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestCaddyHTTPChallengeUpgradePreservesRuntime(t *testing.T) {
	f := core.TLSFrontend{Tag: "one", Mode: "caddy", Domain: "one.example.test", ACMEEmail: "owner@example.test", PublicPort: 8443, BackendPort: 20001, Transport: "websocket", Path: "/old"}
	config, err := renderCaddyWithFallback([]core.TLSFrontend{f}, t.TempDir(), map[string]bool{f.Domain: true})
	if err != nil {
		t.Fatal(err)
	}
	var previous map[string]any
	if err = json.Unmarshal([]byte(config), &previous); err != nil {
		t.Fatal(err)
	}
	servers := previous["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	delete(servers, "http_challenges") // A committed deployment from before this upgrade.
	legacy, _ := json.Marshal(previous)
	upgraded, err := upgradeCaddyHTTPChallengeConfig(string(legacy))
	if err != nil {
		t.Fatal(err)
	}
	var current map[string]any
	if err = json.Unmarshal([]byte(upgraded), &current); err != nil {
		t.Fatal(err)
	}
	currentServers := current["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	if currentServers["http_challenges"] == nil || !strings.Contains(upgraded, "127.0.0.1:9080") {
		t.Fatal("missing managed challenge route")
	}
	delete(currentServers, "http_challenges")
	if !reflect.DeepEqual(current, previous) {
		t.Fatal("challenge upgrade changed committed business routes, issuer fallback or storage")
	}
	// A second startup must not rewrite or reload an unchanged configuration.
	if again, err := upgradeCaddyHTTPChallengeConfig(upgraded); err != nil || again != upgraded {
		t.Fatal("challenge upgrade is not idempotent", err)
	}
	for _, invalid := range []string{`{`, `{}`, `{"apps":{"http":{"servers":null}}}`} {
		if _, err := upgradeCaddyHTTPChallengeConfig(invalid); err == nil {
			t.Fatal("invalid runtime accepted")
		}
	}
}

func TestManagedTLSAddressFamilies(t *testing.T) {
	for _, records := range [][]string{{"1.1.1.1"}, {"2606:4700:4700::1111"}, {"1.1.1.1", "2606:4700:4700::1111"}} {
		var addresses []netip.Addr
		for _, record := range records {
			addresses = append(addresses, netip.MustParseAddr(record))
		}
		if err := validateTLSAddresses(addresses); err != nil {
			t.Fatal(records, err)
		}
	}
	for _, records := range [][]string{nil, {"127.0.0.1"}, {"::1"}, {"1.1.1.1", "fd00::1"}} {
		var addresses []netip.Addr
		for _, record := range records {
			addresses = append(addresses, netip.MustParseAddr(record))
		}
		if err := validateTLSAddresses(addresses); err == nil {
			t.Fatal("invalid DNS accepted", records)
		}
	}
}

func TestManagedTLSCertificatePreparationPreservesRoutes(t *testing.T) {
	f := core.TLSFrontend{Tag: "one", Mode: "caddy", Domain: "one.example.test", PublicPort: 8443, BackendPort: 20001, Transport: "websocket", Path: "/old"}
	next := f
	next.Path = "/new"
	next.PublicPort++
	config, _ := renderCaddyFrontends([]core.TLSFrontend{next}, t.TempDir())
	prepared, err := certificatePreparationRoutes(config, []core.TLSFrontend{f}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prepared, "/old") || strings.Contains(prepared, "/new") || !strings.Contains(prepared, ":8443") || strings.Contains(prepared, ":8444") {
		t.Fatal(prepared)
	}
}

func TestManagedTLSRuntimeDefinitions(t *testing.T) {
	for _, kind := range []string{ServiceManagerSystemd, ServiceManagerOpenRC} {
		manager := &ServiceManager{kind: kind}
		m := tlsFrontendManager{root: tlsFrontendRoot, state: tlsFrontendState, services: manager}
		_, _, modern := m.serviceDefinition("caddy", false)
		_, _, legacy := m.serviceDefinition("caddy", true)
		for _, value := range []string{"XDG_DATA_HOME=", "XDG_CONFIG_HOME=", "HOME=", "qcontrolhub-core"} {
			if !strings.Contains(modern, value) {
				t.Fatal(kind, value)
			}
		}
		if strings.Contains(legacy, "XDG_DATA_HOME") {
			t.Fatal("legacy migration fingerprint changed")
		}
		if kind == ServiceManagerSystemd && !strings.Contains(modern, "ReadWritePaths="+m.state) {
			t.Fatal("missing sandbox write path")
		}
	}
	if core.ManagedTLSLeaseTimeout < 4*time.Minute+core.ManagedTLSOperationTimeout+time.Minute {
		t.Fatal("lease cannot cover installation and rollback")
	}
}

// Real init systems, non-root Caddy and local CA. Invoked by the disposable
// systemd/OpenRC node fixture, never against the developer's host services.
func TestManagedTLSRuntimePersistenceSandbox(t *testing.T) {
	kind := os.Getenv("QCH_TEST_TLS_RUNTIME_MANAGER")
	if kind == "" {
		t.Skip("requires disposable TLS node")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal(err)
	}
	manager, err := NewServiceManager(kind)
	if err != nil {
		t.Fatal(err)
	}
	m := tlsFrontendManager{tlsFrontendRoot, tlsFrontendState, manager, NewCoreUpdater()}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.prepareDirectories(); err != nil {
		t.Fatal(err)
	}
	if err := m.checkCaddyAccess(ctx); err != nil {
		t.Fatal(err)
	}
	f := core.TLSFrontend{Tag: "runtime", Mode: "caddy", Domain: "runtime.example.test", PublicPort: 443, BackendPort: 29999, Transport: "websocket", Path: "/runtime"}
	config, _ := renderCaddyFrontends([]core.TLSFrontend{f}, m.state)
	var root map[string]any
	_ = json.Unmarshal([]byte(config), &root)
	apps := root["apps"].(map[string]any)
	policy := apps["tls"].(map[string]any)["automation"].(map[string]any)["policies"].([]any)[0].(map[string]any)
	policy["issuers"] = []any{map[string]any{"module": "internal"}}
	apps["pki"] = map[string]any{"certificate_authorities": map[string]any{"local": map[string]any{"install_trust": false}}}
	data, _ := json.Marshal(root)
	if err := m.writeConfig(filepath.Join(m.root, "caddy.conf"), string(data), false); err != nil {
		t.Fatal(err)
	}
	service, path, legacy := m.serviceDefinition("caddy", true)
	if err := os.WriteFile(path, []byte(legacy), 0755); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(tlsDeployments{"qch-sing-box": {Frontends: []core.TLSFrontend{f}}})
	if err := m.writeConfig(filepath.Join(m.root, "deployments.json"), string(manifest), true); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(filepath.Join(m.root, "deployments.json"))
	if err := m.repairRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.command(ctx, service, core.ActionStart); err != nil {
		t.Fatal(err)
	}
	defer manager.command(context.Background(), service, core.ActionStop)
	instance := filepath.Join(m.state, "data", "caddy", "instance.uuid")
	cert := filepath.Join(m.state, "caddy", "certificates", "local", f.Domain, f.Domain+".crt")
	var firstID, firstCert []byte
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
		firstID, _ = os.ReadFile(instance)
		firstCert, _ = os.ReadFile(cert)
		if len(firstID) > 0 && len(firstCert) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(firstID) == 0 || len(firstCert) == 0 {
		log, _ := os.ReadFile(filepath.Join(m.state, "caddy.log"))
		t.Fatalf("missing persistent state: %s", log)
	}
	// Recreate the old running service, migrate it, then restart again as on boot.
	if err := os.WriteFile(path, []byte(legacy), 0755); err != nil {
		t.Fatal(err)
	}
	if err := m.ensureService(ctx, "caddy"); err != nil {
		t.Fatal(err)
	}
	if err := m.ensureService(ctx, "caddy"); err != nil {
		t.Fatal("idempotence", err)
	}
	if _, err := manager.command(ctx, service, core.ActionRestart); err != nil {
		t.Fatal(err)
	}
	secondID, _ := os.ReadFile(instance)
	secondCert, _ := os.ReadFile(cert)
	if string(firstID) != string(secondID) || string(firstCert) != string(secondCert) {
		t.Fatal("migration/restart replaced certificate or instance identity")
	}
	stored, _ := os.ReadFile(path)
	custom := string(stored) + "\n# user customization\n"
	if err := os.WriteFile(path, []byte(custom), 0755); err != nil {
		t.Fatal(err)
	}
	if err := m.ensureService(ctx, "caddy"); err == nil || !strings.Contains(err.Error(), "用户修改") {
		t.Fatal("custom service overwritten", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != custom {
		t.Fatal("custom config changed")
	}
	_ = os.WriteFile(path, stored, 0755)
	verifyManagedTLSProviderSwitch(t, &m, ctx, f, cert)
	t.Log("non-root Caddy persisted instance ID, certificates and CA state across legacy migration and restarts; custom units preserved")
}

// Run inside each real init-system fixture with low ports protected. Start
// with a working Caddy listener, then exercise the same manager transaction
// used by deployment. No public CA, DNS change or production state is used.
func verifyManagedTLSProviderSwitch(t *testing.T, m *tlsFrontendManager, ctx context.Context, old core.TLSFrontend, certPath string) {
	t.Helper()
	policy, err := os.ReadFile("/proc/sys/net/ipv4/ip_unprivileged_port_start")
	if err != nil || strings.TrimSpace(string(policy)) != "1024" {
		t.Fatalf("fixture must protect low ports: %s %v", policy, err)
	}
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != old.Path {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Upgrade", "websocket")
		w.Header().Set("Connection", "Upgrade")
		w.WriteHeader(101)
	}))
	backend.Listener, err = net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(old.BackendPort))
	if err != nil {
		t.Fatal(err)
	}
	backend.Start()
	defer backend.Close()
	crt, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(strings.TrimSuffix(certPath, ".crt") + ".key")
	if err != nil {
		t.Fatal(err)
	}
	next := old
	next.Mode = "nginx"
	next.Certificate = string(crt)
	next.PrivateKey = string(key)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(crt)
	probeCaddy := func() error {
		var err error
		for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
			err = probeFrontendProtocol(ctx, old, &tls.Config{RootCAs: roots, ServerName: old.Domain, MinVersion: tls.VersionTLS12})
			if err == nil {
				return nil
			}
			time.Sleep(50 * time.Millisecond)
		}
		return err
	}
	if err = probeCaddy(); err != nil {
		t.Fatal("original Caddy HTTPS backend", err)
	}
	snapshots, err := m.snapshotRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(m.root, "deployments.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		recovery, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := m.restoreRuntime(recovery, nil, snapshots); err != nil {
			t.Error(err)
		}
		if err := m.writeConfig(filepath.Join(m.root, "deployments.json"), string(manifest), true); err != nil {
			t.Error(err)
		}
	}()
	if err = m.prepare(ctx, []core.TLSFrontend{next}); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(m.root, "nginx.permission-test.conf")
	if err = m.writeConfig(staged, renderNginxFrontends([]core.TLSFrontend{next}, m.root, m.state), false); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(staged)
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		t.Fatal(err)
	}
	output, err := runInDirectoryWithEnvironmentAndIdentity(ctx, m.state, "", managedNginxBinary(), &identity, "-t", "-p", m.state+"/", "-c", staged)
	if err == nil || !strings.Contains(output, "Permission denied") || !strings.Contains(output, "syntax is ok") {
		t.Fatalf("original UID-only validation did not reproduce bind EACCES: %v %s", err, output)
	}
	if output, err = m.validateNginx(ctx, staged); err != nil {
		t.Fatalf("capability validation: %v %s", err, output)
	}
	// A late TLS failure must restore the exact internal-CA Caddy runtime.
	bad := next
	bad.Domain = "wrong.example.test"
	failure, cancel := context.WithTimeout(ctx, 2*time.Second)
	err = m.apply(failure, "qch-sing-box", []core.TLSFrontend{bad}, core.ActionDeploy)
	cancel()
	if err == nil || !strings.Contains(err.Error(), "已恢复原反代状态") {
		t.Fatalf("switch failure rollback: %v", err)
	}
	if err = probeCaddy(); err != nil {
		t.Fatal("Caddy rollback HTTPS", err)
	}
	for i := 0; i < 2; i++ {
		if err = m.apply(ctx, "qch-sing-box", []core.TLSFrontend{next}, core.ActionDeploy); err != nil {
			t.Fatal("Caddy-to-Nginx deployment/repeat", err)
		}
	}
	// Rotate the inline certificate and verify the actual peer, not just files.
	pair, err := certificates.SelfSigned([]string{next.Domain}, "ecdsa256", 90)
	if err != nil {
		t.Fatal(err)
	}
	next.Certificate, next.PrivateKey = pair.Certificate, pair.PrivateKey
	if err = m.apply(ctx, "qch-sing-box", []core.TLSFrontend{next}, core.ActionDeploy); err != nil {
		t.Fatal("certificate rotation", err)
	}
	trust := x509.NewCertPool()
	trust.AppendCertsFromPEM([]byte(pair.Certificate))
	conn, err := (&tls.Dialer{Config: &tls.Config{RootCAs: trust, ServerName: next.Domain, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", "127.0.0.1:443")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := tls.X509KeyPair([]byte(pair.Certificate), []byte(pair.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(conn.(*tls.Conn).ConnectionState().PeerCertificates[0].Raw, expected.Certificate[0]) {
		t.Fatal("peer did not use replacement certificate")
	}
	conn.Close()
	pid, err := os.ReadFile(filepath.Join(m.state, "nginx.pid"))
	if err != nil {
		t.Fatal(err)
	}
	status, err := os.ReadFile("/proc/" + strings.TrimSpace(string(pid)) + "/status")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "Uid:":
			if fields[1] != strconv.Itoa(int(identity.uid)) {
				t.Fatal("Nginx ran as wrong UID")
			}
			t.Log(line)
		case "Gid:", "CapInh:", "CapPrm:", "CapBnd:":
			t.Log(line)
		case "CapEff:":
			if fields[1] != "0000000000000400" {
				t.Fatal("Nginx had unexpected effective capabilities", line)
			}
			t.Log(line)
		case "NoNewPrivs:":
			if fields[1] != "1" {
				t.Fatal("Nginx no-new-privileges missing")
			}
			t.Log(line)
		}
	}
	t.Log("reproduced syntax-ok then bind EACCES without capability; non-root CAP_NET_BIND_SERVICE validation passed while Caddy served 443; failed switch restored Caddy HTTPS; Nginx deployment, repeat and actual rotated-certificate TLS/backend passed")
}
