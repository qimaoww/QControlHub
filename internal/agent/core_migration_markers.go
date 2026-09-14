package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

func writeCoreMigrationMarker(prefix string, engine core.Engine, state coreMigrationState, configDigest, sourceDigest, existingEnableState, managedEnableState string) error {
	return writeCoreMigrationMarkerWithRequestDigest(prefix, engine, state, configDigest, configDigest, sourceDigest, existingEnableState, managedEnableState)
}

func writeCoreMigrationMarkerWithRequestDigest(prefix string, engine core.Engine, state coreMigrationState, configDigest, requestConfigDigest, sourceDigest, existingEnableState, managedEnableState string) error {
	if prefix == "" {
		return errors.New("core migration marker path is not configured")
	}
	if state != coreMigrationInProgress && state != coreMigrationComplete {
		return errors.New("invalid core migration state")
	}
	if decoded, err := hex.DecodeString(configDigest); err != nil || len(decoded) != sha256.Size {
		return errors.New("invalid core migration configuration digest")
	}
	if decoded, err := hex.DecodeString(requestConfigDigest); err != nil || len(decoded) != sha256.Size {
		return errors.New("invalid core migration requested configuration digest")
	}
	if decoded, err := hex.DecodeString(sourceDigest); err != nil || len(decoded) != sha256.Size {
		return errors.New("invalid core migration source digest")
	}
	if !validServiceEnableState(existingEnableState) || !validServiceEnableState(managedEnableState) {
		return errors.New("invalid core migration enable state")
	}
	fields := []string{string(state), configDigest, sourceDigest, existingEnableState, managedEnableState}
	if state == coreMigrationComplete && requestConfigDigest != configDigest {
		fields = append(fields, requestConfigDigest)
	}
	contents := strings.Join(fields, " ") + "\n"
	return writeCoreMigrationMarkerContents(prefix, engine, contents)
}

func writePreparedCoreMigrationMarker(prefix string, engine core.Engine, record coreMigrationRecord) error {
	if record.State != coreMigrationInProgress || !record.HasFileRollback {
		return errors.New("invalid prepared core migration record")
	}
	if record.ManagedInitialState == "" {
		record.ManagedInitialState = "inactive"
	}
	if record.ManagedInitialState != "active" && record.ManagedInitialState != "inactive" {
		return errors.New("invalid core migration managed initial state")
	}
	if decoded, err := hex.DecodeString(record.ConfigDigest); err != nil || len(decoded) != sha256.Size {
		return errors.New("invalid core migration configuration digest")
	}
	if record.RequestConfigDigest == "" {
		record.RequestConfigDigest = record.ConfigDigest
	}
	if decoded, err := hex.DecodeString(record.RequestConfigDigest); err != nil || len(decoded) != sha256.Size {
		return errors.New("invalid core migration requested configuration digest")
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
	if !record.HasAssetRollback {
		return errors.New("invalid core migration asset rollback record")
	}
	for _, digest := range []string{
		record.AssetBackupDigests[0], record.StagedAssetDigests[0],
		record.AssetBackupDigests[1], record.StagedAssetDigests[1],
	} {
		if digest == coreMigrationMissingBackup {
			continue
		}
		if decoded, err := hex.DecodeString(digest); err != nil || len(decoded) != sha256.Size {
			return errors.New("invalid core migration asset digest")
		}
	}
	auxiliaryService := coreMigrationMissingBackup
	auxiliaryEnableState := coreMigrationMissingBackup
	auxiliaryInitialState := coreMigrationMissingBackup
	if record.AuxiliaryService != "" {
		if !safeServiceName(record.AuxiliaryService) || !validServiceEnableState(record.AuxiliaryEnableState) ||
			(record.AuxiliaryInitialState != "active" && record.AuxiliaryInitialState != "inactive") {
			return errors.New("invalid core migration auxiliary service state")
		}
		auxiliaryService = record.AuxiliaryService
		auxiliaryEnableState = record.AuxiliaryEnableState
		auxiliaryInitialState = record.AuxiliaryInitialState
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
		record.ManagedInitialState,
		record.AssetBackupDigests[0],
		record.StagedAssetDigests[0],
		record.AssetBackupDigests[1],
		record.StagedAssetDigests[1],
		record.RequestConfigDigest,
		auxiliaryService,
		auxiliaryEnableState,
		auxiliaryInitialState,
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
