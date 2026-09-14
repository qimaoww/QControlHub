package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (e *Executor) LoadCoreMigrationState() error {
	if e == nil || e.MigrationMarkerPrefix == "" || len(e.ExistingSpecs) == 0 {
		return nil
	}
	loadContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	e.specsMu.RLock()
	existingSpecs := make(map[core.Engine]EngineSpec, len(e.ExistingSpecs))
	managedSpecs := make(map[core.Engine]EngineSpec, len(e.ExistingSpecs))
	for engine, existing := range e.ExistingSpecs {
		existingSpecs[engine] = existing
		managedSpecs[engine] = e.Specs[engine]
	}
	e.specsMu.RUnlock()
	for engine, existing := range existingSpecs {
		record, err := readCoreMigrationRecord(e.MigrationMarkerPrefix, engine)
		if err != nil {
			return err
		}
		if record.State == coreMigrationInProgress && !record.HasFileRollback {
			e.specsMu.Lock()
			if e.ExistingDiscoveryIssues == nil {
				e.ExistingDiscoveryIssues = make(map[core.Engine]string)
			}
			e.ExistingDiscoveryIssues[engine] = fmt.Sprintf("检测到旧版未完成的 %s 服务迁移记录；缺少可验证的托管文件回滚信息，相关内核任务已禁用", engine)
			e.specsMu.Unlock()
		}
		if record.State == coreMigrationComplete && record.SourceDigest == coreMigrationSourceDigest(existing) {
			// A completed migration is re-checked on every Agent start. Only the
			// retired legacy service can invalidate it; the current runtime state
			// of the QAgent-managed unit (stopped, disabled, or failed by an
			// operator) belongs to the regular core actions and must not
			// permanently disable the engine.
			ownershipErr := verifyCompletedCoreMigrationOwnership(loadContext, existing, e.serviceManager())
			e.specsMu.Lock()
			if ownershipErr == nil {
				if e.completedMigrations == nil {
					e.completedMigrations = make(map[core.Engine]completedCoreMigration)
				}
				e.completedMigrations[engine] = completedCoreMigration{
					Existing: existing, Managed: managedSpecs[engine], SourceDigest: record.SourceDigest,
				}
				_ = cleanupCoreMigrationBackups(e.MigrationMarkerPrefix, engine)
				delete(e.ExistingSpecs, engine)
				delete(e.ExistingDiscoveryIssues, engine)
			} else {
				if e.ExistingDiscoveryIssues == nil {
					e.ExistingDiscoveryIssues = make(map[core.Engine]string)
				}
				issue := strings.ToValidUTF8(fmt.Sprintf("已完成的 %s 服务迁移状态不再安全，相关内核任务已禁用：%v", engine, ownershipErr), "�")
				if len(issue) > 512 {
					issue = strings.ToValidUTF8(issue[:512], "�")
				}
				e.ExistingDiscoveryIssues[engine] = issue
			}
			e.specsMu.Unlock()
		}
	}
	return nil
}

// ReconcileExistingCoreServices closes the small crash window between a
// successful service switch and its durable marker. The existing service wins
// whenever it is still active; a stable managed service wins only after the
// existing service is already inactive.
func (e *Executor) ReconcileExistingCoreServices(ctx context.Context) error {
	if e == nil || len(e.ExistingSpecs) == 0 {
		return nil
	}
	e.migrationMu.Lock()
	defer e.migrationMu.Unlock()
	e.specsMu.RLock()
	pending := make(map[core.Engine]EngineSpec, len(e.ExistingSpecs))
	managed := make(map[core.Engine]EngineSpec, len(e.Specs))
	for engine, spec := range e.ExistingSpecs {
		pending[engine] = spec
		managed[engine] = e.Specs[engine]
	}
	e.specsMu.RUnlock()
	for engine, existing := range pending {
		migrationRecord, err := readCoreMigrationRecord(e.MigrationMarkerPrefix, engine)
		if err != nil {
			return err
		}
		if migrationRecord.State != coreMigrationInProgress {
			continue
		}
		if migrationRecord.SourceDigest != coreMigrationSourceDigest(existing) {
			return fmt.Errorf("existing %s mapping changed during an incomplete migration", engine)
		}
		if !migrationRecord.HasFileRollback {
			e.specsMu.Lock()
			if e.ExistingDiscoveryIssues == nil {
				e.ExistingDiscoveryIssues = make(map[core.Engine]string)
			}
			e.ExistingDiscoveryIssues[engine] = fmt.Sprintf("检测到旧版未完成的 %s 服务迁移记录；缺少可验证的托管文件回滚信息，相关内核任务已禁用", engine)
			e.specsMu.Unlock()
			if err := restoreLegacyInterruptedCoreMigration(ctx, e.MigrationMarkerPrefix, engine, existing, managed[engine], migrationRecord, e.serviceManager()); err != nil {
				return err
			}
			continue
		}
		manager := e.serviceManager()
		managedStatus, managedStatusErr := serviceStatusWithManager(ctx, manager, managed[engine].Service)
		existingStatus, existingStatusErr := serviceStatusWithManager(ctx, manager, existing.Service)
		rollback := func(cause error) error {
			restoreErr := restoreInterruptedCoreMigration(ctx, e.MigrationMarkerPrefix, engine, existing, managed[engine], migrationRecord, manager)
			return errors.Join(cause, restoreErr)
		}
		if managedStatusErr != nil || existingStatusErr != nil {
			return rollback(errors.Join(managedStatusErr, existingStatusErr))
		}
		if !migrationEnableStatesSupported(migrationRecord.ExistingEnableState, migrationRecord.ManagedEnableState) {
			if err := rollback(nil); err != nil {
				return err
			}
			continue
		}
		if existingStatus != "inactive" || managedStatus != "active" {
			if err := rollback(nil); err != nil {
				return err
			}
			continue
		}
		existingStatus, managedStatus, err = waitForCoreMigrationServicePairStable(ctx, existing.Service, managed[engine].Service, manager)
		if err != nil || existingStatus != "inactive" || managedStatus != "active" {
			if rollbackErr := rollback(err); rollbackErr != nil {
				return rollbackErr
			}
			continue
		}
		if err := verifyCoreMigrationStagedFiles(managed[engine], migrationRecord); err != nil {
			return rollback(fmt.Errorf("verify staged managed files before migration completion: %w", err))
		}
		if err := setServiceEnabled(ctx, managed[engine].Service, true, manager); err != nil {
			return rollback(err)
		}
		if err := stopAndDisableMigrationAuxiliary(ctx, migrationRecord, manager); err != nil {
			return rollback(fmt.Errorf("stop OU-SB auxiliary service: %w", err))
		}
		if err := disableServiceCompletely(ctx, existing.Service, manager); err != nil {
			return rollback(err)
		}
		if err := verifyCoreMigrationCompletionState(ctx, existing, managed[engine], manager); err != nil {
			return rollback(fmt.Errorf("verify service state before migration completion: %w", err))
		}
		if err := writeCoreMigrationMarkerWithRequestDigest(e.MigrationMarkerPrefix, engine, coreMigrationComplete, migrationRecord.ConfigDigest, migrationRecord.RequestConfigDigest, migrationRecord.SourceDigest, migrationRecord.ExistingEnableState, migrationRecord.ManagedEnableState); err != nil {
			return rollback(err)
		}
		_ = cleanupCoreMigrationBackups(e.MigrationMarkerPrefix, engine)
		e.specsMu.Lock()
		if e.completedMigrations == nil {
			e.completedMigrations = make(map[core.Engine]completedCoreMigration)
		}
		e.completedMigrations[engine] = completedCoreMigration{
			Existing: existing, Managed: managed[engine], SourceDigest: migrationRecord.SourceDigest,
		}
		delete(e.ExistingSpecs, engine)
		e.specsMu.Unlock()
	}
	return nil
}

func waitForCoreMigrationServicePairStable(ctx context.Context, existingService, managedService string, managers ...*ServiceManager) (string, string, error) {
	manager := selectedServiceManager(managers...)
	stableContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var stableSince time.Time
	for {
		existingStatus, err := serviceStatusWithManager(stableContext, manager, existingService)
		if err != nil {
			return existingStatus, "", err
		}
		managedStatus, err := serviceStatusWithManager(stableContext, manager, managedService)
		if err != nil {
			return existingStatus, managedStatus, err
		}
		if existingStatus != "inactive" || managedStatus != "active" {
			return existingStatus, managedStatus, nil
		}
		if stableSince.IsZero() {
			stableSince = time.Now()
		}
		if time.Since(stableSince) >= 500*time.Millisecond {
			return existingStatus, managedStatus, nil
		}
		select {
		case <-stableContext.Done():
			return existingStatus, managedStatus, stableContext.Err()
		case <-ticker.C:
		}
	}
}

func verifyCoreMigrationCompletionState(ctx context.Context, existing, managed EngineSpec, managers ...*ServiceManager) error {
	manager := selectedServiceManager(managers...)
	if err := waitForCoreMigrationState(ctx, existing.Service, managed.Service, "inactive", "active", "disabled", "enabled", manager); err != nil {
		return err
	}
	if manager.Kind() == ServiceManagerOpenRC {
		if _, err := boundOpenRCServiceProcess(ctx, existing.Service); !errors.Is(err, errOpenRCServiceProcessUnbound) {
			if err == nil {
				return errors.New("existing OpenRC service still owns a supervised process after migration")
			}
			return fmt.Errorf("existing OpenRC service process state is not safely absent after migration: %w", err)
		}
	}
	return nil
}

// verifyCompletedCoreMigrationOwnership re-checks the durable ownership
// invariants of an already completed migration. Restarting the Agent must fail
// closed only when the retired legacy service can reclaim the core again: it is
// running once more, or it would start again on boot. The runtime state of the
// QAgent-managed unit is intentionally ignored — an operator stopping or
// disabling the managed core, or the unit failing, is an ordinary condition
// that the regular core actions handle; treating it as an unsafe migration
// would disable every task for that engine until an administrator edits the
// node by hand.
//
// The retired service itself must be provably unable to take over again, but
// only states that really can start it are rejected: `enabled` and
// `enabled-runtime`. `disabled`, `static`, `indirect` and `masked` units cannot
// start on boot, and a unit that an administrator or a package removal deleted
// (`not-found`) is gone entirely. A `failed` retired service is still rejected
// because it proves something tried to start it after the migration.
//
// The strict transition check stays in verifyCoreMigrationCompletionState,
// which runs while the migration is still being finalized and therefore must
// observe the managed service up and running.
func verifyCompletedCoreMigrationOwnership(ctx context.Context, existing EngineSpec, managers ...*ServiceManager) error {
	manager := selectedServiceManager(managers...)
	status, err := serviceStatusWithManager(ctx, manager, existing.Service)
	if err != nil {
		return fmt.Errorf("query existing service state: %w", err)
	}
	if status != "inactive" {
		return fmt.Errorf("existing %s service is %s after migration completion", existing.Service, status)
	}
	if manager.Kind() == ServiceManagerOpenRC {
		enableState, err := openRCServiceEnableState(ctx, existing.Service)
		if err != nil {
			return err
		}
		if enableState != "disabled" {
			return fmt.Errorf("existing %s service is %s after migration completion", existing.Service, enableState)
		}
		if _, err := boundOpenRCServiceProcess(ctx, existing.Service); !errors.Is(err, errOpenRCServiceProcessUnbound) {
			if err == nil {
				return errors.New("existing OpenRC service still owns a supervised process after migration")
			}
			return fmt.Errorf("existing OpenRC service process state is not safely absent after migration: %w", err)
		}
		return nil
	}
	enableState, err := retiredSystemdServiceEnableState(ctx, existing.Service, manager)
	if err != nil {
		return err
	}
	switch enableState {
	case "enabled", "enabled-runtime":
		return fmt.Errorf("existing %s service is %s after migration completion", existing.Service, enableState)
	case "disabled", "static", "indirect", "masked", "not-found":
		return nil
	default:
		return fmt.Errorf("existing %s service has an unexpected enable state %q after migration completion", existing.Service, enableState)
	}
}

// retiredSystemdServiceEnableState reads the raw `systemctl is-enabled` answer
// for a retired service. Unlike serviceEnableState it also accepts the states
// that prove the unit cannot start again but are outside the migration
// transition set: `masked` and the `not-found` answer for a unit that no longer
// exists. An empty or failed query stays fail closed.
func retiredSystemdServiceEnableState(ctx context.Context, service string, managers ...*ServiceManager) (string, error) {
	manager := selectedServiceManager(managers...)
	if !safeServiceName(service) {
		return "", errors.New("configured service name is unsafe")
	}
	output, err := run(ctx, manager.enableHelper(), "is-enabled", service)
	state := strings.TrimSpace(output)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if state == "" {
		return "", fmt.Errorf("query whether systemd service %s is enabled: %w", service, err)
	}
	return state, nil
}

func waitForCoreMigrationState(ctx context.Context, existingService, managedService, expectedExistingStatus, expectedManagedStatus, expectedExistingEnableState, expectedManagedEnableState string, managers ...*ServiceManager) error {
	manager := selectedServiceManager(managers...)
	stableContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var stableSince time.Time
	for {
		existingStatus, err := serviceStatusWithManager(stableContext, manager, existingService)
		if err != nil {
			return fmt.Errorf("query existing service state: %w", err)
		}
		managedStatus, err := serviceStatusWithManager(stableContext, manager, managedService)
		if err != nil {
			return fmt.Errorf("query managed service state: %w", err)
		}
		existingEnableState, err := serviceEnableState(stableContext, existingService, manager)
		if err != nil {
			return err
		}
		managedEnableState, err := serviceEnableState(stableContext, managedService, manager)
		if err != nil {
			return err
		}
		if existingStatus != expectedExistingStatus || managedStatus != expectedManagedStatus ||
			existingEnableState != expectedExistingEnableState || managedEnableState != expectedManagedEnableState {
			return fmt.Errorf("service state mismatch: existing status=%s enable=%s, managed status=%s enable=%s; expected existing status=%s enable=%s, managed status=%s enable=%s",
				existingStatus, existingEnableState, managedStatus, managedEnableState,
				expectedExistingStatus, expectedExistingEnableState, expectedManagedStatus, expectedManagedEnableState)
		}
		if stableSince.IsZero() {
			stableSince = time.Now()
		}
		if time.Since(stableSince) >= 500*time.Millisecond {
			return nil
		}
		select {
		case <-stableContext.Done():
			return stableContext.Err()
		case <-ticker.C:
		}
	}
}
