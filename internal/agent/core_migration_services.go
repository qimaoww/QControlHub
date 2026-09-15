package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func serviceEnableState(ctx context.Context, service string, managers ...*ServiceManager) (string, error) {
	manager := selectedServiceManager(managers...)
	if !safeServiceName(service) {
		return "", errors.New("configured service name is unsafe")
	}
	if manager.Kind() == ServiceManagerOpenRC {
		return openRCServiceEnableState(ctx, service)
	}
	output, err := run(ctx, manager.enableHelper(), "is-enabled", service)
	state := strings.TrimSpace(output)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if validServiceEnableState(state) {
		return state, nil
	}
	if err == nil {
		err = errors.New("unexpected systemd enable state")
	}
	return "", fmt.Errorf("query whether systemd service %s is enabled: %w: %s", service, err, state)
}

func validServiceEnableState(state string) bool {
	return state == "enabled" || state == "enabled-runtime" || state == "disabled" || state == "static" || state == "indirect"
}

func migrationEnableStatesSupported(existing, managed string) bool {
	supported := func(state string) bool {
		return state == "enabled" || state == "enabled-runtime" || state == "disabled"
	}
	return supported(existing) && supported(managed)
}

func setServiceEnabled(ctx context.Context, service string, enabled bool, managers ...*ServiceManager) error {
	manager := selectedServiceManager(managers...)
	if !safeServiceName(service) {
		return errors.New("configured service name is unsafe")
	}
	if manager.Kind() == ServiceManagerOpenRC {
		action := "del"
		want := "disabled"
		if enabled {
			action = "add"
			want = "enabled"
		}
		current, err := openRCServiceEnableState(ctx, service)
		if err != nil {
			return err
		}
		// rc-update returns a failure when deleting a service that is already
		// absent from the runlevel. Rollback and restart reconciliation must be
		// idempotent, but only after the protected runlevel tree has proved that
		// the service is already in the exact requested state.
		if current == want {
			return nil
		}
		if output, err := run(ctx, manager.enableHelper(), action, service, "default"); err != nil {
			return fmt.Errorf("openrc rc-update %s %s: %w: %s", action, service, err, output)
		}
		state, err := openRCServiceEnableState(ctx, service)
		if err != nil {
			return err
		}
		if state != want {
			return fmt.Errorf("OpenRC service %s enable state is %s after rc-update %s", service, state, action)
		}
		return nil
	}
	action := "disable"
	if enabled {
		action = "enable"
	}
	if output, err := run(ctx, manager.enableHelper(), action, service); err != nil {
		return fmt.Errorf("systemctl %s %s: %w: %s", action, service, err, output)
	}
	return nil
}

func disableServiceCompletely(ctx context.Context, service string, managers ...*ServiceManager) error {
	manager := selectedServiceManager(managers...)
	if err := setServiceEnabled(ctx, service, false, manager); err != nil {
		return err
	}
	if manager.Kind() == ServiceManagerOpenRC {
		return nil
	}
	if output, err := run(ctx, manager.enableHelper(), "disable", "--runtime", service); err != nil {
		return fmt.Errorf("systemctl disable --runtime %s: %w: %s", service, err, output)
	}
	return nil
}

func restoreServiceEnableState(ctx context.Context, service, state string, managers ...*ServiceManager) error {
	manager := selectedServiceManager(managers...)
	switch state {
	case "enabled":
		return setServiceEnabled(ctx, service, true, manager)
	case "enabled-runtime":
		if manager.Kind() == ServiceManagerOpenRC {
			return errors.New("OpenRC does not support runtime-only enablement")
		}
		if err := disableServiceCompletely(ctx, service, manager); err != nil {
			return err
		}
		if output, err := run(ctx, manager.enableHelper(), "enable", "--runtime", service); err != nil {
			return fmt.Errorf("systemctl enable --runtime %s: %w: %s", service, err, output)
		}
		restored, err := serviceEnableState(ctx, service, manager)
		if err != nil {
			return err
		}
		if restored != "enabled-runtime" {
			return fmt.Errorf("systemd service %s enable state restored as %s instead of enabled-runtime", service, restored)
		}
		return nil
	case "disabled":
		return disableServiceCompletely(ctx, service, manager)
	case "static", "indirect":
		if manager.Kind() == ServiceManagerOpenRC {
			return errors.New("OpenRC does not support static or indirect enablement")
		}
		return nil
	default:
		return errors.New("invalid original systemd enable state")
	}
}

func openRCServiceEnableState(ctx context.Context, service string) (string, error) {
	if !safeServiceName(service) || strings.Contains(service, ".service") {
		return "", errors.New("OpenRC service name is unsafe")
	}
	entries, err := os.ReadDir(openRCRunlevelsRoot)
	if err != nil {
		return "", fmt.Errorf("read OpenRC runlevels: %w", err)
	}
	enabledRunlevel := ""
	expectedTarget := filepath.Join(openRCInitRoot, service)
	for _, entry := range entries {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if !entry.IsDir() {
			continue
		}
		linkPath := filepath.Join(openRCRunlevelsRoot, entry.Name(), service)
		info, err := os.Lstat(linkPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect OpenRC runlevel link %s: %w", linkPath, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return "", fmt.Errorf("OpenRC runlevel entry %s is not a symbolic link", linkPath)
		}
		target, err := os.Readlink(linkPath)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(linkPath), target)
		}
		if filepath.Clean(target) != expectedTarget {
			return "", fmt.Errorf("OpenRC runlevel entry %s has an unexpected target", linkPath)
		}
		if entry.Name() != "default" || enabledRunlevel != "" {
			return "", fmt.Errorf("OpenRC service %s is enabled outside the single supported default runlevel", service)
		}
		enabledRunlevel = entry.Name()
	}
	if enabledRunlevel == "default" {
		return "enabled", nil
	}
	return "disabled", nil
}
