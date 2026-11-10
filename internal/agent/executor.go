package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	SourceContext    string
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
	mainOperationMu            sync.Mutex
	importSnapshotDigests      sync.Map
	coreLogReady               func(context.Context, core.Engine, EngineSpec) error
	Specs                      map[core.Engine]EngineSpec
	ExistingSpecs              map[core.Engine]EngineSpec
	ExistingSources            map[core.Engine][]EngineSpec
	ExistingDiscoveryStatePath string
	RefreshExistingSources     func(context.Context) error
	ExistingDiscoveryIssues    map[core.Engine]string
	ManagedSafetyIssues        map[core.Engine]string
	importedResourceIssues     map[core.Engine]string
	MigrationMarkerPrefix      string
	Updater                    *CoreUpdater
	Services                   *ServiceManager
	coreBootstrapper           *coreServiceBootstrapper
	validationSystemdRunPath   string
	coreStateHelperPath        string
	tlsFrontendHelperPath      string
	specsMu                    sync.RWMutex
	migrationMu                sync.Mutex
	accountingConfigMu         sync.Mutex
	sharedStateMu              sync.Mutex
	sharedAccountingLocks      sync.Map
	accountingConfigs          map[string]nativeAccountingConfigEntry
	completedMigrations        map[core.Engine]completedCoreMigration
	verifyCompletedMigration   func(context.Context, EngineSpec, EngineSpec, *ServiceManager) error
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
	if err := e.validateManagedTargets(); err != nil {
		return err
	}
	for engine, spec := range e.ExistingSpecs {
		if err := e.validateImportMapping(engine, spec); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) validateManagedTargets() error {
	if e == nil {
		return errors.New("agent executor is required")
	}
	if os.Geteuid() != 0 {
		return errors.New("Agent execution must run as root")
	}
	if err := e.serviceManager().validate(); err != nil {
		return err
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
	return nil
}

func (e *Executor) validateImportMapping(engine core.Engine, spec EngineSpec) error {
	if strings.TrimSpace(e.MigrationMarkerPrefix) == "" {
		return errors.New("existing core mappings require a migration state path")
	}
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
	return nil
}

func (e *Executor) Execute(parent context.Context, task core.Task) (output string, resultErr error) {
	if !task.SharedInstance {
		e.mainOperationMu.Lock()
		defer e.mainOperationMu.Unlock()
	}

	if task.SharedInstance && task.Action.ChangesInstance() && task.Engine.Valid() && core.ValidAgentShareID(task.SharedTrafficID) {
		lock := e.sharedAccountingLock(task.Engine, task.SharedTrafficID)
		lock.Lock()
		defer lock.Unlock()
	}
	return e.execute(parent, task)
}

// Recursive preparation keeps the outer instance lock for the entire operation.
func (e *Executor) execute(parent context.Context, task core.Task) (output string, resultErr error) {
	if task.Action == core.ActionValidate || task.Action == core.ActionDeploy || task.Action == core.ActionImportExisting {
		redact := coreDiagnosticRedactor(task.Engine, task.ConfigContent)
		defer func() {
			output = redact(output)
			if resultErr != nil {
				resultErr = redactedCoreError{err: resultErr, message: redact(resultErr.Error())}
			}
		}()
	}
	if !task.Action.Valid() || !task.Engine.Valid() {
		return "", errors.New("task contains an unsupported action or engine")
	}
	if !task.SharedInstance && task.Action == core.ActionReadConfig && e.RefreshExistingSources != nil {
		if err := e.RefreshExistingSources(parent); err != nil {
			return "", fmt.Errorf("refresh existing service discovery: %w", err)
		}
	}
	e.specsMu.RLock()
	spec, ok := e.Specs[task.Engine]
	existing, hasExisting := e.ExistingSpecs[task.Engine]
	discoveryIssue := strings.TrimSpace(e.ExistingDiscoveryIssues[task.Engine])
	managedIssue := strings.TrimSpace(e.ManagedSafetyIssues[task.Engine])
	e.specsMu.RUnlock()
	if !task.SharedInstance && task.Action == core.ActionReadConfig && task.ExistingSourceID == "" && len(e.existingSources(task.Engine)) > 1 {
		return "", errors.New("select one of the discovered existing services before reading")
	}
	if !task.SharedInstance && task.ExistingSourceID != "" {
		var found bool
		for _, candidate := range e.existingSources(task.Engine) {
			if existingSourceID(candidate) == task.ExistingSourceID {
				existing, hasExisting, found = candidate, true, true
				break
			}
		}
		if !found {
			// A reconnect may redeliver the acknowledged import after discovery
			// has retired that source. Only its durable completed record may pass.
			record, err := readCoreMigrationRecord(e.MigrationMarkerPrefix, task.Engine)
			matches, matchErr := completedCoreMigrationMatches(e.MigrationMarkerPrefix, task.Engine, task.ConfigContent)
			if task.Action != core.ActionImportExisting || err != nil || matchErr != nil || record.State != coreMigrationComplete || record.SourceDigest != task.ExistingSourceID || !matches {
				return "", errors.New("selected existing source changed or is no longer available; refresh discovery")
			}
		}
		discoveryIssue = ""
	}
	if !ok {
		return "", fmt.Errorf("engine %s is not enabled on this agent", task.Engine)
	}
	if managedIssue != "" {
		return "", fmt.Errorf("managed %s target is unsafe: %s", task.Engine, managedIssue)
	}
	if !task.SharedInstance && discoveryIssue != "" && (task.Action == core.ActionImportExisting || task.Action == core.ActionReadConfig) {
		return "", fmt.Errorf("%s existing configuration source is unavailable: %s", task.Engine, discoveryIssue)
	}
	if !safeServiceName(spec.Service) {
		return "", errors.New("configured service name is unsafe")
	}
	if task.InstallIfMissing {
		if (task.Action != core.ActionValidate && task.Action != core.ActionDeploy) ||
			task.ConfigVersion < 1 || task.ConfigContent == "" || (task.SharedTrafficID != "" && !task.SharedInstance) ||
			task.CoreVersion != "" || task.CoreSource != "" {
			return "", errors.New("invalid automatic core installation task")
		}
	}
	if !task.SharedInstance && spec == DefaultSpecsForServiceManager(e.serviceManager().Kind())[task.Engine] {
		if _, err := validateManagedServiceTarget(parent, task.Engine, spec, e.serviceManager()); err != nil {
			return "", fmt.Errorf("verify managed %s service target: %w", task.Engine, err)
		}
	}
	if task.Action == core.ActionInstall && (task.SharedInstance || task.SharedTrafficID != "") && !validSharedCoreInstallTask(task) {
		return "", errors.New("shared installation only prepares a missing stable core; version changes require the owner")
	}
	if task.SharedInstance {
		if task.Action.SharedInstanceOperation() && (task.CoreVersion != "" || task.CoreSource != "" || task.InstallIfMissing) {
			return "", errors.New("instance lifecycle tasks cannot install or switch a shared binary")
		}
		if (task.Action != core.ActionInstall && task.Action != core.ActionDeploy && task.Action != core.ActionValidate && !task.Action.SharedInstanceOperation()) ||
			!core.ValidAgentShareID(task.SharedTrafficID) {
			return "", errors.New("invalid shared core deployment task")
		}
		managerKind := e.serviceManager().Kind()
		if spec != DefaultSpecsForServiceManager(managerKind)[task.Engine] {
			return "", errors.New("shared core instance requires the standard managed service")
		}
		if task.InstallIfMissing || task.Action == core.ActionInstall {
			if err := prepareSharedCorePrerequisites(spec); err != nil {
				return "", fmt.Errorf("prepare private core prerequisites: %w", err)
			}
		}
		var err error
		spec, err = sharedInstanceSpec(task.Engine, spec, task.SharedTrafficID, managerKind)
		if err != nil {
			return "", err
		}
		hasExisting = false
		if task.Action.ChangesInstance() {
			defer func() { e.recordSharedOperation(spec, task, resultErr) }()
		}
		if task.Action != core.ActionStatus && task.Action != core.ActionReadConfig && task.Action != core.ActionReadManagedConfig && task.Action != core.ActionStop {
			if err := prepareSharedInstanceDirectory(task.Engine, spec, managerKind); err != nil {
				return "", fmt.Errorf("prepare shared core configuration: %w", err)
			}
			if err := e.ensureSharedInstanceService(parent, task.Engine, spec); err != nil {
				return "", fmt.Errorf("prepare shared core service: %w", err)
			}
		}
		if e.coreLogReady != nil {
			if err := e.coreLogReady(parent, task.Engine, spec); err != nil {
				slog.Warn("shared core log collector is unavailable", "engine", task.Engine, "share_id", task.SharedTrafficID, "error", err)
			}
		}
		if task.Action == core.ActionInstall {
			if _, err := exec.LookPath(spec.Binary); err == nil {
				return "core already installed; keeping the current version", nil
			} else if _, statErr := os.Lstat(spec.Binary); !errors.Is(statErr, os.ErrNotExist) {
				return "", fmt.Errorf("existing core binary is not executable; owner intervention is required: %w", err)
			}
			ctx, cancel := context.WithTimeout(parent, 4*time.Minute)
			defer cancel()
			updater := e.Updater
			if updater == nil {
				updater = NewCoreUpdater()
			}
			return updater.install(ctx, task.Engine, spec, core.CoreVersionStable, "", e.serviceManager(), false)
		}
	}
	if task.InstallIfMissing {

		// This is one durable task with one immutable configuration snapshot.
		// Recheck locally: a retry or a preceding task may have installed the
		// core since the heartbeat. Never update an already installed binary.
		ctx, cancel := context.WithTimeout(parent, 4*time.Minute+core.ManagedTLSOperationTimeout)
		defer cancel()
		output := "core already installed; keeping the current version"
		if _, err := exec.LookPath(spec.Binary); err != nil {
			output = "[阶段:内核准备] 内核尚未安装，准备稳定版"
			if _, statErr := os.Lstat(spec.Binary); !errors.Is(statErr, os.ErrNotExist) {
				return output, fmt.Errorf("existing core binary is not executable; owner intervention is required: %w", err)
			}
			if task.Engine == core.EngineMihomo && task.Action == core.ActionDeploy {
				if err := checkMihomoListenerPorts(spec, task.ConfigContent, false); err != nil {
					return output, tlsPhaseError("依赖和环境检查", err)
				}
			}
			// Prepare without activation. The manual install path starts a default
			// configuration and must never be used by validate/deploy preparation.
			if !task.SharedInstance {
				if err := e.prepareConfigCore(ctx, task.Engine, spec); err != nil {
					return output, fmt.Errorf("[阶段:内核准备] %w", err)
				}
			}
			updater := e.Updater
			if updater == nil {
				updater = NewCoreUpdater()
			}
			var installErr error
			output, installErr = updater.install(ctx, task.Engine, spec, core.CoreVersionStable, "", e.serviceManager(), false)
			if installErr != nil {
				return output, fmt.Errorf("stable core installation failed; configuration was not executed: %w", installErr)
			}
		}
		if task.Action == core.ActionValidate && task.SharedInstance {
			// Validation uses the private paths prepared above, without starting a service.
			if err := preparePanelCertificates(spec, &task); err != nil {
				return output, err
			}
			task.InstallIfMissing = false
			if err := e.executeTLSFrontend(ctx, spec, task, true); err != nil {
				return output, err
			}
			prepared, warning := e.prepareNativeAccountingContent(ctx, task.Engine, spec, task.ConfigContent)
			if warning != "" {
				return output, errors.New(warning)
			}
			result, err := e.validate(ctx, task.Engine, spec, prepared)
			return output + "\n" + result, err
		}
		output = "[阶段:内核准备完成]\n" + output
		task.InstallIfMissing = false
		result, err := e.execute(ctx, task)
		if err == nil && task.Action == core.ActionDeploy && !task.SharedInstance {
			err = setServiceEnabled(ctx, spec.Service, true, e.serviceManager())
		}
		return output + "\n" + result, err
	}
	// A private first-use preparation can leave a shared binary installed
	// without a main service. Ordinary main deployment must safely prepare
	// its own unit too, without downloading or activating default content.
	if !task.SharedInstance && (task.Action == core.ActionDeploy || task.Action == core.ActionValidate) && spec == DefaultSpecsForServiceManager(e.serviceManager().Kind())[task.Engine] {
		if _, binaryErr := exec.LookPath(spec.Binary); binaryErr == nil {
			unitRoot := existingDiscoveryManagedUnitRoot
			if e.serviceManager().Kind() == ServiceManagerOpenRC {
				unitRoot = openRCInitRoot
			}
			_, unitErr := os.Lstat(filepath.Join(unitRoot, spec.Service))
			if unitErr != nil && !errors.Is(unitErr, os.ErrNotExist) {
				return "", unitErr
			}
			if errors.Is(unitErr, os.ErrNotExist) {
				if err := e.prepareConfigCore(parent, task.Engine, spec); err != nil {
					return "", tlsPhaseError("内核准备", err)
				}
				if task.Action == core.ActionDeploy {
					defer func() {
						if resultErr == nil {
							resultErr = setServiceEnabled(parent, spec.Service, true, e.serviceManager())
						}
					}()
				}
			}
		}
	}
	if err := preparePanelCertificates(spec, &task); err != nil {
		return "", err
	}
	timeout := 45 * time.Second
	if len(task.TLSFrontends) > 0 || (task.Engine == core.EngineSingBox || task.Engine == core.EngineXray || task.Engine == core.EngineMihomo) && managedTLSStateExists() {
		timeout = core.ManagedTLSOperationTimeout
	}
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
			plan, err := e.readExistingConfigPlan(ctx, task.Engine, spec, existing)
			if err != nil {
				return "", err
			}
			if task.ID != "" {
				e.importSnapshotDigests.Store(task.ID, plan)
			}
			return plan.managedContent, nil
		}
		return e.readCurrentConfig(ctx, task.Engine, spec)
	case core.ActionImportExisting:
		defer func() {
			if resultErr == nil && task.ConfigID != "" && task.ConfigVersion > 0 {
				actual, err := readConfigurationFile(spec.ConfigPath)
				if err == nil {
					identity := task
					identity.ConfigContent = actual
					err = e.commitSharedDeployment(spec, identity)
				}
				if err != nil {
					resultErr = fmt.Errorf("迁移完成，但托管配置关联未能留存，请核对服务后重试：%w", err)
				}
			}
		}()
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
		return e.importExistingConfig(ctx, task.Engine, spec, existing, task.ConfigContent, task.ImportSourceDigest)
	case core.ActionValidate:
		var report strings.Builder
		if err := e.executeTLSFrontend(ctx, spec, task, true, &report); err != nil {
			return "", err
		}
		prepared, warning := e.prepareNativeAccountingContent(ctx, task.Engine, spec, task.ConfigContent)
		if warning != "" {
			return warning, errors.New("独立出口校验失败，未执行内核校验")
		}
		task.ConfigContent = prepared
		value, err := e.validate(ctx, task.Engine, spec, task.ConfigContent)
		return report.String() + "\n" + value, tlsPhaseError("配置生成及校验", err)
	case core.ActionDeploy:
		var report strings.Builder
		if err := e.executeTLSFrontend(ctx, spec, task, true, &report); err != nil {
			return "", tlsPhaseError("依赖和环境检查", err)
		}
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
		if report.Len() > 0 {
			validation = report.String() + "\n" + validation
		}
		if err != nil {
			return validation, tlsPhaseError("配置生成及校验", err)
		}
		var certificateReport strings.Builder
		if err := e.executeTLSFrontendRequest(ctx, spec, task, true, true, &certificateReport); err != nil {
			return validation, tlsPhaseError("证书准备", err)
		}
		if certificateReport.Len() > 0 {
			validation += "\n" + certificateReport.String()
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
		previousStatus, err := e.serviceManager().status(ctx, spec.Service)
		if err != nil {
			return validation, fmt.Errorf("inspect service before deployment: %w", err)
		}
		applyAction := core.ActionRestart
		if plan, planErr := serverconfig.PrepareIndependentEgress(task.Engine, task.ConfigContent); planErr == nil && plan.Source == "disabled" {
			applyAction = core.ActionStop
		}
		if task.SharedInstance {
			endpoints, endpointErr := serverconfig.SharedTrafficEndpoints(task.Engine, task.ConfigContent)
			if endpointErr != nil {
				return validation, endpointErr
			}
			if len(endpoints) == 0 {
				applyAction = core.ActionStop
			}
		}
		// Persist uncertainty before changing the running process. If rollback
		// fails, matching old bytes alone cannot prove what the process loaded.
		trackIdentity := task.SharedInstance || task.ConfigID != ""
		previousIdentity, identityErr := readSharedDeploymentState(spec)
		restoreIdentity := func() error {
			if !trackIdentity {
				return nil
			}
			if identityErr == nil {
				return writeSharedDeploymentState(spec, previousIdentity)
			}
			if errors.Is(identityErr, os.ErrNotExist) {
				if removeErr := os.Remove(sharedStatePath(spec)); !errors.Is(removeErr, os.ErrNotExist) {
					return removeErr
				}
			}
			return nil
		}
		if trackIdentity {
			applying := previousIdentity
			if identityErr != nil {
				applying = sharedDeploymentState{}
			}
			applying.Applying = true
			if err := writeSharedDeploymentState(spec, applying); err != nil {
				return validation, err
			}
		}
		// Release only this instance's proxy routes after validation. The held
		// manifest retains rollback resources until the core and proxy commit.
		if err := e.executeTLSFrontendOperation(ctx, spec, task, false, false, "release"); err != nil {
			return validation, errors.Join(err, restoreIdentity())
		}
		defer func() {
			recovery, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			operation := "commit"
			if resultErr != nil {
				operation = "restore"
			}
			resultErr = errors.Join(resultErr, e.executeTLSFrontendOperation(recovery, spec, task, false, false, operation))
		}()
		if task.Engine == core.EngineMihomo {
			if err := checkMihomoListenerPorts(spec, task.ConfigContent, previousStatus == "active"); err != nil {
				return validation, errors.Join(err, restoreIdentity())
			}
		}

		if task.Engine == core.EngineXray {
			preflight := task.ConfigContent
			if task.SharedInstance {
				// Shared runtime removes the host's reserved statistics listener.
				// Probe new public listeners before touching the running instance.
				content, prepareErr := serverconfig.SharedInstanceRuntimeContent(task.Engine, preflight)
				if prepareErr != nil {
					return validation, errors.Join(prepareErr, restoreIdentity())
				}
				preflight = content
			}
			if err := checkXrayListenerPorts(spec, preflight, task.SharedInstance); err != nil {
				return validation, errors.Join(err, restoreIdentity())
			}
		}
		backup, err := atomicDeployManagedConfiguration(task.Engine, spec, e.serviceManager(), task.ConfigContent)
		if err != nil {
			return validation, errors.Join(err, restoreIdentity())
		}
		restartOutput, err := serviceCommandAndVerifyWithManager(ctx, e.serviceManager(), spec.Service, applyAction)
		err = tlsPhaseError("服务应用", err)
		if err == nil {
			err = e.executeTLSFrontend(ctx, spec, task, false)
		}
		if err == nil {
			err = tlsPhaseError("TLS与协议就绪检查", waitPanelCertificateListeners(ctx, task))
		}
		if err == nil && (task.SharedInstance || task.ConfigID != "") {
			err = e.commitSharedDeployment(spec, task)
		}
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
			recoveryContext, recoveryCancel := context.WithTimeout(context.Background(), 30*time.Second)
			recoveryOutput, recoveryErr := recoverConfigurationDeployment(recoveryContext, task.Engine, spec, e.serviceManager(), backup, previousStatus)
			recoveryCancel()
			if recoveryOutput != "" {
				output += "\n" + recoveryOutput
			}
			if recoveryErr != nil {
				return output, fmt.Errorf("configuration deployment failed (%v); rollback failed: %w", err, recoveryErr)
			}
			if recoveryErr = restoreIdentity(); recoveryErr != nil {
				return output, fmt.Errorf("恢复实例部署标识失败: %w", recoveryErr)
			}
			if backup == "" {
				return output, fmt.Errorf("configuration deployment failed; newly created configuration was removed and service was stopped: %w", err)
			}
			return output, fmt.Errorf("configuration deployment failed; previous configuration was restored with its service state: %w", err)
		}
		if applyAction == core.ActionStop {
			return output + "\n[阶段:提交成功状态] 空监听配置已应用，当前内核实例已停止", nil
		}
		return output + "\n[阶段:提交成功状态] 服务 active；TLS 与代理链路就绪（如配置托管 TLS）；部署已提交", nil
	case core.ActionStart, core.ActionRestart:
		if task.SharedInstance {
			if err := e.verifySharedStart(ctx, spec, task); err != nil {
				return "", err
			}
		}
		if err := ensureManagedCoreServiceCapabilities(ctx, task.Engine, spec, e.serviceManager()); err != nil {
			return "", err
		}
		if err := ensureDefaultManagedConfigurationAccess(task.Engine, spec, e.serviceManager()); err != nil {
			return "", fmt.Errorf("prepare managed %s configuration access: %w", task.Engine, err)
		}
		output, err := serviceCommandAndVerifyWithManager(ctx, e.serviceManager(), spec.Service, task.Action)
		if err == nil {
			err = e.executeTLSFrontend(ctx, spec, task, false)
		}
		return output, err
	case core.ActionStop:
		output, err := serviceCommandAndVerifyWithManager(ctx, e.serviceManager(), spec.Service, task.Action)
		return output, errors.Join(err, e.executeTLSFrontend(ctx, spec, task, false))
	case core.ActionStatus:
		if err := e.identifyRetainedDeployment(ctx, spec, task); err != nil {
			return "", err
		}
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
		_, statErr := os.Lstat(spec.Binary)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return "", fmt.Errorf("inspect managed core binary before installation: %w", statErr)
		}
		freshInstall := errors.Is(statErr, os.ErrNotExist)
		if freshInstall {
			if spec == DefaultSpecsForServiceManager(e.serviceManager().Kind())[task.Engine] {
				status, err := serviceStatusWithManager(ctx, e.serviceManager(), spec.Service)
				if err != nil {
					return "", fmt.Errorf("inspect service before first installation: %w", err)
				}
				if status == "active" {
					return "", fmt.Errorf("service %s is running without its installed binary; restore that binary before updating so rollback remains possible", spec.Service)
				}
			}
			if err := e.prepareManagedCoreService(ctx, task.Engine, spec, false); err != nil {
				return "", err
			}
		} else if err := ensureManagedCoreBinaryAccess(task.Engine, spec, e.serviceManager()); err != nil {
			return "", err
		}
		// Updating an installed core must not bootstrap a missing main config
		// or restart a working log backend before compatibility is established.
		// The transaction checks permissions for each configured service; a
		// shared-only deployment need not have a main service to synchronize.
		updater := e.Updater
		if updater == nil {
			updater = NewCoreUpdater()
		}
		output, err := updater.installWithTransaction(ctx, task.Engine, spec, version, source, e.serviceManager(), true,
			managedCoreUpdateTransaction(e, task.Engine, spec, e.serviceManager()))
		if err != nil {
			return output, err
		}
		// Uninstall disables the service so it cannot try a missing binary at
		// boot. A fresh installation restores enablement, while a version update
		// leaves an existing service's enablement unchanged.
		if freshInstall {
			if err := setServiceEnabled(ctx, spec.Service, true, e.serviceManager()); err != nil {
				// The updater has already written the binary and started the
				// service. Roll back a first install if enablement fails; otherwise
				// a retry sees an existing binary and skips this step forever.
				rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), 30*time.Second)
				rollbackOutput, rollbackErr := e.uninstallCore(rollbackCtx, task.Engine, spec)
				rollbackCancel()
				if rollbackOutput != "" {
					output += "\nrollback: " + rollbackOutput
				}
				if rollbackErr != nil {
					return output, fmt.Errorf("core installed but could not enable its service (%v); rollback failed: %w", err, rollbackErr)
				}
				return output, fmt.Errorf("core installation was rolled back because its service could not be enabled: %w", err)
			}
		}
		return output, nil
	case core.ActionUninstall:
		if err := e.executeTLSFrontend(ctx, spec, task, false); err != nil {
			return "", err
		}
		return e.uninstallCore(ctx, task.Engine, spec)
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

// Each allocation has its own lock: sampling never observes a half-written
// deployment digest, and another user's core continues sampling independently.
func (e *Executor) sharedAccountingLock(engine core.Engine, shareID string) *sync.RWMutex {
	key := string(engine) + "/" + shareID
	lock, _ := e.sharedAccountingLocks.LoadOrStore(key, &sync.RWMutex{})
	return lock.(*sync.RWMutex)
}
