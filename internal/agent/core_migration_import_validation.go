package agent

import (
	"context"
	"fmt"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (e *Executor) validateImportedSnapshot(ctx context.Context, engine core.Engine, spec EngineSpec, content string) (string, error) {
	if engine != core.EngineSingBox {
		return e.validate(ctx, engine, spec, content)
	}
	output, destination, err := singBoxLogOutput(content)
	if err != nil {
		return "", err
	}
	if destination == singBoxLogDestinationFile {
		if _, err := importedSingBoxLogPath(output); err != nil {
			return "", fmt.Errorf("imported sing-box log output is unsafe: %w", err)
		}
	}
	defaultSpec, managed := DefaultSpecsForServiceManager(e.serviceManager().Kind())[engine]
	if managed && spec == defaultSpec {
		return e.validateManagedServiceSnapshot(ctx, engine, spec, content)
	}
	return e.validateSnapshot(ctx, engine, spec, content)
}

func requireManagedServiceSafeInactive(ctx context.Context, engine core.Engine, managed EngineSpec, managers ...*ServiceManager) error {
	manager := selectedServiceManager(managers...)
	if manager.Kind() == ServiceManagerOpenRC {
		marker := "# QControlHub managed OpenRC service: " + managed.Service
		if err := validateOpenRCServiceScript(managed.Service, marker); err != nil {
			return fmt.Errorf("QAgent %s OpenRC service script is unsafe: %w", engine, err)
		}
	}
	status, err := serviceStatusWithManager(ctx, manager, managed.Service)
	if err != nil {
		return fmt.Errorf("query QAgent %s service before migration: %w", engine, err)
	}
	if status != "inactive" && status != "failed" {
		return fmt.Errorf("QAgent %s service must remain inactive or failed before migration (status %q); both services were left unchanged", engine, status)
	}
	return nil
}

// managedServiceInitialState is the migration task's explicit coordination
// point: an active QAgent service is stopped by the protected import task,
// recorded in the durable migration marker, and restored on every rollback.
func managedServiceInitialState(ctx context.Context, engine core.Engine, managed EngineSpec, managers ...*ServiceManager) (string, error) {
	manager := selectedServiceManager(managers...)
	if manager.Kind() == ServiceManagerOpenRC {
		marker := "# QControlHub managed OpenRC service: " + managed.Service
		if err := validateOpenRCServiceScript(managed.Service, marker); err != nil {
			return "", fmt.Errorf("QAgent %s OpenRC service script is unsafe: %w", engine, err)
		}
	}
	status, err := serviceStatusWithManager(ctx, manager, managed.Service)
	if err != nil {
		return "", fmt.Errorf("query QAgent %s service before migration: %w", engine, err)
	}
	if status != "inactive" && status != "failed" && status != "active" {
		return "", fmt.Errorf("QAgent %s service has unsupported status %q before migration; both services were left unchanged", engine, status)
	}
	if status == "active" {
		if err := ensureManagedCoreServiceCapabilities(ctx, engine, managed, manager); err != nil {
			return "", err
		}
		return "active", nil
	}
	return "inactive", nil
}
