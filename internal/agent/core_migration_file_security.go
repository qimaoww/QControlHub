package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func protectedCoreMigrationFileDigest(path string, limit int64) (string, bool, error) {
	return protectedCoreMigrationFileDigestWithOwnerPolicy(path, limit, false)
}

func protectedExistingCoreMigrationFileDigest(path string, limit int64) (string, bool, error) {
	return protectedCoreMigrationFileDigestWithOwnerPolicy(path, limit, installerCoreAllowsOrphanOwner(path))
}

func protectedCoreMigrationFileDigestWithOwnerPolicy(path string, limit int64, allowOrphanOwner bool) (string, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	file, _, err := openProtectedCoreMigrationFileWithOwnerPolicy(path, info, limit, allowOrphanOwner)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	digest, err := digestCoreMigrationFile(file, limit)
	return digest, true, err
}

func openProtectedCoreMigrationFile(path string, expected os.FileInfo, limit int64) (*os.File, os.FileInfo, error) {
	return openProtectedCoreMigrationFileWithOwnerPolicy(path, expected, limit, false)
}

func openProtectedCoreMigrationFileWithOwnerPolicy(path string, expected os.FileInfo, limit int64, allowOrphanOwner bool) (*os.File, os.FileInfo, error) {
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
		if !allowOrphanOwner || !fileOwnerIsInactiveAndUnassigned(expected) {
			return nil, nil, err
		}
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
