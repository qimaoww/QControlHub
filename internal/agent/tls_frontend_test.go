package agent

import (
	"encoding/json"
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
