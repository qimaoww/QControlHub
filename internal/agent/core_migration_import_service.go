package agent

import (
	"context"
	"fmt"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func verifyExistingServiceMapping(ctx context.Context, engine core.Engine, existing EngineSpec, managers ...*ServiceManager) error {
	manager := selectedServiceManager(managers...)
	status, err := serviceStatusWithManager(ctx, manager, existing.Service)
	if err != nil {
		return fmt.Errorf("query existing %s service before migration: %w", engine, err)
	}
	if status != "active" {
		return fmt.Errorf("existing %s service must remain active before migration (status %q)", engine, status)
	}
	if manager.Kind() == ServiceManagerOpenRC {
		if err := validateOpenRCServiceScript(existing.Service, ""); err != nil {
			return fmt.Errorf("existing %s OpenRC service script is unsafe: %w", engine, err)
		}
		if _, err := verifyOpenRCExistingServiceProcess(ctx, engine, existing); err != nil {
			return fmt.Errorf("inspect existing %s OpenRC process before migration: %w", engine, err)
		}
	} else {
		output, err := run(ctx, systemctlPath, "show", existing.Service, "--property=ExecStart", "--value")
		if err != nil {
			return fmt.Errorf("query existing %s service ExecStart before migration: %w", engine, err)
		}
		executable, argv, err := parseSingleSystemdExecStart(output)
		if err != nil || executable != existingServiceBinary(existing) || !supportedExistingExecStart(engine, existing, argv) {
			return fmt.Errorf("existing %s service ExecStart no longer matches the exact discovered binary and single configuration", engine)
		}
	}
	if err := validateExistingServiceExecutable(existing); err != nil {
		return fmt.Errorf("existing %s service executable mapping is no longer safe: %w", engine, err)
	}
	status, err = serviceStatusWithManager(ctx, manager, existing.Service)
	if err != nil {
		return fmt.Errorf("recheck existing %s service before migration: %w", engine, err)
	}
	if status != "active" {
		return fmt.Errorf("existing %s service changed to %q while its mapping was checked", engine, status)
	}
	return nil
}
