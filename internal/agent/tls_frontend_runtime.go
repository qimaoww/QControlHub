package agent

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

// Certificate storage and Caddy's application directories are independent.
// Keep the old storage root so existing ACME accounts and renewal locks survive.
func (m *tlsFrontendManager) caddyEnvironment() []string {
	return []string{"XDG_DATA_HOME=" + filepath.Join(m.state, "data"), "XDG_CONFIG_HOME=" + filepath.Join(m.state, "config"), "HOME=" + m.state}
}

func (m *tlsFrontendManager) runCaddy(ctx context.Context, identity *commandIdentity, args ...string) (string, error) {
	return runTLSCommand(ctx, m.state, m.caddyEnvironment(), managedCaddyBinary, identity, args...)
}

func runTLSCommand(ctx context.Context, directory string, environment []string, binary string, identity *commandIdentity, args ...string) (string, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = directory
	command.Env = append(commandEnvironment(""), environment...)
	configureCommand(command)
	configureCommandIdentity(command, identity)
	output := &boundedOutput{limit: 64 << 10}
	command.Stdout, command.Stderr = output, output
	err := command.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if output.Truncated() {
		err = fmt.Errorf("TLS helper output exceeded 64 KiB")
	}
	return strings.TrimSpace(output.String()), err
}

func (m *tlsFrontendManager) checkCaddyAccess(ctx context.Context) error {
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return err
	}
	// Use the actual non-root identity and the same explicit environment as
	// both init systems. Probe creation/removal, not just root's mode checks.
	script := `for dir in "$XDG_DATA_HOME" "$XDG_CONFIG_HOME" "$HOME/caddy"; do file=$(mktemp "$dir/.qch-access-XXXXXX") || exit 1; rm "$file" || exit 1; done`
	output, err := runTLSCommand(ctx, m.state, m.caddyEnvironment(), "/bin/sh", &identity, "-ec", script)
	if err != nil {
		return fmt.Errorf("[阶段:依赖和环境检查] Caddy 服务账号无法写入受管数据/配置/证书目录：%w；%s；检查目录权限和服务沙箱", err, output)
	}
	return nil
}

// Called at Agent startup as well as deployment. Do not start stopped proxies.
func (m *tlsFrontendManager) repairRuntime(ctx context.Context) error {
	deployments, err := m.load()
	if err != nil {
		return err
	}
	if err = m.prepareDirectories(); err != nil {
		return err
	}
	modes := map[string]bool{}
	for _, d := range deployments {
		for _, f := range d.Frontends {
			modes[f.Mode] = true
		}
	}
	for mode := range modes {
		_, path, _ := m.serviceDefinition(mode, false)
		if _, err = os.Stat(path); err != nil {
			return err
		}
		if err = m.ensureService(ctx, mode); err != nil {
			return err
		}
	}
	return m.upgradeHTTPChallengeRoutes(ctx, deployments)
}

func (e *Executor) repairTLSRuntime(ctx context.Context) error {
	if !managedTLSStateExists() {
		return nil
	}
	spec := DefaultSpecsForServiceManager(e.serviceManager().Kind())[core.EngineSingBox]
	return e.executeTLSFrontend(ctx, spec, core.Task{Engine: core.EngineSingBox, Action: core.ActionStatus}, false)
}

// Existing external port-80 services are left intact. An existing project
// listener is recognized only in the protected project configuration.
func (m *tlsFrontendManager) nginxHTTPRouteAvailable() bool {
	old, err := os.ReadFile(filepath.Join(m.root, "nginx.conf"))
	if err == nil && strings.Contains(string(old), "# qch-managed-http-challenges-v1") {
		return true
	}
	listener, err := net.Listen("tcp", ":80")
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

// Upgrade active project proxies to the challenge route without starting
// stopped services. Validation/reload failures restore the prior file/config.
func (m *tlsFrontendManager) upgradeHTTPChallengeRoutes(ctx context.Context, deployments tlsDeployments) error {
	active, err := activeTLSFrontends(deployments)
	if err != nil {
		return err
	}
	modes := map[string]bool{}
	for _, f := range active {
		modes[f.Mode] = true
	}
	for mode := range modes {
		service := "qagent-tls-" + mode
		if m.services.Kind() == ServiceManagerSystemd {
			service += ".service"
		}
		status, _ := m.services.status(ctx, service)
		if status != "active" {
			continue
		}
		path := filepath.Join(m.root, mode+".conf")
		old, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := renderNginxFrontends(active, m.root, m.state, m.nginxHTTPRouteAvailable())
		if mode == "caddy" {
			content, err = renderCaddyFrontends(active, m.state)
			if err != nil {
				return err
			}
		}
		if content == string(old) {
			continue
		}
		staged := filepath.Join(m.root, mode+".challenge-upgrade.conf")
		if err = m.writeConfig(staged, content, false); err != nil {
			return err
		}
		defer os.Remove(staged)
		if mode == "caddy" {
			_, err = m.runCaddy(ctx, nil, "validate", "--config", staged)
		} else {
			_, err = run(ctx, managedNginxBinary(), "-t", "-p", m.state+"/", "-c", staged)
		}
		if err != nil {
			return fmt.Errorf("托管 HTTP-01 路由升级配置校验失败：%w", err)
		}
		if err = m.writeConfig(path, content, false); err != nil {
			return err
		}
		reload := func() error {
			var e error
			if mode == "caddy" {
				_, e = m.runCaddy(ctx, nil, "reload", "--config", path, "--address", "unix/"+filepath.Join(m.state, "caddy-admin.sock"))
			} else {
				_, e = run(ctx, managedNginxBinary(), "-p", m.state+"/", "-c", path, "-s", "reload")
			}
			return e
		}
		if err = reload(); err != nil {
			_ = m.writeConfig(path, string(old), false)
			_ = reload()
			return fmt.Errorf("托管 HTTP-01 路由升级失败，已恢复原配置：%w", err)
		}
	}
	return nil
}
