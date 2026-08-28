package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type EngineSpec struct {
	Binary           string
	ConfigPath       string
	ConfigDirectory  string
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
	specsMu                  sync.RWMutex
	migrationMu              sync.Mutex
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

func supportedExistingService(engine core.Engine, service string) bool {
	return supportedExistingServiceForManager(defaultSystemdServiceManager(), engine, service)
}

func supportedExistingServiceForManager(manager *ServiceManager, engine core.Engine, service string) bool {
	if manager != nil && manager.Kind() == ServiceManagerOpenRC {
		return (engine == core.EngineXray && service == "xray") ||
			(engine == core.EngineSingBox && (service == "sing-box" || service == "singbox"))
	}
	return (engine == core.EngineXray && service == "xray.service") ||
		(engine == core.EngineSingBox && (service == "sing-box.service" || service == "singbox.service"))
}

func validatePrivilegedExecutable(path string) error {
	return validateExecutableMetadata(path, false)
}

func validateExecutableMetadata(path string, allowOrphanOwner bool) error {
	if !filepath.IsAbs(path) {
		return errors.New("executable path is not absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("executable must be a regular, non-symlink file")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return errors.New("file is not executable")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return errors.New("executable is writable by group or others")
	}
	if err := validateOwner(info, "privileged executable"); err != nil {
		if !allowOrphanOwner || !fileOwnerIsInactiveAndUnassigned(info) {
			return err
		}
	}
	directoryInfo, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return err
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() || directoryInfo.Mode().Perm()&0o022 != 0 {
		return errors.New("executable directory is symlinked or writable by group/others")
	}
	return validateOwner(directoryInfo, "executable directory")
}

func existingServiceBinary(spec EngineSpec) string {
	if spec.ServiceBinary != "" {
		return spec.ServiceBinary
	}
	return spec.Binary
}

// validateExistingServiceExecutable accepts the real core directly, a symlink
// to that core, or one narrowly defined forwarding script. The forwarding
// script must contain exactly a /bin/sh shebang and an unconditional
// `exec <protected-real-core> "$@"`; arbitrary wrappers are never invoked or
// copied into the managed core namespace.
func validateExistingServiceExecutable(spec EngineSpec) error {
	serviceBinary := existingServiceBinary(spec)
	if strings.ContainsAny(serviceBinary+spec.Binary, " \t\r\n") {
		return errors.New("service executable mapping contains unsupported whitespace")
	}
	if serviceBinary == spec.Binary {
		if err := validateProtectedDirectoryChain(filepath.Dir(spec.Binary)); err != nil {
			return fmt.Errorf("service executable parent chain: %w", err)
		}
		return validateExistingCoreExecutable(spec.Binary)
	}
	if !filepath.IsAbs(serviceBinary) {
		return errors.New("service executable path is not absolute")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(serviceBinary)); err != nil {
		return fmt.Errorf("service executable parent chain: %w", err)
	}
	serviceInfo, err := os.Lstat(serviceBinary)
	if err != nil {
		return err
	}
	if serviceInfo.Mode()&os.ModeSymlink == 0 {
		return errors.New("alternate service executable must be a symlink")
	}
	resolved, err := os.Readlink(serviceBinary)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(serviceBinary), resolved)
	}
	resolved = filepath.Clean(resolved)
	resolvedInfo, err := os.Lstat(resolved)
	if err != nil {
		return err
	}
	if resolvedInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("service executable must use at most one symlink")
	}
	if resolved == spec.Binary {
		if err := validateProtectedDirectoryChain(filepath.Dir(spec.Binary)); err != nil {
			return fmt.Errorf("real core parent chain: %w", err)
		}
		return validateExistingCoreExecutable(spec.Binary)
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(resolved)); err != nil {
		return fmt.Errorf("forwarder parent chain: %w", err)
	}
	if err := validatePrivilegedExecutable(resolved); err != nil {
		return fmt.Errorf("forwarder script: %w", err)
	}
	contents, err := os.ReadFile(resolved)
	if err != nil {
		return err
	}
	if len(contents) > 1024 {
		return errors.New("forwarder script exceeds the supported fixed form")
	}
	want := "#!/bin/sh\nexec " + spec.Binary + " \"$@\"\n"
	if string(contents) != want {
		return errors.New("service wrapper is not the supported fixed exec forwarder")
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(spec.Binary)); err != nil {
		return fmt.Errorf("real core parent chain: %w", err)
	}
	return validateExistingCoreExecutable(spec.Binary)
}

func validateNativeCoreExecutable(path string) error {
	return validateNativeCoreExecutableWithOwnerPolicy(path, false)
}

// validateExistingCoreExecutable permits one historical installer artifact:
// 233boy archives can preserve a numeric owner such as 1001 even though that
// UID has no account on the target. The exception applies only to fixed,
// whitelisted real-core paths and only while no live thread holds that UID.
// Every managed/helper executable and every other source remains root-owned.
func validateExistingCoreExecutable(path string) error {
	return validateNativeCoreExecutableWithOwnerPolicy(path, installerCoreAllowsOrphanOwner(path))
}

// existingCoreInvocationSpec invokes every fixed installer-path core through a
// root-owned private copy without restoring CAP_DAC_OVERRIDE to QAgent. This
// covers both historical orphan-owned archives with a missing other-execute
// bit and installer directories whose execution policy can deny the original
// path even when the core itself is root-owned. Every source must first pass
// the full owner-policy, protected-path, regular-file, and native-core checks;
// cores outside the four fixed compatibility paths continue to run in place.
func (e *Executor) existingCoreInvocationSpec(engine core.Engine, managed, existing EngineSpec) (EngineSpec, func(), error) {
	if err := validateProtectedDirectoryChain(filepath.Dir(existing.Binary)); err != nil {
		return EngineSpec{}, func() {}, err
	}
	if err := validateExistingCoreExecutable(existing.Binary); err != nil {
		return EngineSpec{}, func() {}, err
	}
	info, err := os.Lstat(existing.Binary)
	if err != nil {
		return EngineSpec{}, func() {}, err
	}
	if !installerCoreAllowsOrphanOwner(existing.Binary) {
		return existing, func() {}, nil
	}

	stagingDirectory := filepath.Dir(managed.ConfigPath)
	if e != nil && e.MigrationMarkerPrefix != "" {
		stagingDirectory = filepath.Dir(e.MigrationMarkerPrefix)
	}
	if !filepath.IsAbs(stagingDirectory) {
		return EngineSpec{}, func() {}, errors.New("existing core invocation directory is not absolute")
	}
	if _, err := os.Lstat(stagingDirectory); errors.Is(err, os.ErrNotExist) {
		if err := validateProtectedDirectoryChain(filepath.Dir(stagingDirectory)); err != nil {
			return EngineSpec{}, func() {}, fmt.Errorf("existing core invocation directory parent is unsafe: %w", err)
		}
		if err := os.Mkdir(stagingDirectory, 0o700); err != nil {
			return EngineSpec{}, func() {}, err
		}
	} else if err != nil {
		return EngineSpec{}, func() {}, err
	}
	if err := validateProtectedDirectoryChain(stagingDirectory); err != nil {
		return EngineSpec{}, func() {}, fmt.Errorf("existing core invocation directory is unsafe: %w", err)
	}

	destinationRoot, err := os.OpenRoot(stagingDirectory)
	if err != nil {
		return EngineSpec{}, func() {}, err
	}
	tempName, err := randomCoreTempName(destinationRoot)
	if err != nil {
		destinationRoot.Close()
		return EngineSpec{}, func() {}, err
	}
	cleanup := func() {
		_ = destinationRoot.Remove(tempName)
		_ = destinationRoot.Close()
	}

	sourceInfo, err := os.Lstat(existing.Binary)
	if err != nil || !os.SameFile(info, sourceInfo) {
		cleanup()
		return EngineSpec{}, func() {}, errors.New("existing core binary changed before its invocation copy was opened")
	}
	sourceRoot, err := os.OpenRoot(filepath.Dir(existing.Binary))
	if err != nil {
		cleanup()
		return EngineSpec{}, func() {}, err
	}
	input, err := sourceRoot.Open(filepath.Base(existing.Binary))
	if err != nil {
		sourceRoot.Close()
		cleanup()
		return EngineSpec{}, func() {}, err
	}
	openedInfo, err := input.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		input.Close()
		sourceRoot.Close()
		cleanup()
		return EngineSpec{}, func() {}, errors.New("existing core binary changed while its invocation copy was opened")
	}
	if openedInfo.Size() <= 0 || openedInfo.Size() > maxReleaseAssetSize {
		input.Close()
		sourceRoot.Close()
		cleanup()
		return EngineSpec{}, func() {}, errors.New("existing core invocation copy source has an invalid size")
	}

	output, err := destinationRoot.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		input.Close()
		sourceRoot.Close()
		cleanup()
		return EngineSpec{}, func() {}, err
	}
	written, copyErr := io.Copy(output, io.LimitReader(input, maxReleaseAssetSize+1))
	input.Close()
	sourceRoot.Close()
	if copyErr == nil && (written <= 0 || written > maxReleaseAssetSize) {
		copyErr = errors.New("existing core invocation copy exceeded the supported limit")
	}
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		cleanup()
		return EngineSpec{}, func() {}, copyErr
	}
	if closeErr != nil {
		cleanup()
		return EngineSpec{}, func() {}, closeErr
	}
	if err := destinationRoot.Chmod(tempName, 0o700); err != nil {
		cleanup()
		return EngineSpec{}, func() {}, err
	}

	invocationPath := filepath.Join(stagingDirectory, tempName)
	if err := validateNativeCoreExecutable(invocationPath); err != nil {
		cleanup()
		return EngineSpec{}, func() {}, fmt.Errorf("validate existing core invocation copy: %w", err)
	}
	invocation := existing
	invocation.Binary = invocationPath
	invocation.commandDirectory = filepath.Dir(existing.Binary)
	if engine == core.EngineXray {
		invocation.commandEnv = "XRAY_LOCATION_ASSET=" + filepath.Dir(existing.Binary)
	}
	return invocation, cleanup, nil
}

func validateNativeCoreExecutableWithOwnerPolicy(path string, allowOrphanOwner bool) error {
	if err := validateExecutableMetadata(path, allowOrphanOwner); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	prefix := make([]byte, 2)
	if _, err := io.ReadFull(file, prefix); err != nil {
		return err
	}
	if string(prefix) == "#!" {
		return errors.New("real core executable must not be a script")
	}
	return nil
}

func validateProtectedDirectoryChain(directory string) error {
	return validateDirectoryChain(directory, false)
}

// validateOpenRCStateDirectoryChain validates a directory inside OpenRC's own
// runtime state. OpenRC creates /run/openrc as root:root 0775. Tolerating
// exactly that policy shape — root owner, root group, no world-write — makes
// the stock state readable. This is a real but deliberately narrow relaxation
// because a non-root account could be a member of gid 0; every path outside the
// supplied OpenRC state root keeps the stricter rule.
func validateOpenRCStateDirectoryChain(directory, stateRoot string) error {
	relative, err := filepath.Rel(stateRoot, directory)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("OpenRC state directory is outside the OpenRC state root")
	}
	if err := validateDirectoryChain(directory, true); err != nil {
		return err
	}
	// The gid-0 write exception ends at the state root. Its parent chain (for
	// stock OpenRC, /run and /) is still a general protected path and therefore
	// must remain non-writable by both group and others.
	return validateProtectedDirectoryChain(filepath.Dir(filepath.Clean(stateRoot)))
}

func validateDirectoryChain(directory string, allowRootGroupWrite bool) error {
	if !filepath.IsAbs(directory) {
		return errors.New("path is not absolute")
	}
	for {
		info, err := os.Lstat(directory)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%s is not a real directory", directory)
		}
		if info.Mode().Perm()&0o022 != 0 {
			// A sticky directory is a safe traversal boundary: another user
			// cannot replace a protected child entry. This keeps tests and
			// deliberately staged installations below /tmp safe without
			// accepting a writable non-sticky parent.
			if info.Mode()&os.ModeSticky != 0 {
				if err := validateOwnerOrRoot(info, "sticky protected path parent"); err != nil {
					return err
				}
				return nil
			}
			if !allowRootGroupWrite || !rootOwnedRootGroupDirectory(info) {
				return fmt.Errorf("%s is writable by group or others", directory)
			}
		}
		if err := validateOwner(info, "protected path parent"); err != nil {
			return err
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return nil
		}
		directory = parent
	}
}

// rootOwnedRootGroupDirectory reports whether a directory has the exact relaxed
// OpenRC ownership shape: root owner, root group, and no world-write bit. Any
// unknown ownership fails closed.
func rootOwnedRootGroupDirectory(info os.FileInfo) bool {
	if info.Mode().Perm()&0o002 != 0 {
		return false
	}
	uid, gid, known := fileOwnership(info)
	return known && uid == 0 && gid == 0
}

// validateExistingSpecPaths checks the pure structural path constraints of an
// existing core mapping: absolute executable and configuration paths, no
// whitespace anywhere, and an absolute configuration/working directory for
// sing-box. It is independent of the running UID so the same fail-closed rule
// can be asserted by tests running as root or as an unprivileged CI user.
func validateExistingSpecPaths(engine core.Engine, spec EngineSpec) error {
	// A directory-authoritative mapping carries no main configuration file, so
	// the confdir stands in as the single required absolute source.
	configPathMapped := filepath.IsAbs(spec.ConfigPath) ||
		(spec.ConfigPath == "" && filepath.IsAbs(spec.ConfigDirectory))
	if !filepath.IsAbs(spec.Binary) || !configPathMapped {
		return fmt.Errorf("existing %s paths must be absolute", engine)
	}
	for label, path := range map[string]string{
		"binary": spec.Binary, "configuration": spec.ConfigPath,
		"configuration directory": spec.ConfigDirectory, "service executable": existingServiceBinary(spec),
		"working directory": spec.WorkingDirectory,
	} {
		if strings.ContainsAny(path, " \t\r\n") {
			return fmt.Errorf("existing %s %s path contains unsupported whitespace", engine, label)
		}
	}
	// Xray reads a confdir via -confdir and sing-box via -C; both are supported.
	// A working directory remains a sing-box-only packaging shape.
	if spec.ConfigDirectory != "" && !filepath.IsAbs(spec.ConfigDirectory) {
		return fmt.Errorf("existing %s configuration directory is unsupported or not absolute", engine)
	}
	if spec.WorkingDirectory != "" && (engine != core.EngineSingBox || !filepath.IsAbs(spec.WorkingDirectory)) {
		return fmt.Errorf("existing %s working directory is unsupported or not absolute", engine)
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
		return e.validate(ctx, task.Engine, spec, task.ConfigContent)
	case core.ActionDeploy:
		validation, err := e.validate(ctx, task.Engine, spec, task.ConfigContent)
		if err != nil {
			return validation, err
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

func (e *Executor) readCurrentConfig(ctx context.Context, engine core.Engine, spec EngineSpec) (string, error) {
	content, err := readConfigurationFile(spec.ConfigPath)
	if err != nil {
		return "", fmt.Errorf("read current %s configuration: %w", engine, err)
	}
	if err := validatePrivilegedExecutable(spec.Binary); err != nil {
		return "", fmt.Errorf("cannot safely invoke %s for current configuration validation: %w", engine, err)
	}
	defaultSpec, managed := DefaultSpecsForServiceManager(e.serviceManager().Kind())[engine]
	if managed && spec == defaultSpec && engine != core.EngineShadowsocksRust {
		_, err = e.validateManagedServiceSnapshot(ctx, engine, spec, content)
	} else {
		_, err = e.validateSnapshot(ctx, engine, spec, content)
	}
	if err != nil {
		return "", fmt.Errorf("current %s configuration failed real core validation: %w", engine, err)
	}
	return content, nil
}

func (e *Executor) readExistingConfig(ctx context.Context, engine core.Engine, managed, existing EngineSpec) (string, error) {
	var content, sourceDigest string
	var err error
	if engine == core.EngineXray && existing.ConfigDirectory != "" {
		sourceDigest, err = readExistingXraySourceDigest(existing)
	} else {
		content, sourceDigest, err = readExistingConfigurationSources(existing)
	}
	if err != nil {
		return "", fmt.Errorf("read existing %s configuration: %w", engine, err)
	}
	if existing.WorkingDirectory != "" {
		if err := validateNoRelativeSingBoxResources(content); err != nil {
			return "", fmt.Errorf("existing %s configuration has a resource that cannot be migrated safely: %w", engine, err)
		}
	}
	if err := validateExistingCoreExecutable(existing.Binary); err != nil {
		return "", fmt.Errorf("cannot safely invoke existing %s binary: %w", engine, err)
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(existing.Binary)); err != nil {
		return "", fmt.Errorf("cannot safely traverse existing %s binary path: %w", engine, err)
	}
	if err := validateExistingServiceExecutable(existing); err != nil {
		return "", fmt.Errorf("cannot safely map existing %s service executable: %w", engine, err)
	}
	invocationSpec, cleanupInvocation, err := e.existingCoreInvocationSpec(engine, managed, existing)
	if err != nil {
		return "", fmt.Errorf("prepare existing %s binary for protected invocation: %w", engine, err)
	}
	defer cleanupInvocation()
	validationSpec := managed
	validationSpec.Binary = invocationSpec.Binary
	validationSpec.commandEnv = invocationSpec.commandEnv
	if engine == core.EngineXray && existing.ConfigDirectory != "" {
		// Xray's multi-file semantics are typed and tag-aware: later scalar
		// sections replace earlier ones, while same-tag inbounds/outbounds are
		// updated rather than appended. Reusing sing-box's generic JSON merge
		// would silently change a valid service. Ask the protected source binary
		// for its canonical merged form instead and fail closed if it cannot dump
		// one.
		content, err = dumpExistingXrayConfiguration(ctx, invocationSpec)
		if err != nil {
			return "", fmt.Errorf("dump existing Xray configuration sources: %w", err)
		}
	} else if engine != core.EngineXray {
		if err := validateExistingSourceInvocation(ctx, engine, invocationSpec); err != nil {
			return "", fmt.Errorf("existing %s configuration sources failed real core validation: %w", engine, err)
		}
	}
	switch engine {
	case core.EngineXray:
		content, err = normalizeImportedXrayLogDestinations(content)
		if err != nil {
			return "", fmt.Errorf("normalize existing Xray log destinations: %w", err)
		}
	case core.EngineSingBox:
		content, err = normalizeImportedSingBoxLogDestination(content)
		if err != nil {
			return "", fmt.Errorf("normalize existing sing-box log destination: %w", err)
		}
	}
	if _, err := e.validateSnapshot(ctx, engine, validationSpec, content); err != nil {
		return "", fmt.Errorf("existing %s configuration failed real core validation: %w", engine, err)
	}
	var currentDigest string
	if engine == core.EngineXray && existing.ConfigDirectory != "" {
		currentDigest, err = readExistingXraySourceDigest(existing)
	} else {
		_, currentDigest, err = readExistingConfigurationSources(existing)
	}
	if err != nil || currentDigest != sourceDigest {
		if err == nil {
			err = errors.New("configuration sources changed while the snapshot was validated")
		}
		return "", fmt.Errorf("recheck existing %s configuration sources: %w", engine, err)
	}
	return content, nil
}

// readExistingXraySourceDigest reads and fingerprints every exact source that
// Xray will merge. It deliberately does not apply the sing-box JSON merger:
// Xray's own -dump output is the only configuration snapshot used for import.
func readExistingXraySourceDigest(spec EngineSpec) (string, error) {
	if spec.ConfigDirectory == "" {
		return "", errors.New("Xray configuration directory is empty")
	}
	if err := validateProtectedDirectoryChain(spec.ConfigDirectory); err != nil {
		return "", fmt.Errorf("configuration directory parent chain is unsafe: %w", err)
	}
	entries, err := os.ReadDir(spec.ConfigDirectory)
	if err != nil {
		return "", err
	}
	sources := make([]existingConfigSource, 0, len(entries)+1)
	// Unlike sing-box's official -C-only form, any Xray ConfigPath here came
	// from an explicit -config flag. If it also lives in the confdir, Xray reads
	// it once from the flag and once from the directory, so preserve both source
	// occurrences in the size budget and digest.
	if spec.ConfigPath != "" {
		primary, err := readConfigurationFile(spec.ConfigPath)
		if err != nil {
			return "", err
		}
		sources = append(sources, existingConfigSource{path: spec.ConfigPath, content: primary})
	}
	for _, entry := range entries {
		if !isXrayConfigurationFilename(entry.Name()) {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return "", fmt.Errorf("configuration directory entry %q is not a regular non-symlink Xray configuration file", entry.Name())
		}
		path := filepath.Join(spec.ConfigDirectory, entry.Name())
		contents, err := readConfigurationFile(path)
		if err != nil {
			return "", fmt.Errorf("read configuration directory entry %q: %w", entry.Name(), err)
		}
		sources = append(sources, existingConfigSource{path: path, content: contents})
	}
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].path < sources[j].path })
	total := 0
	digest := sha256.New()
	for _, source := range sources {
		total += len(source.content)
		if total > core.MaxConfigBytes {
			return "", fmt.Errorf("combined configuration sources exceed %d bytes", core.MaxConfigBytes)
		}
		digest.Write([]byte(source.path))
		digest.Write([]byte{0})
		digest.Write([]byte(source.content))
		digest.Write([]byte{0})
	}
	if len(sources) == 0 {
		return "", errors.New("Xray configuration directory has no supported configuration sources")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func isXrayConfigurationFilename(name string) bool {
	// Xray's confdir regex is case-sensitive; keep this list exact so the
	// fingerprint covers precisely the files its loader selects.
	switch filepath.Ext(name) {
	case ".json", ".jsonc", ".toml", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

// dumpExistingXrayConfiguration invokes Xray's own config merger over the exact
// protected sources discovered from the service argv. stdout is the canonical
// JSON snapshot; diagnostics stay on stderr so they can never be mistaken for
// configuration content.
func dumpExistingXrayConfiguration(ctx context.Context, spec EngineSpec) (string, error) {
	args := []string{"run", "-dump"}
	if spec.ConfigPath != "" {
		args = append(args, "-config", spec.ConfigPath)
	}
	if spec.ConfigDirectory != "" {
		args = append(args, "-confdir", spec.ConfigDirectory)
	}
	commandContext, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(commandContext, spec.Binary, args...)
	command.Dir = spec.commandDirectory
	if command.Dir == "" {
		command.Dir = filepath.Dir(spec.Binary)
	}
	command.Env = commandEnvironment(spec.commandEnv)
	configureCommand(command)
	stdout := &boundedOutput{limit: core.MaxConfigBytes, onLimit: cancel}
	stderr := &boundedOutput{limit: 64 << 10, onLimit: cancel}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if stdout.Truncated() {
		return "", fmt.Errorf("Xray merged configuration exceeds %d bytes", core.MaxConfigBytes)
	}
	if stderr.Truncated() {
		return "", errors.New("Xray dump diagnostics exceeded the output limit")
	}
	diagnostics := strings.TrimSpace(strings.ToValidUTF8(stderr.String(), "�"))
	if err != nil {
		if diagnostics != "" {
			return "", fmt.Errorf("%w: %s", err, diagnostics)
		}
		return "", err
	}
	content := strings.TrimSpace(stdout.String())
	if content == "" {
		return "", errors.New("Xray returned an empty merged configuration")
	}
	if !utf8.ValidString(content) {
		return "", errors.New("Xray returned a non-UTF-8 merged configuration")
	}
	if err := core.ValidateConfig(core.EngineXray, content); err != nil {
		return "", fmt.Errorf("Xray returned malformed merged configuration: %w", err)
	}
	return content + "\n", nil
}

// normalizeImportedXrayLogDestinations moves existing file logging onto the
// managed service's stdout/stderr stream. Empty Xray destinations mean console;
// explicit "none" remains disabled. Other log policy fields are preserved.
func normalizeImportedXrayLogDestinations(content string) (string, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return "", err
	}
	logValue, ok := root["log"]
	if !ok {
		return content, nil
	}
	var logging map[string]json.RawMessage
	if err := json.Unmarshal(logValue, &logging); err != nil {
		return "", fmt.Errorf("Xray log configuration is not an object: %w", err)
	}
	if logging == nil {
		return content, nil
	}
	changed := false
	for _, key := range []string{"access", "error"} {
		raw, ok := logging[key]
		if !ok {
			continue
		}
		var destination string
		if err := json.Unmarshal(raw, &destination); err != nil {
			return "", fmt.Errorf("Xray log.%s destination is not a string: %w", key, err)
		}
		// Xray recognizes only the exact lowercase literal "none". Whitespace
		// and case variants are file names, so they must be normalized too.
		if destination != "" && destination != "none" {
			logging[key] = json.RawMessage(`""`)
			changed = true
		}
	}
	if !changed {
		return content, nil
	}
	normalizedLog, err := json.Marshal(logging)
	if err != nil {
		return "", err
	}
	root["log"] = normalizedLog
	normalized, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	return string(normalized) + "\n", nil
}

// normalizeImportedSingBoxLogDestination moves an existing absolute file log
// outside the managed state directory onto the managed service's console log.
// Safe relative/managed-state file outputs and disabled logging are preserved.
func normalizeImportedSingBoxLogDestination(content string) (string, error) {
	output, destination, err := singBoxLogOutput(content)
	if err != nil {
		return "", err
	}
	if destination != singBoxLogDestinationFile {
		return content, nil
	}
	if strings.ContainsAny(output, "\x00\r\n") {
		return "", errors.New("sing-box log output contains a control character")
	}
	if _, err := importedSingBoxLogPath(output); err == nil {
		return content, nil
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return "", err
	}
	var logging map[string]json.RawMessage
	if err := json.Unmarshal(root["log"], &logging); err != nil {
		return "", fmt.Errorf("sing-box log configuration is not an object: %w", err)
	}
	logging["output"] = json.RawMessage(`"stdout"`)
	normalizedLog, err := json.Marshal(logging)
	if err != nil {
		return "", err
	}
	root["log"] = normalizedLog
	normalized, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return "", err
	}
	return string(normalized) + "\n", nil
}

func readExistingConfigurationSources(spec EngineSpec) (string, string, error) {
	// A mapping with no main configuration file is directory-authoritative: the
	// confdir alone supplies every source, so there is no primary to read.
	if spec.ConfigPath == "" {
		if spec.ConfigDirectory == "" {
			return "", "", errors.New("configuration mapping has neither a file nor a directory")
		}
		return mergeExistingConfigurationDirectory(spec, "")
	}
	primary, err := readConfigurationFile(spec.ConfigPath)
	if err != nil {
		return "", "", err
	}
	if spec.ConfigDirectory == "" {
		digest := sha256.Sum256([]byte(spec.ConfigPath + "\x00" + primary))
		return primary, hex.EncodeToString(digest[:]), nil
	}
	return mergeExistingConfigurationDirectory(spec, primary)
}

func mergeExistingConfigurationDirectory(spec EngineSpec, primary string) (string, string, error) {
	if err := validateProtectedDirectoryChain(spec.ConfigDirectory); err != nil {
		return "", "", fmt.Errorf("configuration directory parent chain is unsafe: %w", err)
	}
	entries, err := os.ReadDir(spec.ConfigDirectory)
	if err != nil {
		return "", "", err
	}
	// A config directory is authoritative on its own: when the primary file is
	// the directory's own config.json, it is a fragment of that directory and
	// must not be merged twice (the core reads it once). A mapping with no
	// primary at all is directory-authoritative for the same reason.
	directoryPrimary := spec.ConfigPath == "" ||
		filepath.Clean(spec.ConfigPath) == filepath.Clean(filepath.Join(spec.ConfigDirectory, "config.json"))
	sources := make([]existingConfigSource, 0, len(entries)+1)
	if !directoryPrimary {
		sources = append(sources, existingConfigSource{path: spec.ConfigPath, content: primary})
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return "", "", fmt.Errorf("configuration directory entry %q is not a regular non-symlink JSON file", entry.Name())
		}
		path := filepath.Join(spec.ConfigDirectory, entry.Name())
		contents, err := readConfigurationFile(path)
		if err != nil {
			return "", "", fmt.Errorf("read configuration directory entry %q: %w", entry.Name(), err)
		}
		sources = append(sources, existingConfigSource{path: path, content: contents})
	}
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].path < sources[j].path })
	total := 0
	digest := sha256.New()
	var merged any
	for _, source := range sources {
		total += len(source.content)
		if total > core.MaxConfigBytes {
			return "", "", fmt.Errorf("combined configuration sources exceed %d bytes", core.MaxConfigBytes)
		}
		digest.Write([]byte(source.path))
		digest.Write([]byte{0})
		digest.Write([]byte(source.content))
		digest.Write([]byte{0})
		decoded, err := decodeExtendedJSON(source.content)
		if err != nil {
			return "", "", fmt.Errorf("decode configuration source %q: %w", source.path, err)
		}
		if merged == nil {
			merged = decoded
		} else {
			merged, err = mergeSingBoxJSON(decoded, merged)
			if err != nil {
				return "", "", fmt.Errorf("merge configuration source %q: %w", source.path, err)
			}
		}
	}
	contents, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return "", "", err
	}
	contents = append(contents, '\n')
	if len(contents) > core.MaxConfigBytes {
		return "", "", fmt.Errorf("merged configuration exceeds %d bytes", core.MaxConfigBytes)
	}
	return string(contents), hex.EncodeToString(digest.Sum(nil)), nil
}

// decodeExtendedJSON decodes exactly one JSON value from content while
// tolerating sing-box extended JSON comments (//, # and /* */) that appear
// outside string literals. It rejects trailing non-whitespace so malformed or
// ambiguous sources still fail closed.
func decodeExtendedJSON(content string) (any, error) {
	cleaned, err := stripExtendedJSONComments(content)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(cleaned))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data")
	}
	return decoded, nil
}

// stripExtendedJSONComments removes sing-box extended JSON comments such as
// //, # and /* */ outside string literals. It is string-aware, so comment
// shaped text inside a JSON string is preserved byte for byte. An unterminated
// block comment fails closed instead of being treated as legal trailing data.
func stripExtendedJSONComments(content string) (string, error) {
	out := make([]byte, 0, len(content))
	for i := 0; i < len(content); {
		switch content[i] {
		case '"':
			out = append(out, content[i])
			i++
			for i < len(content) {
				out = append(out, content[i])
				if content[i] == '\\' {
					i++
					if i < len(content) {
						out = append(out, content[i])
						i++
					}
					continue
				}
				if content[i] == '"' {
					i++
					break
				}
				i++
			}
		case '/':
			if i+1 < len(content) && content[i+1] == '/' {
				for i < len(content) && content[i] != '\n' {
					i++
				}
				out = append(out, ' ')
			} else if i+1 < len(content) && content[i+1] == '*' {
				i += 2
				commentEnd := i
				for commentEnd+1 < len(content) && !(content[commentEnd] == '*' && content[commentEnd+1] == '/') {
					commentEnd++
				}
				if commentEnd+1 >= len(content) {
					return "", errors.New("unexpected end of JSON comment")
				}
				commentEnd += 2
				i = commentEnd
				out = append(out, ' ')
			} else {
				out = append(out, content[i])
				i++
			}
		case '#':
			for i < len(content) && content[i] != '\n' {
				i++
			}
			out = append(out, ' ')
		default:
			out = append(out, content[i])
			i++
		}
	}
	return string(out), nil
}

type existingConfigSource struct {
	path    string
	content string
}

// mergeSingBoxJSON mirrors sing-box's ordered badjson merge: an existing
// destination wins scalar conflicts, objects merge recursively, and source
// arrays append after the destination array. Paths are sorted before this
// function is called.
func mergeSingBoxJSON(source, destination any) (any, error) {
	if source == nil {
		return destination, nil
	}
	if destination == nil {
		return source, nil
	}
	switch current := destination.(type) {
	case []any:
		if values, ok := source.([]any); ok {
			return append(current, values...), nil
		}
		return append(current, source), nil
	case map[string]any:
		values, ok := source.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("cannot merge JSON object with %T", source)
		}
		for key, value := range values {
			if previous, exists := current[key]; exists {
				var err error
				value, err = mergeSingBoxJSON(value, previous)
				if err != nil {
					return nil, err
				}
			}
			current[key] = value
		}
		return current, nil
	default:
		return destination, nil
	}
}

func validateExistingSourceInvocation(ctx context.Context, engine core.Engine, spec EngineSpec) error {
	if spec.ConfigDirectory == "" {
		return nil
	}
	if engine == core.EngineXray {
		return errors.New("Xray config directories must be dumped with the source core merger")
	}
	if engine != core.EngineSingBox {
		return nil
	}
	args := []string{"check"}
	runDirectory := filepath.Dir(spec.ConfigPath)
	if spec.WorkingDirectory != "" {
		args = append(args, "-D", spec.WorkingDirectory)
		runDirectory = spec.WorkingDirectory
	}
	if filepath.Clean(spec.ConfigPath) == filepath.Clean(filepath.Join(spec.ConfigDirectory, "config.json")) {
		args = append(args, "-C", spec.ConfigDirectory)
	} else {
		args = append(args, "-c", spec.ConfigPath, "-C", spec.ConfigDirectory)
	}
	_, err := runInDirectory(ctx, runDirectory, spec.Binary, args...)
	return err
}

// singBoxResourceKind distinguishes sing-box configuration fields whose value
// is a regular file from fields whose value is a directory. The official sing-box
// service form (-D dir) chdirs before it resolves any of these paths, so after
// migration the QAgent managed unit runs with a different working directory and
// a relative resource would silently resolve elsewhere.
type singBoxResourceKind int

const (
	singBoxFileResource singBoxResourceKind = iota
	singBoxDirectoryResource
)

func (k singBoxResourceKind) String() string {
	if k == singBoxDirectoryResource {
		return "directory"
	}
	return "file"
}

type singBoxResourceField struct {
	key string
	// kind is the kind of filesystem resource the field carries.
	kind singBoxResourceKind
	// allowLogSpecial permits log.output console literals and relative file
	// names. The import-specific validator separately resolves file names into
	// the protected managed sing-box state directory.
	allowLogSpecial bool
}

// singBoxResourceFieldsByParent is the set of sing-box JSON fields that are
// resolved as filesystem resources at runtime, keyed by the enclosing option
// object. It is derived from the sing-box v1.13.19 option package, which is the
// version used by the real deployment target, and covers every field that
// sing-box opens, reads, creates, or persists through its working directory.
// The parent key is used instead of a suffix heuristic so URL fields named path
// (HTTP/transport) are not mistaken for files while directory resources such as
// acme.data_directory, external_ui, and certificate_directory_path are caught.
var singBoxResourceFieldsByParent = map[string][]singBoxResourceField{
	"log": {
		{key: "output", kind: singBoxFileResource, allowLogSpecial: true},
	},
	"certificate": {
		{key: "certificate_path", kind: singBoxFileResource},
		{key: "certificate_directory_path", kind: singBoxDirectoryResource},
	},
	"tls": {
		{key: "certificate_path", kind: singBoxFileResource},
		{key: "key_path", kind: singBoxFileResource},
		{key: "client_certificate_path", kind: singBoxFileResource},
		{key: "client_key_path", kind: singBoxFileResource},
	},
	"acme": {
		{key: "data_directory", kind: singBoxDirectoryResource},
	},
	"ech": {
		{key: "key_path", kind: singBoxFileResource},
		{key: "config_path", kind: singBoxFileResource},
	},
	"geoip": {
		{key: "path", kind: singBoxFileResource},
	},
	"geosite": {
		{key: "path", kind: singBoxFileResource},
	},
	"cache_file": {
		{key: "path", kind: singBoxFileResource},
	},
	"clash_api": {
		{key: "external_ui", kind: singBoxDirectoryResource},
		{key: "cache_file", kind: singBoxFileResource},
	},
	"masquerade": {
		// Hysteria2 masquerade type=file serves a directory.
		{key: "directory", kind: singBoxDirectoryResource},
	},
}

// singBoxTypeResourceFields covers sing-box option objects that are identified
// by their type discriminator rather than by a stable parent key, including
// the local rule set, SSH/Tor outbounds, Tailscale endpoints, and the CCM, OCM,
// DERP, and SSM-API services from the v1.13.19 option package.
func singBoxTypeResourceFields(nodeType string) []singBoxResourceField {
	switch nodeType {
	case "local":
		return []singBoxResourceField{{key: "path", kind: singBoxFileResource}}
	case "ssh":
		return []singBoxResourceField{{key: "private_key_path", kind: singBoxFileResource}}
	case "tor":
		return []singBoxResourceField{
			{key: "executable_path", kind: singBoxFileResource},
			{key: "data_directory", kind: singBoxDirectoryResource},
		}
	case "tailscale":
		// Tailscale endpoint and tailscale DNS server persist state in this
		// directory; the DERP service uses config_path and mesh_psk_file.
		return []singBoxResourceField{
			{key: "state_directory", kind: singBoxDirectoryResource},
			{key: "config_path", kind: singBoxFileResource},
			{key: "mesh_psk_file", kind: singBoxFileResource},
		}
	case "ccm", "ocm":
		return []singBoxResourceField{
			{key: "credential_path", kind: singBoxFileResource},
			{key: "usages_path", kind: singBoxFileResource},
		}
	case "derp":
		return []singBoxResourceField{
			{key: "config_path", kind: singBoxFileResource},
			{key: "mesh_psk_file", kind: singBoxFileResource},
		}
	case "ssm-api":
		return []singBoxResourceField{{key: "cache_path", kind: singBoxFileResource}}
	case "hosts":
		// DNS hosts server reads one or more hosts files.
		return []singBoxResourceField{{key: "path", kind: singBoxFileResource}}
	default:
		return nil
	}
}

// singBoxGlobalResourceFields are unambiguous filesystem resources wherever
// they appear in a sing-box option object, such as a dialer protect socket.
var singBoxGlobalResourceFields = []singBoxResourceField{
	{key: "protect_path", kind: singBoxFileResource},
}

// singBoxResourceNameSuffixes is a conservative, non-ambiguous fallback for
// sing-box fields whose JSON key ends in a filesystem-resource naming
// convention. It deliberately excludes the bare URL-valued path field on
// HTTP/transport objects and the process_path/process_path_regex rule matching
// patterns, which are not files opened from the configuration. Any future
// sing-box resource field that follows these conventions is still rejected
// when its value is relative, instead of silently drifting with the working
// directory.
var singBoxResourceNameSuffixes = []string{
	"_path",
	"_file",
	"_dir",
	"_directory",
	"_ui",
}

// singBoxResourceNameExceptions are keys that match a resource suffix but are
// not filesystem resources read from the working directory.
var singBoxResourceNameExceptions = map[string]struct{}{
	"process_path":       {},
	"process_path_regex": {},
}

// validateNoRelativeSingBoxResources fails closed for every sing-box option field
// that sing-box resolves against its working directory. By enumerating the
// official option contract by enclosing object and type discriminator, then
// applying a non-ambiguous resource-name fallback, it catches every current
// file and directory resource (including string-array forms) without treating
// URL path fields as files. Every path must be absolute or migration cannot
// preserve the original semantics.
func validateNoRelativeSingBoxResources(content string) error {
	var root any
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return err
	}
	return validateSingBoxResourceNodes(root, "", "")
}

func validateSingBoxResourceNodes(value any, parentKey, nodeType string) error {
	switch node := value.(type) {
	case map[string]any:
		if nodeType == "" {
			if resourceType, ok := node["type"].(string); ok {
				nodeType = resourceType
			}
		}
		if nodeType == "inbounds" || nodeType == "outbounds" || nodeType == "rule_set" ||
			nodeType == "endpoints" || nodeType == "http_clients" ||
			nodeType == "certificate_providers" || nodeType == "services" {
			// A map that is an array element of a named option collection must
			// derive its discriminator from its own type field, not from the
			// collection key passed down from its parent.
			nodeType = ""
			if resourceType, ok := node["type"].(string); ok {
				nodeType = resourceType
			}
		}
		for _, field := range singBoxResourceFieldsByParent[parentKey] {
			if raw, ok := node[field.key]; ok {
				if err := checkSingBoxResourceField(field, raw); err != nil {
					return err
				}
			}
		}
		for _, field := range singBoxTypeResourceFields(nodeType) {
			if raw, ok := node[field.key]; ok {
				if err := checkSingBoxResourceField(field, raw); err != nil {
					return err
				}
			}
		}
		for _, field := range singBoxGlobalResourceFields {
			if raw, ok := node[field.key]; ok {
				if err := checkSingBoxResourceField(field, raw); err != nil {
					return err
				}
			}
		}
		for key, raw := range node {
			field, ok := singBoxResourceFieldForName(key)
			if !ok {
				continue
			}
			alreadyChecked := false
			for _, explicit := range append(append([]singBoxResourceField{},
				singBoxResourceFieldsByParent[parentKey]...),
				append(singBoxTypeResourceFields(nodeType), singBoxGlobalResourceFields...)...) {
				if explicit.key == key {
					alreadyChecked = true
					break
				}
			}
			if alreadyChecked {
				continue
			}
			if err := checkSingBoxResourceField(field, raw); err != nil {
				return err
			}
		}
		for key, child := range node {
			if err := validateSingBoxResourceNodes(child, key, ""); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range node {
			if err := validateSingBoxResourceNodes(item, parentKey, ""); err != nil {
				return err
			}
		}
	case string:
		// The Hysteria2 masquerade option is polymorphic: a JSON string is a
		// URL that sing-box parses into a file, proxy, or string masquerade.
		if parentKey != "masquerade" {
			return nil
		}
		return validateSingBoxMasqueradeURL(node)
	}
	return nil
}

// validateSingBoxMasqueradeURL applies the sing-box v1.13.19 URL semantics for
// the Hysteria2 masquerade string form. A file: URL is served by http.Dir on
// its path, so a relative path would resolve against the process working
// directory and drift after migration; it is rejected unless the directory is
// absolute. http/https URLs are network proxies and are not filesystem
// resources. Unknown or malformed schemes are left to the real core to reject
// strictly.
func validateSingBoxMasqueradeURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	switch parsed.Scheme {
	case "file":
		if parsed.Path == "" || !filepath.IsAbs(parsed.Path) {
			return fmt.Errorf("relative sing-box directory resource path in masquerade (%q) cannot be migrated safely", raw)
		}
	case "http", "https":
		// Network proxy URL, not a filesystem resource.
	default:
		// Unknown scheme: let the real core reject it.
	}
	return nil
}

// singBoxResourceFieldForName maps a key to a filesystem-resource field when it
// follows a resource naming convention and is not a process-path or URL field.
func singBoxResourceFieldForName(key string) (singBoxResourceField, bool) {
	if _, exception := singBoxResourceNameExceptions[key]; exception {
		return singBoxResourceField{}, false
	}
	if key == "path" {
		// Bare path is URL-valued on HTTP/transport objects and only a resource
		// in the explicit parent/type contract (geoip, geosite, cache_file,
		// hosts, local rule set).
		return singBoxResourceField{}, false
	}
	for _, suffix := range singBoxResourceNameSuffixes {
		if strings.HasSuffix(key, suffix) {
			kind := singBoxFileResource
			if strings.HasSuffix(key, "_directory") || strings.HasSuffix(key, "_dir") ||
				strings.HasSuffix(key, "data_directory") || strings.HasSuffix(key, "state_directory") ||
				strings.HasSuffix(key, "_ui") || key == "certificate_directory_path" {
				kind = singBoxDirectoryResource
			}
			return singBoxResourceField{key: key, kind: kind}, true
		}
	}
	return singBoxResourceField{}, false
}

func checkSingBoxResourceField(field singBoxResourceField, value any) error {
	check := func(path string) error {
		if path == "" {
			return nil
		}
		if field.allowLogSpecial {
			return nil
		}
		if filepath.IsAbs(path) {
			return nil
		}
		return fmt.Errorf("relative sing-box %s resource path in %s (%q) cannot be migrated safely", field.kind, field.key, path)
	}
	switch raw := value.(type) {
	case string:
		return check(raw)
	case []any:
		for _, item := range raw {
			path, ok := item.(string)
			if !ok {
				return fmt.Errorf("sing-box resource field %q contains a non-string element", field.key)
			}
			if err := check(path); err != nil {
				return err
			}
		}
		return nil
	default:
		// A non-string value is a type error caught by sing-box's own strict
		// option unmarshal; it is not a path that can drift with the working
		// directory, so it is left for the real core validation to reject.
		return nil
	}
}

// ReadCurrentConfig returns an exact, real-core-validated snapshot for explicit
// inspection in the manual configuration page. It does not save or deploy it.
func (e *Executor) ReadCurrentConfig(ctx context.Context, engine core.Engine) (string, error) {
	spec, ok := e.Specs[engine]
	if !ok {
		return "", fmt.Errorf("engine %s is not enabled", engine)
	}
	return e.readCurrentConfig(ctx, engine, spec)
}

func readConfigurationFile(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("configuration path is not absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("configuration must be a regular, non-symlink file")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("configuration is writable by group or others")
	}
	if err := validateOwner(info, "configuration file"); err != nil {
		return "", err
	}
	directory := filepath.Dir(path)
	if err := validateProtectedDirectoryChain(directory); err != nil {
		return "", fmt.Errorf("configuration parent chain is unsafe: %w", err)
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() || directoryInfo.Mode().Perm()&0o022 != 0 {
		return "", errors.New("configuration directory is symlinked or writable by group/others")
	}
	if err := validateOwner(directoryInfo, "configuration directory"); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return "", err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() {
		return "", errors.New("configuration changed while it was being opened")
	}
	content, err := io.ReadAll(io.LimitReader(file, core.MaxConfigBytes+1))
	if err != nil {
		return "", err
	}
	if len(content) > core.MaxConfigBytes {
		return "", fmt.Errorf("configuration exceeds %d bytes", core.MaxConfigBytes)
	}
	if !utf8.Valid(content) {
		return "", errors.New("configuration is not valid UTF-8")
	}
	return string(content), nil
}

func validateConfigurationPath(ctx context.Context, engine core.Engine, binary, configPath string) (string, error) {
	var args []string
	switch engine {
	case core.EngineMihomo:
		args = []string{"-t", "-f", configPath}
	case core.EngineXray:
		args = []string{"run", "-test", "-config", configPath}
	case core.EngineSingBox:
		args = []string{"check", "-c", configPath}
	case core.EngineShadowsocksRust:
		return "ss-rust configuration syntax validated (ssserver has no non-running check mode)", nil
	default:
		return "", fmt.Errorf("unsupported engine %q", engine)
	}
	return runInDirectory(ctx, filepath.Dir(configPath), binary, args...)
}

func (e *Executor) Runtime(ctx context.Context) map[core.Engine]core.RuntimeState {
	e.specsMu.RLock()
	specs := make(map[core.Engine]EngineSpec, len(e.Specs))
	existingSpecs := make(map[core.Engine]EngineSpec, len(e.ExistingSpecs))
	discoveryIssues := make(map[core.Engine]string, len(e.ExistingDiscoveryIssues))
	for engine, spec := range e.Specs {
		specs[engine] = spec
	}
	for engine, spec := range e.ExistingSpecs {
		existingSpecs[engine] = spec
	}
	for engine, issue := range e.ExistingDiscoveryIssues {
		discoveryIssues[engine] = issue
	}
	e.specsMu.RUnlock()
	result := make(map[core.Engine]core.RuntimeState, len(specs))
	for engine, spec := range specs {
		state := core.RuntimeState{}
		if issue := discoveryIssues[engine]; issue != "" {
			state.ServiceStatus = "active"
			state.ExistingConfigUnsupportedReason = issue
			result[engine] = state
			continue
		}
		if _, ok := existingSpecs[engine]; ok {
			state.ExistingConfigAvailable = true
		}
		if path, err := exec.LookPath(spec.Binary); err == nil {
			state.Installed = true
			state.Version = binaryVersion(ctx, engine, path)
		}
		if status, err := serviceStatusWithManager(ctx, e.serviceManager(), spec.Service); err == nil {
			state.ServiceStatus = strings.TrimSpace(status)
		} else {
			state.ServiceStatus = "unknown"
		}
		result[engine] = state
	}
	return result
}

func (e *Executor) validate(ctx context.Context, engine core.Engine, spec EngineSpec, content string) (string, error) {
	if err := core.ValidateConfig(engine, content); err != nil {
		return "", err
	}
	if err := e.validateManagedLogPolicy(ctx, engine, spec, content); err != nil {
		return "", err
	}
	defaultSpec, managed := DefaultSpecsForServiceManager(e.serviceManager().Kind())[engine]
	if managed && spec == defaultSpec && engine != core.EngineShadowsocksRust {
		return e.validateManagedServiceSnapshot(ctx, engine, spec, content)
	}
	return e.validateSnapshot(ctx, engine, spec, content)
}

func (e *Executor) validateManagedLogPolicy(ctx context.Context, engine core.Engine, spec EngineSpec, content string) error {
	if engine != core.EngineSingBox {
		return validateNoPersistentCoreLogs(engine, content)
	}
	output, destination, err := singBoxLogOutput(content)
	if err != nil {
		return err
	}
	if destination != singBoxLogDestinationFile {
		return nil
	}
	if _, err := e.completedMigrationOwnership(ctx, engine, spec); err != nil {
		return validateNoPersistentCoreLogs(engine, content)
	}
	if _, err := importedSingBoxLogPath(output); err != nil {
		return fmt.Errorf("imported sing-box log output is unsafe: %w", err)
	}
	return nil
}

func (e *Executor) validateSnapshot(ctx context.Context, engine core.Engine, spec EngineSpec, content string) (string, error) {
	return e.validateSnapshotWithIdentity(ctx, engine, spec, content, nil, fileMetadata{})
}

func (e *Executor) validateManagedServiceSnapshot(ctx context.Context, engine core.Engine, spec EngineSpec, content string) (string, error) {
	metadata, err := prepareManagedConfigurationAccess(managedCoreConfigurationRoot, spec.ConfigPath)
	if err != nil {
		return "", fmt.Errorf("prepare managed %s validation access: %w", engine, err)
	}
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return "", err
	}
	return e.validateSnapshotWithIdentity(ctx, engine, spec, content, &identity, metadata)
}

func (e *Executor) validateSnapshotWithIdentity(ctx context.Context, engine core.Engine, spec EngineSpec, content string, identity *commandIdentity, metadata fileMetadata) (string, error) {
	if err := core.ValidateConfig(engine, content); err != nil {
		return "", err
	}
	if _, err := exec.LookPath(spec.Binary); err != nil {
		return "", fmt.Errorf("%s binary not found in PATH", spec.Binary)
	}
	extension := ".json"
	if engine == core.EngineMihomo {
		extension = ".yaml"
	}
	directory := filepath.Dir(spec.ConfigPath)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", fmt.Errorf("create configuration directory for validation: %w", err)
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return "", fmt.Errorf("open configuration directory for validation: %w", err)
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() || directoryInfo.Mode().Perm()&0o022 != 0 {
		return "", errors.New("configuration directory for validation is unsafe")
	}
	if err := validateOwner(directoryInfo, "configuration directory"); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	suffix, err := randomSuffix(10)
	if err != nil {
		return "", err
	}
	tempName := ".qcontrolhub-validate-" + suffix + extension
	temp, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	tempPath := filepath.Join(directory, tempName)
	defer root.Remove(tempName)
	if _, err := temp.WriteString(content); err != nil {
		temp.Close()
		return "", err
	}
	if identity != nil {
		if err := applyFileMetadata(temp, metadata); err != nil {
			temp.Close()
			return "", fmt.Errorf("prepare managed validation file: %w", err)
		}
	}
	if err := temp.Close(); err != nil {
		return "", err
	}

	var args []string
	switch engine {
	case core.EngineMihomo:
		args = []string{"-t", "-f", tempPath}
	case core.EngineXray:
		args = []string{"run", "-test", "-config", tempPath}
	case core.EngineSingBox:
		args = []string{"check", "-c", tempPath}
	case core.EngineShadowsocksRust:
		return "ss-rust configuration syntax validated (ssserver has no non-running check mode)", nil
	}
	output, err := runInDirectoryWithEnvironmentAndIdentity(ctx, directory, spec.commandEnv, spec.Binary, identity, args...)
	if err != nil {
		return output, fmt.Errorf("%s rejected the configuration: %w", engine, err)
	}
	if output == "" {
		output = fmt.Sprintf("%s validation passed", engine)
	}
	return output, nil
}

func validateNoPersistentCoreLogs(engine core.Engine, content string) error {
	if engine != core.EngineXray && engine != core.EngineSingBox {
		return nil
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return err
	}
	logging, _ := root["log"].(map[string]any)
	if logging == nil {
		return nil
	}
	keys := []string{"output"}
	if engine == core.EngineXray {
		keys = []string{"access", "error"}
	}
	for _, key := range keys {
		value, _ := logging[key].(string)
		if engine != core.EngineXray {
			value = strings.TrimSpace(value)
		}
		if value != "" && (engine != core.EngineXray || value != "none") {
			return fmt.Errorf("persistent %s log output %q is disabled; managed core logs are stored by the control plane", engine, key)
		}
	}
	return nil
}

func serviceCommand(ctx context.Context, service string, action core.Action) (string, error) {
	return defaultSystemdServiceManager().command(ctx, service, action)
}

func serviceCommandAndVerify(ctx context.Context, service string, action core.Action) (string, error) {
	return serviceCommandAndVerifyWithManager(ctx, defaultSystemdServiceManager(), service, action)
}

func serviceCommandAndVerifyWithManager(ctx context.Context, manager *ServiceManager, service string, action core.Action) (string, error) {
	output, err := manager.command(ctx, service, action)
	if err != nil {
		return output, err
	}
	if action == core.ActionStop && manager.Kind() == ServiceManagerSystemd {
		status, statusErr := manager.status(ctx, service)
		if statusErr != nil {
			return output, withServiceFailureDiagnostics(ctx, manager, service, statusErr)
		}
		if status == "failed" {
			if err := manager.resetFailed(ctx, service); err != nil {
				return output, withServiceFailureDiagnostics(ctx, manager, service, err)
			}
		}
	}
	expected := "active"
	stableFor := 500 * time.Millisecond
	verificationTimeout := 12 * time.Second
	if action == core.ActionStop {
		expected = "inactive"
		stableFor = 0
		verificationTimeout = 5 * time.Second
	}
	verifyContext, verifyCancel := context.WithTimeout(ctx, verificationTimeout)
	status, statusErr := waitForServiceState(verifyContext, expected, stableFor, 100*time.Millisecond, func(probeContext context.Context) (string, error) {
		return manager.status(probeContext, service)
	})
	verifyCancel()
	if statusErr != nil {
		cause := fmt.Errorf("verify %s service %s after %s: %w", manager.Kind(), service, action, statusErr)
		return output, withServiceFailureDiagnostics(ctx, manager, service, cause)
	}
	if status != expected {
		cause := fmt.Errorf("%s service %s is %s after %s, expected %s", manager.Kind(), service, status, action, expected)
		return output + "\nservice status: " + status, withServiceFailureDiagnostics(ctx, manager, service, cause)
	}
	return output + "\nservice status: " + status, nil
}

func withServiceFailureDiagnostics(ctx context.Context, manager *ServiceManager, service string, cause error) error {
	if manager == nil || manager.Kind() != ServiceManagerSystemd {
		return cause
	}
	diagnosticContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output, _ := run(diagnosticContext, manager.executable, "show", service, "--no-pager",
		"--property=ActiveState", "--property=SubState", "--property=Result",
		"--property=ExecMainCode", "--property=ExecMainStatus", "--property=NRestarts")
	output = strings.TrimSpace(strings.ToValidUTF8(output, "�"))
	journal := serviceFailureJournal(diagnosticContext, service)
	if len(output) > 2048 {
		output = strings.ToValidUTF8(output[:2048], "�")
	}
	if output == "" && journal == "" {
		return cause
	}
	details := ""
	if output != "" {
		details = "unit state:\n" + output
	}
	if journal != "" {
		if details != "" {
			details += "\n"
		}
		details += "recent unit logs:\n" + journal
	}
	return fmt.Errorf("%w; %s", cause, details)
}

func serviceFailureJournal(ctx context.Context, service string) string {
	if !safeServiceName(service) {
		return ""
	}
	arguments := []string{"--unit=" + service, "--since=-30s", "--lines=20", "--no-pager", "--output=cat"}
	outputs := make([]string, 0, 2)
	for _, prefix := range [][]string{{"--namespace=qagent-cores"}, nil} {
		current, _ := run(ctx, journalctlPath, append(prefix, arguments...)...)
		current = strings.TrimSpace(strings.ToValidUTF8(current, "�"))
		if current == "" || len(outputs) > 0 && current == outputs[0] {
			continue
		}
		outputs = append(outputs, current)
	}
	output := strings.Join(outputs, "\n")
	if len(output) > 4096 {
		output = strings.ToValidUTF8(output[len(output)-4096:], "�")
	}
	return output
}

type serviceStatusProbe func(context.Context) (string, error)

func waitForServiceState(ctx context.Context, expected string, stableFor, pollEvery time.Duration, probe serviceStatusProbe) (string, error) {
	if probe == nil || pollEvery <= 0 || stableFor < 0 {
		return "", errors.New("invalid service verification settings")
	}
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	var stableSince time.Time
	lastStatus := "unknown"
	for {
		status, err := probe(ctx)
		if err != nil {
			return lastStatus, err
		}
		lastStatus = status
		if status == expected {
			if stableFor == 0 {
				return status, nil
			}
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
			if time.Since(stableSince) >= stableFor {
				return status, nil
			}
		} else {
			stableSince = time.Time{}
			if status == "failed" || status == "inactive" || (expected == "inactive" && status == "active") {
				return status, nil
			}
		}
		select {
		case <-ctx.Done():
			return lastStatus, ctx.Err()
		case <-ticker.C:
		}
	}
}

func serviceStatus(ctx context.Context, service string) (string, error) {
	return serviceStatusWithManager(ctx, defaultSystemdServiceManager(), service)
}

func serviceStatusWithManager(ctx context.Context, manager *ServiceManager, service string) (string, error) {
	return manager.status(ctx, service)
}

func binaryVersion(ctx context.Context, engine core.Engine, binary string) string {
	args := []string{"version"}
	if engine == core.EngineMihomo {
		args = []string{"-v"}
	} else if engine == core.EngineShadowsocksRust {
		args = []string{"--version"}
	}
	output, err := run(ctx, binary, args...)
	if err != nil {
		return "unknown"
	}
	if line, _, found := strings.Cut(output, "\n"); found {
		output = line
	}
	if len(output) > 160 {
		output = output[:160]
	}
	return strings.TrimSpace(output)
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	return runInDirectory(ctx, "", name, args...)
}

func runInDirectory(ctx context.Context, directory, name string, args ...string) (string, error) {
	return runInDirectoryWithEnvironment(ctx, directory, "", name, args...)
}

func runInDirectoryWithEnvironment(ctx context.Context, directory, environment, name string, args ...string) (string, error) {
	return runInDirectoryWithEnvironmentAndIdentity(ctx, directory, environment, name, nil, args...)
}

func runInDirectoryWithEnvironmentAndIdentity(ctx context.Context, directory, environment, name string, identity *commandIdentity, args ...string) (string, error) {
	if !filepath.IsAbs(name) {
		return "", errors.New("refusing to execute a non-absolute binary path")
	}
	commandContext, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(commandContext, name, args...)
	if directory != "" {
		command.Dir = directory
	}
	command.Env = commandEnvironment(environment)
	configureCommand(command)
	configureCommandIdentity(command, identity)
	output := &boundedOutput{limit: 64 << 10, onLimit: cancel}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	value := strings.TrimSpace(strings.ToValidUTF8(output.String(), "�"))
	if output.Truncated() {
		value += "\n… process terminated after exceeding the 64 KiB output limit"
		if ctx.Err() == nil {
			err = errors.New("command output limit exceeded")
		}
	}
	if ctx.Err() != nil {
		return value, ctx.Err()
	}
	return value, err
}

func commandEnvironment(additional string) []string {
	environment := []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	if additional != "" {
		environment = append(environment, additional)
	}
	return environment
}

type boundedOutput struct {
	mu        sync.Mutex
	contents  []byte
	limit     int
	truncated bool
	onLimit   func()
}

func (w *boundedOutput) Write(input []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	originalLength := len(input)
	remaining := w.limit - len(w.contents)
	if remaining > 0 {
		if len(input) > remaining {
			input = input[:remaining]
		}
		w.contents = append(w.contents, input...)
	}
	if originalLength > remaining && !w.truncated {
		w.truncated = true
		if w.onLimit != nil {
			go w.onLimit()
		}
	}
	return originalLength, nil
}

func (w *boundedOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(append([]byte(nil), w.contents...))
}

func (w *boundedOutput) Truncated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.truncated
}

func safeServiceName(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("_.@:-", character) {
			continue
		}
		return false
	}
	return true
}

func atomicDeploy(destination, content string) (string, error) {
	return atomicDeployWithDefaultMetadata(destination, content, fileMetadata{mode: 0o600})
}

func atomicDeployWithDefaultMetadata(destination, content string, defaultMetadata fileMetadata) (string, error) {
	if destination == "" || !filepath.IsAbs(destination) {
		return "", errors.New("configuration destination must be an absolute path")
	}
	directory := filepath.Dir(destination)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", fmt.Errorf("create configuration directory: %w", err)
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return "", errors.New("configuration directory must be a real directory, not a symlink")
	}
	if directoryInfo.Mode().Perm()&0o022 != 0 {
		return "", errors.New("configuration directory must not be writable by group or others")
	}
	if err := validateOwner(directoryInfo, "configuration directory"); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", fmt.Errorf("open configuration directory: %w", err)
	}
	defer root.Close()
	baseName := filepath.Base(destination)
	metadata := defaultMetadata
	var backup string
	var backupName string
	if info, err := root.Lstat(baseName); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("refusing to replace a symlinked configuration file")
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("configuration destination is not a regular file")
		}
		if err := validateOwner(info, "configuration file"); err != nil {
			return "", err
		}
		if info.Mode().Perm()&0o022 != 0 {
			return "", errors.New("configuration file must not be writable by group or others")
		}
		metadata = metadataFromFileInfo(info)
		suffix, err := randomSuffix(6)
		if err != nil {
			return "", err
		}
		backupName = baseName + ".bak-" + time.Now().UTC().Format("20060102T150405Z") + "-" + suffix
		backup = filepath.Join(directory, backupName)
		if err := copyFileInRoot(root, baseName, backupName, metadata); err != nil {
			return "", fmt.Errorf("back up current configuration: %w", err)
		}
		if err := cleanupBackups(root, baseName, backupName, 3); err != nil {
			_ = root.Remove(backupName)
			return "", fmt.Errorf("clean up old configuration backups: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	suffix, err := randomSuffix(10)
	if err != nil {
		return backup, err
	}
	tempName := ".qcontrolhub-config-" + suffix + ".tmp"
	temp, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return backup, err
	}
	defer root.Remove(tempName)
	if _, err := temp.WriteString(content); err != nil {
		temp.Close()
		return backup, err
	}
	if err := applyFileMetadata(temp, metadata); err != nil {
		temp.Close()
		return backup, fmt.Errorf("preserve configuration metadata: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return backup, err
	}
	if err := temp.Close(); err != nil {
		return backup, err
	}
	if err := root.Rename(tempName, baseName); err != nil {
		return backup, err
	}
	if err := syncRootDirectory(root); err != nil {
		return backup, fmt.Errorf("sync configuration directory: %w", err)
	}
	return backup, nil
}

func syncRootDirectory(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

type fileMetadata struct {
	mode           os.FileMode
	uid            int
	gid            int
	ownershipKnown bool
}

func metadataFromFileInfo(info os.FileInfo) fileMetadata {
	uid, gid, known := fileOwnership(info)
	return fileMetadata{mode: info.Mode().Perm(), uid: uid, gid: gid, ownershipKnown: known}
}

func applyFileMetadata(file *os.File, metadata fileMetadata) error {
	if metadata.ownershipKnown {
		if err := file.Chown(metadata.uid, metadata.gid); err != nil {
			return err
		}
	}
	return file.Chmod(metadata.mode)
}

func applyRootFileMetadata(root *os.Root, name string, metadata fileMetadata) error {
	if metadata.ownershipKnown {
		if err := root.Chown(name, metadata.uid, metadata.gid); err != nil {
			return err
		}
	}
	return root.Chmod(name, metadata.mode)
}

func copyFileInRoot(root *os.Root, source, destination string, metadata fileMetadata) (err error) {
	input, err := root.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := root.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = root.Remove(destination)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	if err := applyFileMetadata(output, metadata); err != nil {
		output.Close()
		return err
	}
	if err := output.Sync(); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}

func rollbackDeploy(destination, backup string) (string, error) {
	directory := filepath.Dir(destination)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	destinationName := filepath.Base(destination)
	if backup == "" {
		if err := root.Remove(destinationName); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err := syncRootDirectory(root); err != nil {
			return "", err
		}
		return "rollback: removed newly created configuration", nil
	}
	if filepath.Dir(backup) != directory {
		return "", errors.New("backup is outside the configuration directory")
	}
	backupName := filepath.Base(backup)
	if !strings.HasPrefix(backupName, destinationName+".bak-") {
		return "", errors.New("backup name does not match configuration")
	}
	if err := root.Rename(backupName, destinationName); err != nil {
		return "", err
	}
	if err := syncRootDirectory(root); err != nil {
		return "", err
	}
	return "rollback: previous configuration restored", nil
}

func cleanupBackups(root *os.Root, baseName, preserve string, keep int) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := directory.ReadDir(-1)
	directory.Close()
	if err != nil {
		return err
	}
	prefix := baseName + ".bak-"
	type backupEntry struct {
		name     string
		modified time.Time
	}
	backups := make([]backupEntry, 0)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			backups = append(backups, backupEntry{name: entry.Name(), modified: info.ModTime()})
		}
	}
	sort.Slice(backups, func(left, right int) bool {
		if backups[left].modified.Equal(backups[right].modified) {
			return backups[left].name < backups[right].name
		}
		return backups[left].modified.Before(backups[right].modified)
	})
	for len(backups) > keep {
		removeIndex := 0
		if backups[removeIndex].name == preserve && len(backups) > 1 {
			removeIndex = 1
		}
		if err := root.Remove(backups[removeIndex].name); err != nil {
			return err
		}
		backups = append(backups[:removeIndex], backups[removeIndex+1:]...)
	}
	return nil
}

func randomSuffix(bytesCount int) (string, error) {
	value := make([]byte, bytesCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
