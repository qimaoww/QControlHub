package agent

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
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
	f := core.TLSFrontend{Tag: "runtime", Mode: "caddy", Domain: "runtime.example.test", PublicPort: 9443, BackendPort: 29999, Transport: "websocket", Path: "/runtime"}
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
	t.Log("non-root Caddy persisted instance ID, certificates and CA state across legacy migration and restarts; custom units preserved")
}
