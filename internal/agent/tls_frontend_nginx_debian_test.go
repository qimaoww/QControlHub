package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Called only inside the disposable node. Start with the legacy unit and an
// independently running system nginx, as on hosts provisioned by older Agents.
func verifyDebianNginxMigration(t *testing.T, m *tlsFrontendManager, deployments tlsDeployments) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	private := m.nginxBinary()
	if privateNginxRevision(private) == "" {
		t.Fatal("fresh provisioning did not use a private binary")
	}
	system := systemNginxBinary()
	if _, err := os.Stat(system); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fresh provisioning installed a system package", err)
	}
	if err := installManagedPackages(ctx, true); err != nil {
		t.Fatal(err)
	}
	systemInfo, err := os.Stat(system)
	if err != nil {
		t.Fatal(err)
	}
	service, unit, _ := m.serviceDefinition("nginx", false)
	if err := m.switchNginx(ctx, service, unit, private, system, systemInfo); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := ln.Addr().String()
	ln.Close()
	if err = os.WriteFile("/etc/nginx/sites-available/default", []byte("server {listen "+address+"; return 204;}"), 0644); err != nil {
		t.Fatal(err)
	}
	const independent = "nginx.service"
	if _, err = m.services.command(ctx, independent, core.ActionStart); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.services.command(context.Background(), independent, core.ActionStop) })
	pid, err := managedServiceProcessPID(ctx, m.services, independent)
	if err != nil {
		t.Fatal(err)
	}
	build := "from-legacy-agent"
	if err = m.writeConfig(filepath.Join(m.root, "nginx-package-build"), build, true); err != nil {
		t.Fatal(err)
	}
	out, err := m.upgradeManagedNginx(ctx, build, false, m.upgradeNginxBinary)
	if err != nil || !strings.Contains(out, "nginx.org") {
		t.Fatal("legacy migration", out, err)
	}
	if m.nginxBinary() != private {
		t.Fatalf("wrong managed executable %s", m.nginxBinary())
	}
	if current, err := managedServiceProcessPID(ctx, m.services, independent); err != nil || current != pid {
		t.Fatal("independent service restarted", current, pid, err)
	}
	if info, err := os.Stat(system); err != nil || !os.SameFile(info, systemInfo) {
		t.Fatal("system package executable changed", err)
	}
	_, version, err := nginxDebianOwner(ctx, system)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("system nginx %s retained PID %d; managed nginx %s", version, pid, privateNginxRevision(private))

	// A service that fails after the candidate was installed must restore
	// the prior unit and working TLS routes.
	platform, _ := nginxHostPlatform()
	broken := filepath.Join(privateNginxDir, "nginx-1.31.9-2~"+platform.suite+"-"+strings.Repeat("0", 64))
	if err = os.WriteFile(broken, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(broken)
	info, err := os.Stat(broken)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.switchNginx(ctx, service, unit, private, broken, info); err == nil {
		t.Fatal("reported activation of failed binary")
	}
	if m.nginxBinary() != private {
		t.Fatal("failed candidate left in unit")
	}
	routes, err := activeTLSFrontends(deployments)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range routes {
		if err := probeFrontendTLS(ctx, f, true); err != nil {
			t.Fatal(fmt.Errorf("after rollback: %w", err))
		}
	}
}
