package agent

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/qimaoww/qcontrolhub/internal/core"
)

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
