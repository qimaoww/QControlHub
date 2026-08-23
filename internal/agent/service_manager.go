package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

const (
	ServiceManagerSystemd = "systemd"
	ServiceManagerOpenRC  = "openrc"
)

const rcServicePath = "/sbin/rc-service"
const rcUpdatePath = "/sbin/rc-update"

// ServiceManager is the small, fixed adapter used for privileged service
// operations. The executable path is selected locally and never comes from a
// task sent by the control plane.
type ServiceManager struct {
	kind             string
	executable       string
	enableExecutable string
}

func NewServiceManager(kind string) (*ServiceManager, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", ServiceManagerSystemd:
		return &ServiceManager{kind: ServiceManagerSystemd, executable: systemctlPath, enableExecutable: systemctlPath}, nil
	case ServiceManagerOpenRC:
		return &ServiceManager{
			kind:             ServiceManagerOpenRC,
			executable:       openRCHelperExecutable("rc-service", rcServicePath),
			enableExecutable: openRCHelperExecutable("rc-update", rcUpdatePath),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported QCH_SERVICE_MANAGER %q", kind)
	}
}

func openRCServiceExecutable() string {
	return openRCHelperExecutable("rc-service", rcServicePath)
}

func openRCHelperExecutable(name, legacyPath string) string {
	// Alpine 3.22 and older install OpenRC below /sbin. /usr-merged Alpine
	// installations expose the canonical regular file below /usr/sbin and a
	// compatibility path through /sbin. Prefer the non-symlinked parent so the
	// privileged-executable validation remains meaningful.
	for _, candidate := range []string{"/usr/sbin/" + name, legacyPath} {
		if info, err := os.Lstat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate
		}
	}
	return legacyPath
}

func (manager *ServiceManager) Kind() string {
	if manager == nil || manager.kind == "" {
		return ServiceManagerSystemd
	}
	return manager.kind
}

func defaultSystemdServiceManager() *ServiceManager {
	return &ServiceManager{kind: ServiceManagerSystemd, executable: systemctlPath, enableExecutable: systemctlPath}
}

func selectedServiceManager(managers ...*ServiceManager) *ServiceManager {
	if len(managers) > 0 && managers[0] != nil {
		return managers[0]
	}
	return defaultSystemdServiceManager()
}

func (manager *ServiceManager) enableHelper() string {
	if manager != nil && manager.enableExecutable != "" {
		return manager.enableExecutable
	}
	if manager != nil && manager.Kind() == ServiceManagerOpenRC {
		return openRCHelperExecutable("rc-update", rcUpdatePath)
	}
	return systemctlPath
}

func (manager *ServiceManager) validate() error {
	if manager == nil {
		return errors.New("service manager is required")
	}
	if manager.kind != ServiceManagerSystemd && manager.kind != ServiceManagerOpenRC {
		return fmt.Errorf("unsupported service manager %q", manager.kind)
	}
	if err := validatePrivilegedExecutable(manager.executable); err != nil {
		return fmt.Errorf("unsafe %s service helper: %w", manager.kind, err)
	}
	if err := validatePrivilegedExecutable(manager.enableHelper()); err != nil {
		return fmt.Errorf("unsafe %s enablement helper: %w", manager.kind, err)
	}
	return nil
}

func (manager *ServiceManager) command(ctx context.Context, service string, action core.Action) (string, error) {
	if !safeServiceName(service) {
		return "", errors.New("configured service name is unsafe")
	}
	if action != core.ActionStart && action != core.ActionStop && action != core.ActionRestart {
		return "", errors.New("unsupported service action")
	}
	var arguments []string
	if manager.Kind() == ServiceManagerOpenRC {
		// Unlike systemd, OpenRC service scripts are not required to make
		// `restart` start an inactive service. Core installation uses restart
		// for both first install and upgrades, so promote that first restart to
		// start when the service is known to be inactive.
		if action == core.ActionRestart {
			status, statusErr := manager.status(ctx, service)
			if statusErr == nil && status == "inactive" {
				action = core.ActionStart
			}
		}
		arguments = []string{service, string(action)}
	} else {
		arguments = []string{string(action), service}
	}
	output, err := run(ctx, manager.executable, arguments...)
	if err != nil {
		return output, fmt.Errorf("%s %s %s: %w", manager.Kind(), action, service, err)
	}
	if output == "" {
		output = fmt.Sprintf("%s %s %s completed", manager.Kind(), action, service)
	}
	return output, nil
}

func (manager *ServiceManager) status(ctx context.Context, service string) (string, error) {
	if !safeServiceName(service) {
		return "", errors.New("configured service name is unsafe")
	}
	if manager.Kind() == ServiceManagerOpenRC {
		output, err := run(ctx, manager.executable, service, "status")
		lower := strings.ToLower(strings.TrimSpace(output))
		if err == nil {
			return "active", nil
		}
		if strings.Contains(lower, "crashed") {
			return "failed", nil
		}
		if strings.Contains(lower, "stopped") || strings.Contains(lower, "not running") || strings.Contains(lower, "inactive") {
			return "inactive", nil
		}
		return output, err
	}
	output, err := run(ctx, manager.executable, "is-active", service)
	if err != nil {
		trimmed := strings.TrimSpace(output)
		if trimmed == "inactive" || trimmed == "failed" || trimmed == "activating" || trimmed == "deactivating" || trimmed == "reloading" {
			return trimmed, nil
		}
		return output, err
	}
	return strings.TrimSpace(output), nil
}
