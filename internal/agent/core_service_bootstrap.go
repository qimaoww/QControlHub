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

const defaultCoreBootstrapScript = "/usr/local/share/qcontrolhub/core-install/deploy/bootstrap-core-services.sh"

const legacyCoreStateDirectoryInstall = `install -d -o "$service_user" -g "$service_group" -m 0750 "$state_directory"`

type coreServiceBootstrapper struct {
	scriptPath     string
	systemdRunPath string
}

func defaultCoreServiceBootstrapper() coreServiceBootstrapper {
	return coreServiceBootstrapper{
		scriptPath: defaultCoreBootstrapScript, systemdRunPath: "/usr/bin/systemd-run",
	}
}

// prepareManagedCoreService creates one QAgent-owned configuration and service
// only after the panel has submitted an explicit core install/import task.
// Existing deployments that already have a valid managed service do not need
// the staged bootstrap assets and continue to upgrade normally.
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
	if status == "not-found" {
		bootstrapper := defaultCoreServiceBootstrapper()
		if e.coreBootstrapper != nil {
			bootstrapper = *e.coreBootstrapper
		}
		if err := bootstrapper.install(ctx, engine, existing, manager); err != nil {
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
	if err := ensureManagedCoreLogStreaming(ctx, map[core.Engine]EngineSpec{engine: spec}, manager); err != nil {
		return fmt.Errorf("prepare managed %s logs: %w", engine, err)
	}
	return nil
}

func (bootstrapper coreServiceBootstrapper) install(ctx context.Context, engine core.Engine, existing bool, manager *ServiceManager) error {
	if !engine.Valid() {
		return errors.New("unsupported core bootstrap engine")
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
