package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

var (
	openRCProcRoot             = "/proc"
	openRCStateRoot            = "/run/openrc"
	openRCSupervisorRoot       = "/run"
	openRCRunlevelsRoot        = "/etc/runlevels"
	openRCInitRoot             = "/etc/init.d"
	openRCSupervisorExecutable = openRCHelperExecutable("supervise-daemon", "/sbin/supervise-daemon")
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
			completionErr := verifyCoreMigrationCompletionState(loadContext, existing, managedSpecs[engine], e.serviceManager())
			e.specsMu.Lock()
			if completionErr == nil {
				_ = cleanupCoreMigrationBackups(e.MigrationMarkerPrefix, engine)
				delete(e.ExistingSpecs, engine)
				delete(e.ExistingDiscoveryIssues, engine)
			} else {
				if e.ExistingDiscoveryIssues == nil {
					e.ExistingDiscoveryIssues = make(map[core.Engine]string)
				}
				issue := strings.ToValidUTF8(fmt.Sprintf("已完成的 %s 服务迁移状态不再安全，相关内核任务已禁用：%v", engine, completionErr), "�")
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
		if err := disableServiceCompletely(ctx, existing.Service, manager); err != nil {
			return rollback(err)
		}
		if err := verifyCoreMigrationCompletionState(ctx, existing, managed[engine], manager); err != nil {
			return rollback(fmt.Errorf("verify service state before migration completion: %w", err))
		}
		if err := writeCoreMigrationMarker(e.MigrationMarkerPrefix, engine, coreMigrationComplete, migrationRecord.ConfigDigest, migrationRecord.SourceDigest, migrationRecord.ExistingEnableState, migrationRecord.ManagedEnableState); err != nil {
			return rollback(err)
		}
		_ = cleanupCoreMigrationBackups(e.MigrationMarkerPrefix, engine)
		e.specsMu.Lock()
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

type coreMigrationState string

type coreMigrationRecord struct {
	State               coreMigrationState
	ConfigDigest        string
	SourceDigest        string
	ExistingEnableState string
	ManagedEnableState  string
	HasFileRollback     bool
	BinaryBackupDigest  string
	ConfigBackupDigest  string
	StagedBinaryDigest  string
}

const (
	coreMigrationNone          coreMigrationState = ""
	coreMigrationInProgress    coreMigrationState = "migrating"
	coreMigrationComplete      coreMigrationState = "migrated"
	coreMigrationPreparedToken                    = "migrating-v2"
	coreMigrationMissingBackup                    = "-"
)

func coreMigrationMarked(prefix string, engine core.Engine) (bool, error) {
	record, err := readCoreMigrationRecord(prefix, engine)
	return record.State == coreMigrationComplete, err
}

func restoreInterruptedCoreMigration(ctx context.Context, prefix string, engine core.Engine, existing, managed EngineSpec, record coreMigrationRecord, managers ...*ServiceManager) error {
	if !record.HasFileRollback {
		return errors.New("legacy core migration marker has no durable managed-file rollback information")
	}
	return restoreCoreMigrationServicesAndFiles(ctx, prefix, engine, existing, managed, record, true, managers...)
}

func restoreLegacyInterruptedCoreMigration(ctx context.Context, prefix string, engine core.Engine, existing, managed EngineSpec, record coreMigrationRecord, managers ...*ServiceManager) error {
	return restoreCoreMigrationServicesAndFiles(ctx, prefix, engine, existing, managed, record, false, managers...)
}

func restoreCoreMigrationServicesAndFiles(ctx context.Context, prefix string, engine core.Engine, existing, managed EngineSpec, record coreMigrationRecord, restoreFiles bool, managers ...*ServiceManager) error {
	manager := selectedServiceManager(managers...)
	if _, err := serviceCommandAndVerifyWithManager(ctx, manager, managed.Service, core.ActionStop); err != nil {
		return fmt.Errorf("restore existing %s service after interrupted migration: stop managed service: %w", engine, err)
	}
	if err := waitForSingleMigrationServiceStable(ctx, managed.Service, "inactive", manager); err != nil {
		return fmt.Errorf("restore existing %s service after interrupted migration: verify managed service is stopped: %w", engine, err)
	}
	if err := restoreServiceEnableState(ctx, managed.Service, record.ManagedEnableState, manager); err != nil {
		return fmt.Errorf("restore existing %s service after interrupted migration: %w", engine, err)
	}
	if err := restoreServiceEnableState(ctx, existing.Service, record.ExistingEnableState, manager); err != nil {
		return fmt.Errorf("restore existing %s service after interrupted migration: %w", engine, err)
	}
	if err := waitForSingleMigrationServiceStable(ctx, managed.Service, "inactive", manager); err != nil {
		return fmt.Errorf("restore existing %s service after interrupted migration: managed service restarted before original recovery: %w", engine, err)
	}
	if restoreFiles {
		if err := restoreCoreMigrationFiles(prefix, engine, managed, record); err != nil {
			return fmt.Errorf("restore existing %s service after interrupted migration: restore managed files: %w", engine, err)
		}
	}
	if _, err := serviceCommandAndVerifyWithManager(ctx, manager, existing.Service, core.ActionStart); err != nil {
		return fmt.Errorf("restore existing %s service after interrupted migration: start original service: %w", engine, err)
	}
	if err := waitForCoreMigrationState(ctx, existing.Service, managed.Service, "active", "inactive", record.ExistingEnableState, record.ManagedEnableState, manager); err != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, stopErr := serviceCommandAndVerifyWithManager(cleanupContext, manager, managed.Service, core.ActionStop)
		stopErr = errors.Join(stopErr, waitForSingleMigrationServiceStable(cleanupContext, managed.Service, "inactive", manager))
		return fmt.Errorf("restore existing %s service after interrupted migration: final safety check failed: %w", engine, errors.Join(err, stopErr))
	}
	if restoreFiles {
		if err := removeCoreMigrationMarker(prefix, engine); err != nil {
			return fmt.Errorf("restore existing %s service after interrupted migration: %w", engine, err)
		}
		if err := cleanupCoreMigrationBackups(prefix, engine); err != nil {
			return fmt.Errorf("clean restored %s migration backups: %w", engine, err)
		}
	}
	return nil
}

func waitForSingleMigrationServiceStable(ctx context.Context, service, expected string, managers ...*ServiceManager) error {
	manager := selectedServiceManager(managers...)
	stableContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status, err := waitForServiceState(stableContext, expected, 500*time.Millisecond, 100*time.Millisecond, func(probeContext context.Context) (string, error) {
		return serviceStatusWithManager(probeContext, manager, service)
	})
	if err != nil {
		return err
	}
	if status != expected {
		return fmt.Errorf("service %s is %s, expected %s", service, status, expected)
	}
	return nil
}

func readCoreMigrationRecord(prefix string, engine core.Engine) (coreMigrationRecord, error) {
	if prefix == "" {
		return coreMigrationRecord{}, nil
	}
	path := coreMigrationMarkerPath(prefix, engine)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return coreMigrationRecord{}, nil
	}
	if err != nil {
		return coreMigrationRecord{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return coreMigrationRecord{}, errors.New("core migration marker is not a protected regular file")
	}
	if err := validateOwner(info, "core migration marker"); err != nil {
		return coreMigrationRecord{}, err
	}
	directoryInfo, err := os.Lstat(filepath.Dir(path))
	if err != nil || directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() || directoryInfo.Mode().Perm()&0o022 != 0 {
		return coreMigrationRecord{}, errors.New("core migration state directory is unsafe")
	}
	if err := validateOwner(directoryInfo, "core migration state directory"); err != nil {
		return coreMigrationRecord{}, err
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return coreMigrationRecord{}, err
	}
	fields := strings.Fields(string(contents))
	if len(fields) != 5 && len(fields) != 8 {
		return coreMigrationRecord{}, errors.New("core migration marker is invalid")
	}
	state := coreMigrationState(fields[0])
	hasFileRollback := false
	if fields[0] == coreMigrationPreparedToken {
		state = coreMigrationInProgress
		hasFileRollback = true
	}
	if state != coreMigrationInProgress && state != coreMigrationComplete {
		return coreMigrationRecord{}, errors.New("core migration marker is invalid")
	}
	if hasFileRollback != (len(fields) == 8) || state == coreMigrationComplete && len(fields) != 5 {
		return coreMigrationRecord{}, errors.New("core migration marker version is invalid")
	}
	if decoded, err := hex.DecodeString(fields[1]); err != nil || len(decoded) != sha256.Size {
		return coreMigrationRecord{}, errors.New("core migration configuration digest is invalid")
	}
	if decoded, err := hex.DecodeString(fields[2]); err != nil || len(decoded) != sha256.Size {
		return coreMigrationRecord{}, errors.New("core migration source digest is invalid")
	}
	if !validServiceEnableState(fields[3]) || !validServiceEnableState(fields[4]) {
		return coreMigrationRecord{}, errors.New("core migration enable state is invalid")
	}
	record := coreMigrationRecord{
		State: state, ConfigDigest: fields[1], SourceDigest: fields[2],
		ExistingEnableState: fields[3], ManagedEnableState: fields[4],
		HasFileRollback: hasFileRollback,
	}
	if hasFileRollback {
		for _, digest := range fields[5:8] {
			if digest == coreMigrationMissingBackup {
				continue
			}
			if decoded, err := hex.DecodeString(digest); err != nil || len(decoded) != sha256.Size {
				return coreMigrationRecord{}, errors.New("core migration file backup digest is invalid")
			}
		}
		if fields[7] == coreMigrationMissingBackup {
			return coreMigrationRecord{}, errors.New("core migration staged binary digest is invalid")
		}
		record.BinaryBackupDigest = fields[5]
		record.ConfigBackupDigest = fields[6]
		record.StagedBinaryDigest = fields[7]
	}
	return record, nil
}

func coreMigrationMarkerPath(prefix string, engine core.Engine) string {
	return prefix + "-" + string(engine)
}

func completedCoreMigrationMatches(prefix string, engine core.Engine, content string) (bool, error) {
	record, err := readCoreMigrationRecord(prefix, engine)
	if err != nil || record.State != coreMigrationComplete {
		return false, err
	}
	return record.ConfigDigest == coreMigrationConfigDigest(content), nil
}

func coreMigrationConfigDigest(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func coreMigrationSourceDigest(existing EngineSpec) string {
	source := existing.Binary + "\x00" + existing.ConfigPath + "\x00" + existing.Service
	if existing.ConfigDirectory != "" || existingServiceBinary(existing) != existing.Binary {
		source = existing.Binary + "\x00" + existing.ConfigPath + "\x00" + existing.ConfigDirectory + "\x00" + existingServiceBinary(existing) + "\x00" + existing.Service
	}
	digest := sha256.Sum256([]byte(source))
	return hex.EncodeToString(digest[:])
}

type coreMigrationBackup struct {
	file   *os.File
	info   os.FileInfo
	exists bool
}

type coreMigrationRestoreAction int

const (
	coreMigrationRestoreNone coreMigrationRestoreAction = iota
	coreMigrationRestoreCopy
	coreMigrationRestoreRemove
)

func prepareCoreMigrationFileRollback(prefix string, engine core.Engine, existing, managed EngineSpec, record coreMigrationRecord) (coreMigrationRecord, error) {
	current, err := readCoreMigrationRecord(prefix, engine)
	if err != nil {
		return coreMigrationRecord{}, err
	}
	if current.State != coreMigrationNone {
		return coreMigrationRecord{}, errors.New("a core migration marker already exists")
	}
	root, err := openCoreMigrationStateRoot(prefix)
	if err != nil {
		return coreMigrationRecord{}, err
	}
	defer root.Close()
	for _, kind := range []string{"binary", "config"} {
		if err := root.Remove(coreMigrationBackupName(prefix, engine, kind)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return coreMigrationRecord{}, fmt.Errorf("remove stale core migration %s backup: %w", kind, err)
		}
	}
	if err := syncRootDirectory(root); err != nil {
		return coreMigrationRecord{}, err
	}
	binaryBackupDigest, err := snapshotCoreMigrationFile(root, prefix, engine, "binary", managed.Binary, maxReleaseAssetSize)
	if err != nil {
		return coreMigrationRecord{}, fmt.Errorf("snapshot managed core binary: %w", err)
	}
	configBackupDigest, err := snapshotCoreMigrationFile(root, prefix, engine, "config", managed.ConfigPath, core.MaxConfigBytes)
	if err != nil {
		_ = cleanupCoreMigrationBackups(prefix, engine)
		return coreMigrationRecord{}, fmt.Errorf("snapshot managed configuration: %w", err)
	}
	stagedBinaryDigest, exists, err := protectedCoreMigrationFileDigest(existing.Binary, maxReleaseAssetSize)
	if err != nil {
		_ = cleanupCoreMigrationBackups(prefix, engine)
		return coreMigrationRecord{}, fmt.Errorf("digest existing core binary: %w", err)
	}
	if !exists {
		_ = cleanupCoreMigrationBackups(prefix, engine)
		return coreMigrationRecord{}, errors.New("existing core binary disappeared while preparing migration")
	}
	record.HasFileRollback = true
	record.BinaryBackupDigest = binaryBackupDigest
	record.ConfigBackupDigest = configBackupDigest
	record.StagedBinaryDigest = stagedBinaryDigest
	if err := writePreparedCoreMigrationMarker(prefix, engine, record); err != nil {
		_ = cleanupCoreMigrationBackups(prefix, engine)
		return coreMigrationRecord{}, err
	}
	return record, nil
}

func snapshotCoreMigrationFile(stateRoot *os.Root, prefix string, engine core.Engine, kind, sourcePath string, limit int64) (string, error) {
	info, err := os.Lstat(sourcePath)
	if errors.Is(err, os.ErrNotExist) {
		return coreMigrationMissingBackup, nil
	}
	if err != nil {
		return "", err
	}
	input, openedInfo, err := openProtectedCoreMigrationFile(sourcePath, info, limit)
	if err != nil {
		return "", err
	}
	defer input.Close()
	tempSuffix, err := randomSuffix(8)
	if err != nil {
		return "", err
	}
	tempName := ".qagent-core-migration-backup-" + tempSuffix
	defer stateRoot.Remove(tempName)
	output, err := stateRoot.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(output, hasher), io.LimitReader(input, limit+1))
	if copyErr == nil && written > limit {
		copyErr = errors.New("core migration backup exceeds the supported limit")
	}
	if copyErr == nil {
		copyErr = applyFileMetadata(output, metadataFromFileInfo(openedInfo))
	}
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	backupName := coreMigrationBackupName(prefix, engine, kind)
	if err := stateRoot.Rename(tempName, backupName); err != nil {
		return "", err
	}
	if err := syncRootDirectory(stateRoot); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func restoreCoreMigrationFiles(prefix string, engine core.Engine, managed EngineSpec, record coreMigrationRecord) error {
	if !record.HasFileRollback {
		return errors.New("core migration marker has no durable file rollback information")
	}
	binaryBackup, err := openCoreMigrationBackup(prefix, engine, "binary", record.BinaryBackupDigest, maxReleaseAssetSize)
	if err != nil {
		return fmt.Errorf("validate managed binary rollback backup: %w", err)
	}
	if binaryBackup.file != nil {
		defer binaryBackup.file.Close()
	}
	configBackup, err := openCoreMigrationBackup(prefix, engine, "config", record.ConfigBackupDigest, core.MaxConfigBytes)
	if err != nil {
		return fmt.Errorf("validate managed configuration rollback backup: %w", err)
	}
	if configBackup.file != nil {
		defer configBackup.file.Close()
	}
	binaryAction, err := planCoreMigrationFileRestore(managed.Binary, record.BinaryBackupDigest, record.StagedBinaryDigest, maxReleaseAssetSize)
	if err != nil {
		return fmt.Errorf("plan managed binary rollback: %w", err)
	}
	configAction, err := planCoreMigrationFileRestore(managed.ConfigPath, record.ConfigBackupDigest, record.ConfigDigest, core.MaxConfigBytes)
	if err != nil {
		return fmt.Errorf("plan managed configuration rollback: %w", err)
	}
	if err := applyCoreMigrationFileRestore(managed.Binary, binaryBackup, binaryAction); err != nil {
		return fmt.Errorf("restore managed binary: %w", err)
	}
	if err := applyCoreMigrationFileRestore(managed.ConfigPath, configBackup, configAction); err != nil {
		return fmt.Errorf("restore managed configuration: %w", err)
	}
	return nil
}

func verifyCoreMigrationStagedFiles(managed EngineSpec, record coreMigrationRecord) error {
	binaryDigest, binaryExists, err := protectedCoreMigrationFileDigest(managed.Binary, maxReleaseAssetSize)
	if err != nil || !binaryExists || binaryDigest != record.StagedBinaryDigest {
		return errors.New("managed binary does not match the recorded staged binary")
	}
	configDigest, configExists, err := protectedCoreMigrationFileDigest(managed.ConfigPath, core.MaxConfigBytes)
	if err != nil || !configExists || configDigest != record.ConfigDigest {
		return errors.New("managed configuration does not match the recorded staged configuration")
	}
	return nil
}

func verifyCoreMigrationOriginalFiles(managed EngineSpec, record coreMigrationRecord) error {
	for _, file := range []struct {
		path   string
		digest string
		limit  int64
		label  string
	}{
		{path: managed.Binary, digest: record.BinaryBackupDigest, limit: maxReleaseAssetSize, label: "binary"},
		{path: managed.ConfigPath, digest: record.ConfigBackupDigest, limit: core.MaxConfigBytes, label: "configuration"},
	} {
		currentDigest, exists, err := protectedCoreMigrationFileDigest(file.path, file.limit)
		if err != nil {
			return fmt.Errorf("query original managed %s: %w", file.label, err)
		}
		if file.digest == coreMigrationMissingBackup {
			if exists {
				return fmt.Errorf("originally absent managed %s appeared during migration preparation", file.label)
			}
			continue
		}
		if !exists || currentDigest != file.digest {
			return fmt.Errorf("original managed %s changed during migration preparation", file.label)
		}
	}
	return nil
}

func abortPreparedCoreMigration(prefix string, engine core.Engine) error {
	if err := removeCoreMigrationMarker(prefix, engine); err != nil {
		return err
	}
	return cleanupCoreMigrationBackups(prefix, engine)
}

func openCoreMigrationBackup(prefix string, engine core.Engine, kind, expectedDigest string, limit int64) (coreMigrationBackup, error) {
	path := coreMigrationBackupPath(prefix, engine, kind)
	if expectedDigest == coreMigrationMissingBackup {
		if _, err := os.Lstat(path); err == nil {
			return coreMigrationBackup{}, errors.New("unexpected backup exists for an originally absent file")
		} else if !errors.Is(err, os.ErrNotExist) {
			return coreMigrationBackup{}, err
		}
		return coreMigrationBackup{}, nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		return coreMigrationBackup{}, err
	}
	file, openedInfo, err := openProtectedCoreMigrationFile(path, info, limit)
	if err != nil {
		return coreMigrationBackup{}, err
	}
	digest, err := digestCoreMigrationFile(file, limit)
	if err != nil {
		file.Close()
		return coreMigrationBackup{}, err
	}
	if digest != expectedDigest {
		file.Close()
		return coreMigrationBackup{}, errors.New("core migration rollback backup digest mismatch")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return coreMigrationBackup{}, err
	}
	return coreMigrationBackup{file: file, info: openedInfo, exists: true}, nil
}

func planCoreMigrationFileRestore(destination, originalDigest, stagedDigest string, limit int64) (coreMigrationRestoreAction, error) {
	currentDigest, exists, err := protectedCoreMigrationFileDigest(destination, limit)
	if err != nil {
		return coreMigrationRestoreNone, err
	}
	if originalDigest == coreMigrationMissingBackup {
		if !exists {
			return coreMigrationRestoreNone, nil
		}
		if currentDigest == stagedDigest {
			return coreMigrationRestoreRemove, nil
		}
		return coreMigrationRestoreNone, errors.New("originally absent managed file changed outside the recorded migration")
	}
	if !exists {
		return coreMigrationRestoreNone, errors.New("managed file disappeared during migration")
	}
	if currentDigest == originalDigest {
		return coreMigrationRestoreNone, nil
	}
	if currentDigest == stagedDigest {
		return coreMigrationRestoreCopy, nil
	}
	return coreMigrationRestoreNone, errors.New("managed file content does not match either the original backup or staged migration")
}

func applyCoreMigrationFileRestore(destination string, backup coreMigrationBackup, action coreMigrationRestoreAction) error {
	if action == coreMigrationRestoreNone {
		return nil
	}
	directory := filepath.Dir(destination)
	if err := validateProtectedDirectoryChain(directory); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	destinationName := filepath.Base(destination)
	if action == coreMigrationRestoreRemove {
		if err := root.Remove(destinationName); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return syncRootDirectory(root)
	}
	if !backup.exists || backup.file == nil {
		return errors.New("core migration rollback backup is unavailable")
	}
	if _, err := backup.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	suffix, err := randomSuffix(8)
	if err != nil {
		return err
	}
	tempName := ".qagent-core-migration-restore-" + suffix
	defer root.Remove(tempName)
	output, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	copyErr := error(nil)
	if _, err := io.Copy(output, backup.file); err != nil {
		copyErr = err
	} else if err := applyFileMetadata(output, metadataFromFileInfo(backup.info)); err != nil {
		copyErr = err
	} else if err := output.Sync(); err != nil {
		copyErr = err
	}
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := root.Rename(tempName, destinationName); err != nil {
		return err
	}
	return syncRootDirectory(root)
}

func protectedCoreMigrationFileDigest(path string, limit int64) (string, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	file, _, err := openProtectedCoreMigrationFile(path, info, limit)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	digest, err := digestCoreMigrationFile(file, limit)
	return digest, true, err
}

func openProtectedCoreMigrationFile(path string, expected os.FileInfo, limit int64) (*os.File, os.FileInfo, error) {
	if !filepath.IsAbs(path) {
		return nil, nil, errors.New("core migration file path is not absolute")
	}
	if expected.Mode()&os.ModeSymlink != 0 || !expected.Mode().IsRegular() || expected.Mode().Perm()&0o022 != 0 {
		return nil, nil, errors.New("core migration file is not a protected regular file")
	}
	if expected.Size() < 0 || expected.Size() > limit {
		return nil, nil, errors.New("core migration file exceeds the supported limit")
	}
	if err := validateOwner(expected, "core migration file"); err != nil {
		return nil, nil, err
	}
	directory := filepath.Dir(path)
	if err := validateProtectedDirectoryChain(directory); err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, nil, err
	}
	file, err := root.Open(filepath.Base(path))
	root.Close()
	if err != nil {
		return nil, nil, err
	}
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(expected, openedInfo) || !openedInfo.Mode().IsRegular() ||
		openedInfo.Size() != expected.Size() || metadataFromFileInfo(openedInfo) != metadataFromFileInfo(expected) {
		file.Close()
		return nil, nil, errors.New("core migration file changed while it was being opened")
	}
	return file, openedInfo, nil
}

func digestCoreMigrationFile(file *os.File, limit int64) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hasher := sha256.New()
	written, err := io.Copy(hasher, io.LimitReader(file, limit+1))
	if err != nil {
		return "", err
	}
	if written > limit {
		return "", errors.New("core migration file exceeds the supported limit")
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func openCoreMigrationStateRoot(prefix string) (*os.Root, error) {
	if prefix == "" {
		return nil, errors.New("core migration marker path is not configured")
	}
	directory := filepath.Dir(prefix)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	if err := validateProtectedDirectoryChain(directory); err != nil {
		return nil, fmt.Errorf("core migration state directory is unsafe: %w", err)
	}
	return os.OpenRoot(directory)
}

func coreMigrationBackupName(prefix string, engine core.Engine, kind string) string {
	return filepath.Base(coreMigrationMarkerPath(prefix, engine)) + "." + kind + "-backup"
}

func coreMigrationBackupPath(prefix string, engine core.Engine, kind string) string {
	return filepath.Join(filepath.Dir(prefix), coreMigrationBackupName(prefix, engine, kind))
}

func cleanupCoreMigrationBackups(prefix string, engine core.Engine) error {
	root, err := openCoreMigrationStateRoot(prefix)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, kind := range []string{"binary", "config"} {
		if err := root.Remove(coreMigrationBackupName(prefix, engine, kind)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return syncRootDirectory(root)
}

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
	if err := requireManagedServiceSafeInactive(ctx, engine, managed, manager); err != nil {
		return "", err
	}
	currentContent, err := e.readExistingConfig(ctx, engine, managed, existing)
	if err != nil {
		return "", err
	}
	if currentContent != content {
		return "", fmt.Errorf("existing %s configuration sources changed after the saved snapshot; both services were left unchanged", engine)
	}
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
	validationSpec.Binary = existing.Binary
	if _, err := e.validate(ctx, engine, validationSpec, content); err != nil {
		return "", fmt.Errorf("existing %s configuration is not safe for managed deployment: %w", engine, err)
	}
	if err := requireManagedServiceSafeInactive(ctx, engine, managed, manager); err != nil {
		return "", err
	}

	configDigest := coreMigrationConfigDigest(content)
	sourceDigest := coreMigrationSourceDigest(existing)
	migrationRecord, err := prepareCoreMigrationFileRollback(
		e.MigrationMarkerPrefix,
		engine,
		existing,
		managed,
		coreMigrationRecord{
			State: coreMigrationInProgress, ConfigDigest: configDigest, SourceDigest: sourceDigest,
			ExistingEnableState: existingEnableState, ManagedEnableState: managedEnableState,
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
		if rollbackErr != nil {
			return "migration failed and rollback was incomplete", fmt.Errorf("%v; rollback: %w", cause, rollbackErr)
		}
		return "migration failed; original configuration, binary, and service were restored", cause
	}

	if _, err := copyExistingCoreBinary(existing.Binary, managed.Binary); err != nil {
		return rollbackMigration(fmt.Errorf("copy existing %s binary into the QAgent namespace: %w", engine, err))
	}
	if _, err := e.validate(ctx, engine, managed, content); err != nil {
		return rollbackMigration(fmt.Errorf("copied %s binary rejected the configuration: %w", engine, err))
	}

	if _, err := atomicDeploy(managed.ConfigPath, content); err != nil {
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
	if err := writeCoreMigrationMarker(e.MigrationMarkerPrefix, engine, coreMigrationComplete, configDigest, sourceDigest, existingEnableState, managedEnableState); err != nil {
		return rollbackMigration(fmt.Errorf("persist completed migration: %w", err))
	}
	_ = cleanupCoreMigrationBackups(e.MigrationMarkerPrefix, engine)

	e.specsMu.Lock()
	delete(e.ExistingSpecs, engine)
	e.specsMu.Unlock()
	return fmt.Sprintf("imported %s configuration; stopped and disabled %s; started and enabled %s", engine, existing.Service, managed.Service), nil
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

func copyExistingCoreBinary(source, destination string) (string, error) {
	if source == destination {
		return "", errors.New("existing and managed core binary paths must differ")
	}
	if err := validatePrivilegedExecutable(source); err != nil {
		return "", err
	}
	if err := validateCoreInstallDestination(destination); err != nil {
		return "", err
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return "", err
	}
	sourceRoot, err := os.OpenRoot(filepath.Dir(source))
	if err != nil {
		return "", err
	}
	defer sourceRoot.Close()
	input, err := sourceRoot.Open(filepath.Base(source))
	if err != nil {
		return "", err
	}
	defer input.Close()
	openedInfo, err := input.Stat()
	if err != nil || !os.SameFile(sourceInfo, openedInfo) {
		return "", errors.New("existing core binary changed while it was being opened")
	}
	if openedInfo.Size() <= 0 || openedInfo.Size() > maxReleaseAssetSize {
		return "", fmt.Errorf("existing core binary size is outside the supported limit")
	}

	destinationRoot, err := os.OpenRoot(filepath.Dir(destination))
	if err != nil {
		return "", err
	}
	defer destinationRoot.Close()
	tempName, err := randomCoreTempName(destinationRoot)
	if err != nil {
		return "", err
	}
	defer destinationRoot.Remove(tempName)
	output, err := destinationRoot.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return "", err
	}
	written, copyErr := io.Copy(output, io.LimitReader(input, maxReleaseAssetSize+1))
	if copyErr == nil && (written <= 0 || written > maxReleaseAssetSize) {
		copyErr = errors.New("existing core binary copy exceeded the supported limit")
	}
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return replaceCoreBinary(destinationRoot, filepath.Base(destination), tempName)
}

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

var errOpenRCServiceProcessUnbound = errors.New("OpenRC service has no protected supervise-daemon process metadata")

type openRCProcessIdentity struct {
	PID        int
	ParentPID  int
	StartTime  string
	Executable string
	Argv       []string
}

type openRCServiceProcessIdentity struct {
	Service    string
	Supervisor openRCProcessIdentity
	Child      openRCProcessIdentity
}

func verifyOpenRCExistingServiceProcess(ctx context.Context, engine core.Engine, existing EngineSpec) (openRCServiceProcessIdentity, error) {
	identity, err := boundOpenRCServiceProcess(ctx, existing.Service)
	if err != nil {
		return openRCServiceProcessIdentity{}, err
	}
	if filepath.Clean(identity.Child.Executable) != filepath.Clean(existing.Binary) || !openRCProcessArgvMatches(engine, existing, identity.Child.Argv) {
		return openRCServiceProcessIdentity{}, errors.New("service-bound process executable or arguments no longer match the discovered core mapping")
	}
	matches, err := matchingOpenRCCoreProcessIDs(ctx, engine, existing)
	if err != nil {
		return openRCServiceProcessIdentity{}, err
	}
	if len(matches) != 1 || matches[0] != identity.Child.PID {
		return openRCServiceProcessIdentity{}, errors.New("existing OpenRC core process is ambiguous or is not uniquely owned by the reported service")
	}
	identityAgain, err := boundOpenRCServiceProcess(ctx, existing.Service)
	if err != nil || identityAgain.Supervisor.PID != identity.Supervisor.PID || identityAgain.Supervisor.StartTime != identity.Supervisor.StartTime ||
		identityAgain.Child.PID != identity.Child.PID || identityAgain.Child.StartTime != identity.Child.StartTime {
		return openRCServiceProcessIdentity{}, errors.New("service-bound OpenRC process identity changed during mapping verification")
	}
	return identity, nil
}

func matchingOpenRCCoreProcessIDs(ctx context.Context, engine core.Engine, existing EngineSpec) ([]int, error) {
	entries, err := os.ReadDir(openRCProcRoot)
	if err != nil {
		return nil, err
	}
	matches := make([]int, 0, 1)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || !decimalProcessID(entry.Name()) {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		identity, err := readOpenRCProcessIdentity(pid)
		if err != nil || filepath.Clean(identity.Executable) != filepath.Clean(existing.Binary) || !openRCProcessArgvMatches(engine, existing, identity.Argv) {
			continue
		}
		matches = append(matches, pid)
	}
	return matches, nil
}

func boundOpenRCServiceProcess(ctx context.Context, service string) (openRCServiceProcessIdentity, error) {
	if !safeServiceName(service) || strings.Contains(service, ".service") {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC service name is unsafe")
	}
	if err := ctx.Err(); err != nil {
		return openRCServiceProcessIdentity{}, err
	}
	optionsDirectory := filepath.Join(openRCStateRoot, "options", service)
	childPIDPath := filepath.Join(optionsDirectory, "child_pid")
	pidfileMetadataPath := filepath.Join(optionsDirectory, "pidfile")
	childPIDText, childPIDInfo, err := readProtectedOpenRCMetadata(childPIDPath)
	if errors.Is(err, os.ErrNotExist) {
		return openRCServiceProcessIdentity{}, errOpenRCServiceProcessUnbound
	}
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("read supervise-daemon child PID metadata: %w", err)
	}
	childPID, err := parseOpenRCProcessID(childPIDText)
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("parse supervise-daemon child PID metadata: %w", err)
	}
	pidfileValue, pidfileMetadataInfo, err := readProtectedOpenRCMetadata(pidfileMetadataPath)
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("read supervise-daemon pidfile metadata: %w", err)
	}
	expectedPIDFileName := "supervise-" + service + ".pid"
	if pidfileValue != filepath.Join("/run", expectedPIDFileName) && pidfileValue != filepath.Join("/var/run", expectedPIDFileName) {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC service uses an unsupported supervise-daemon pidfile")
	}
	supervisorPIDPath := filepath.Join(openRCSupervisorRoot, expectedPIDFileName)
	supervisorPIDText, supervisorPIDInfo, err := readProtectedOpenRCMetadata(supervisorPIDPath)
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("read supervise-daemon supervisor PID: %w", err)
	}
	supervisorPID, err := parseOpenRCProcessID(supervisorPIDText)
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("parse supervise-daemon supervisor PID: %w", err)
	}

	supervisor, err := readOpenRCProcessIdentity(supervisorPID)
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("read supervise-daemon supervisor identity: %w", err)
	}
	child, err := readOpenRCProcessIdentity(childPID)
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("read supervise-daemon child identity: %w", err)
	}
	if child.ParentPID != supervisor.PID {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC service child is not owned by its supervise-daemon process")
	}
	if err := validatePrivilegedExecutable(openRCSupervisorExecutable); err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("unsafe supervise-daemon executable: %w", err)
	}
	expectedSupervisorExecutable, err := filepath.EvalSymlinks(openRCSupervisorExecutable)
	if err != nil {
		return openRCServiceProcessIdentity{}, fmt.Errorf("resolve supervise-daemon executable: %w", err)
	}
	if filepath.Clean(supervisor.Executable) != filepath.Clean(expectedSupervisorExecutable) ||
		len(supervisor.Argv) < 3 || filepath.Base(supervisor.Argv[0]) != "supervise-daemon" || supervisor.Argv[1] != service || !stringInSlice("--start", supervisor.Argv) {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC supervisor identity does not match this service's supervise-daemon invocation")
	}

	childPIDAgain, childPIDInfoAgain, err := readProtectedOpenRCMetadata(childPIDPath)
	if err != nil || !os.SameFile(childPIDInfo, childPIDInfoAgain) || childPIDAgain != childPIDText {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC child PID metadata changed during process verification")
	}
	pidfileValueAgain, pidfileMetadataInfoAgain, err := readProtectedOpenRCMetadata(pidfileMetadataPath)
	if err != nil || !os.SameFile(pidfileMetadataInfo, pidfileMetadataInfoAgain) || pidfileValueAgain != pidfileValue {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC pidfile metadata changed during process verification")
	}
	supervisorPIDAgain, supervisorPIDInfoAgain, err := readProtectedOpenRCMetadata(supervisorPIDPath)
	if err != nil || !os.SameFile(supervisorPIDInfo, supervisorPIDInfoAgain) || supervisorPIDAgain != supervisorPIDText {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC supervisor PID changed during process verification")
	}
	if alive, err := openRCProcessIdentityAlive(supervisor); err != nil || !alive {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC supervise-daemon identity changed during process verification")
	}
	if alive, err := openRCProcessIdentityAlive(child); err != nil || !alive {
		return openRCServiceProcessIdentity{}, errors.New("OpenRC service child identity changed during process verification")
	}
	return openRCServiceProcessIdentity{Service: service, Supervisor: supervisor, Child: child}, nil
}

func readProtectedOpenRCMetadata(path string) (string, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Size() <= 0 || info.Size() > 4096 {
		return "", nil, errors.New("OpenRC process metadata is not a protected small regular file")
	}
	if err := validateOwner(info, "OpenRC process metadata"); err != nil {
		return "", nil, err
	}
	directory := filepath.Dir(path)
	if err := validateProtectedDirectoryChain(directory); err != nil {
		return "", nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", nil, err
	}
	defer root.Close()
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) || openedInfo.Size() != info.Size() {
		return "", nil, errors.New("OpenRC process metadata changed while it was opened")
	}
	contents, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(contents) == 0 || len(contents) > 4096 {
		return "", nil, errors.New("OpenRC process metadata is empty or too large")
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(contents), "\n"), "\r")
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", nil, errors.New("OpenRC process metadata is malformed")
	}
	return value, openedInfo, nil
}

func parseOpenRCProcessID(value string) (int, error) {
	if !decimalProcessID(value) {
		return 0, errors.New("process ID is not decimal")
	}
	pid, err := strconv.Atoi(value)
	if err != nil || pid <= 1 {
		return 0, errors.New("process ID is outside the supported range")
	}
	return pid, nil
}

func readOpenRCProcessIdentity(pid int) (openRCProcessIdentity, error) {
	processRoot := filepath.Join(openRCProcRoot, strconv.Itoa(pid))
	parentPID, startTime, err := readOpenRCProcessStat(filepath.Join(processRoot, "stat"))
	if err != nil {
		return openRCProcessIdentity{}, err
	}
	executable, err := os.Readlink(filepath.Join(processRoot, "exe"))
	if err != nil {
		return openRCProcessIdentity{}, err
	}
	argv, err := readOpenRCProcessArgv(filepath.Join(processRoot, "cmdline"))
	if err != nil {
		return openRCProcessIdentity{}, err
	}
	parentPIDAgain, startTimeAgain, err := readOpenRCProcessStat(filepath.Join(processRoot, "stat"))
	if err != nil || parentPIDAgain != parentPID || startTimeAgain != startTime {
		return openRCProcessIdentity{}, errors.New("process identity changed while executable and arguments were read")
	}
	return openRCProcessIdentity{PID: pid, ParentPID: parentPID, StartTime: startTime, Executable: filepath.Clean(executable), Argv: argv}, nil
}

func readOpenRCProcessStat(path string) (int, string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return 0, "", err
	}
	separator := strings.LastIndex(string(contents), ") ")
	if separator < 0 {
		return 0, "", errors.New("process stat is malformed")
	}
	fields := strings.Fields(string(contents)[separator+2:])
	if len(fields) < 20 || !decimalProcessID(fields[19]) {
		return 0, "", errors.New("process stat lacks a valid start time")
	}
	parentPID, err := strconv.Atoi(fields[1])
	if err != nil || parentPID < 0 {
		return 0, "", errors.New("process stat lacks a valid parent PID")
	}
	return parentPID, fields[19], nil
}

func openRCProcessIdentityAlive(identity openRCProcessIdentity) (bool, error) {
	_, startTime, err := readOpenRCProcessStat(filepath.Join(openRCProcRoot, strconv.Itoa(identity.PID), "stat"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return startTime == identity.StartTime, nil
}

func waitForOpenRCServiceProcessExit(ctx context.Context, identity openRCServiceProcessIdentity) error {
	stableContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var stableSince time.Time
	for {
		supervisorAlive, supervisorErr := openRCProcessIdentityAlive(identity.Supervisor)
		childAlive, childErr := openRCProcessIdentityAlive(identity.Child)
		if supervisorErr != nil || childErr != nil {
			return errors.Join(supervisorErr, childErr)
		}
		_, boundErr := boundOpenRCServiceProcess(stableContext, identity.Service)
		unbound := errors.Is(boundErr, errOpenRCServiceProcessUnbound)
		if !supervisorAlive && !childAlive && unbound {
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
			if time.Since(stableSince) >= 500*time.Millisecond {
				return nil
			}
		} else {
			stableSince = time.Time{}
		}
		select {
		case <-stableContext.Done():
			return fmt.Errorf("OpenRC service-bound process remained alive or supervised after stop: %w", stableContext.Err())
		case <-ticker.C:
		}
	}
}

func decimalProcessID(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func readOpenRCProcessArgv(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return nil, err
	}
	if len(contents) == 0 || len(contents) > 64<<10 {
		return nil, errors.New("OpenRC process command line is empty or too large")
	}
	if contents[len(contents)-1] == 0 {
		contents = contents[:len(contents)-1]
	}
	fields := strings.Split(string(contents), "\x00")
	for _, field := range fields {
		if field == "" || strings.ContainsAny(field, "\r\n") {
			return nil, errors.New("OpenRC process command line is malformed")
		}
	}
	return fields, nil
}

func openRCProcessArgvMatches(engine core.Engine, existing EngineSpec, argv []string) bool {
	if len(argv) == 0 || (argv[0] != existingServiceBinary(existing) && argv[0] != existing.Binary) {
		return false
	}
	switch engine {
	case core.EngineXray:
		return existing.ConfigDirectory == "" && len(argv) == 4 && argv[1] == "run" &&
			(argv[2] == "-config" || argv[2] == "-c") && argv[3] == existing.ConfigPath
	case core.EngineSingBox:
		if len(argv) == 4 && existing.ConfigDirectory == "" {
			return argv[1] == "run" && (argv[2] == "-c" || argv[2] == "--config") && argv[3] == existing.ConfigPath
		}
		return len(argv) == 6 && existing.ConfigDirectory != "" && argv[1] == "run" && argv[2] == "-c" &&
			argv[3] == existing.ConfigPath && argv[4] == "-C" && argv[5] == existing.ConfigDirectory
	default:
		return false
	}
}

func validateOpenRCServiceScript(service, ownershipMarker string) error {
	if !safeServiceName(service) || strings.Contains(service, ".service") {
		return errors.New("OpenRC service name is unsafe")
	}
	path := filepath.Join(openRCInitRoot, service)
	if err := validateProtectedDirectoryChain(filepath.Dir(path)); err != nil {
		return err
	}
	if err := validatePrivilegedExecutable(path); err != nil {
		return err
	}
	if ownershipMarker == "" {
		return nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(contents), ownershipMarker+"\n") {
		return errors.New("OpenRC service script lacks the QAgent ownership marker")
	}
	return nil
}

func parseSingleSystemdExecStart(value string) (string, string, error) {
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	if strings.ContainsAny(value, "\r\n") || strings.Contains(value, "} {") || strings.Contains(value, "; path=") {
		return "", "", errors.New("systemd ExecStart contains multiple commands")
	}
	const prefix = "{ path="
	if !strings.HasPrefix(value, prefix) {
		return "", "", errors.New("systemd ExecStart has an unsupported structure")
	}
	remainder := strings.TrimPrefix(value, prefix)
	const argvSeparator = " ; argv[]="
	argvIndex := strings.Index(remainder, argvSeparator)
	if argvIndex <= 0 {
		return "", "", errors.New("systemd ExecStart has no executable argv")
	}
	executable := remainder[:argvIndex]
	remainder = remainder[argvIndex+len(argvSeparator):]
	const metadataSeparator = " ; ignore_errors="
	metadataIndex := strings.Index(remainder, metadataSeparator)
	if metadataIndex <= 0 {
		return "", "", errors.New("systemd ExecStart has no command metadata")
	}
	argv := remainder[:metadataIndex]
	metadata := remainder[metadataIndex:]
	if !strings.HasSuffix(metadata, " }") || strings.ContainsAny(strings.TrimSuffix(metadata, " }"), "{}") {
		return "", "", errors.New("systemd ExecStart has ambiguous command metadata")
	}
	return executable, argv, nil
}

func supportedExistingExecStart(engine core.Engine, existing EngineSpec, argv string) bool {
	serviceBinary := existingServiceBinary(existing)
	switch engine {
	case core.EngineXray:
		return existing.ConfigDirectory == "" && (argv == serviceBinary+" run -config "+existing.ConfigPath ||
			argv == serviceBinary+" run -c "+existing.ConfigPath)
	case core.EngineSingBox:
		if existing.ConfigDirectory != "" {
			return argv == serviceBinary+" run -c "+existing.ConfigPath+" -C "+existing.ConfigDirectory
		}
		return argv == serviceBinary+" run -c "+existing.ConfigPath ||
			argv == serviceBinary+" run --config "+existing.ConfigPath
	default:
		return false
	}
}

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

func writeCoreMigrationMarker(prefix string, engine core.Engine, state coreMigrationState, configDigest, sourceDigest, existingEnableState, managedEnableState string) error {
	if prefix == "" {
		return errors.New("core migration marker path is not configured")
	}
	if state != coreMigrationInProgress && state != coreMigrationComplete {
		return errors.New("invalid core migration state")
	}
	if decoded, err := hex.DecodeString(configDigest); err != nil || len(decoded) != sha256.Size {
		return errors.New("invalid core migration configuration digest")
	}
	if decoded, err := hex.DecodeString(sourceDigest); err != nil || len(decoded) != sha256.Size {
		return errors.New("invalid core migration source digest")
	}
	if !validServiceEnableState(existingEnableState) || !validServiceEnableState(managedEnableState) {
		return errors.New("invalid core migration enable state")
	}
	contents := strings.Join([]string{
		string(state), configDigest, sourceDigest, existingEnableState, managedEnableState,
	}, " ") + "\n"
	return writeCoreMigrationMarkerContents(prefix, engine, contents)
}

func writePreparedCoreMigrationMarker(prefix string, engine core.Engine, record coreMigrationRecord) error {
	if record.State != coreMigrationInProgress || !record.HasFileRollback {
		return errors.New("invalid prepared core migration record")
	}
	if decoded, err := hex.DecodeString(record.ConfigDigest); err != nil || len(decoded) != sha256.Size {
		return errors.New("invalid core migration configuration digest")
	}
	if decoded, err := hex.DecodeString(record.SourceDigest); err != nil || len(decoded) != sha256.Size {
		return errors.New("invalid core migration source digest")
	}
	if !validServiceEnableState(record.ExistingEnableState) || !validServiceEnableState(record.ManagedEnableState) {
		return errors.New("invalid core migration enable state")
	}
	for _, digest := range []string{record.BinaryBackupDigest, record.ConfigBackupDigest, record.StagedBinaryDigest} {
		if digest == coreMigrationMissingBackup {
			continue
		}
		if decoded, err := hex.DecodeString(digest); err != nil || len(decoded) != sha256.Size {
			return errors.New("invalid core migration file backup digest")
		}
	}
	if record.StagedBinaryDigest == coreMigrationMissingBackup {
		return errors.New("invalid core migration staged binary digest")
	}
	contents := strings.Join([]string{
		coreMigrationPreparedToken,
		record.ConfigDigest,
		record.SourceDigest,
		record.ExistingEnableState,
		record.ManagedEnableState,
		record.BinaryBackupDigest,
		record.ConfigBackupDigest,
		record.StagedBinaryDigest,
	}, " ") + "\n"
	return writeCoreMigrationMarkerContents(prefix, engine, contents)
}

func writeCoreMigrationMarkerContents(prefix string, engine core.Engine, contents string) error {
	if prefix == "" {
		return errors.New("core migration marker path is not configured")
	}
	path := coreMigrationMarkerPath(prefix, engine)
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("core migration state directory is unsafe")
	}
	if err := validateOwner(info, "core migration state directory"); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	suffix, err := randomSuffix(8)
	if err != nil {
		return err
	}
	tempName := ".qagent-core-migration-" + suffix
	defer root.Remove(tempName)
	file, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(contents); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := root.Rename(tempName, filepath.Base(path)); err != nil {
		return err
	}
	return syncRootDirectory(root)
}

func removeCoreMigrationMarker(prefix string, engine core.Engine) error {
	path := coreMigrationMarkerPath(prefix, engine)
	directory := filepath.Dir(path)
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("core migration state directory is unsafe")
	}
	if err := validateOwner(info, "core migration state directory"); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(filepath.Base(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncRootDirectory(root)
}
