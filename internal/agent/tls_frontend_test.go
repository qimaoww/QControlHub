package agent

import (
	"encoding/json"
	"strings"
	"testing"

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
