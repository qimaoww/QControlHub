package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
	Binary     string
	ConfigPath string
	Service    string
}

type Executor struct {
	DryRun  bool
	Specs   map[core.Engine]EngineSpec
	Updater *CoreUpdater
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

func (e *Executor) Validate() error {
	if e == nil {
		return errors.New("agent executor is required")
	}
	if !e.DryRun {
		if os.Geteuid() != 0 {
			return errors.New("live Agent execution must run as root; use dry-run mode for unprivileged operation")
		}
		if err := validatePrivilegedExecutable(systemctlPath); err != nil {
			return fmt.Errorf("unsafe systemctl binary: %w", err)
		}
	}
	for engine, spec := range e.Specs {
		if !engine.Valid() {
			return fmt.Errorf("invalid executor engine %q", engine)
		}
		if !safeServiceName(spec.Service) {
			return fmt.Errorf("unsafe systemd service name %q", spec.Service)
		}
		if !e.DryRun && (!filepath.IsAbs(spec.Binary) || !filepath.IsAbs(spec.ConfigPath)) {
			return fmt.Errorf("live executor paths for %s must be absolute", engine)
		}
		if !e.DryRun {
			if err := validatePrivilegedExecutable(spec.Binary); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("unsafe %s binary: %w", engine, err)
				}
				if err := validateCoreInstallDestination(spec.Binary); err != nil {
					return fmt.Errorf("unsafe %s install destination: %w", engine, err)
				}
			}
		}
	}
	return nil
}

func validatePrivilegedExecutable(path string) error {
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
		return err
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

func (e *Executor) Execute(parent context.Context, task core.Task) (string, error) {
	if !task.Action.Valid() || !task.Engine.Valid() {
		return "", errors.New("task contains an unsupported action or engine")
	}
	spec, ok := e.Specs[task.Engine]
	if !ok {
		return "", fmt.Errorf("engine %s is not enabled on this agent", task.Engine)
	}
	if !safeServiceName(spec.Service) {
		return "", errors.New("configured systemd service name is unsafe")
	}
	timeout := 45 * time.Second
	if task.Action == core.ActionInstall {
		timeout = 4 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	switch task.Action {
	case core.ActionReadConfig:
		return e.readCurrentConfig(ctx, task.Engine, spec)
	case core.ActionValidate:
		return e.validate(ctx, task.Engine, spec, task.ConfigContent)
	case core.ActionDeploy:
		validation, err := e.validate(ctx, task.Engine, spec, task.ConfigContent)
		if err != nil {
			return validation, err
		}
		if e.DryRun {
			return validation + "\ndry-run: configuration was not written and the service was not restarted", nil
		}
		backup, err := atomicDeploy(spec.ConfigPath, task.ConfigContent)
		if err != nil {
			return validation, err
		}
		restartOutput, err := serviceCommandAndVerify(ctx, spec.Service, core.ActionRestart)
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
			recoveryOutput, recoveryErr := serviceCommandAndVerify(recoveryContext, spec.Service, core.ActionRestart)
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
	case core.ActionStart, core.ActionStop, core.ActionRestart:
		if e.DryRun {
			return fmt.Sprintf("dry-run: would run systemctl %s %s", task.Action, spec.Service), nil
		}
		return serviceCommandAndVerify(ctx, spec.Service, task.Action)
	case core.ActionStatus:
		if e.DryRun {
			return "dry-run", nil
		}
		return serviceStatus(ctx, spec.Service)
	case core.ActionInstall:
		version, err := core.NormalizeCoreVersionSelector(task.CoreVersion)
		if err != nil {
			return "", err
		}
		if e.DryRun {
			return fmt.Sprintf("dry-run: would install %s %s from its official GitHub release", task.Engine, version), nil
		}
		updater := e.Updater
		if updater == nil {
			updater = NewCoreUpdater()
		}
		return updater.Install(ctx, task.Engine, spec, version)
	default:
		return "", fmt.Errorf("unsupported action %q", task.Action)
	}
}

func (e *Executor) readCurrentConfig(ctx context.Context, engine core.Engine, spec EngineSpec) (string, error) {
	content, err := readConfigurationFile(spec.ConfigPath)
	if err != nil {
		return "", fmt.Errorf("read current %s configuration: %w", engine, err)
	}
	if err := core.ValidateConfig(engine, content); err != nil {
		return "", fmt.Errorf("current %s configuration is structurally invalid: %w", engine, err)
	}
	if err := validatePrivilegedExecutable(spec.Binary); err != nil {
		return "", fmt.Errorf("cannot safely invoke %s for current configuration validation: %w", engine, err)
	}
	_, err = validateConfigurationPath(ctx, engine, spec.Binary, spec.ConfigPath)
	if err != nil {
		return "", fmt.Errorf("current %s configuration failed real core validation: %w", engine, err)
	}
	return content, nil
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
	result := make(map[core.Engine]core.RuntimeState, len(e.Specs))
	for engine, spec := range e.Specs {
		state := core.RuntimeState{}
		if path, err := exec.LookPath(spec.Binary); err == nil {
			state.Installed = true
			state.Version = binaryVersion(ctx, engine, path)
		}
		if e.DryRun {
			state.ServiceStatus = "dry-run"
		} else if status, err := serviceStatus(ctx, spec.Service); err == nil {
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
	if e.DryRun {
		return "syntax validation passed (dry-run; engine binary was not invoked)", nil
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
	output, err := runInDirectory(ctx, directory, spec.Binary, args...)
	if err != nil {
		return output, fmt.Errorf("%s rejected the configuration: %w", engine, err)
	}
	if output == "" {
		output = fmt.Sprintf("%s validation passed", engine)
	}
	return output, nil
}

func serviceCommand(ctx context.Context, service string, action core.Action) (string, error) {
	if service == "" {
		return "", errors.New("service name is not configured")
	}
	if action != core.ActionStart && action != core.ActionStop && action != core.ActionRestart {
		return "", errors.New("unsupported service action")
	}
	output, err := run(ctx, systemctlPath, string(action), service)
	if err != nil {
		return output, fmt.Errorf("systemctl %s %s: %w", action, service, err)
	}
	if output == "" {
		output = fmt.Sprintf("systemctl %s %s completed", action, service)
	}
	return output, nil
}

func serviceCommandAndVerify(ctx context.Context, service string, action core.Action) (string, error) {
	output, err := serviceCommand(ctx, service, action)
	if err != nil {
		return output, err
	}
	expected := "active"
	stableFor := 500 * time.Millisecond
	if action == core.ActionStop {
		expected = "inactive"
		stableFor = 0
	}
	verifyContext, verifyCancel := context.WithTimeout(ctx, 5*time.Second)
	status, statusErr := waitForServiceState(verifyContext, expected, stableFor, 100*time.Millisecond, func(probeContext context.Context) (string, error) {
		return serviceStatus(probeContext, service)
	})
	verifyCancel()
	if statusErr != nil {
		return output, fmt.Errorf("verify systemd service %s after %s: %w", service, action, statusErr)
	}
	if status != expected {
		return output + "\nservice status: " + status, fmt.Errorf("systemd service %s is %s after %s, expected %s", service, status, action, expected)
	}
	return output + "\nservice status: " + status, nil
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
			if status == "failed" || status == "inactive" {
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
	if !safeServiceName(service) {
		return "", errors.New("configured systemd service name is unsafe")
	}
	output, err := run(ctx, systemctlPath, "is-active", service)
	if err != nil {
		trimmed := strings.TrimSpace(output)
		if trimmed == "inactive" || trimmed == "failed" || trimmed == "activating" || trimmed == "deactivating" {
			return trimmed, nil
		}
		return output, err
	}
	return strings.TrimSpace(output), nil
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
	if !filepath.IsAbs(name) {
		return "", errors.New("refusing to execute a non-absolute binary path")
	}
	commandContext, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(commandContext, name, args...)
	if directory != "" {
		command.Dir = directory
	}
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	configureCommand(command)
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
	metadata := fileMetadata{mode: 0o600}
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
