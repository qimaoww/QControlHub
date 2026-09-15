package agent

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func atomicDeploy(destination, content string) (string, error) {
	return atomicDeployWithDefaultMetadata(destination, content, fileMetadata{mode: 0o600})
}

func atomicDeployWithDefaultMetadata(destination, content string, defaultMetadata fileMetadata) (string, error) {
	if destination == "" || !filepath.IsAbs(destination) {
		return "", errors.New("configuration destination must be an absolute path")
	}
	directory := filepath.Dir(destination)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", fmt.Errorf("create configuration directory: %w", err)
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
		return "", errors.New("configuration directory must be a real directory, not a symlink")
	}
	if directoryInfo.Mode().Perm()&0o022 != 0 {
		return "", errors.New("configuration directory must not be writable by group or others")
	}
	if err := validateOwner(directoryInfo, "configuration directory"); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", fmt.Errorf("open configuration directory: %w", err)
	}
	defer root.Close()
	baseName := filepath.Base(destination)
	metadata := defaultMetadata
	var backup string
	var backupName string
	if info, err := root.Lstat(baseName); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("refusing to replace a symlinked configuration file")
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("configuration destination is not a regular file")
		}
		if err := validateOwner(info, "configuration file"); err != nil {
			return "", err
		}
		if info.Mode().Perm()&0o022 != 0 {
			return "", errors.New("configuration file must not be writable by group or others")
		}
		metadata = metadataFromFileInfo(info)
		suffix, err := randomSuffix(6)
		if err != nil {
			return "", err
		}
		backupName = baseName + ".bak-" + time.Now().UTC().Format("20060102T150405Z") + "-" + suffix
		backup = filepath.Join(directory, backupName)
		if err := copyFileInRoot(root, baseName, backupName, metadata); err != nil {
			return "", fmt.Errorf("back up current configuration: %w", err)
		}
		if err := cleanupBackups(root, baseName, backupName, 3); err != nil {
			_ = root.Remove(backupName)
			return "", fmt.Errorf("clean up old configuration backups: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	suffix, err := randomSuffix(10)
	if err != nil {
		return backup, err
	}
	tempName := ".qcontrolhub-config-" + suffix + ".tmp"
	temp, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return backup, err
	}
	defer root.Remove(tempName)
	if _, err := temp.WriteString(content); err != nil {
		temp.Close()
		return backup, err
	}
	if err := applyFileMetadata(temp, metadata); err != nil {
		temp.Close()
		return backup, fmt.Errorf("preserve configuration metadata: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return backup, err
	}
	if err := temp.Close(); err != nil {
		return backup, err
	}
	if err := root.Rename(tempName, baseName); err != nil {
		return backup, err
	}
	if err := syncRootDirectory(root); err != nil {
		return backup, fmt.Errorf("sync configuration directory: %w", err)
	}
	return backup, nil
}

func syncRootDirectory(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

type fileMetadata struct {
	mode           os.FileMode
	uid            int
	gid            int
	ownershipKnown bool
}

func metadataFromFileInfo(info os.FileInfo) fileMetadata {
	uid, gid, known := fileOwnership(info)
	return fileMetadata{mode: info.Mode().Perm(), uid: uid, gid: gid, ownershipKnown: known}
}

func applyFileMetadata(file *os.File, metadata fileMetadata) error {
	if metadata.ownershipKnown {
		if err := file.Chown(metadata.uid, metadata.gid); err != nil {
			return err
		}
	}
	return file.Chmod(metadata.mode)
}

func applyRootFileMetadata(root *os.Root, name string, metadata fileMetadata) error {
	if metadata.ownershipKnown {
		if err := root.Chown(name, metadata.uid, metadata.gid); err != nil {
			return err
		}
	}
	return root.Chmod(name, metadata.mode)
}

func copyFileInRoot(root *os.Root, source, destination string, metadata fileMetadata) (err error) {
	input, err := root.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := root.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = root.Remove(destination)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	if err := applyFileMetadata(output, metadata); err != nil {
		output.Close()
		return err
	}
	if err := output.Sync(); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}

func rollbackDeploy(destination, backup string) (string, error) {
	directory := filepath.Dir(destination)
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	destinationName := filepath.Base(destination)
	if backup == "" {
		if err := root.Remove(destinationName); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err := syncRootDirectory(root); err != nil {
			return "", err
		}
		return "rollback: removed newly created configuration", nil
	}
	if filepath.Dir(backup) != directory {
		return "", errors.New("backup is outside the configuration directory")
	}
	backupName := filepath.Base(backup)
	if !strings.HasPrefix(backupName, destinationName+".bak-") {
		return "", errors.New("backup name does not match configuration")
	}
	if err := root.Rename(backupName, destinationName); err != nil {
		return "", err
	}
	if err := syncRootDirectory(root); err != nil {
		return "", err
	}
	return "rollback: previous configuration restored", nil
}

func cleanupBackups(root *os.Root, baseName, preserve string, keep int) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := directory.ReadDir(-1)
	directory.Close()
	if err != nil {
		return err
	}
	prefix := baseName + ".bak-"
	type backupEntry struct {
		name     string
		modified time.Time
	}
	backups := make([]backupEntry, 0)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			backups = append(backups, backupEntry{name: entry.Name(), modified: info.ModTime()})
		}
	}
	sort.Slice(backups, func(left, right int) bool {
		if backups[left].modified.Equal(backups[right].modified) {
			return backups[left].name < backups[right].name
		}
		return backups[left].modified.Before(backups[right].modified)
	})
	for len(backups) > keep {
		removeIndex := 0
		if backups[removeIndex].name == preserve && len(backups) > 1 {
			removeIndex = 1
		}
		if err := root.Remove(backups[removeIndex].name); err != nil {
			return err
		}
		backups = append(backups[:removeIndex], backups[removeIndex+1:]...)
	}
	return nil
}

func randomSuffix(bytesCount int) (string, error) {
	value := make([]byte, bytesCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
