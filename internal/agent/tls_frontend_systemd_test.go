package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

func TestManagedTLSSystemdSandbox(t *testing.T) {
	if os.Getenv("QCH_TEST_MANAGED_TLS_SYSTEMD") != "1" {
		t.Skip("requires disposable systemd node")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires disposable Docker node")
	}
	kind := ServiceManagerSystemd
	if os.Getenv("QCH_TEST_TLS_MANAGER") == ServiceManagerOpenRC {
		kind = ServiceManagerOpenRC
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	if _, err := os.Stat(tlsFrontendRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture must start without TLS configuration directories: %v", err)
	}
	if kind == ServiceManagerSystemd {
		if err := os.MkdirAll(tlsFrontendRoot, 0700); !errors.Is(err, syscall.EROFS) {
			t.Fatalf("Agent must retain a read-only /etc mount: %v", err)
		}
		t.Log("reproduced read-only TLS configuration directory in the long-running Agent sandbox")
	}
	protocol, _ := serverconfig.FindProtocol(core.EngineSingBox, "vless-ws-tls")
	input, err := serverconfig.NewPlan(protocol)
	if err != nil {
		t.Fatal(err)
	}
	input.TLSMode = "nginx"
	input.TLSServerName = "tls.example.test"
	input.TLSPublicPort = 443
	source, err := serverconfig.Generate(core.EngineSingBox, input)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := serverconfig.MarshalClientMetadataWithPrevious(input, "")
	if err != nil {
		t.Fatal(err)
	}
	frontends, err := serverconfig.ManagedTLSFrontends(core.EngineSingBox, source, map[string]string{input.Tag: metadata})
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(service string, action core.Action, stage bool, frontends []core.TLSFrontend, success bool) {
		t.Helper()
		name, err := writeTLSFrontendRequest(tlsFrontendRequest{service, kind, action, stage, frontends})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(tlsFrontendRequests, name)
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("TLS request must remain private: %v", err)
		}
		defer os.Remove(path)
		args := tlsFrontendHelperArguments("/usr/local/lib/qagent/qagent", "qagent-tls-test-"+name[:24]+".service", name)
		var output string
		if kind == ServiceManagerOpenRC {
			output, err = runIPQualityCommand(ctx, "/usr/local/lib/qagent/qagent", []string{"manage-tls", name})
		} else {
			output, err = runIPQualityCommand(ctx, "/usr/bin/systemd-run", args)
		}
		if success && err != nil {
			t.Fatalf("%s stage=%t: %v\n%s", action, stage, err, output)
		}
		if !success && err == nil {
			t.Fatal("invalid frontend deployment accepted")
		}
	}
	service := "qagent-sing-box.service"
	if kind == ServiceManagerOpenRC {
		service = "qagent-sing-box"
	}
	invoke(service, core.ActionDeploy, true, frontends, true)
	invoke(service, core.ActionDeploy, false, frontends, true)
	if err = probeFrontendTLS(ctx, frontends[0]); err != nil {
		t.Fatal(err)
	}
	other := frontends[0]
	other.Tag = "other"
	other.Path = "/second"
	other.BackendPort++
	shared := "qagent-sing-box@shr_0000000000000001.service"
	if kind == ServiceManagerOpenRC {
		shared = "qagent-sing-box-shr_0000000000000001"
	}
	invoke(shared, core.ActionDeploy, false, []core.TLSFrontend{other}, true)
	invoke(service, core.ActionStop, false, nil, true)
	if err = probeFrontendTLS(ctx, other); err != nil {
		t.Fatal("stopping one instance broke another", err)
	}
	collision := other
	collision.Tag = "collision"
	invoke(service, core.ActionDeploy, false, []core.TLSFrontend{collision}, false)
	if err = probeFrontendTLS(ctx, other); err != nil {
		t.Fatal("rejected deployment broke previous TLS", err)
	}
	invoke(shared, core.ActionStop, false, nil, true)
	manager, _ := NewServiceManager(kind)
	proxyService := "qagent-tls-nginx.service"
	if kind == ServiceManagerOpenRC {
		proxyService = "qagent-tls-nginx"
	}
	status, err := manager.status(ctx, proxyService)
	if err != nil || status != "inactive" {
		t.Fatalf("last frontend did not stop: %s %v", status, err)
	}
	invoke(service, core.ActionStart, false, nil, true)
	if err = probeFrontendTLS(ctx, frontends[0]); err != nil {
		t.Fatal(err)
	}
	invoke(shared, core.ActionStart, false, nil, true)
	invoke(service, core.ActionDeploy, true, nil, true)
	invoke(service, core.ActionDeploy, false, nil, true)
	if err = probeFrontendTLS(ctx, other); err != nil {
		t.Fatal("deleting final inbound broke another instance", err)
	}
	// Starting an empty deployment must not restore its removed TLS routes.
	invoke(shared, core.ActionStop, false, nil, true)
	invoke(service, core.ActionStart, false, nil, true)
	status, err = manager.status(ctx, proxyService)
	if err != nil || status != "inactive" {
		t.Fatalf("deleted frontend was restored: %s %v", status, err)
	}
	invoke(service, core.ActionUninstall, false, nil, true)
	caddy := frontends[0]
	caddy.Mode = "caddy"
	caddy.Certificate = ""
	caddy.PrivateKey = ""
	invoke(service, core.ActionDeploy, true, []core.TLSFrontend{caddy}, true)
	if err := validatePrivilegedExecutable(managedCaddyBinary); err != nil {
		t.Fatal("official Caddy installation", err)
	}
	// The same privileged helper must handle local Xray targets while the
	// long-running Agent retains its shipped read-only /etc sandbox.
	xrayService, sharedXray := "qagent-xray.service", "qagent-xray@shr_0000000000000001.service"
	if kind == ServiceManagerOpenRC {
		xrayService, sharedXray = "qagent-xray", "qagent-xray-shr_0000000000000001"
	}
	xhttp := core.TLSFrontend{Tag: "five", Mode: "nginx", Domain: "tls.example.test", RealityName: "reality.example.test", PublicPort: 31001, BackendPort: 31002, Transport: "xhttp", Path: "/xhttp-five", LocalOnly: true, CertificatePath: "/etc/qcontrolhub/tls/xhttp.crt", PrivateKeyPath: "/etc/qcontrolhub/tls/xhttp.key"}
	invoke(xrayService, core.ActionDeploy, true, []core.TLSFrontend{xhttp}, true)
	invoke(xrayService, core.ActionDeploy, false, []core.TLSFrontend{xhttp}, true)
	if err := probeFrontendTLS(ctx, xhttp); err != nil {
		t.Fatal("local XHTTP TLS target", err)
	}
	second := xhttp
	second.Tag, second.Path, second.PublicPort, second.BackendPort = "mixed", "/xhttp-mixed", 31003, 31004
	invoke(sharedXray, core.ActionDeploy, false, []core.TLSFrontend{second}, true)
	invoke(xrayService, core.ActionStop, false, nil, true)
	if err := probeFrontendTLS(ctx, second); err != nil {
		t.Fatal("stopping Xray group removed another instance", err)
	}
	invoke(xrayService, core.ActionDeploy, false, []core.TLSFrontend{second}, false)
	broken := xhttp
	broken.PrivateKeyPath = "/etc/qcontrolhub/tls/missing.key"
	invoke(xrayService, core.ActionDeploy, true, []core.TLSFrontend{broken}, false)
	if err := probeFrontendTLS(ctx, second); err != nil {
		t.Fatal("failed stage damaged existing target", err)
	}
	invoke(xrayService, core.ActionStart, false, nil, true)
	invoke(xrayService, core.ActionRestart, false, nil, true)
	if err := probeFrontendTLS(ctx, xhttp); err != nil {
		t.Fatal("Xray target restart", err)
	}
	invoke(xrayService, core.ActionDeploy, false, nil, true)
	if err := probeFrontendTLS(ctx, second); err != nil {
		t.Fatal("deleting Xray group removed another instance", err)
	}
	invoke(sharedXray, core.ActionUninstall, false, nil, true)
	status, err = manager.status(ctx, proxyService)
	if err != nil || status != "inactive" {
		t.Fatalf("last Xray target not stopped: %s %v", status, err)
	}
	t.Log("XHTTP local targets validated, deployed, isolated, rolled back, restarted and removed as non-root on " + kind)
	t.Log("managed TLS installed, validated, reloaded, preserved shared routes, rejected conflicts, stopped and restarted under the shipped Agent sandbox")
}
