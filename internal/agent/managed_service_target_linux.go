//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func serviceUnitFingerprint(kind, unitRoot, validator, service string) string {
	var b strings.Builder
	b.Grow(128)
	b.WriteString(kind)
	b.WriteByte('|')
	b.WriteString(validator)
	b.WriteByte('|')
	b.WriteString(unitRoot)
	b.WriteByte('|')
	b.WriteString(service)
	unitPath := filepath.Join(unitRoot, service)
	appendLstatFingerprint(&b, unitPath)
	if kind != ServiceManagerOpenRC {
		dropInDir := unitPath + ".d"
		info, err := os.Lstat(dropInDir)
		if err != nil {
			b.WriteString("|d:")
			b.WriteString(err.Error())
			return b.String()
		}
		b.WriteString("|d:")
		b.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), 10))
		b.WriteByte(':')
		b.WriteString(strconv.FormatUint(uint64(info.Mode()), 8))
		if entries, readErr := os.ReadDir(dropInDir); readErr == nil {
			for _, entry := range entries {
				b.WriteByte('|')
				b.WriteString(entry.Name())
				appendLstatFingerprint(&b, filepath.Join(dropInDir, entry.Name()))
			}
		}
	}
	return b.String()
}

func appendLstatFingerprint(b *strings.Builder, path string) {
	info, err := os.Lstat(path)
	if err != nil {
		b.WriteString("|missing:")
		if errors.Is(err, os.ErrNotExist) {
			b.WriteString("enoent")
		} else {
			b.WriteString(err.Error())
		}
		return
	}
	b.WriteString("|stat:")
	b.WriteString(strconv.FormatInt(info.Size(), 10))
	b.WriteByte(':')
	b.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), 10))
	b.WriteByte(':')
	b.WriteString(strconv.FormatUint(uint64(info.Mode()), 8))
}

func managedServiceTargetFingerprint(managed EngineSpec, manager *ServiceManager) string {
	if manager != nil && manager.Kind() == ServiceManagerOpenRC {
		return serviceUnitFingerprint(ServiceManagerOpenRC, openRCInitRoot, rcServicePath, managed.Service)
	}
	return serviceUnitFingerprint(ServiceManagerSystemd, existingDiscoveryManagedUnitRoot, systemctlPath, managed.Service)
}

func validateManagedServiceTarget(ctx context.Context, engine core.Engine, managed EngineSpec, managers ...*ServiceManager) (string, error) {
	manager := selectedServiceManager(managers...)
	defaultSpec, ok := DefaultSpecsForServiceManager(manager.Kind())[engine]
	if !ok || managed != defaultSpec {
		return "", errors.New("managed service mapping is not the QAgent default")
	}
	if manager.Kind() == ServiceManagerOpenRC {
		if _, err := os.Lstat(filepath.Join(openRCInitRoot, managed.Service)); errors.Is(err, os.ErrNotExist) {
			return "not-found", nil
		} else if err != nil {
			return "", err
		}
		status, err := serviceStatusWithManager(ctx, manager, managed.Service)
		if err != nil {
			return "", fmt.Errorf("read managed OpenRC service state: %w", err)
		}
		if status != "active" && status != "inactive" && status != "failed" {
			return "", errors.New("managed OpenRC service has an unsupported state")
		}
		marker := "# QControlHub managed OpenRC service: " + managed.Service
		if err := validateOpenRCServiceScript(managed.Service, marker); err != nil {
			return "", err
		}
		return status, nil
	}
	loadState, err := run(ctx, systemctlPath, "show", managed.Service, "--property=LoadState", "--value")
	if strings.TrimSpace(loadState) == "not-found" {
		return "not-found", nil
	}
	if err != nil {
		return "", fmt.Errorf("read managed service LoadState: %w\n%s", err, core.DiagnosticText(loadState))
	}
	if strings.TrimSpace(loadState) != "loaded" {
		return "", fmt.Errorf("managed service unit is not loaded: LoadState=%q", core.DiagnosticText(loadState))
	}
	status, err := serviceStatusWithManager(ctx, manager, managed.Service)
	if err != nil {
		return "", fmt.Errorf("read managed service state: %w", err)
	}
	if status != "active" && status != "inactive" && status != "failed" {
		return "", errors.New("managed service has an unsupported state")
	}
	fragmentPath, err := run(ctx, systemctlPath, "show", managed.Service, "--property=FragmentPath", "--value")
	expectedFragmentPath := filepath.Join(existingDiscoveryManagedUnitRoot, managed.Service)
	if err != nil {
		return "", fmt.Errorf("read managed service FragmentPath: %w\n%s", err, core.DiagnosticText(fragmentPath))
	}
	if strings.TrimSpace(fragmentPath) != expectedFragmentPath {
		return "", errors.New("managed service fragment path is not the QAgent-owned location")
	}
	fragmentPath = strings.TrimSpace(fragmentPath)
	if err := validateProtectedDirectoryChain(filepath.Dir(fragmentPath)); err != nil {
		return "", err
	}
	info, err := os.Lstat(fragmentPath)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("managed service unit file is unsafe")
	}
	if err := validateOwner(info, "managed service unit file"); err != nil {
		return "", err
	}
	contents, err := os.ReadFile(fragmentPath)
	if err != nil {
		return "", err
	}
	if err := validateManagedUnitFragment(contents, engine, managed); err != nil {
		return "", err
	}
	if err := validateManagedServiceExecutionContext(ctx, engine, managed); err != nil {
		return "", err
	}
	if err := validateManagedUnitDropIns(ctx, managed.Service); err != nil {
		return "", err
	}
	for property, expected := range map[string]string{"Description": engineDisplayName(engine) + " core managed by QAgent", "User": "qcontrolhub-core", "Group": "qcontrolhub-core"} {
		value, err := systemdUnitProperty(ctx, managed.Service, property)
		if err != nil {
			return "", fmt.Errorf("read managed service effective %s: %w", property, err)
		}
		if value != expected {
			return "", fmt.Errorf("managed service effective %s is not %q", property, expected)
		}
	}
	for _, property := range []string{"ExecCondition", "ExecStartPre", "ExecStartPost", "ExecReload", "ExecStop", "ExecStopPost"} {
		value, err := systemdUnitProperty(ctx, managed.Service, property)
		if err != nil {
			return "", fmt.Errorf("read managed service effective %s: %w", property, err)
		}
		if value != "" {
			return "", fmt.Errorf("managed service has an unsupported effective %s hook", property)
		}
	}
	execStart, err := run(ctx, systemctlPath, "show", managed.Service, "--property=ExecStart", "--value")
	if err != nil {
		return "", fmt.Errorf("read managed service ExecStart: %w\n%s", err, core.DiagnosticText(execStart))
	}
	executable, argv, err := parseSingleSystemdExecStart(execStart)
	if err != nil {
		return "", fmt.Errorf("parse managed service ExecStart: %w", err)
	}
	if !supportedManagedExecStart(engine, managed, executable, argv) {
		return "", errors.New("managed service ExecStart is not the exact QAgent-owned command")
	}
	return status, nil
}
