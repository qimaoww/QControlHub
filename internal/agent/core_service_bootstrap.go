package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const legacyCoreStateDirectoryInstall = `install -d -o "$service_user" -g "$service_group" -m 0750 "$state_directory"`

type coreServiceBootstrapper struct {
	scriptPath     string
	systemdRunPath string
	assetRoot      string
}

func defaultCoreServiceBootstrapper() coreServiceBootstrapper {
	return coreServiceBootstrapper{
		assetRoot: coreInstallAssetRoot, systemdRunPath: "/usr/bin/systemd-run",
	}
}

// prepareManagedCoreService creates one QAgent-owned configuration and service
// only after the panel has submitted an explicit core install/import task.
// Existing deployments with a valid managed service skip bootstrap. When
// bootstrap is needed, use assets bundled with this Agent, not stale files
// left by an older installer in /usr/local/share.
func (e *Executor) prepareManagedCoreService(ctx context.Context, engine core.Engine, spec EngineSpec, existing bool) error {
	manager := e.serviceManager()
	defaultSpec, ok := DefaultSpecsForServiceManager(manager.Kind())[engine]
	if !ok || spec != defaultSpec {
		return nil
	}
	status, err := validateManagedServiceForExistingDiscovery(ctx, engine, spec, manager)
	if err != nil {
		return fmt.Errorf("refusing to replace an unrecognized managed %s service: %w", engine, err)
	}
	missing, err := managedCorePrerequisitesMissing(engine, spec)
	if err != nil {
		return fmt.Errorf("inspect managed %s prerequisites: %w", engine, err)
	}
	if status == "not-found" || missing {
		bootstrapper := defaultCoreServiceBootstrapper()
		if e.coreBootstrapper != nil {
			bootstrapper = *e.coreBootstrapper
		}
		if err := bootstrapper.install(ctx, engine, existing, manager); err != nil {
			return err
		}
		if missing, err := managedCorePrerequisitesMissing(engine, spec); err != nil || missing {
			if err == nil {
				err = errors.New("managed core prerequisites remain incomplete after bootstrap")
			}
			return err
		}
		status, err = validateManagedServiceForExistingDiscovery(ctx, engine, spec, manager)
		if err != nil || status == "not-found" {
			if err == nil {
				err = errors.New("service remains unavailable after bootstrap")
			}
			return fmt.Errorf("validate newly installed managed %s service: %w", engine, err)
		}
	}
	if err := ensureDefaultManagedConfigurationAccess(engine, spec, manager); err != nil {
		return fmt.Errorf("prepare managed %s configuration access: %w", engine, err)
	}
	if err := ensureManagedCoreLogStreaming(ctx, map[core.Engine]EngineSpec{engine: spec}, manager); err != nil {
		return fmt.Errorf("prepare managed %s logs: %w", engine, err)
	}
	return nil
}

// A previous bootstrap can exit after writing the unit but before finishing
// prerequisites. Never equate an existing unit with a complete installation.
// Only absence is repairable; unsafe files stay a hard error.
func managedCorePrerequisitesMissing(engine core.Engine, spec EngineSpec) (bool, error) {
	return managedCorePrerequisitesMissingAt(engine, spec, filepath.Join("/var/lib", "qcontrolhub-"+managedCoreAssetName(engine)))
}

func managedCorePrerequisitesMissingAt(engine core.Engine, spec EngineSpec, statePath string) (bool, error) {
	paths := []struct {
		path      string
		directory bool
	}{
		{filepath.Dir(spec.Binary), true},
		{filepath.Dir(spec.ConfigPath), true},
		{spec.ConfigPath, false},
		{statePath, true},
	}
	if engine == core.EngineShadowsocksRust {
		paths = append(paths, struct {
			path      string
			directory bool
		}{filepath.Join(filepath.Dir(spec.ConfigPath), "qch-mainland-block.acl"), false})
	}
	missing := false
	for _, item := range paths {
		info, err := os.Lstat(item.path)
		if errors.Is(err, os.ErrNotExist) {
			missing = true
			continue
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 ||
			(item.directory && !info.IsDir()) || (!item.directory && !info.Mode().IsRegular()) {
			return false, fmt.Errorf("unsafe managed prerequisite %s", item.path)
		}
	}
	return missing, nil
}

// prepareShadowsocksRustACLService refreshes the exact QAgent-owned service
// template before a destination-blocking policy is activated. This upgrades
// older nodes from a config-only ssserver command to the native --acl file
// without accepting or replacing administrator-owned service definitions.
func (e *Executor) prepareShadowsocksRustACLService(ctx context.Context, spec EngineSpec) error {
	manager := e.serviceManager()
	defaultSpec, ok := DefaultSpecsForServiceManager(manager.Kind())[core.EngineShadowsocksRust]
	if !ok || spec != defaultSpec {
		return errors.New("Shadowsocks Rust mainland destination blocking requires the QAgent-managed service")
	}
	servicePath, marker, command := shadowsocksRustACLServiceIdentity(manager, spec)
	if err := validateManagedUnitFile(servicePath); err == nil {
		if contents, readErr := os.ReadFile(servicePath); readErr == nil && strings.Contains(string(contents), marker) && strings.Contains(string(contents), command) {
			return nil
		}
	}
	bootstrapper := defaultCoreServiceBootstrapper()
	if e.coreBootstrapper != nil {
		bootstrapper = *e.coreBootstrapper
	}
	if err := bootstrapper.install(ctx, core.EngineShadowsocksRust, false, manager); err != nil {
		return fmt.Errorf("upgrade managed Shadowsocks Rust ACL service: %w", err)
	}
	if err := validateManagedUnitFile(servicePath); err != nil {
		return fmt.Errorf("validate upgraded Shadowsocks Rust service: %w", err)
	}
	contents, err := os.ReadFile(servicePath)
	if err != nil {
		return fmt.Errorf("read upgraded Shadowsocks Rust service: %w", err)
	}
	if !strings.Contains(string(contents), marker) || !strings.Contains(string(contents), command) {
		return errors.New("staged QAgent service assets do not support the Shadowsocks Rust ACL; redeploy the current QAgent package")
	}
	return nil
}

func shadowsocksRustACLServiceIdentity(manager *ServiceManager, spec EngineSpec) (string, string, string) {
	servicePath := filepath.Join(existingDiscoveryManagedUnitRoot, spec.Service)
	marker := "Description=Shadowsocks Rust core managed by QAgent"
	if manager.Kind() == ServiceManagerOpenRC {
		servicePath = filepath.Join(openRCInitRoot, spec.Service)
		marker = "# QControlHub managed OpenRC service: " + spec.Service
	}
	command := spec.Binary + " -c " + spec.ConfigPath + " --acl " + shadowsocksRustACLPath
	return servicePath, marker, command
}

func (bootstrapper coreServiceBootstrapper) install(ctx context.Context, engine core.Engine, existing bool, manager *ServiceManager) error {
	if !engine.Valid() {
		return errors.New("unsupported core bootstrap engine")
	}
	if bootstrapper.assetRoot != "" {
		scriptPath, err := stageBundledCoreInstallAssets(bootstrapper.assetRoot)
		if err != nil {
			return fmt.Errorf("prepare bundled core install assets: %w", err)
		}
		bootstrapper.scriptPath = scriptPath
	}
	if strings.TrimSpace(bootstrapper.scriptPath) == "" {
		return errors.New("managed core bootstrap assets are unavailable; redeploy QAgent from the node page")
	}
	if err := validatePrivilegedExecutable(bootstrapper.scriptPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("managed core bootstrap assets are unavailable; redeploy QAgent from the node page")
		}
		return fmt.Errorf("unsafe managed core bootstrap script: %w", err)
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(bootstrapper.scriptPath)); err != nil {
		return fmt.Errorf("unsafe managed core bootstrap directory: %w", err)
	}

	installContext, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if manager.Kind() == ServiceManagerOpenRC {
		return bootstrapper.runOpenRC(installContext, engine, existing)
	}
	return bootstrapper.runSystemd(installContext, engine, existing)
}

func (bootstrapper coreServiceBootstrapper) runSystemd(ctx context.Context, engine core.Engine, existing bool) error {
	if err := validatePrivilegedExecutable(bootstrapper.systemdRunPath); err != nil {
		return fmt.Errorf("unsafe managed core bootstrap helper: %w", err)
	}
	capabilities, err := managedCoreBootstrapCapabilities(bootstrapper.scriptPath)
	if err != nil {
		return fmt.Errorf("inspect managed core bootstrap compatibility: %w", err)
	}
	arguments := []string{
		"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec",
		"--property=User=root", "--property=UMask=0022", "--property=NoNewPrivileges=yes",
		"--property=CapabilityBoundingSet=" + capabilities, "--property=AmbientCapabilities=" + capabilities,
		"--property=ProtectSystem=strict", "--property=ProtectHome=yes", "--property=PrivateTmp=yes",
		"--property=PrivateDevices=yes", "--property=RestrictAddressFamilies=AF_UNIX",
		"--property=ConfigurationDirectory=qagent", "--property=StateDirectory=qcontrolhub-" + managedCoreAssetName(engine),
		"--property=ReadWritePaths=/etc/systemd", "--property=ReadWritePaths=/usr/local/lib/qagent",
		"--setenv=QCH_SERVICE_MANAGER=systemd",
	}
	if existing {
		arguments = append(arguments, "--setenv=QCH_SKIP_CORE_SERVICES="+managedCoreAssetName(engine))
	}
	arguments = append(arguments, "--", bootstrapper.scriptPath, managedCoreAssetName(engine))
	return runCoreBootstrapCommand(ctx, bootstrapper.systemdRunPath, arguments, nil)
}

// Older staged bootstrap assets use GNU install to chown a state directory
// before chmodding it. Once ownership changes, capability-bounded root needs
// CAP_FOWNER for the chmod. New assets order those operations safely and need
// only CAP_CHOWN; retain the additional transient capability solely while an
// upgraded Agent is still paired with the exact legacy command.
func managedCoreBootstrapCapabilities(scriptPath string) (string, error) {
	contents, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", err
	}
	capabilities := "CAP_CHOWN"
	if strings.Contains(string(contents), legacyCoreStateDirectoryInstall) {
		capabilities += " CAP_FOWNER"
	}
	return capabilities, nil
}

func (bootstrapper coreServiceBootstrapper) runOpenRC(ctx context.Context, engine core.Engine, existing bool) error {
	environment := []string{"QCH_SERVICE_MANAGER=openrc"}
	if existing {
		environment = append(environment, "QCH_SKIP_CORE_SERVICES="+managedCoreAssetName(engine))
	}
	return runCoreBootstrapCommand(ctx, bootstrapper.scriptPath, []string{managedCoreAssetName(engine)}, environment)
}

func runCoreBootstrapCommand(ctx context.Context, executable string, arguments, environment []string) error {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Env = append([]string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}, environment...)
	configureCommand(command)
	output := &boundedOutput{limit: 64 << 10}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(strings.ToValidUTF8(output.String(), "�"))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("install managed core service: %s", message)
	}
	return nil
}
