package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"golang.org/x/sys/unix"
)

// Certificate storage and Caddy's application directories are independent.
// Keep the old storage root so existing ACME accounts and renewal locks survive.
func (m *tlsFrontendManager) caddyEnvironment() []string {
	return []string{"XDG_DATA_HOME=" + filepath.Join(m.state, "data"), "XDG_CONFIG_HOME=" + filepath.Join(m.state, "config"), "HOME=" + m.state}
}

func (m *tlsFrontendManager) runCaddy(ctx context.Context, identity *commandIdentity, args ...string) (string, error) {
	return runTLSCommand(ctx, m.state, m.caddyEnvironment(), managedCaddyBinary, identity, args...)
}

// nginx -t opens listening sockets as well as checking syntax and files.
// Match the proxy's non-root identity and its sole ambient capability. Dropping
// UID alone clears capabilities, even when the privileged helper has them.
func (m *tlsFrontendManager) validateNginx(ctx context.Context, path string) (string, error) {
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return "", err
	}
	identity.ambientCaps = []uintptr{unix.CAP_NET_BIND_SERVICE}
	return runTLSCommand(ctx, m.state, nil, managedNginxBinary(), &identity, "-t", "-p", m.state+"/", "-c", path)
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
	if err := m.retireCaddyCertificateHelper(ctx, deployments); err != nil {
		return err
	}
	if err := m.upgradeHTTPChallengeRoutes(ctx, deployments); err != nil {
		return err
	}
	return m.refreshNginxCertificates(ctx, deployments)
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
			content, err = upgradeCaddyHTTPChallengeConfig(string(old))
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
			_, err = m.validateNginx(ctx, staged)
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

// Migrate the committed runtime, preserving issuer fallback, storage and every
// business route. Re-rendering the manifest would reset runtime issuer choices.
func upgradeCaddyHTTPChallengeConfig(stored string) (string, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stored), &root); err != nil || root == nil {
		return "", fmt.Errorf("托管 Caddy 配置格式无效")
	}
	keys := []string{"apps", "http", "servers"}
	layers := []map[string]json.RawMessage{root}
	for _, key := range keys {
		var child map[string]json.RawMessage
		if err := json.Unmarshal(layers[len(layers)-1][key], &child); err != nil || child == nil {
			return "", fmt.Errorf("托管 Caddy 配置缺少 %s", key)
		}
		layers = append(layers, child)
	}
	servers := layers[len(layers)-1]
	if _, exists := servers["http_challenges"]; exists {
		return stored, nil
	}
	challenge, err := json.Marshal(managedCaddyHTTPChallenges())
	if err != nil {
		return "", err
	}
	servers["http_challenges"] = challenge
	for i := len(keys) - 1; i >= 0; i-- {
		raw, err := json.Marshal(layers[i+1])
		if err != nil {
			return "", err
		}
		layers[i][keys[i]] = raw
	}
	data, err := json.MarshalIndent(root, "", "  ")
	return string(data), err
}

// Keep certificate reloads independent of the control-plane connection. The
// helper serializes them with deployments and never starts stopped services.
func (e *Executor) maintainTLSCertificates(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check, cancel := context.WithTimeout(ctx, 3*time.Minute)
			spec := DefaultSpecsForServiceManager(e.serviceManager().Kind())[core.EngineSingBox]
			if err := e.executeTLSFrontendRequest(check, spec, core.Task{Engine: core.EngineSingBox, Action: core.ActionStatus}, false, true); err != nil && ctx.Err() == nil {
				slog.Warn("refresh managed Nginx certificates", "error", err)
			}
			cancel()
		}
	}
}
