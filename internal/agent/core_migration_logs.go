package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

var (
	legacyCoreLogRoot        = "/var/log"
	managedXrayLogRoot       = "/var/lib/qcontrolhub-xray"
	managedSSRustLogRoot     = "/var/lib/qcontrolhub-shadowsocks-rust"
	retiredLogSystemdRunPath = "/usr/bin/systemd-run"
	retiredLogTruncatePath   = "/usr/bin/truncate"
	truncateRetiredLogDirect = os.Truncate
)

// upgradeManagedCoreLogPolicies repairs configurations written by Agent builds
// that still allowed a core to bypass the bounded console transport. It covers
// both completed imports and ordinary panel deployments so a binary-only Agent
// upgrade cannot leave Xray, sing-box, or SS Rust writing durable local logs.
// The managed service is restarted only when it was active; an operator-stopped
// service stays stopped.
func (e *Executor) upgradeManagedCoreLogPolicies(ctx context.Context) error {
	if e == nil {
		return nil
	}
	e.migrationMu.Lock()
	defer e.migrationMu.Unlock()
	e.specsMu.RLock()
	specs := make(map[core.Engine]EngineSpec, len(e.Specs))
	for engine, spec := range e.Specs {
		specs[engine] = spec
	}
	e.specsMu.RUnlock()
	for _, engine := range []core.Engine{core.EngineXray, core.EngineSingBox, core.EngineShadowsocksRust} {
		spec, configured := specs[engine]
		if !configured {
			continue
		}
		content, err := readConfigurationFile(spec.ConfigPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read managed %s configuration for log upgrade: %w", engine, err)
		}
		normalized, err := normalizeManagedCoreLogDestinations(engine, content)
		if err != nil {
			return fmt.Errorf("normalize managed %s log output: %w", engine, err)
		}
		if normalized == content {
			continue
		}
		legacyPaths, err := retiredCoreLogPaths(engine, content)
		if err != nil {
			return fmt.Errorf("identify retired %s logs: %w", engine, err)
		}
		if err := core.ValidateConfig(engine, normalized); err != nil {
			return fmt.Errorf("validate managed %s log upgrade: %w", engine, err)
		}
		if err := e.validateManagedLogPolicy(ctx, engine, spec, normalized); err != nil {
			return fmt.Errorf("normalized %s configuration still uses persistent logging: %w", engine, err)
		}
		defaultSpec, managed := DefaultSpecsForServiceManager(e.serviceManager().Kind())[engine]
		if managed && spec == defaultSpec {
			_, err = e.validateManagedServiceSnapshot(ctx, engine, spec, normalized)
		} else {
			_, err = e.validateSnapshot(ctx, engine, spec, normalized)
		}
		if err != nil {
			return fmt.Errorf("managed %s core rejected normalized log output: %w", engine, err)
		}
		status, err := serviceStatusWithManager(ctx, e.serviceManager(), spec.Service)
		if err != nil {
			return fmt.Errorf("query managed %s service before log upgrade: %w", engine, err)
		}
		if status != "active" && status != "inactive" && status != "failed" {
			return fmt.Errorf("managed %s service is %s during log upgrade", engine, status)
		}
		backup, err := atomicDeployManagedConfiguration(engine, spec, e.serviceManager(), normalized)
		if err != nil {
			return fmt.Errorf("deploy normalized %s log output: %w", engine, err)
		}
		rollback := func(cause error) error {
			_, restoreErr := rollbackDeploy(spec.ConfigPath, backup)
			if status == "active" && restoreErr == nil {
				recoveryContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				_, restartErr := serviceCommandAndVerifyWithManager(recoveryContext, e.serviceManager(), spec.Service, core.ActionRestart)
				cancel()
				restoreErr = errors.Join(restoreErr, restartErr)
			}
			return errors.Join(cause, restoreErr)
		}
		if status == "active" {
			if _, err := serviceCommandAndVerifyWithManager(ctx, e.serviceManager(), spec.Service, core.ActionRestart); err != nil {
				return fmt.Errorf("restart %s after log output upgrade: %w", engine, rollback(err))
			}
		}
		slog.Info("upgraded managed core to panel-managed logging", "engine", engine)
		for _, legacyPath := range legacyPaths {
			reclaimed, cleanupErr := truncateRetiredCoreLog(ctx, legacyPath, e.serviceManager())
			if cleanupErr != nil {
				// The running configuration no longer writes this path, so failure
				// cannot let it grow again. Keep the Agent online and leave an
				// actionable warning for manual reclamation.
				slog.Warn("truncate retired core log", "path", legacyPath, "error", cleanupErr)
			} else if reclaimed > 0 {
				slog.Info("truncated retired core log", "path", legacyPath, "bytes", reclaimed)
			}
		}
	}
	return nil
}

func normalizeManagedCoreLogDestinations(engine core.Engine, content string) (string, error) {
	switch engine {
	case core.EngineXray:
		return normalizeImportedXrayLogDestinations(content)
	case core.EngineSingBox:
		return normalizeImportedSingBoxLogDestination(content)
	case core.EngineShadowsocksRust:
		return normalizeImportedSSRustLogDestinations(content)
	default:
		return content, nil
	}
}

func retiredCoreLogPaths(engine core.Engine, content string) ([]string, error) {
	switch engine {
	case core.EngineXray:
		return retiredXrayLogPaths(content)
	case core.EngineSingBox:
		if path := retiredSingBoxLogPath(content); path != "" {
			return []string{path}, nil
		}
		return nil, nil
	case core.EngineShadowsocksRust:
		return retiredSSRustLogPaths(content)
	default:
		return nil, nil
	}
}

func retiredXrayLogPaths(content string) ([]string, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return nil, err
	}
	var logging map[string]json.RawMessage
	if raw, exists := root["log"]; !exists || string(raw) == "null" {
		return nil, nil
	} else if err := json.Unmarshal(raw, &logging); err != nil {
		return nil, err
	}
	paths := make([]string, 0, 2)
	seen := make(map[string]struct{})
	for _, key := range []string{"access", "error"} {
		raw, exists := logging[key]
		if !exists {
			continue
		}
		var destination string
		if err := json.Unmarshal(raw, &destination); err != nil {
			return nil, fmt.Errorf("Xray log.%s destination is not a string: %w", key, err)
		}
		if destination == "" || destination == "none" {
			continue
		}
		path := filepath.Clean(destination)
		if !filepath.IsAbs(path) {
			path = filepath.Join(managedXrayLogRoot, path)
		}
		if !retiredCoreLogRootContains(path) {
			continue
		}
		if _, duplicate := seen[path]; !duplicate {
			paths = append(paths, path)
			seen[path] = struct{}{}
		}
	}
	return paths, nil
}

func retiredSSRustLogPaths(content string) ([]string, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		return nil, err
	}
	var logging map[string]json.RawMessage
	if raw, exists := root["log"]; !exists || string(raw) == "null" {
		return nil, nil
	} else if err := json.Unmarshal(raw, &logging); err != nil {
		return nil, err
	}
	rawWriters, exists := logging["writers"]
	if !exists {
		return nil, nil
	}
	var writers []json.RawMessage
	if err := json.Unmarshal(rawWriters, &writers); err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	seen := make(map[string]struct{})
	for _, rawWriter := range writers {
		var writer map[string]json.RawMessage
		if err := json.Unmarshal(rawWriter, &writer); err != nil {
			return nil, err
		}
		rawFile, fileWriter := writer["file"]
		if !fileWriter {
			continue
		}
		var settings map[string]json.RawMessage
		if err := json.Unmarshal(rawFile, &settings); err != nil {
			return nil, err
		}
		directory, err := ssRustLogWriterString(settings, "directory", "")
		if err != nil || directory == "" {
			if err == nil {
				err = errors.New("SS Rust file log directory is empty")
			}
			return nil, err
		}
		prefix, err := ssRustLogWriterString(settings, "prefix", "ssserver")
		if err != nil {
			return nil, err
		}
		suffix, err := ssRustLogWriterString(settings, "suffix", "log")
		if err != nil {
			return nil, err
		}
		rotation, err := ssRustLogWriterString(settings, "rotation", "never")
		if err != nil {
			return nil, err
		}
		if !safeSSRustLogNamePart(prefix) || !safeSSRustLogNamePart(suffix) {
			continue
		}
		directory = filepath.Clean(directory)
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(managedSSRustLogRoot, directory)
		}
		if !retiredCoreLogRootContains(filepath.Join(directory, prefix+"."+suffix)) {
			continue
		}
		entries, err := os.ReadDir(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !ssRustRetiredLogName(entry.Name(), prefix, suffix, rotation) {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			if _, duplicate := seen[path]; !duplicate {
				paths = append(paths, path)
				seen[path] = struct{}{}
			}
		}
	}
	return paths, nil
}

func ssRustLogWriterString(settings map[string]json.RawMessage, key, fallback string) (string, error) {
	raw, exists := settings[key]
	if !exists {
		return fallback, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("SS Rust file log %s is not a string: %w", key, err)
	}
	return value, nil
}

func safeSSRustLogNamePart(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "/\\\x00\r\n")
}

func ssRustRetiredLogName(name, prefix, suffix, rotation string) bool {
	if rotation == "never" {
		return name == prefix+"."+suffix
	}
	start, end := prefix+".", "."+suffix
	if !strings.HasPrefix(name, start) || !strings.HasSuffix(name, end) {
		return false
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, start), end)
	layout := "2006-01-02"
	if rotation == "hourly" {
		layout = "2006-01-02-15"
	} else if rotation != "daily" {
		return false
	}
	_, err := time.Parse(layout, stamp)
	return err == nil
}

func retiredSingBoxLogPath(content string) string {
	output, destination, err := singBoxLogOutput(content)
	if err != nil || destination != singBoxLogDestinationFile {
		return ""
	}
	path, err := importedSingBoxLogPath(output)
	if filepath.IsAbs(output) {
		path, err = filepath.Clean(output), nil
	}
	if err != nil || !retiredCoreLogRootContains(path) {
		return ""
	}
	return path
}

func truncateRetiredCoreLog(ctx context.Context, path string, manager *ServiceManager) (int64, error) {
	path = filepath.Clean(path)
	root, managedState, allowed := retiredCoreLogRoot(path)
	if !filepath.IsAbs(path) || !allowed {
		return 0, errors.New("retired core log is outside an Agent-managed cleanup root")
	}
	if err := validateRetiredCoreLogDirectory(filepath.Dir(path), root, managedState); err != nil {
		return 0, fmt.Errorf("retired core log directory is unsafe: %w", err)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !coreLogFileHasSingleLink(info) || info.Mode().Perm()&0o022 != 0 {
		return 0, errors.New("retired core log is not a protected regular file")
	}
	reclaimed := info.Size()
	if err := truncateRetiredLogDirect(path, 0); err == nil {
		return reclaimed, nil
	} else if selectedServiceManager(manager).Kind() != ServiceManagerSystemd {
		return 0, err
	}
	// Installed systemd Agents use ProtectSystem=strict and cannot directly
	// write /var/log. A short-lived root unit exposes only the already-validated
	// file and carries only DAC_OVERRIDE so installer-owned logs can be truncated
	// after the legacy service is retired.
	for _, executable := range []string{retiredLogSystemdRunPath, retiredLogTruncatePath} {
		if err := validatePrivilegedExecutable(executable); err != nil {
			return 0, fmt.Errorf("unsafe retired-log helper %s: %w", executable, err)
		}
	}
	arguments := []string{
		"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec",
		"--property=User=root", "--property=UMask=0077", "--property=NoNewPrivileges=yes",
		"--property=CapabilityBoundingSet=CAP_DAC_OVERRIDE", "--property=AmbientCapabilities=CAP_DAC_OVERRIDE",
		"--property=ProtectSystem=strict", "--property=ProtectHome=yes", "--property=PrivateTmp=yes",
		"--property=PrivateDevices=yes", "--property=RestrictAddressFamilies=AF_UNIX",
		"--property=ReadWritePaths=" + path, "--", retiredLogTruncatePath, "--size=0", "--", path,
	}
	if output, err := run(ctx, retiredLogSystemdRunPath, arguments...); err != nil {
		return 0, fmt.Errorf("truncate retired core log: %w: %s", err, strings.TrimSpace(output))
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, after) || after.Size() != 0 {
		if err == nil {
			err = errors.New("retired core log changed during cleanup")
		}
		return 0, err
	}
	return reclaimed, nil
}

func retiredCoreLogRootContains(path string) bool {
	_, _, allowed := retiredCoreLogRoot(path)
	return allowed
}

func retiredCoreLogRoot(path string) (string, bool, bool) {
	path = filepath.Clean(path)
	for _, candidate := range []struct {
		path         string
		managedState bool
	}{
		{path: legacyCoreLogRoot},
		{path: managedXrayLogRoot, managedState: true},
		{path: importedSingBoxLogRoot, managedState: true},
		{path: managedSSRustLogRoot, managedState: true},
	} {
		root := filepath.Clean(candidate.path)
		if path != root && pathWithin(path, root) {
			return root, candidate.managedState, true
		}
	}
	return "", false, false
}

func validateRetiredCoreLogDirectory(directory, root string, managedState bool) error {
	if !managedState {
		return validateProtectedDirectoryChain(directory)
	}
	if err := validateProtectedDirectoryChain(filepath.Dir(root)); err != nil {
		return err
	}
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return err
	}
	directory = filepath.Clean(directory)
	for current := filepath.Clean(root); ; {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("%s is not a protected real directory", current)
		}
		uid, _, known := fileOwnership(info)
		if !known || (uid != 0 && uint32(uid) != identity.uid) {
			return fmt.Errorf("%s is not owned by root or the managed core identity", current)
		}
		if current == directory {
			return nil
		}
		relative, err := filepath.Rel(current, directory)
		if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("retired core log directory escapes its managed state root")
		}
		component := strings.Split(relative, string(filepath.Separator))[0]
		current = filepath.Join(current, component)
	}
}
