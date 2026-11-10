package agent

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (e *Executor) validateImportedSnapshot(ctx context.Context, engine core.Engine, spec EngineSpec, content string) (string, error) {
	if engine != core.EngineShadowsocksRust {
		return e.validate(ctx, engine, spec, content)
	}
	if err := core.ValidateConfig(engine, content); err != nil {
		return "", err
	}
	identity, err := managedCoreServiceIdentity()
	if err != nil {
		return "", err
	}
	if err := ensureManagedCoreBinaryAccess(engine, spec, e.serviceManager()); err != nil {
		return "", err
	}
	root := filepath.Dir(spec.ConfigPath)
	if pathWithin(spec.ConfigPath, managedCoreConfigurationRoot) {
		root = managedCoreConfigurationRoot
	}
	metadata, err := prepareManagedConfigurationAccessWithGID(root, spec.ConfigPath, int(identity.gid))
	if err != nil {
		return "", err
	}
	if wantEngine, want, ok := managedSpecForService(spec.Service, e.serviceManager().Kind()); ok && wantEngine == engine && want == spec {
		if err := e.prepareCoreUpdateStateDirectory(ctx, engine, spec); err != nil {
			return "", err
		}
	}
	if err := e.validateSSRustStartupCandidate(ctx, spec, content, spec.Binary, "", identity, metadata); err != nil {
		return "", err
	}
	return "ss-rust isolated service startup validated", nil
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
