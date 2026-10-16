package agent

import (
	"context"
	"fmt"
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
	return nil
}

func (e *Executor) repairTLSRuntime(ctx context.Context) error {
	if !managedTLSStateExists() {
		return nil
	}
	spec := DefaultSpecsForServiceManager(e.serviceManager().Kind())[core.EngineSingBox]
	return e.executeTLSFrontend(ctx, spec, core.Task{Engine: core.EngineSingBox, Action: core.ActionStatus}, false)
}
