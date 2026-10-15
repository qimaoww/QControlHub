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
	t.Log("managed TLS installed, validated, reloaded, preserved shared routes, rejected conflicts, stopped and restarted under the shipped Agent sandbox")
}
