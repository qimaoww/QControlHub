package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func TestTLSFrontendIsolationAndRendering(t *testing.T) {
	a := core.TLSFrontend{Tag: "a", Mode: "caddy", Domain: "a.example.test", PublicPort: 8443, BackendPort: 23456, Transport: "websocket", Path: "/one"}
	b := a
	b.Tag = "b"
	b.Domain = "b.example.test"
	b.BackendPort++
	b.Path = "/two"
	deployments := tlsDeployments{"one": {Frontends: []core.TLSFrontend{a}}, "two": {Frontends: []core.TLSFrontend{b}}}
	frontends, err := activeTLSFrontends(deployments)
	if err != nil {
		t.Fatal(err)
	}
	config, err := renderCaddyFrontends(frontends, "/var/lib/qcontrolhub-tls")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if json.Unmarshal([]byte(config), &root) != nil {
		t.Fatal("invalid Caddy JSON")
	}
	for _, want := range []string{":8443", "127.0.0.1:23456", "127.0.0.1:23457", "/one", "/two", "a.example.test", "b.example.test"} {
		if !strings.Contains(config, want) {
			t.Fatal(want)
		}
	}
	deployments["one"] = tlsDeployment{Frontends: []core.TLSFrontend{a}, Stopped: true}
	frontends, err = activeTLSFrontends(deployments)
	if err != nil || len(frontends) != 1 || frontends[0].Tag != "b" {
		t.Fatal("stopping an instance removed another instance")
	}
	b.Path = a.Path
	deployments["two"] = tlsDeployment{Frontends: []core.TLSFrontend{b}}
	deployments["one"] = tlsDeployment{Frontends: []core.TLSFrontend{a}}
	if _, err = activeTLSFrontends(deployments); err == nil {
		t.Fatal("duplicate public path accepted")
	}
	b.Mode = "nginx"
	b.Path = "/two"
	b.Certificate = "cert"
	b.PrivateKey = "key"
	deployments["two"] = tlsDeployment{Frontends: []core.TLSFrontend{b}}
	if _, err = activeTLSFrontends(deployments); err == nil {
		t.Fatal("two providers on the same port accepted")
	}
	b.PublicPort = 9443
	config = renderNginxFrontends([]core.TLSFrontend{b}, "/etc/qagent/tls-frontends", "/var/lib/qcontrolhub-tls")
	for _, want := range []string{"listen 9443 ssl", "location = /two", "proxy_pass http://127.0.0.1:23457", "proxy_set_header Host $http_host", "proxy_set_header Upgrade $http_upgrade"} {
		if !strings.Contains(config, want) {
			t.Fatal(want)
		}
	}
}

func TestTLSFrontendRejectsMixedNginxTrustOnSameSNI(t *testing.T) {
	a := core.TLSFrontend{Tag: "auto", Mode: "nginx", AutomaticCertificate: true, Domain: "tls.example.test", PublicPort: 443, BackendPort: 23456, Transport: "websocket", Path: "/auto"}
	b := a
	b.Tag, b.Path, b.BackendPort = "legacy", "/legacy", 23457
	b.AutomaticCertificate, b.Certificate, b.PrivateKey = false, "legacy", "key"
	deployments := tlsDeployments{"one": {Frontends: []core.TLSFrontend{a, b}}}
	if _, err := activeTLSFrontends(deployments); err == nil {
		t.Fatal("mixed public/system and legacy/custom trust accepted for one SNI")
	}
	b.Domain = "legacy.example.test"
	deployments["one"] = tlsDeployment{Frontends: []core.TLSFrontend{a, b}}
	if _, err := activeTLSFrontends(deployments); err != nil {
		t.Fatal("independent SNI certificates rejected", err)
	}
}

func TestTLSFrontendIndependentNginxChallenges(t *testing.T) {
	f := core.TLSFrontend{Tag: "auto", Mode: "nginx", AutomaticCertificate: true, Domain: "auto.example.test", PublicPort: 443, BackendPort: 23456, Transport: "websocket", Path: "/auto"}
	nginx := renderNginxFrontends([]core.TLSFrontend{f}, "/tls", "/state", false)
	if !strings.Contains(nginx, "listen 80;") || !strings.Contains(nginx, "127.0.0.1:9080") {
		t.Fatal("independent Nginx cannot serve HTTP-01 challenges")
	}
	caddy := f
	caddy.Tag, caddy.Mode, caddy.Domain, caddy.PublicPort, caddy.BackendPort, caddy.Path, caddy.AutomaticCertificate = "shared", "caddy", "shared.example.test", 8443, 23457, "/shared", false
	frontends := []core.TLSFrontend{f, caddy}
	if nginx = renderNginxFrontends(frontends, "/tls", "/state", true); strings.Contains(nginx, "listen 80;") {
		t.Fatal("Nginx stole a shared Caddy challenge listener")
	}
	config, err := renderCaddyFrontends(frontends, "/state")
	if err != nil || strings.Contains(config, f.Domain) || !strings.Contains(config, caddy.Domain) {
		t.Fatal("Caddy still manages automatic Nginx certificates or lost a shared inbound", err)
	}
}

func TestXHTTPStaticPageOwnershipAndMixedRoutes(t *testing.T) {
	a := core.TLSFrontend{Tag: "site", Mode: "nginx", AutomaticCertificate: true, Domain: "tls.example.test", PublicPort: 8443, BackendPort: 23456, Transport: "xhttp", Path: "/xhttp", StaticHTML: "<html><p>site</p></html>"}
	b := a
	b.Tag, b.Transport, b.Path, b.BackendPort, b.StaticHTML = "ws", "websocket", "/ws", 23457, ""
	for _, routes := range [][]core.TLSFrontend{{a, b}, {b, a}} {
		nginx := renderNginxFrontends(routes, "/tls", "/state")
		if !strings.Contains(nginx, "listen 8443 ssl http2;") || !strings.Contains(nginx, "root /tls/"+staticPageFile(a)) || !strings.Contains(nginx, "grpc_pass grpc://127.0.0.1:23456") || !strings.Contains(nginx, "location = /ws") {
			t.Fatal("route order lost the page, WebSocket or XHTTP HTTP/2")
		}
	}
	b = a
	b.Tag, b.Path, b.BackendPort = "other", "/other", 23457
	if _, err := activeTLSFrontends(tlsDeployments{"one": {Frontends: []core.TLSFrontend{a}}, "two": {Frontends: []core.TLSFrontend{b}}}); err == nil {
		t.Fatal("two instances may not claim the same public page")
	}
	b.StaticHTML, b.Path = "", a.Path+"/child"
	if _, err := activeTLSFrontends(tlsDeployments{"one": {Frontends: []core.TLSFrontend{a, b}}}); err == nil {
		t.Fatal("overlapping XHTTP subpaths accepted")
	}
	// Identical names, ports and HTML in stopped instances remain separate resources.
	m := tlsFrontendManager{root: t.TempDir()}
	before := tlsDeployments{"one": {Frontends: []core.TLSFrontend{a}, Stopped: true}, "two": {Frontends: []core.TLSFrontend{a}, Stopped: true}}
	paths := map[string]string{}
	for service := range before {
		f := a
		f.StaticPageOwner = service
		paths[service] = filepath.Join(m.root, staticPageFile(f))
		if err := os.Mkdir(paths[service], 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(paths[service], "index.html"), []byte(a.StaticHTML), 0600); err != nil {
			t.Fatal(err)
		}
	}
	after := tlsDeployments{"two": before["two"]}
	if err := m.cleanupStaticPages(before, after); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths["one"]); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("removed instance retained its page", err)
	}
	if _, err := os.Stat(paths["two"]); err != nil {
		t.Fatal("cleanup released another instance's page", err)
	}
}

func TestCaddyIssuerFallbackAndAccounts(t *testing.T) {
	a := core.TLSFrontend{Tag: "a", Mode: "caddy", Domain: "a.example.test", PublicPort: 443, BackendPort: 23456, Transport: "websocket", Path: "/a", ACMEEmail: "certs@example.test"}
	b := a
	b.Tag, b.Domain, b.Path, b.ACMEEmail = "b", "b.example.test", "/b", "other@example.test"
	config, err := renderCaddyFrontends([]core.TLSFrontend{a, b}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Apps struct {
			TLS struct {
				Automation struct {
					Policies []struct {
						Subjects []string                     `json:"subjects"`
						Issuers  []struct{ CA, Email string } `json:"issuers"`
					} `json:"policies"`
				} `json:"automation"`
			} `json:"tls"`
		} `json:"apps"`
	}
	if err = json.Unmarshal([]byte(config), &root); err != nil {
		t.Fatal(err)
	}
	for i, policy := range root.Apps.TLS.Automation.Policies {
		f := []core.TLSFrontend{a, b}[i]
		if len(policy.Issuers) != 2 || policy.Issuers[0].CA != letsEncryptDirectory || policy.Issuers[1].CA != zeroSSLDirectory || policy.Issuers[1].Email != f.ACMEEmail || policy.Subjects[0] != f.Domain {
			t.Fatalf("issuer/account mismatch: %+v", policy)
		}
	}
	b.Domain = a.Domain
	if _, err = activeTLSFrontends(tlsDeployments{"one": {Frontends: []core.TLSFrontend{a, b}}}); err == nil {
		t.Fatal("conflicting accounts for one certificate accepted")
	}
	a.ACMEEmail = "not-an-email"
	if err = a.Validate(); err == nil {
		t.Fatal("invalid account email accepted")
	}
	a.ACMEEmail = ""
	if err = a.Validate(); err != nil {
		t.Fatal("legacy configuration must remain readable", err)
	}
}

func TestCaddyFailureDiagnosticsUseCurrentAttempt(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)
	old := fmt.Sprintf(`{"level":"error","ts":%d,"msg":"previous deployment failure"}`, now.Unix()-60)
	current := fmt.Sprintf(`{"level":"error","ts":%d,"msg":"could not get certificate from issuer","error":"Get https://acme.zerossl.com/v2/DV90: i/o timeout"}`, now.Unix()+1)
	if err := os.WriteFile(filepath.Join(dir, "caddy.log"), []byte(old+"\n"+current), 0600); err != nil {
		t.Fatal(err)
	}
	value := caddyFailureLog(dir, now)
	if !strings.Contains(value, "zerossl.com") || strings.Contains(value, "previous deployment") {
		t.Fatalf("wrong diagnostics: %s", value)
	}
	if err := os.Remove(filepath.Join(dir, "caddy.log")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "caddy.log")); err != nil {
		t.Fatal(err)
	}
	if value = caddyFailureLog(dir, now); value != "" {
		t.Fatal("followed a proxy-writable diagnostic symlink")
	}
}

func TestCaddyFailureSeparatesWarningAndCause(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)
	log := fmt.Sprintf(`{"level":"warn","ts":%d,"msg":"unable to get instance ID","error":"mkdir /nonexistent: read-only file system"}
{"level":"error","ts":%d,"msg":"registering account","error":"Get https://acme-v02.api.letsencrypt.org/directory: i/o timeout"}`, now.Unix()+1, now.Unix()+1)
	if err := os.WriteFile(filepath.Join(dir, "caddy.log"), []byte(log), 0600); err != nil {
		t.Fatal(err)
	}
	m := tlsFrontendManager{state: dir}
	err := m.withFrontendDiagnostics(errors.New("tls: internal error"), core.TLSFrontend{Mode: "caddy"}, now)
	for _, want := range []string{"[原因:网络超时]", "[告警]", "[错误]", "ZeroSSL 无法自动绑定账户"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatal(err)
		}
	}
}

func TestCaddyIssuerFailureTakesPriorityOverReadinessDeadline(t *testing.T) {
	for _, test := range []struct{ name, level, message, reason string }{
		{"account", "error", "ACME account requires external account binding (EAB)", "证书账户"},
		{"challenge", "error", "HTTP-01 challenge unauthorized: invalid response", "挑战失败"},
		{"dns", "error", "DNS problem: NXDOMAIN", "DNS"},
		{"rate_limit", "error", "urn:ietf:params:acme:error:rateLimited", "机构限流"},
		{"permission", "error", "permission denied writing certificate", "存储权限"},
		{"network", "error", "registering account: i/o timeout", "网络超时"},
		{"warning_only", "warn", "mkdir /nonexistent: read-only file system", "网络超时"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			now := time.Now().Truncate(time.Second)
			entry, _ := json.Marshal(map[string]any{"level": test.level, "ts": now.Unix() + 1, "msg": "certificate issuance", "error": test.message})
			if err := os.WriteFile(filepath.Join(dir, "caddy.log"), entry, 0600); err != nil {
				t.Fatal(err)
			}
			m := tlsFrontendManager{state: dir}
			err := m.withFrontendDiagnostics(context.DeadlineExceeded, core.TLSFrontend{Mode: "caddy"}, now)
			if !strings.Contains(err.Error(), "[原因:"+test.reason+"]") || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), test.message) {
				t.Fatal("issuer diagnosis or original failure lost:", err)
			}
		})
	}
}

func TestManagedTLSReadinessReportsActualPhase(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := tlsFrontendManager{state: t.TempDir()}
	deployments := tlsDeployments{"service": {Frontends: []core.TLSFrontend{{Mode: "caddy", Domain: "phase.example.test", PublicPort: 8443}}}}
	for _, preparation := range []bool{false, true} {
		phase := "TLS与协议就绪检查"
		var previous []tlsDeployments
		if preparation {
			phase = "证书准备"
			previous = []tlsDeployments{{}}
		}
		err := m.waitReady(ctx, deployments, "service", previous...)
		if err == nil || !strings.Contains(err.Error(), "[阶段:"+phase+"]") {
			t.Fatal("readiness reported incorrect phase:", err)
		}
	}
}
