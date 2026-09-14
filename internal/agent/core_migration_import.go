package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func (e *Executor) importExistingConfig(ctx context.Context, engine core.Engine, managed, existing EngineSpec, content string) (string, error) {
	e.migrationMu.Lock()
	defer e.migrationMu.Unlock()
	manager := e.serviceManager()

	e.specsMu.RLock()
	currentExisting, stillPending := e.ExistingSpecs[engine]
	e.specsMu.RUnlock()
	if !stillPending || currentExisting != existing {
		return "", fmt.Errorf("%s existing service migration is no longer pending", engine)
	}
	if err := verifyExistingServiceMapping(ctx, engine, existing, manager); err != nil {
		return "", err
	}
	managedInitialState, err := managedServiceInitialState(ctx, engine, managed, manager)
	if err != nil {
		return "", err
	}
	currentContent, err := e.readExistingConfig(ctx, engine, managed, existing)
	if err != nil {
		return "", err
	}
	if currentContent != content {
		return "", fmt.Errorf("existing %s configuration sources changed after the saved snapshot; both services were left unchanged", engine)
	}
	resourcePlan, err := prepareOUSBSingBoxImport(ctx, manager, existing, content)
	if engine == core.EngineShadowsocksRust {
		var original string
		original, err = readConfigurationFile(existing.ConfigPath)
		if err == nil {
			resourcePlan, err = prepareSSRustImport(existing, original)
		}
		if err == nil && resourcePlan.managedContent != content {
			err = errors.New("SS Rust configuration or ACL changed after the saved snapshot")
		}
	}
	if err != nil {
		return "", fmt.Errorf("prepare protected resource import: %w", err)
	}
	managedContent := resourcePlan.managedContent
	existingEnableState, err := serviceEnableState(ctx, existing.Service, manager)
	if err != nil {
		return "", err
	}
	managedEnableState, err := serviceEnableState(ctx, managed.Service, manager)
	if err != nil {
		return "", err
	}
	if !migrationEnableStatesSupported(existingEnableState, managedEnableState) {
		return "", fmt.Errorf("%s enable states cannot be migrated safely: existing %s is %s and managed %s is %s; both services were left unchanged", manager.Kind(), existing.Service, existingEnableState, managed.Service, managedEnableState)
	}

	validationSpec := managed
	invocationSpec, cleanupInvocation, err := e.existingCoreInvocationSpec(engine, managed, existing)
	if err != nil {
		return "", fmt.Errorf("prepare existing %s binary for protected import validation: %w", engine, err)
	}
	defer cleanupInvocation()
	validationSpec.Binary = invocationSpec.Binary
	validationSpec.commandEnv = invocationSpec.commandEnv
	if _, err := e.validateImportedSnapshot(ctx, engine, validationSpec, content); err != nil {
		return "", fmt.Errorf("existing %s configuration is not safe for managed deployment: %w", engine, err)
	}
	configDigest := coreMigrationConfigDigest(managedContent)
	requestConfigDigest := coreMigrationConfigDigest(content)
	sourceDigest := coreMigrationSourceDigest(existing)
	migrationRecord, err := prepareCoreMigrationFileRollback(
		e.MigrationMarkerPrefix,
		engine,
		existing,
		managed,
		coreMigrationRecord{
			State: coreMigrationInProgress, ConfigDigest: configDigest, RequestConfigDigest: requestConfigDigest, SourceDigest: sourceDigest,
			ExistingEnableState: existingEnableState, ManagedEnableState: managedEnableState,
			ManagedInitialState: managedInitialState,
			AuxiliaryService:    resourcePlan.auxiliary.name, AuxiliaryEnableState: resourcePlan.auxiliary.enableState,
			AuxiliaryInitialState: resourcePlan.auxiliary.initialState,
		},
	)
	if err != nil {
		return "", fmt.Errorf("persist migration intent before staging managed files: %w", err)
	}
	if err := verifyCoreMigrationOriginalFiles(managed, migrationRecord); err != nil {
		return "", errors.Join(err, abortPreparedCoreMigration(e.MigrationMarkerPrefix, engine))
	}
	rollbackMigration := func(cause error) (string, error) {
		rollbackContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rollbackErr := restoreInterruptedCoreMigration(
			rollbackContext, e.MigrationMarkerPrefix, engine, existing, managed, migrationRecord,
			manager,
		)
		var resourceErr error
		// An incomplete rollback can still leave the managed configuration
		// referencing these resources. Keep them for restart reconciliation.
		if rollbackErr == nil {
			resourceErr = resourcePlan.cleanup()
		}
		if rollbackErr != nil || resourceErr != nil {
			return "migration failed and rollback was incomplete", fmt.Errorf("%v; rollback: %w", cause, errors.Join(rollbackErr, resourceErr))
		}
		return "migration failed; original configuration, binary, and service were restored", cause
	}
	if managedInitialState == "active" {
		// Temporarily remove the unit from the enablement target before stopping
		// it. This prevents systemd/OpenRC supervision from immediately
		// respawning the managed service during the migration critical section;
		// the recorded enable state is restored while it remains stopped.
		if err := disableServiceCompletely(ctx, managed.Service, manager); err != nil {
			return rollbackMigration(fmt.Errorf("temporarily disable QAgent %s service before migration: %w", engine, err))
		}
		if _, err := serviceCommandAndVerifyWithManager(ctx, manager, managed.Service, core.ActionStop); err != nil {
			return rollbackMigration(fmt.Errorf("stop QAgent %s service before migration: %w", engine, err))
		}
		if err := waitForSingleMigrationServiceStable(ctx, managed.Service, "inactive", manager); err != nil {
			return rollbackMigration(fmt.Errorf("confirm stopped QAgent %s service before migration: %w", engine, err))
		}
		if err := restoreServiceEnableState(ctx, managed.Service, migrationRecord.ManagedEnableState, manager); err != nil {
			return rollbackMigration(fmt.Errorf("restore QAgent %s enable state before migration: %w", engine, err))
		}
	}

	if _, err := copyExistingCoreBinary(existing.Binary, managed.Binary); err != nil {
		return rollbackMigration(fmt.Errorf("copy existing %s binary into the QAgent namespace: %w", engine, err))
	}
	if engine == core.EngineXray {
		if err := stageExistingXrayMigrationAssets(existing, managed, migrationRecord); err != nil {
			return rollbackMigration(fmt.Errorf("copy existing Xray assets into the QAgent namespace: %w", err))
		}
	}
	if resourcePlan.active() {
		identity, identityErr := managedCoreServiceIdentity()
		if identityErr != nil {
			return rollbackMigration(identityErr)
		}
		if err := resourcePlan.stage(identity); err != nil {
			return rollbackMigration(fmt.Errorf("copy protected resources into the QAgent namespace: %w", err))
		}
	}
	if _, err := e.validateImportedSnapshot(ctx, engine, managed, managedContent); err != nil {
		return rollbackMigration(fmt.Errorf("copied %s binary rejected the configuration: %w", engine, err))
	}

	if _, err := atomicDeployManagedConfiguration(engine, managed, manager, managedContent); err != nil {
		return rollbackMigration(err)
	}
	if err := verifyCoreMigrationStagedFiles(managed, migrationRecord); err != nil {
		return rollbackMigration(err)
	}
	currentExistingEnableState, err := serviceEnableState(ctx, existing.Service, manager)
	if err != nil {
		return rollbackMigration(err)
	}
	currentManagedEnableState, err := serviceEnableState(ctx, managed.Service, manager)
	if err != nil {
		return rollbackMigration(err)
	}
	if currentExistingEnableState != existingEnableState || currentManagedEnableState != managedEnableState {
		return rollbackMigration(fmt.Errorf("%s enable states changed during migration preparation: existing %s changed from %s to %s and managed %s changed from %s to %s; both services were left unchanged", manager.Kind(), existing.Service, existingEnableState, currentExistingEnableState, managed.Service, managedEnableState, currentManagedEnableState))
	}
	if err := verifyExistingServiceMapping(ctx, engine, existing, manager); err != nil {
		return rollbackMigration(err)
	}
	currentContent, err = e.readExistingConfig(ctx, engine, managed, existing)
	if err != nil {
		return rollbackMigration(err)
	}
	if currentContent != content {
		return rollbackMigration(fmt.Errorf("existing %s configuration sources changed during migration preparation; both services were left unchanged", engine))
	}
	if err := requireManagedServiceSafeInactive(ctx, engine, managed, manager); err != nil {
		return rollbackMigration(err)
	}
	if err := ensureManagedCoreServiceCapabilities(ctx, engine, managed, manager); err != nil {
		return rollbackMigration(err)
	}
	if err := requireManagedServiceSafeInactive(ctx, engine, managed, manager); err != nil {
		return rollbackMigration(err)
	}

	var stoppedOpenRCProcess *openRCServiceProcessIdentity
	if manager.Kind() == ServiceManagerOpenRC {
		identity, err := verifyOpenRCExistingServiceProcess(ctx, engine, existing)
		if err != nil {
			return rollbackMigration(fmt.Errorf("revalidate service-bound %s OpenRC process immediately before stop: %w", engine, err))
		}
		stoppedOpenRCProcess = &identity
	}
	if _, err := serviceCommandAndVerifyWithManager(ctx, manager, existing.Service, core.ActionStop); err != nil {
		return rollbackMigration(fmt.Errorf("stop existing %s service: %w", engine, err))
	}
	if stoppedOpenRCProcess != nil {
		if err := waitForOpenRCServiceProcessExit(ctx, *stoppedOpenRCProcess); err != nil {
			return rollbackMigration(fmt.Errorf("confirm stopped %s OpenRC service process exited: %w", engine, err))
		}
	}
	if err := stopAndDisableMigrationAuxiliary(ctx, migrationRecord, manager); err != nil {
		return rollbackMigration(fmt.Errorf("stop and disable OU-SB firewall helper: %w", err))
	}
	if _, err := serviceCommandAndVerifyWithManager(ctx, manager, managed.Service, core.ActionStart); err != nil {
		return rollbackMigration(fmt.Errorf("start QAgent %s service: %w", engine, err))
	}
	if err := setServiceEnabled(ctx, managed.Service, true, manager); err != nil {
		return rollbackMigration(err)
	}
	if err := disableServiceCompletely(ctx, existing.Service, manager); err != nil {
		return rollbackMigration(err)
	}
	if err := verifyCoreMigrationCompletionState(ctx, existing, managed, manager); err != nil {
		return rollbackMigration(fmt.Errorf("verify service state before migration completion: %w", err))
	}
	if err := writeCoreMigrationMarkerWithRequestDigest(e.MigrationMarkerPrefix, engine, coreMigrationComplete, configDigest, requestConfigDigest, sourceDigest, existingEnableState, managedEnableState); err != nil {
		return rollbackMigration(fmt.Errorf("persist completed migration: %w", err))
	}
	_ = cleanupCoreMigrationBackups(e.MigrationMarkerPrefix, engine)

	e.specsMu.Lock()
	if e.completedMigrations == nil {
		e.completedMigrations = make(map[core.Engine]completedCoreMigration)
	}
	e.completedMigrations[engine] = completedCoreMigration{
		Existing: existing, Managed: managed, SourceDigest: sourceDigest,
	}
	delete(e.ExistingSpecs, engine)
	e.specsMu.Unlock()
	result := fmt.Sprintf("imported %s configuration; stopped and disabled %s; started and enabled %s", engine, existing.Service, managed.Service)
	if resourcePlan.active() {
		result += "; copied protected external resources into the QAgent state directory"
		if migrationRecord.AuxiliaryService != "" {
			result += "; stopped and disabled " + migrationRecord.AuxiliaryService
		}
	}
	if engine == core.EngineShadowsocksRust {
		result += "; script-owned inbound firewall rules and helper were left unchanged; manage them separately before changing ports"
	}
	return result, nil
}
