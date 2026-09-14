package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
	for _, kind := range coreMigrationBackupKinds() {
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
	stagedBinaryDigest, exists, err := protectedExistingCoreMigrationFileDigest(existing.Binary, maxReleaseAssetSize)
	if err != nil {
		_ = cleanupCoreMigrationBackups(prefix, engine)
		return coreMigrationRecord{}, fmt.Errorf("digest existing core binary: %w", err)
	}
	if !exists {
		_ = cleanupCoreMigrationBackups(prefix, engine)
		return coreMigrationRecord{}, errors.New("existing core binary disappeared while preparing migration")
	}
	record.HasFileRollback = true
	if record.ManagedInitialState == "" {
		record.ManagedInitialState = "inactive"
	}
	record.BinaryBackupDigest = binaryBackupDigest
	record.ConfigBackupDigest = configBackupDigest
	record.StagedBinaryDigest = stagedBinaryDigest
	record.AssetBackupDigests = [2]string{coreMigrationMissingBackup, coreMigrationMissingBackup}
	record.StagedAssetDigests = [2]string{coreMigrationMissingBackup, coreMigrationMissingBackup}
	record.HasAssetRollback = true
	if engine == core.EngineXray {
		for index, name := range xrayMigrationAssetNames {
			sourcePath := filepath.Join(filepath.Dir(existing.Binary), name)
			stagedDigest, assetExists, digestErr := protectedCoreMigrationFileDigest(sourcePath, maxReleaseAssetSize)
			if digestErr != nil {
				_ = cleanupCoreMigrationBackups(prefix, engine)
				return coreMigrationRecord{}, fmt.Errorf("digest existing Xray asset %s: %w", name, digestErr)
			}
			if !assetExists {
				continue
			}
			destinationPath := filepath.Join(filepath.Dir(managed.Binary), name)
			backupDigest, snapshotErr := snapshotCoreMigrationFile(root, prefix, engine, "asset-"+name, destinationPath, maxReleaseAssetSize)
			if snapshotErr != nil {
				_ = cleanupCoreMigrationBackups(prefix, engine)
				return coreMigrationRecord{}, fmt.Errorf("snapshot managed Xray asset %s: %w", name, snapshotErr)
			}
			record.AssetBackupDigests[index] = backupDigest
			record.StagedAssetDigests[index] = stagedDigest
		}
	}
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
	type assetRestore struct {
		path   string
		name   string
		backup coreMigrationBackup
		action coreMigrationRestoreAction
	}
	assetRestores := make([]assetRestore, 0, len(xrayMigrationAssetNames))
	if record.HasAssetRollback {
		for index, name := range xrayMigrationAssetNames {
			stagedDigest := record.StagedAssetDigests[index]
			if stagedDigest == coreMigrationMissingBackup {
				continue
			}
			backup, backupErr := openCoreMigrationBackup(prefix, engine, "asset-"+name, record.AssetBackupDigests[index], maxReleaseAssetSize)
			if backupErr != nil {
				return fmt.Errorf("validate managed Xray asset %s rollback backup: %w", name, backupErr)
			}
			if backup.file != nil {
				defer backup.file.Close()
			}
			path := filepath.Join(filepath.Dir(managed.Binary), name)
			action, planErr := planCoreMigrationFileRestore(path, record.AssetBackupDigests[index], stagedDigest, maxReleaseAssetSize)
			if planErr != nil {
				return fmt.Errorf("plan managed Xray asset %s rollback: %w", name, planErr)
			}
			assetRestores = append(assetRestores, assetRestore{path: path, name: name, backup: backup, action: action})
		}
	}
	if err := applyCoreMigrationFileRestore(managed.Binary, binaryBackup, binaryAction); err != nil {
		return fmt.Errorf("restore managed binary: %w", err)
	}
	if err := applyCoreMigrationFileRestore(managed.ConfigPath, configBackup, configAction); err != nil {
		return fmt.Errorf("restore managed configuration: %w", err)
	}
	for _, asset := range assetRestores {
		if err := applyCoreMigrationFileRestore(asset.path, asset.backup, asset.action); err != nil {
			return fmt.Errorf("restore managed Xray asset %s: %w", asset.name, err)
		}
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
	if record.HasAssetRollback {
		for index, name := range xrayMigrationAssetNames {
			expectedDigest := record.StagedAssetDigests[index]
			if expectedDigest == coreMigrationMissingBackup {
				continue
			}
			path := filepath.Join(filepath.Dir(managed.Binary), name)
			digest, exists, err := protectedCoreMigrationFileDigest(path, maxReleaseAssetSize)
			if err != nil || !exists || digest != expectedDigest {
				return fmt.Errorf("managed Xray asset %s does not match the recorded staged asset", name)
			}
		}
	}
	return nil
}

func verifyCoreMigrationOriginalFiles(managed EngineSpec, record coreMigrationRecord) error {
	files := []struct {
		path   string
		digest string
		limit  int64
		label  string
	}{
		{path: managed.Binary, digest: record.BinaryBackupDigest, limit: maxReleaseAssetSize, label: "binary"},
		{path: managed.ConfigPath, digest: record.ConfigBackupDigest, limit: core.MaxConfigBytes, label: "configuration"},
	}
	if record.HasAssetRollback {
		for index, name := range xrayMigrationAssetNames {
			if record.StagedAssetDigests[index] == coreMigrationMissingBackup {
				continue
			}
			files = append(files, struct {
				path   string
				digest string
				limit  int64
				label  string
			}{
				path: filepath.Join(filepath.Dir(managed.Binary), name), digest: record.AssetBackupDigests[index],
				limit: maxReleaseAssetSize, label: "Xray asset " + name,
			})
		}
	}
	for _, file := range files {
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
