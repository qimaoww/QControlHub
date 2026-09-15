package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
	_, destination, err := singBoxLogOutput(content)
	if err != nil {
		return err
	}
	if destination != singBoxLogDestinationFile {
		return nil
	}
	return validateNoPersistentCoreLogs(engine, content)
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
	validationHome := ""
	validationHomeName := ""
	if engine == core.EngineMihomo {
		homeSuffix, err := randomSuffix(10)
		if err != nil {
			return "", err
		}
		validationHomeName = ".qcontrolhub-validate-home-" + homeSuffix
		if err := root.Mkdir(validationHomeName, 0o700); err != nil {
			return "", fmt.Errorf("create Mihomo validation home: %w", err)
		}
		defer func() {
			if validationHomeName != "" {
				_ = e.cleanupMihomoValidationHome(root, directory, validationHomeName, identity)
			}
		}()
		if identity != nil {
			// The directory is created with its final 0700 mode. Chown last:
			// capability-bounded root deliberately lacks CAP_FOWNER and cannot
			// chmod the directory after transferring it to the service user.
			if err := root.Chown(validationHomeName, int(identity.uid), int(identity.gid)); err != nil {
				return "", fmt.Errorf("prepare Mihomo validation home: %w", err)
			}
		}
		validationHome = filepath.Join(directory, validationHomeName)
	}
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
		args = []string{"-t", "-d", validationHome, "-f", tempPath}
	case core.EngineXray:
		args = []string{"run", "-test", "-config", tempPath}
	case core.EngineSingBox:
		args = []string{"check", "-c", tempPath}
	case core.EngineShadowsocksRust:
		return "ss-rust configuration syntax validated (ssserver has no non-running check mode)", nil
	}
	output, err := e.runManagedIdentityCommand(ctx, directory, spec.commandEnv, spec.Binary, identity, args...)
	if validationHomeName != "" {
		cleanupErr := e.cleanupMihomoValidationHome(root, directory, validationHomeName, identity)
		validationHomeName = ""
		if cleanupErr != nil {
			if err != nil {
				return output, fmt.Errorf("%s rejected the configuration: %w; clean up validation home: %v", engine, err, cleanupErr)
			}
			return output, fmt.Errorf("clean up Mihomo validation home: %w", cleanupErr)
		}
	}
	if err != nil {
		return output, fmt.Errorf("%s rejected the configuration: %w", engine, err)
	}
	if output == "" {
		output = fmt.Sprintf("%s validation passed", engine)
	}
	return output, nil
}

func (e *Executor) cleanupMihomoValidationHome(root *os.Root, directory, homeName string, identity *commandIdentity) error {
	if err := root.RemoveAll(homeName); err == nil {
		return nil
	} else if identity == nil {
		return err
	}
	if err := validatePrivilegedExecutable(validationCleanupBinary); err != nil {
		return fmt.Errorf("unsafe validation cleanup helper: %w", err)
	}
	cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The service identity can remove everything below its 0700 home but
	// cannot unlink the home from the root-owned configuration directory.
	// Ignore that expected final rmdir error; the Agent removes the now-empty
	// top-level directory through its already-open, protected os.Root.
	_, _ = e.runManagedIdentityCommand(
		cleanupContext, directory, "", validationCleanupBinary, identity,
		"-rf", "--", filepath.Join(directory, homeName),
	)
	return root.RemoveAll(homeName)
}

func (e *Executor) runManagedIdentityCommand(ctx context.Context, directory, environment, name string, identity *commandIdentity, args ...string) (string, error) {
	// systemd owns the service identity transition. Running managed validation
	// through a transient unit keeps upgraded Agents compatible with qagent
	// units from before validation identity capabilities were added: the Agent
	// process itself no longer needs CAP_SETUID or CAP_SETGID to start the core.
	if identity != nil && e != nil && e.Services != nil && e.Services.Kind() == ServiceManagerSystemd {
		return e.runSystemdValidationIdentityCommand(ctx, directory, environment, name, *identity, args...)
	}
	return runInDirectoryWithEnvironmentAndIdentity(ctx, directory, environment, name, identity, args...)
}

func (e *Executor) runSystemdValidationIdentityCommand(ctx context.Context, directory, environment, name string, identity commandIdentity, args ...string) (string, error) {
	systemdRun := "/usr/bin/systemd-run"
	if e != nil && strings.TrimSpace(e.validationSystemdRunPath) != "" {
		systemdRun = e.validationSystemdRunPath
	}
	if err := validatePrivilegedExecutable(systemdRun); err != nil {
		return "", fmt.Errorf("unsafe managed validation launcher: %w", err)
	}
	if !filepath.IsAbs(name) || !filepath.IsAbs(directory) {
		return "", errors.New("managed validation requires absolute binary and working directory paths")
	}
	arguments := []string{
		"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec",
		"--property=User=" + strconv.FormatUint(uint64(identity.uid), 10),
		"--property=Group=" + strconv.FormatUint(uint64(identity.gid), 10),
		"--property=WorkingDirectory=" + directory,
		"--property=NoNewPrivileges=yes", "--property=ProtectSystem=strict", "--property=ProtectHome=yes",
		"--property=PrivateTmp=yes", "--property=PrivateDevices=yes", "--property=RestrictSUIDSGID=yes",
		"--property=LockPersonality=yes", "--property=RestrictNamespaces=yes", "--property=RestrictRealtime=yes",
		"--property=RemoveIPC=yes", "--property=SystemCallArchitectures=native",
		"--property=RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK",
		"--property=ReadWritePaths=" + directory, "--property=RuntimeMaxSec=5min",
	}
	for _, item := range commandEnvironment(environment) {
		arguments = append(arguments, "--setenv="+item)
	}
	arguments = append(arguments, "--", name)
	arguments = append(arguments, args...)
	return run(ctx, systemdRun, arguments...)
}

func validateNoPersistentCoreLogs(engine core.Engine, content string) error {
	if engine != core.EngineXray && engine != core.EngineSingBox && engine != core.EngineShadowsocksRust {
		return nil
	}
	if engine == core.EngineShadowsocksRust {
		normalized, err := normalizeImportedSSRustLogDestinations(content)
		if err != nil {
			return err
		}
		if normalized != content {
			return errors.New("persistent SS Rust log writer is disabled; managed core logs are stored by the control plane")
		}
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
