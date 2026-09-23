package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
)

const validationCleanupBinary = "/usr/bin/rm"

type EngineSpec struct {
	Binary           string
	ConfigPath       string
	ConfigDirectory  string
	ACLPath          string
	WorkingDirectory string
	ServiceBinary    string
	Service          string
	commandDirectory string
	commandEnv       string
}

type Executor struct {
	Specs                    map[core.Engine]EngineSpec
	ExistingSpecs            map[core.Engine]EngineSpec
	ExistingDiscoveryIssues  map[core.Engine]string
	MigrationMarkerPrefix    string
	Updater                  *CoreUpdater
	Services                 *ServiceManager
	coreBootstrapper         *coreServiceBootstrapper
	validationSystemdRunPath string
	specsMu                  sync.RWMutex
	migrationMu              sync.Mutex
	accountingConfigMu       sync.Mutex
	accountingConfigs        map[core.Engine]nativeAccountingConfigEntry
	completedMigrations      map[core.Engine]completedCoreMigration
	verifyCompletedMigration func(context.Context, EngineSpec, EngineSpec, *ServiceManager) error
}

type completedCoreMigration struct {
	Existing     EngineSpec
	Managed      EngineSpec
	SourceDigest string
}

var systemctlPath = "/usr/bin/systemctl"

func DefaultSpecs() map[core.Engine]EngineSpec {
	return map[core.Engine]EngineSpec{
		core.EngineMihomo:          {Binary: "/usr/local/lib/qagent/cores/mihomo", ConfigPath: "/etc/qagent/mihomo/config.yaml", Service: "qagent-mihomo.service"},
		core.EngineXray:            {Binary: "/usr/local/lib/qagent/cores/xray", ConfigPath: "/etc/qagent/xray/config.json", Service: "qagent-xray.service"},
		core.EngineSingBox:         {Binary: "/usr/local/lib/qagent/cores/sing-box", ConfigPath: "/etc/qagent/sing-box/config.json", Service: "qagent-sing-box.service"},
		core.EngineShadowsocksRust: {Binary: "/usr/local/lib/qagent/cores/ssserver", ConfigPath: "/etc/qagent/shadowsocks-rust/config.json", Service: "qagent-shadowsocks-rust.service"},
	}
}

func DefaultSpecsForServiceManager(kind string) map[core.Engine]EngineSpec {
	specs := DefaultSpecs()
	if strings.EqualFold(strings.TrimSpace(kind), ServiceManagerOpenRC) {
		for engine, spec := range specs {
			spec.Service = strings.TrimSuffix(spec.Service, ".service")
			specs[engine] = spec
		}
	}
	return specs
}

func (e *Executor) serviceManager() *ServiceManager {
	if e != nil && e.Services != nil {
		return e.Services
	}
	return defaultSystemdServiceManager()
}

func (e *Executor) Validate() error {
	if e == nil {
		return errors.New("agent executor is required")
	}
	if os.Geteuid() != 0 {
		return errors.New("Agent execution must run as root")
	}
	if err := e.serviceManager().validate(); err != nil {
		return err
	}
	if len(e.ExistingSpecs) > 0 && strings.TrimSpace(e.MigrationMarkerPrefix) == "" {
		return errors.New("existing core mappings require a migration state path")
	}
	for engine, spec := range e.Specs {
		if !engine.Valid() {
			return fmt.Errorf("invalid executor engine %q", engine)
		}
		if !safeServiceName(spec.Service) {
			return fmt.Errorf("unsafe service name %q", spec.Service)
		}
		if !filepath.IsAbs(spec.Binary) || !filepath.IsAbs(spec.ConfigPath) {
			return fmt.Errorf("live executor paths for %s must be absolute", engine)
		}
		if err := validatePrivilegedExecutable(spec.Binary); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("unsafe %s binary: %w", engine, err)
			}
			if err := validateCoreInstallDestination(spec.Binary); err != nil {
				return fmt.Errorf("unsafe %s install destination: %w", engine, err)
			}
		}
	}
	for engine, spec := range e.ExistingSpecs {
		if _, enabled := e.Specs[engine]; !enabled {
			return fmt.Errorf("existing %s mapping is not an enabled engine", engine)
		}
		if !supportedExistingServiceForManager(e.serviceManager(), engine, spec.Service) {
			return fmt.Errorf("unsupported existing %s service %q", engine, spec.Service)
		}
		if err := validateExistingSpecPaths(engine, spec); err != nil {
			return err
		}
		if err := validateExistingCoreExecutable(spec.Binary); err != nil {
			return fmt.Errorf("unsafe existing %s binary: %w", engine, err)
		}
		if err := validateProtectedDirectoryChain(filepath.Dir(spec.Binary)); err != nil {
			return fmt.Errorf("unsafe existing %s binary parent chain: %w", engine, err)
		}
		if err := validateExistingServiceExecutable(spec); err != nil {
			return fmt.Errorf("unsafe existing %s service executable: %w", engine, err)
		}
	}
	return nil
}

func (e *Executor) Execute(parent context.Context, task core.Task) (string, error) {
	if !task.Action.Valid() || !task.Engine.Valid() {
		return "", errors.New("task contains an unsupported action or engine")
	}
	e.specsMu.RLock()
	spec, ok := e.Specs[task.Engine]
	existing, hasExisting := e.ExistingSpecs[task.Engine]
	discoveryIssue := strings.TrimSpace(e.ExistingDiscoveryIssues[task.Engine])
	e.specsMu.RUnlock()
	if !ok {
		return "", fmt.Errorf("engine %s is not enabled on this agent", task.Engine)
	}
	if discoveryIssue != "" {
		return "", fmt.Errorf("%s core tasks are disabled because an existing service could not be mapped safely: %s", task.Engine, discoveryIssue)
	}
	if !safeServiceName(spec.Service) {
		return "", errors.New("configured service name is unsafe")
	}
	if task.SharedInstance {
		if task.Action != core.ActionDeploy ||
			!core.ValidAgentShareID(task.SharedTrafficID) {
			return "", errors.New("invalid shared core deployment task")
		}
		managerKind := e.serviceManager().Kind()
		if spec != DefaultSpecsForServiceManager(managerKind)[task.Engine] {
			return "", errors.New("shared core instance requires the standard managed service")
		}
		var err error
		spec, err = sharedInstanceSpec(task.Engine, spec, task.SharedTrafficID, managerKind)
		if err != nil {
			return "", err
		}
		if err := prepareSharedInstanceDirectory(task.Engine, spec, managerKind); err != nil {
			return "", fmt.Errorf("prepare shared core configuration: %w", err)
		}
		if err := e.ensureSharedInstanceService(parent, task.Engine, spec); err != nil {
			return "", fmt.Errorf("prepare shared core service: %w", err)
		}
	}
	if task.InstallIfMissing {
		if (task.Action != core.ActionValidate && task.Action != core.ActionDeploy) ||
			task.ConfigVersion < 1 || task.ConfigContent == "" || task.SharedTrafficID != "" ||
			task.CoreVersion != "" || task.CoreSource != "" {
			return "", errors.New("invalid automatic core installation task")
		}
		// This is one durable task with one immutable configuration snapshot.
		// Recheck locally: a retry or a preceding task may have installed the
		// core since the heartbeat. Never update an already installed binary.
		ctx, cancel := context.WithTimeout(parent, 4*time.Minute+45*time.Second)
		defer cancel()
		output := "core already installed; keeping the current version"
		if _, err := exec.LookPath(spec.Binary); err != nil {
			var installErr error
			output, installErr = e.Execute(ctx, core.Task{
				Action: core.ActionInstall, Engine: task.Engine, CoreVersion: core.CoreVersionStable,
			})
			if installErr != nil {
				return output, fmt.Errorf("stable core installation failed; configuration was not executed: %w", installErr)
			}
		}
		task.InstallIfMissing = false
		result, err := e.Execute(ctx, task)
		return output + "\n" + result, err
	}
	timeout := 45 * time.Second
	if task.Action == core.ActionInstall {
		timeout = 4 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	switch task.Action {
	case core.ActionReadManagedConfig:
		// The manual configuration page can inspect the QAgent-managed file
		// independently while an external service remains available to import.
		return e.readCurrentConfig(ctx, task.Engine, spec)
	case core.ActionReadConfig:
		// Reading an available external service remains the explicit preview step
		// for import. All other actions below continue to target the independent
		// QAgent-managed service so the two installations can coexist.
		if hasExisting {
			return e.readExistingConfig(ctx, task.Engine, spec, existing)
		}
		return e.readCurrentConfig(ctx, task.Engine, spec)
	case core.ActionImportExisting:
		if !hasExisting {
			completed, err := completedCoreMigrationMatches(e.MigrationMarkerPrefix, task.Engine, task.ConfigContent)
			if err != nil {
				return "", fmt.Errorf("check completed %s migration: %w", task.Engine, err)
			}
			if completed {
				return fmt.Sprintf("%s existing service migration was already completed with this configuration", task.Engine), nil
			}
			return "", fmt.Errorf("%s has no existing service pending manual import", task.Engine)
		}
		if err := e.prepareManagedCoreService(ctx, task.Engine, spec, true); err != nil {
			return "", err
		}
		return e.importExistingConfig(ctx, task.Engine, spec, existing, task.ConfigContent)
	case core.ActionValidate:
		prepared, warning := e.prepareNativeAccountingContent(ctx, task.Engine, spec, task.ConfigContent)
		if warning != "" {
			return warning, errors.New("独立出口校验失败，未执行内核校验")
		}
		task.ConfigContent = prepared
		return e.validate(ctx, task.Engine, spec, task.ConfigContent)
	case core.ActionDeploy:
		originalInput := task.ConfigContent
		prepared, accountingWarning := e.prepareNativeAccountingContent(ctx, task.Engine, spec, task.ConfigContent)
		if accountingWarning != "" {
			return accountingWarning, errors.New("独立出口校验失败，未部署配置")
		}
		if task.SharedInstance {
			stripped, stripErr := serverconfig.SharedInstanceRuntimeContent(task.Engine, prepared)
			if stripErr != nil {
				return "", fmt.Errorf("shared instance independent egress unavailable: %w", stripErr)
			}
			prepared = stripped
		}
		task.ConfigContent = prepared
		validation, err := e.validate(ctx, task.Engine, spec, task.ConfigContent)
		if err != nil {
			return validation, err
		}
		if spec == DefaultSpecsForServiceManager(e.serviceManager().Kind())[task.Engine] &&
			accountingWarning == "" && strings.Contains(originalInput, "qch-trf-") && originalInput != prepared {
			if err := rememberAccountingInput(spec.ConfigPath, originalInput, prepared); err != nil {
				return validation, fmt.Errorf("persist repeatable accounting source: %w", err)
			}
		}
		if task.Engine == core.EngineShadowsocksRust && mainlandDestinationPolicyEnabled(task.MainlandAccessPolicies) {
			if err := e.prepareShadowsocksRustACLService(ctx, spec); err != nil {
				return validation, err
			}
		}
		if err := ensureManagedCoreServiceCapabilities(ctx, task.Engine, spec, e.serviceManager()); err != nil {
			return validation, err
		}
		backup, err := atomicDeployManagedConfiguration(task.Engine, spec, e.serviceManager(), task.ConfigContent)
		if err != nil {
			return validation, err
		}
		restartOutput, err := serviceCommandAndVerifyWithManager(ctx, e.serviceManager(), spec.Service, core.ActionRestart)
		output := validation + "\ndeployed to " + spec.ConfigPath
		if accountingWarning != "" {
			output += "\n" + accountingWarning
		}
		if backup != "" {
			output += "\nbackup: " + backup
		}
		if restartOutput != "" {
			output += "\n" + restartOutput
		}
		if err != nil {
			rollbackOutput, rollbackErr := rollbackDeploy(spec.ConfigPath, backup)
			if rollbackOutput != "" {
				output += "\n" + rollbackOutput
			}
			if rollbackErr != nil {
				return output, fmt.Errorf("configuration deployed but service restart failed (%v); rollback also failed: %w", err, rollbackErr)
			}
			recoveryContext, recoveryCancel := context.WithTimeout(context.Background(), 30*time.Second)
			recoveryOutput, recoveryErr := serviceCommandAndVerifyWithManager(recoveryContext, e.serviceManager(), spec.Service, core.ActionRestart)
			recoveryCancel()
			if recoveryOutput != "" {
				output += "\nrollback restart: " + recoveryOutput
			}
			if recoveryErr != nil {
				return output, fmt.Errorf("configuration restart failed (%v); previous file was restored but service recovery failed: %w", err, recoveryErr)
			}
			return output, fmt.Errorf("configuration restart failed and the previous configuration was restored: %w", err)
		}
		return output, nil
	case core.ActionStart, core.ActionRestart:
		if err := ensureManagedCoreServiceCapabilities(ctx, task.Engine, spec, e.serviceManager()); err != nil {
			return "", err
		}
		if err := ensureDefaultManagedConfigurationAccess(task.Engine, spec, e.serviceManager()); err != nil {
			return "", fmt.Errorf("prepare managed %s configuration access: %w", task.Engine, err)
		}
		return serviceCommandAndVerifyWithManager(ctx, e.serviceManager(), spec.Service, task.Action)
	case core.ActionStop:
		return serviceCommandAndVerifyWithManager(ctx, e.serviceManager(), spec.Service, task.Action)
	case core.ActionStatus:
		return serviceStatusWithManager(ctx, e.serviceManager(), spec.Service)
	case core.ActionInstall:
		version, err := core.NormalizeCoreVersionSelector(task.CoreVersion)
		if err != nil {
			return "", err
		}
		source, err := core.NormalizeCoreSource(task.Engine, version, task.CoreSource)
		if err != nil {
			return "", err
		}
		if err := e.prepareManagedCoreService(ctx, task.Engine, spec, false); err != nil {
			return "", err
		}
		if err := ensureManagedCoreServiceCapabilities(ctx, task.Engine, spec, e.serviceManager()); err != nil {
			return "", err
		}
		updater := e.Updater
		if updater == nil {
			updater = NewCoreUpdater()
		}
		return updater.Install(ctx, task.Engine, spec, version, source, e.serviceManager())
	default:
		return "", fmt.Errorf("unsupported action %q", task.Action)
	}
}

func mainlandDestinationPolicyEnabled(policies []core.MainlandAccessPolicy) bool {
	for _, policy := range policies {
		if policy.Engine == core.EngineShadowsocksRust && policy.BlockMainlandDestination {
			return true
		}
	}
	return false
}
