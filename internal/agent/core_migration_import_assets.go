package agent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func copyExistingCoreBinary(source, destination string) (string, error) {
	if source == destination {
		return "", errors.New("existing and managed core binary paths must differ")
	}
	if err := validateExistingCoreExecutable(source); err != nil {
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

func stageExistingXrayMigrationAssets(existing, managed EngineSpec, record coreMigrationRecord) error {
	if !record.HasAssetRollback {
		return errors.New("Xray migration record has no asset rollback information")
	}
	for index, name := range xrayMigrationAssetNames {
		expectedDigest := record.StagedAssetDigests[index]
		if expectedDigest == coreMigrationMissingBackup {
			continue
		}
		source := filepath.Join(filepath.Dir(existing.Binary), name)
		destination := filepath.Join(filepath.Dir(managed.Binary), name)
		if err := copyExistingCoreAsset(source, destination, expectedDigest); err != nil {
			return fmt.Errorf("stage %s: %w", name, err)
		}
	}
	return nil
}

func copyExistingCoreAsset(source, destination, expectedDigest string) error {
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	input, _, err := openProtectedCoreMigrationFile(source, sourceInfo, maxReleaseAssetSize)
	if err != nil {
		return err
	}
	defer input.Close()
	digest, err := digestCoreMigrationFile(input, maxReleaseAssetSize)
	if err != nil {
		return err
	}
	if digest != expectedDigest {
		return errors.New("source asset changed after migration preparation")
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return err
	}

	directory := filepath.Dir(destination)
	if err := validateProtectedDirectoryChain(directory); err != nil {
		return err
	}
	metadata := fileMetadata{mode: 0o644}
	if destinationInfo, err := os.Lstat(destination); err == nil {
		existing, openedInfo, openErr := openProtectedCoreMigrationFile(destination, destinationInfo, maxReleaseAssetSize)
		if openErr != nil {
			return openErr
		}
		existing.Close()
		metadata = metadataFromFileInfo(openedInfo)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	tempName, err := randomCoreTempName(root)
	if err != nil {
		return err
	}
	defer root.Remove(tempName)
	output, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(output, io.LimitReader(input, maxReleaseAssetSize+1))
	if copyErr == nil && (written <= 0 || written > maxReleaseAssetSize) {
		copyErr = errors.New("Xray asset copy exceeded the supported limit")
	}
	if copyErr == nil {
		copyErr = applyFileMetadata(output, metadata)
	}
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := root.Rename(tempName, filepath.Base(destination)); err != nil {
		return err
	}
	return syncRootDirectory(root)
}
