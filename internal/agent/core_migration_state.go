package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

type coreMigrationState string

type coreMigrationRecord struct {
	State                 coreMigrationState
	ConfigDigest          string
	RequestConfigDigest   string
	SourceDigest          string
	ExistingEnableState   string
	ManagedEnableState    string
	ManagedInitialState   string
	HasFileRollback       bool
	BinaryBackupDigest    string
	ConfigBackupDigest    string
	StagedBinaryDigest    string
	AssetBackupDigests    [2]string
	StagedAssetDigests    [2]string
	HasAssetRollback      bool
	AuxiliaryService      string
	AuxiliaryEnableState  string
	AuxiliaryInitialState string
}

const (
	coreMigrationNone                coreMigrationState = ""
	coreMigrationInProgress          coreMigrationState = "migrating"
	coreMigrationComplete            coreMigrationState = "migrated"
	coreMigrationPreparedToken                          = "migrating-v5"
	coreMigrationV4PreparedToken                        = "migrating-v4"
	coreMigrationV3PreparedToken                        = "migrating-v3"
	coreMigrationLegacyPreparedToken                    = "migrating-v2"
	coreMigrationMissingBackup                          = "-"
)

var xrayMigrationAssetNames = [2]string{"geoip.dat", "geosite.dat"}

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
	if err := restoreMigrationAuxiliary(ctx, record, manager); err != nil {
		return fmt.Errorf("restore existing %s service after interrupted migration: restore OU-SB auxiliary service: %w", engine, err)
	}
	if _, err := serviceCommandAndVerifyWithManager(ctx, manager, existing.Service, core.ActionStart); err != nil {
		return fmt.Errorf("restore existing %s service after interrupted migration: start original service: %w", engine, err)
	}
	managedExpectedStatus := "inactive"
	if record.ManagedInitialState == "active" {
		if _, err := serviceCommandAndVerifyWithManager(ctx, manager, managed.Service, core.ActionStart); err != nil {
			return fmt.Errorf("restore existing %s service after interrupted migration: restart originally active managed service: %w", engine, err)
		}
		managedExpectedStatus = "active"
	}
	if err := waitForCoreMigrationState(ctx, existing.Service, managed.Service, "active", managedExpectedStatus, record.ExistingEnableState, record.ManagedEnableState, manager); err != nil {
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
	if len(fields) != 5 && len(fields) != 6 && len(fields) != 8 && len(fields) != 9 && len(fields) != 13 && len(fields) != 17 {
		return coreMigrationRecord{}, errors.New("core migration marker is invalid")
	}
	if (fields[0] == coreMigrationPreparedToken && len(fields) != 17) ||
		(fields[0] == coreMigrationV4PreparedToken && len(fields) != 13) ||
		(fields[0] == coreMigrationV3PreparedToken && len(fields) != 9) ||
		(fields[0] == coreMigrationLegacyPreparedToken && len(fields) != 8) {
		return coreMigrationRecord{}, errors.New("core migration marker version is invalid")
	}
	state := coreMigrationState(fields[0])
	hasFileRollback := false
	hasAssetRollback := false
	managedInitialState := "inactive"
	if fields[0] == coreMigrationPreparedToken {
		state = coreMigrationInProgress
		hasFileRollback = true
		hasAssetRollback = true
		managedInitialState = fields[8]
	} else if fields[0] == coreMigrationV4PreparedToken {
		state = coreMigrationInProgress
		hasFileRollback = true
		hasAssetRollback = true
		managedInitialState = fields[8]
	} else if fields[0] == coreMigrationV3PreparedToken {
		state = coreMigrationInProgress
		hasFileRollback = true
		managedInitialState = fields[8]
	} else if fields[0] == coreMigrationLegacyPreparedToken {
		state = coreMigrationInProgress
		hasFileRollback = true
	}
	if state != coreMigrationInProgress && state != coreMigrationComplete {
		return coreMigrationRecord{}, errors.New("core migration marker is invalid")
	}
	if state == coreMigrationComplete && len(fields) != 5 && len(fields) != 6 {
		return coreMigrationRecord{}, errors.New("core migration marker version is invalid")
	}
	if managedInitialState != "active" && managedInitialState != "inactive" {
		return coreMigrationRecord{}, errors.New("core migration managed initial state is invalid")
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
		ManagedInitialState: managedInitialState,
		HasFileRollback:     hasFileRollback,
		HasAssetRollback:    hasAssetRollback,
	}
	record.RequestConfigDigest = record.ConfigDigest
	if state == coreMigrationComplete && len(fields) == 6 {
		record.RequestConfigDigest = fields[5]
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
	if hasAssetRollback {
		for _, digest := range fields[9:13] {
			if digest == coreMigrationMissingBackup {
				continue
			}
			if decoded, err := hex.DecodeString(digest); err != nil || len(decoded) != sha256.Size {
				return coreMigrationRecord{}, errors.New("core migration asset digest is invalid")
			}
		}
		record.AssetBackupDigests = [2]string{fields[9], fields[11]}
		record.StagedAssetDigests = [2]string{fields[10], fields[12]}
	}
	if fields[0] == coreMigrationPreparedToken {
		record.RequestConfigDigest = fields[13]
		if fields[14] != coreMigrationMissingBackup {
			if !safeServiceName(fields[14]) || !validServiceEnableState(fields[15]) ||
				(fields[16] != "active" && fields[16] != "inactive") {
				return coreMigrationRecord{}, errors.New("core migration auxiliary service state is invalid")
			}
			record.AuxiliaryService = fields[14]
			record.AuxiliaryEnableState = fields[15]
			record.AuxiliaryInitialState = fields[16]
		} else if fields[15] != coreMigrationMissingBackup || fields[16] != coreMigrationMissingBackup {
			return coreMigrationRecord{}, errors.New("core migration auxiliary service marker is invalid")
		}
	}
	if decoded, err := hex.DecodeString(record.RequestConfigDigest); err != nil || len(decoded) != sha256.Size {
		return coreMigrationRecord{}, errors.New("core migration requested configuration digest is invalid")
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
	return record.RequestConfigDigest == coreMigrationConfigDigest(content), nil
}

func coreMigrationConfigDigest(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func coreMigrationSourceDigest(existing EngineSpec) string {
	source := existing.Binary + "\x00" + existing.ConfigPath + "\x00" + existing.Service
	if existing.ConfigDirectory != "" || existingServiceBinary(existing) != existing.Binary || existing.WorkingDirectory != "" {
		source = existing.Binary + "\x00" + existing.ConfigPath + "\x00" + existing.ConfigDirectory
		if existing.WorkingDirectory != "" {
			source += "\x00" + existing.WorkingDirectory
		}
		source += "\x00" + existingServiceBinary(existing) + "\x00" + existing.Service
	}
	if existing.ACLPath != "" {
		source += "\x00acl\x00" + existing.ACLPath
	}
	digest := sha256.Sum256([]byte(source))
	return hex.EncodeToString(digest[:])
}
