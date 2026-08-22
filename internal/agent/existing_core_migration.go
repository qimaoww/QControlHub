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
			completionErr := verifyCoreMigrationCompletionState(loadContext, existing, managedSpecs[engine])
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
			if err := restoreLegacyInterruptedCoreMigration(ctx, e.MigrationMarkerPrefix, engine, existing, managed[engine], migrationRecord); err != nil {
				return err
			}
			continue
		}
		managedStatus, managedStatusErr := serviceStatus(ctx, managed[engine].Service)
		existingStatus, existingStatusErr := serviceStatus(ctx, existing.Service)
		rollback := func(cause error) error {
			restoreErr := restoreInterruptedCoreMigration(ctx, e.MigrationMarkerPrefix, engine, existing, managed[engine], migrationRecord)
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
		existingStatus, managedStatus, err = waitForCoreMigrationServicePairStable(ctx, existing.Service, managed[engine].Service)
		if err != nil || existingStatus != "inactive" || managedStatus != "active" {
			if rollbackErr := rollback(err); rollbackErr != nil {
				return rollbackErr
			}
			continue
		}
		if err := verifyCoreMigrationStagedFiles(managed[engine], migrationRecord); err != nil {
			return rollback(fmt.Errorf("verify staged managed files before migration completion: %w", err))
		}
		if err := setServiceEnabled(ctx, managed[engine].Service, true); err != nil {
			return rollback(err)
		}
		if err := disableServiceCompletely(ctx, existing.Service); err != nil {
			return rollback(err)
		}
		if err := verifyCoreMigrationCompletionState(ctx, existing, managed[engine]); err != nil {
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

func waitForCoreMigrationServicePairStable(ctx context.Context, existingService, managedService string) (string, string, error) {
	stableContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var stableSince time.Time
	for {
		existingStatus, err := serviceStatus(stableContext, existingService)
		if err != nil {
			return existingStatus, "", err
		}
		managedStatus, err := serviceStatus(stableContext, managedService)
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

func verifyCoreMigrationCompletionState(ctx context.Context, existing, managed EngineSpec) error {
	return waitForCoreMigrationState(ctx, existing.Service, managed.Service, "inactive", "active", "disabled", "enabled")
}

func waitForCoreMigrationState(ctx context.Context, existingService, managedService, expectedExistingStatus, expectedManagedStatus, expectedExistingEnableState, expectedManagedEnableState string) error {
	stableContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var stableSince time.Time
	for {
		existingStatus, err := serviceStatus(stableContext, existingService)
		if err != nil {
			return fmt.Errorf("query existing service state: %w", err)
		}
		managedStatus, err := serviceStatus(stableContext, managedService)
		if err != nil {
			return fmt.Errorf("query managed service state: %w", err)
		}
		existingEnableState, err := serviceEnableState(stableContext, existingService)
		if err != nil {
			return err
		}
		managedEnableState, err := serviceEnableState(stableContext, managedService)
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

func restoreInterruptedCoreMigration(ctx context.Context, prefix string, engine core.Engine, existing, managed EngineSpec, record coreMigrationRecord) error {
	if !record.HasFileRollback {
		return errors.New("legacy core migration marker has no durable managed-file rollback information")
	}
	return restoreCoreMigrationServicesAndFiles(ctx, prefix, engine, existing, managed, record, true)
}

func restoreLegacyInterruptedCoreMigration(ctx context.Context, prefix string, engine core.Engine, existing, managed EngineSpec, record coreMigrationRecord) error {
	return restoreCoreMigrationServicesAndFiles(ctx, prefix, engine, existing, managed, record, false)
}

func restoreCoreMigrationServicesAndFiles(ctx context.Context, prefix string, engine core.Engine, existing, managed EngineSpec, record coreMigrationRecord, restoreFiles bool) error {
	if _, err := serviceCommandAndVerify(ctx, managed.Service, core.ActionStop); err != nil {
		return fmt.Errorf("restore existing %s service after interrupted migration: stop managed service: %w", engine, err)
	}
	if err := waitForSingleMigrationServiceStable(ctx, managed.Service, "inactive"); err != nil {
		return fmt.Errorf("restore existing %s service after interrupted migration: verify managed service is stopped: %w", engine, err)
	}
	if err := restoreServiceEnableState(ctx, managed.Service, record.ManagedEnableState); err != nil {
		return fmt.Errorf("restore existing %s service after interrupted migration: %w", engine, err)
	}
	if err := restoreServiceEnableState(ctx, existing.Service, record.ExistingEnableState); err != nil {
		return fmt.Errorf("restore existing %s service after interrupted migration: %w", engine, err)
	}
	if err := waitForSingleMigrationServiceStable(ctx, managed.Service, "inactive"); err != nil {
		return fmt.Errorf("restore existing %s service after interrupted migration: managed service restarted before original recovery: %w", engine, err)
	}
	if restoreFiles {
		if err := restoreCoreMigrationFiles(prefix, engine, managed, record); err != nil {
			return fmt.Errorf("restore existing %s service after interrupted migration: restore managed files: %w", engine, err)
		}
	}
	if _, err := serviceCommandAndVerify(ctx, existing.Service, core.ActionStart); err != nil {
		return fmt.Errorf("restore existing %s service after interrupted migration: start original service: %w", engine, err)
	}
	if err := waitForCoreMigrationState(ctx, existing.Service, managed.Service, "active", "inactive", record.ExistingEnableState, record.ManagedEnableState); err != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, stopErr := serviceCommandAndVerify(cleanupContext, managed.Service, core.ActionStop)
		stopErr = errors.Join(stopErr, waitForSingleMigrationServiceStable(cleanupContext, managed.Service, "inactive"))
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

func waitForSingleMigrationServiceStable(ctx context.Context, service, expected string) error {
	stableContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status, err := waitForServiceState(stableContext, expected, 500*time.Millisecond, 100*time.Millisecond, func(probeContext context.Context) (string, error) {
		return serviceStatus(probeContext, service)
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

	e.specsMu.RLock()
	currentExisting, stillPending := e.ExistingSpecs[engine]
	e.specsMu.RUnlock()
	if !stillPending || currentExisting != existing {
		return "", fmt.Errorf("%s existing service migration is no longer pending", engine)
	}
	if err := verifyExistingServiceMapping(ctx, engine, existing); err != nil {
		return "", err
	}
	if err := requireManagedServiceSafeInactive(ctx, engine, managed); err != nil {
		return "", err
	}
	currentContent, err := e.readExistingConfig(ctx, engine, managed, existing)
	if err != nil {
		return "", err
	}
	if currentContent != content {
		return "", fmt.Errorf("existing %s configuration sources changed after the saved snapshot; both services were left unchanged", engine)
	}
	existingEnableState, err := serviceEnableState(ctx, existing.Service)
	if err != nil {
		return "", err
	}
	managedEnableState, err := serviceEnableState(ctx, managed.Service)
	if err != nil {
		return "", err
	}
	if !migrationEnableStatesSupported(existingEnableState, managedEnableState) {
		return "", fmt.Errorf("systemd enable states cannot be migrated safely: existing %s is %s and managed %s is %s; both services were left unchanged", existing.Service, existingEnableState, managed.Service, managedEnableState)
	}

	validationSpec := managed
	validationSpec.Binary = existing.Binary
	if _, err := e.validate(ctx, engine, validationSpec, content); err != nil {
		return "", fmt.Errorf("existing %s configuration is not safe for managed deployment: %w", engine, err)
	}
	if err := requireManagedServiceSafeInactive(ctx, engine, managed); err != nil {
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
	currentExistingEnableState, err := serviceEnableState(ctx, existing.Service)
	if err != nil {
		return rollbackMigration(err)
	}
	currentManagedEnableState, err := serviceEnableState(ctx, managed.Service)
	if err != nil {
		return rollbackMigration(err)
	}
	if currentExistingEnableState != existingEnableState || currentManagedEnableState != managedEnableState {
		return rollbackMigration(fmt.Errorf("systemd enable states changed during migration preparation: existing %s changed from %s to %s and managed %s changed from %s to %s; both services were left unchanged", existing.Service, existingEnableState, currentExistingEnableState, managed.Service, managedEnableState, currentManagedEnableState))
	}
	if err := verifyExistingServiceMapping(ctx, engine, existing); err != nil {
		return rollbackMigration(err)
	}
	currentContent, err = e.readExistingConfig(ctx, engine, managed, existing)
	if err != nil {
		return rollbackMigration(err)
	}
	if currentContent != content {
		return rollbackMigration(fmt.Errorf("existing %s configuration sources changed during migration preparation; both services were left unchanged", engine))
	}
	if err := requireManagedServiceSafeInactive(ctx, engine, managed); err != nil {
		return rollbackMigration(err)
	}
	if err := ensureManagedCoreServiceCapabilities(ctx, engine, managed); err != nil {
		return rollbackMigration(err)
	}
	if err := requireManagedServiceSafeInactive(ctx, engine, managed); err != nil {
		return rollbackMigration(err)
	}

	if _, err := serviceCommandAndVerify(ctx, existing.Service, core.ActionStop); err != nil {
		return rollbackMigration(fmt.Errorf("stop existing %s service: %w", engine, err))
	}
	if _, err := serviceCommandAndVerify(ctx, managed.Service, core.ActionStart); err != nil {
		return rollbackMigration(fmt.Errorf("start QAgent %s service: %w", engine, err))
	}
	if err := setServiceEnabled(ctx, managed.Service, true); err != nil {
		return rollbackMigration(err)
	}
	if err := disableServiceCompletely(ctx, existing.Service); err != nil {
		return rollbackMigration(err)
	}
	if err := verifyCoreMigrationCompletionState(ctx, existing, managed); err != nil {
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

func requireManagedServiceSafeInactive(ctx context.Context, engine core.Engine, managed EngineSpec) error {
	status, err := serviceStatus(ctx, managed.Service)
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

func verifyExistingServiceMapping(ctx context.Context, engine core.Engine, existing EngineSpec) error {
	status, err := serviceStatus(ctx, existing.Service)
	if err != nil {
		return fmt.Errorf("query existing %s service before migration: %w", engine, err)
	}
	if status != "active" {
		return fmt.Errorf("existing %s service must remain active before migration (status %q)", engine, status)
	}
	output, err := run(ctx, systemctlPath, "show", existing.Service, "--property=ExecStart", "--value")
	if err != nil {
		return fmt.Errorf("query existing %s service ExecStart before migration: %w", engine, err)
	}
	executable, argv, err := parseSingleSystemdExecStart(output)
	if err != nil || executable != existingServiceBinary(existing) || !supportedExistingExecStart(engine, existing, argv) {
		return fmt.Errorf("existing %s service ExecStart no longer matches the exact discovered binary and single configuration", engine)
	}
	if err := validateExistingServiceExecutable(existing); err != nil {
		return fmt.Errorf("existing %s service executable mapping is no longer safe: %w", engine, err)
	}
	status, err = serviceStatus(ctx, existing.Service)
	if err != nil {
		return fmt.Errorf("recheck existing %s service before migration: %w", engine, err)
	}
	if status != "active" {
		return fmt.Errorf("existing %s service changed to %q while its mapping was checked", engine, status)
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

func serviceEnableState(ctx context.Context, service string) (string, error) {
	output, err := run(ctx, systemctlPath, "is-enabled", service)
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

func setServiceEnabled(ctx context.Context, service string, enabled bool) error {
	action := "disable"
	if enabled {
		action = "enable"
	}
	if output, err := run(ctx, systemctlPath, action, service); err != nil {
		return fmt.Errorf("systemctl %s %s: %w: %s", action, service, err, output)
	}
	return nil
}

func disableServiceCompletely(ctx context.Context, service string) error {
	if err := setServiceEnabled(ctx, service, false); err != nil {
		return err
	}
	if output, err := run(ctx, systemctlPath, "disable", "--runtime", service); err != nil {
		return fmt.Errorf("systemctl disable --runtime %s: %w: %s", service, err, output)
	}
	return nil
}

func restoreServiceEnableState(ctx context.Context, service, state string) error {
	switch state {
	case "enabled":
		return setServiceEnabled(ctx, service, true)
	case "enabled-runtime":
		if err := disableServiceCompletely(ctx, service); err != nil {
			return err
		}
		if output, err := run(ctx, systemctlPath, "enable", "--runtime", service); err != nil {
			return fmt.Errorf("systemctl enable --runtime %s: %w: %s", service, err, output)
		}
		restored, err := serviceEnableState(ctx, service)
		if err != nil {
			return err
		}
		if restored != "enabled-runtime" {
			return fmt.Errorf("systemd service %s enable state restored as %s instead of enabled-runtime", service, restored)
		}
		return nil
	case "disabled":
		return disableServiceCompletely(ctx, service)
	case "static", "indirect":
		return nil
	default:
		return errors.New("invalid original systemd enable state")
	}
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
